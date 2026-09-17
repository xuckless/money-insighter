// Package sync is the sync engine: one complete /transactions/sync
// pagination of an item, applied to Postgres atomically with the new
// cursor, with retries, error classification and an audit row.
//
// The rules it implements (docs/plaid-canada-research.md section 3.3):
//
//   - Hold the item's advisory lock for the whole pagination, so two syncs
//     of one item can never interleave.
//   - Keep the original cursor in memory, accumulate every page's added,
//     modified and removed rows, and commit them together with the final
//     cursor in one transaction only after has_more is false. Never persist
//     an intermediate cursor.
//   - On any error mid-pagination discard everything and restart from the
//     original cursor, with exponential backoff and a bounded number of
//     attempts, but only for retryable errors.
//   - Use /accounts/get as the account list, because the accounts in a
//     sync response are only the ones with transactions in that response.
//   - Classify failures into the sync_runs outcome buckets and move the
//     item into the matching status.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/plaid"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
)

// CredentialAAD is the additional authenticated data every access token is
// encrypted under: the item id, so a blob copied to another row will not
// decrypt. The API uses it when it stores a new credential and the engine
// when it reads one back.
func CredentialAAD(itemID string) []byte {
	return []byte(itemID)
}

// Engine runs syncs. Build it with New; it is safe for concurrent use and
// SyncItem may run for different items in parallel (the item lock keeps
// the same item from syncing twice).
type Engine struct {
	store *store.Store
	plaid plaid.Client
	keys  *crypto.Keyring
	cfg   config.SyncConfig
	log   *slog.Logger

	// sleep waits for the backoff; tests replace it.
	sleep func(context.Context, time.Duration) error
	// jitter returns a factor in [0.5, 1.5); tests replace it.
	jitter func() float64
}

// New builds an Engine. logger may be nil.
func New(st *store.Store, pc plaid.Client, keys *crypto.Keyring, cfg config.SyncConfig, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		store:  st,
		plaid:  pc,
		keys:   keys,
		cfg:    cfg,
		log:    logger.With("component", "sync"),
		sleep:  sleepCtx,
		jitter: func() float64 { return 0.5 + rand.Float64() },
	}
}

// Result is what one SyncItem call did. Outcome is always set; RunID is 0
// when no sync_runs row could be written (the item does not exist, or the
// database was unreachable); Err is nil exactly when Outcome is success.
type Result struct {
	ItemID  string
	Trigger store.JobKind
	Outcome store.SyncOutcome
	RunID   int64
	Err     error

	// Skipped is set when the engine decided not to call Plaid at all:
	// the item is removed or waiting for a human. Reason says which.
	Skipped bool
	Reason  string

	// Attempts is how many times the pagination was started.
	Attempts int
	// Pages is the number of /transactions/sync calls in the successful
	// attempt, or in the last failed one.
	Pages int
	// Added, Modified and Removed are Plaid's counts across the pages.
	Added, Modified, Removed int
	// UpdateStatus is the transactions_update_status of the last page.
	UpdateStatus string
	// Apply is what the store changed; zero when nothing was applied.
	Apply store.ApplyResult
	// NotReady is set when Plaid answered with an empty cursor: the
	// item's history is not available yet and nothing was written.
	NotReady bool
}

// Succeeded reports whether the sync completed and committed.
func (r *Result) Succeeded() bool { return r.Outcome == store.SyncOutcomeSuccess }

// pagination is the in-memory state of one attempt.
type pagination struct {
	batch        store.SyncBatch
	pages        int
	removed      int
	updateStatus string
}

