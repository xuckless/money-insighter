package store

import (
	"context"
	"errors"
	"fmt"
)

// syncRunSelectColumns is the column list that maps onto SyncRun. The
// INSERT below writes the same columns minus run_id, in the same order.
const syncRunSelectColumns = `
	run_id, item_id, job_id, trigger, started_at, finished_at,
	cursor_before, cursor_after,
	pages, added, modified, removed, inserted, updated, superseded,
	accounts_seen, accounts_missing,
	outcome, error_code, error_type, error_message, request_id`

// validSyncOutcome reports whether o is one of the outcomes the schema's
// CHECK constraint allows.
func validSyncOutcome(o SyncOutcome) bool {
	switch o {
	case SyncOutcomeSuccess, SyncOutcomeRetryableError, SyncOutcomeNeedsReauth,
		SyncOutcomeFatal, SyncOutcomeLocked, SyncOutcomeCanceled, SyncOutcomeError:
		return true
	}
	return false
}

// validateSyncRun checks r before it is written. Everything here would
// either be rejected by the database anyway (with a less helpful message)
// or would silently record nonsense (negative counts, zero timestamps).
func validateSyncRun(r SyncRun) error {
	if r.ItemID == "" {
		return errors.New("item id is empty")
	}
	if !validJobKind(r.Trigger) {
		return fmt.Errorf("unknown trigger %q", r.Trigger)
	}
	if !validSyncOutcome(r.Outcome) {
		return fmt.Errorf("unknown outcome %q", r.Outcome)
	}
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		return errors.New("started_at and finished_at must be set")
	}
	if r.FinishedAt.Before(r.StartedAt) {
		return errors.New("finished_at is before started_at")
	}
	counts := []struct {
		name string
		n    int
	}{
		{"pages", r.Pages}, {"added", r.Added}, {"modified", r.Modified},
		{"removed", r.Removed}, {"inserted", r.Inserted}, {"updated", r.Updated},
		{"superseded", r.Superseded}, {"accounts_seen", r.AccountsSeen},
		{"accounts_missing", r.AccountsMissing},
	}
	for _, c := range counts {
		if c.n < 0 {
			return fmt.Errorf("%s is negative (%d)", c.name, c.n)
		}
	}
	return nil
}

// insertSyncRun writes r through q and returns the run_id the database
// assigned. q is the pool for RecordSyncRun and the item transaction for
// ItemTx.RecordSyncRun, so a run recorded inside a sync is committed or
// rolled back together with the rows and cursor it describes. r.RunID is
// ignored. It returns ErrNotFound when the item does not exist or JobID
// names no job (including when it is not a well-formed UUID). Errors are
// fully wrapped, so callers can return them as they are.
func insertSyncRun(ctx context.Context, q querier, r SyncRun) (int64, error) {
	if err := validateSyncRun(r); err != nil {
		return 0, fmt.Errorf("store: record sync run: %w", err)
	}
	var jobID *string
	if r.JobID != nil {
		id, ok := normalizeUUID(*r.JobID)
		if !ok {
			return 0, fmt.Errorf("store: record sync run: malformed job id: %w", ErrNotFound)
		}
		jobID = &id
	}

	var runID int64
	err := q.QueryRow(ctx, `
		INSERT INTO sync_runs (
			item_id, job_id, trigger, started_at, finished_at,
			cursor_before, cursor_after,
			pages, added, modified, removed, inserted, updated, superseded,
			accounts_seen, accounts_missing,
			outcome, error_code, error_type, error_message, request_id
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9, $10, $11, $12, $13, $14,
			$15, $16,
			$17, $18, $19, $20, $21
		)
		RETURNING run_id`,
		r.ItemID, jobID, string(r.Trigger), r.StartedAt, r.FinishedAt,
		r.CursorBefore, r.CursorAfter,
		r.Pages, r.Added, r.Modified, r.Removed, r.Inserted, r.Updated, r.Superseded,
		r.AccountsSeen, r.AccountsMissing,
		string(r.Outcome), r.ErrorCode, r.ErrorType, r.ErrorMessage, r.RequestID,
	).Scan(&runID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return 0, fmt.Errorf("store: record sync run: item or job does not exist: %w", ErrNotFound)
		}
		return 0, fmt.Errorf("store: record sync run: %w", err)
	}
	return runID, nil
}

// RecordSyncRun inserts the audit row for a sync attempt outside any item
// transaction and returns its run_id. It is the path for runs that never
// opened a transaction or whose transaction was rolled back: lock
// contention, Plaid errors, cancellation. A run that committed rows should
// be recorded through ItemTx.RecordSyncRun instead, so the audit row and
// the data it describes land together. It returns ErrNotFound when the
// item does not exist or r.JobID names no job.
func (s *Store) RecordSyncRun(ctx context.Context, r SyncRun) (int64, error) {
	return insertSyncRun(ctx, s.pool, r)
}

// ListSyncRuns returns the item's most recent runs, newest first by
// started_at, at most limit of them (1..1000). An unknown item yields an
// empty list, not an error.
func (s *Store) ListSyncRuns(ctx context.Context, itemID string, limit int) ([]SyncRun, error) {
	if err := checkLimit(limit); err != nil {
		return nil, fmt.Errorf("store: list sync runs: %w", err)
	}
	runs, err := scanAllByName[SyncRun](ctx, s.pool, `
		SELECT `+syncRunSelectColumns+`
		FROM sync_runs
		WHERE item_id = $1
		ORDER BY started_at DESC, run_id DESC
		LIMIT $2`,
		itemID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list sync runs: %w", err)
	}
	return runs, nil
}