// SyncItem syncs one item and returns what happened. trigger is recorded
// on the run; jobID, when non-nil, ties the run to a sync_jobs row. It
// never panics on Plaid or database failures: every path ends in a Result
// whose Outcome says what to do next. Only a context cancellation (normally
// shutdown) leaves no run row and no item status change.
func (e *Engine) SyncItem(ctx context.Context, itemID string, trigger store.JobKind, jobID *string) *Result {
	res := &Result{ItemID: itemID, Trigger: trigger, Outcome: store.SyncOutcomeError}
	started := time.Now().UTC()
	log := e.log.With("item_id", itemID, "trigger", string(trigger))

	var (
		statusBefore store.ItemStatus
		cursorBefore *string
		failure      error // the Plaid or decode failure that ended the attempt
		runRecorded  bool
	)

	lockErr := e.store.WithItemLock(ctx, itemID, func(ctx context.Context, tx store.ItemTx) error {
		item := tx.Item()
		statusBefore = item.Status
		cursorBefore = item.Cursor

		switch item.Status {
		case store.ItemStatusRemoved:
			res.Skipped, res.Reason, res.Outcome = true, "item is removed", store.SyncOutcomeFatal
			return nil
		case store.ItemStatusLoginRequired, store.ItemStatusPendingExpiration, store.ItemStatusPermissionRevoked:
			res.Skipped, res.Reason, res.Outcome = true, "item is "+string(item.Status)+"; Link update mode is required", store.SyncOutcomeNeedsReauth
			return nil
		}

		cred, err := tx.Credential(ctx)
		if err != nil {
			return fmt.Errorf("read credential: %w", err)
		}
		token, err := e.keys.Decrypt(cred.Ciphertext, cred.KeyVersion, CredentialAAD(itemID))
		if err != nil {
			// Wrong or missing KEK version: a deployment problem, not a
			// Plaid one. Fatal, and the run says so.
			failure = fmt.Errorf("decrypt access token (key version %d): %w", cred.KeyVersion, err)
			res.Outcome = store.SyncOutcomeFatal
			res.RunID, runRecorded = e.recordFailure(ctx, tx, res, started, cursorBefore, jobID, nil, failure)
			return nil
		}

		orig := ""
		if item.Cursor != nil {
			orig = *item.Cursor
		}
		pg, err := e.paginate(ctx, log, token, orig, res)
		if err != nil {
			if plaid.Classify(err) == plaid.ClassCanceled {
				res.Outcome = store.SyncOutcomeCanceled
				return err
			}
			failure = err
			res.Outcome = plaid.Classify(err).Outcome()
			res.RunID, runRecorded = e.recordFailure(ctx, tx, res, started, cursorBefore, jobID, pg, failure)
			return nil
		}
		res.Pages, res.Added, res.Modified, res.Removed = pg.pages, pg.batch.Added, pg.batch.Modified, pg.removed
		res.UpdateStatus = pg.updateStatus

		if pg.batch.NextCursor == "" {
			// Plaid has not finished the item's first pull yet. Nothing to
			// apply and no cursor to save; SYNC_UPDATES_AVAILABLE will
			// arrive when there is.
			res.NotReady = true
			res.Outcome = store.SyncOutcomeSuccess
			res.RunID, runRecorded = e.recordRun(ctx, tx, store.SyncRun{
				ItemID: itemID, JobID: jobID, Trigger: trigger,
				StartedAt: started, FinishedAt: time.Now().UTC(),
				CursorBefore: cursorBefore, CursorAfter: cursorBefore,
				Pages: pg.pages, Outcome: store.SyncOutcomeSuccess,
			})
			return nil
		}

		accts, err := e.plaid.GetAccounts(ctx, token)
		if err != nil {
			if plaid.Classify(err) == plaid.ClassCanceled {
				res.Outcome = store.SyncOutcomeCanceled
				return err
			}
			failure = fmt.Errorf("accounts: %w", err)
			res.Outcome = plaid.Classify(err).Outcome()
			res.RunID, runRecorded = e.recordFailure(ctx, tx, res, started, cursorBefore, jobID, pg, failure)
			return nil
		}
		pg.batch.Accounts = accts.Accounts

		apply, err := tx.ApplySyncBatch(ctx, pg.batch)
		if err != nil {
			return fmt.Errorf("apply: %w", err)
		}
		res.Apply = apply
		if len(apply.MissingAccountIDs) > 0 {
			log.Warn("accounts no longer listed by Plaid; flagged missing",
				"account_ids", apply.MissingAccountIDs)
		}

		next := pg.batch.NextCursor
		runID, err := tx.RecordSyncRun(ctx, store.SyncRun{
			ItemID: itemID, JobID: jobID, Trigger: trigger,
			StartedAt: started, FinishedAt: time.Now().UTC(),
			CursorBefore: cursorBefore, CursorAfter: &next,
			Pages: pg.pages, Added: pg.batch.Added, Modified: pg.batch.Modified, Removed: pg.removed,
			Inserted: apply.Inserted, Updated: apply.Updated, Superseded: apply.Superseded,
			AccountsSeen: len(accts.Accounts), AccountsMissing: len(apply.MissingAccountIDs),
			Outcome: store.SyncOutcomeSuccess,
		})
		if err != nil {
			return fmt.Errorf("record run: %w", err)
		}
		res.RunID, runRecorded = runID, true
		res.Outcome = store.SyncOutcomeSuccess
		return nil
	})

	switch {
	case lockErr == nil && res.Skipped:
		log.Info("sync skipped", "reason", res.Reason)
		return res
	case lockErr == nil && failure == nil:
		log.Info("sync succeeded",
			"run_id", res.RunID, "attempts", res.Attempts, "pages", res.Pages,
			"added", res.Added, "modified", res.Modified, "removed", res.Removed,
			"inserted", res.Apply.Inserted, "updated", res.Apply.Updated, "unchanged", res.Apply.Unchanged,
			"superseded", res.Apply.Superseded, "update_status", res.UpdateStatus, "not_ready", res.NotReady)
		if e.cfg.RecurringEnabled && !res.NotReady {
			// After the lock is released: the refresh never touches the
			// cursor, and its outcome is recorded apart from the sync's.
			_ = e.RefreshRecurring(ctx, itemID)
		}
		return res
	case lockErr == nil:
		// A Plaid failure, recorded inside the (otherwise empty) committed
		// transaction. Move the item into the matching status.
		res.Err = failure
		e.setItemStatusAfterFailure(ctx, log, itemID, statusBefore, res.Outcome, failure)
		log.Warn("sync failed", "outcome", string(res.Outcome), "attempts", res.Attempts, "run_id", res.RunID, "error", failure)
		return res
	}

	// The lock callback returned an error: the transaction was rolled back.
	res.Err = lockErr
	switch {
	case errors.Is(lockErr, store.ErrItemLocked):
		res.Outcome = store.SyncOutcomeLocked
		log.Info("sync skipped: another sync holds the item lock")
	case errors.Is(lockErr, store.ErrNotFound):
		res.Outcome = store.SyncOutcomeError
		log.Warn("sync failed: item not found")
		return res // no item row to hang a run on
	case res.Outcome == store.SyncOutcomeCanceled || plaid.Classify(lockErr) == plaid.ClassCanceled:
		res.Outcome = store.SyncOutcomeCanceled
		log.Info("sync canceled")
		return res // the context is gone; nothing more can be written
	default:
		res.Outcome = store.SyncOutcomeError
		log.Error("sync failed", "error", lockErr)
	}
	if !runRecorded {
		msg := lockErr.Error()
		runID, err := e.store.RecordSyncRun(ctx, store.SyncRun{
			ItemID: itemID, JobID: jobID, Trigger: trigger,
			StartedAt: started, FinishedAt: time.Now().UTC(),
			CursorBefore: cursorBefore, Pages: res.Pages,
			Outcome: res.Outcome, ErrorMessage: &msg,
		})
		if err != nil {
			log.Error("could not record sync run", "error", err)
		} else {
			res.RunID = runID
		}
	}
	if res.Outcome == store.SyncOutcomeError {
		e.setItemStatusAfterFailure(ctx, log, itemID, statusBefore, res.Outcome, lockErr)
	}
	return res
}

// RefreshRecurring replaces the item's recurring streams with what
// /transactions/recurring/get reports now. It is the recurring add-on's
// only entry point: SyncItem calls it after every successful sync when
// the add-on is enabled, and the job runner calls it for items whose
// streams are stale. A Plaid or decryption failure is recorded on the item
// (recurring_error_*) and returned, but never changes the item's status:
// the add-on not being enabled for the Plaid account is the common case
// and the item itself is healthy. A canceled context records nothing.
func (e *Engine) RefreshRecurring(ctx context.Context, itemID string) error {
	log := e.log.With("item_id", itemID, "op", "recurring")
	fail := func(code string, err error) error {
		if plaid.Classify(err) == plaid.ClassCanceled {
			return err
		}
		msg := err.Error()
		if pe, ok := plaid.AsError(err); ok {
			code = pe.Code
			if pe.Message != "" {
				msg = pe.Message
			}
		}
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if rerr := e.store.RecordRecurringFailure(wctx, itemID, code, msg); rerr != nil {
			log.Error("could not record recurring refresh failure", "error", rerr)
		}
		log.Warn("recurring refresh failed", "code", code, "error", err)
		return err
	}

	cred, err := e.store.GetCredential(ctx, itemID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return err // removed or unknown: nothing to record against
		}
		return fail("credential", err)
	}
	token, err := e.keys.Decrypt(cred.Ciphertext, cred.KeyVersion, CredentialAAD(itemID))
	if err != nil {
		return fail("decrypt", fmt.Errorf("decrypt access token (key version %d): %w", cred.KeyVersion, err))
	}
	streams, err := e.plaid.GetRecurringTransactions(ctx, token)
	if err != nil {
		return fail("plaid", err)
	}
	if err := e.store.ReplaceRecurringStreams(ctx, itemID, streams.Streams); err != nil {
		return fail("store", err)
	}
	log.Info("recurring streams refreshed", "streams", len(streams.Streams))
	return nil
}

// paginate drains /transactions/sync from orig, restarting from orig on a
// retryable error up to cfg.MaxAttempts times. It returns the accumulated
// batch, or the last error together with the partial state for the audit
// row. Removed ids are collected in order; duplicates are harmless.
func (e *Engine) paginate(ctx context.Context, log *slog.Logger, token secret.Token, orig string, res *Result) (*pagination, error) {
	var lastErr error
	var last *pagination
	for attempt := 1; attempt <= e.cfg.MaxAttempts; attempt++ {
		res.Attempts = attempt
		pg := &pagination{}
		cursor := orig
		for {
			page, err := e.plaid.SyncTransactions(ctx, token, cursor)
			if err != nil {
				lastErr = err
				break
			}
			pg.pages++
			pg.updateStatus = page.UpdateStatus
			pg.batch.Upserts = append(pg.batch.Upserts, page.Added...)
			pg.batch.Upserts = append(pg.batch.Upserts, page.Modified...)
			for _, r := range page.Removed {
				pg.batch.Removed = append(pg.batch.Removed, r.TransactionID)
			}
			pg.batch.Added += len(page.Added)
			pg.batch.Modified += len(page.Modified)
			pg.removed += len(page.Removed)
			pg.batch.NextCursor = page.NextCursor
			if !page.HasMore {
				return pg, nil
			}
			if page.NextCursor == "" {
				lastErr = &plaid.Error{Endpoint: "/transactions/sync", Type: "INVALID_RESULT", Code: "EMPTY_CURSOR_WITH_HAS_MORE",
					Message: "Plaid reported has_more with an empty next_cursor", RequestID: page.RequestID}
				break
			}
			cursor = page.NextCursor
		}
		last = pg
		class := plaid.Classify(lastErr)
		if class != plaid.ClassRetryable || attempt == e.cfg.MaxAttempts {
			return last, lastErr
		}
		delay := e.backoff(attempt)
		log.Warn("sync attempt failed; restarting pagination from the original cursor",
			"attempt", attempt, "max_attempts", e.cfg.MaxAttempts, "pages", pg.pages, "retry_in", delay, "error", lastErr)
		if err := e.sleep(ctx, delay); err != nil {
			return last, err
		}
	}
	return last, lastErr
}

// backoff is the wait before attempt+1: RetryBase doubled per attempt,
// capped at RetryMax, with ±50% jitter so parallel items do not retry in
// lockstep after a shared outage.
func (e *Engine) backoff(attempt int) time.Duration {
	d := e.cfg.RetryBase
	for i := 1; i < attempt && d < e.cfg.RetryMax; i++ {
		d *= 2
	}
	if d > e.cfg.RetryMax {
		d = e.cfg.RetryMax
	}
	d = time.Duration(float64(d) * e.jitter())
	if d > e.cfg.RetryMax {
		d = e.cfg.RetryMax
	}
	return d
}

// recordFailure writes the audit row for a failed attempt inside the item
// transaction. A failure to write it is logged; the sync result stands.
func (e *Engine) recordFailure(ctx context.Context, tx store.ItemTx, res *Result, started time.Time, cursorBefore *string, jobID *string, pg *pagination, failure error) (int64, bool) {
	run := store.SyncRun{
		ItemID: res.ItemID, JobID: jobID, Trigger: res.Trigger,
		StartedAt: started, FinishedAt: time.Now().UTC(),
		CursorBefore: cursorBefore, Outcome: res.Outcome,
	}
	if pg != nil {
		run.Pages, run.Added, run.Modified, run.Removed = pg.pages, pg.batch.Added, pg.batch.Modified, pg.removed
		res.Pages, res.Added, res.Modified, res.Removed = pg.pages, pg.batch.Added, pg.batch.Modified, pg.removed
		res.UpdateStatus = pg.updateStatus
	}
	msg := failure.Error()
	run.ErrorMessage = &msg
	if pe, ok := plaid.AsError(failure); ok {
		if pe.Code != "" {
			c := pe.Code
			run.ErrorCode = &c
		}
		if pe.Type != "" {
			t := pe.Type
			run.ErrorType = &t
		}
		if pe.RequestID != "" {
			r := pe.RequestID
			run.RequestID = &r
		}
		m := pe.Message
		if m == "" {
			m = failure.Error()
		}
		run.ErrorMessage = &m
	}
	return e.recordRun(ctx, tx, run)
}

// recordRun writes a run row through tx and reports whether it succeeded.
func (e *Engine) recordRun(ctx context.Context, tx store.ItemTx, run store.SyncRun) (int64, bool) {
	id, err := tx.RecordSyncRun(ctx, run)
	if err != nil {
		e.log.ErrorContext(ctx, "could not record sync run", "item_id", run.ItemID, "error", err)
		return 0, false
	}
	return id, true
}

// setItemStatusAfterFailure moves the item into the status the failure
// implies and records the error on the row: the re-auth state for
// needs_reauth, error for fatal and error outcomes, and for a retryable
// error that exhausted its attempts the status is left as it was (the
// institution or Plaid was unavailable; the item itself is fine) with the
// error still recorded so operators can see it.
func (e *Engine) setItemStatusAfterFailure(ctx context.Context, log *slog.Logger, itemID string, before store.ItemStatus, outcome store.SyncOutcome, failure error) {
	ie := &store.ItemError{Message: failure.Error(), At: time.Now().UTC()}
	status := store.ItemStatusError
	if pe, ok := plaid.AsError(failure); ok {
		ie.Code, ie.Type = pe.Code, pe.Type
		if pe.Message != "" {
			ie.Message = pe.Message
		}
		if outcome == store.SyncOutcomeNeedsReauth {
			status = pe.ItemStatus()
		}
	}
	if outcome == store.SyncOutcomeRetryableError {
		status = before
		if status == store.ItemStatusRemoved {
			return
		}
	}
	// Status writes must outlive a canceled request context: the sync has
	// already happened and its outcome must be visible.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := e.store.SetItemStatus(wctx, itemID, status, ie); err != nil {
		log.Error("could not update item status after failed sync", "status", string(status), "error", err)
	}
}

// sleepCtx waits for d or until ctx is done, whichever is first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
