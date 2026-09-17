package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrJobState is returned by StartJob and FinishJob when the job exists but
// is not in a state the transition is allowed from: StartJob needs queued,
// FinishJob needs queued or running. A job's terminal state is written
// exactly once, so a second FinishJob cannot rewrite the audit trail.
var ErrJobState = errors.New("store: job is not in the expected state")

// jobSelectColumns is the column list that maps onto Job. job_id is a UUID
// column; pgx renders it as canonical lowercase text when it is scanned
// into a string, which is the form every job id in this package uses.
const jobSelectColumns = `
	job_id, item_id, kind, state, created_at, started_at, finished_at,
	error_code, error_message`

// validJobKind reports whether k is one of the kinds the schema's CHECK
// constraint allows. The database enforces this too; checking first gives a
// clearer error than a constraint violation.
func validJobKind(k JobKind) bool {
	switch k {
	case JobKindInitial, JobKindManual, JobKindWebhook, JobKindScheduled:
		return true
	}
	return false
}

// CreateJob records a new queued sync job for the item and returns it as
// stored. The job id is a fresh v4 UUID from crypto/rand. It returns
// ErrNotFound when the item does not exist.
//
// The item's status is not checked: whether a removed or errored item may
// be synced is the caller's decision, and a job that ends up skipped is
// still worth recording so the client polling it learns why.
func (s *Store) CreateJob(ctx context.Context, itemID string, kind JobKind) (*Job, error) {
	if itemID == "" {
		return nil, errors.New("store: create job: item id is empty")
	}
	if !validJobKind(kind) {
		return nil, fmt.Errorf("store: create job: unknown job kind %q", kind)
	}
	id, err := newUUID()
	if err != nil {
		return nil, fmt.Errorf("store: create job: %w", err)
	}

	job, err := scanOneByName[Job](ctx, s.pool, `
		INSERT INTO sync_jobs (job_id, item_id, kind, state)
		VALUES ($1, $2, $3, 'queued')
		RETURNING `+jobSelectColumns,
		id, itemID, string(kind))
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, fmt.Errorf("store: create job: item %q: %w", itemID, ErrNotFound)
		}
		return nil, fmt.Errorf("store: create job: %w", err)
	}
	return job, nil
}

// GetJob returns the job with the given id. It returns ErrNotFound when no
// such job exists, and also when jobID is not a well-formed UUID: ids come
// straight from URL paths, and a malformed one names nothing.
func (s *Store) GetJob(ctx context.Context, jobID string) (*Job, error) {
	id, ok := normalizeUUID(jobID)
	if !ok {
		return nil, fmt.Errorf("store: get job: malformed id: %w", ErrNotFound)
	}
	job, err := scanOneByName[Job](ctx, s.pool,
		`SELECT `+jobSelectColumns+` FROM sync_jobs WHERE job_id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("store: get job: %w", err)
	}
	return job, nil
}

// StartJob moves a queued job to running and stamps started_at with the
// database clock. It returns ErrNotFound when no such job exists and
// ErrJobState when the job has already been started or finished.
func (s *Store) StartJob(ctx context.Context, jobID string) error {
	id, ok := normalizeUUID(jobID)
	if !ok {
		return fmt.Errorf("store: start job: malformed id: %w", ErrNotFound)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE sync_jobs
		SET state = 'running', started_at = now()
		WHERE job_id = $1 AND state = 'queued'`,
		id)
	if err != nil {
		return fmt.Errorf("store: start job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: start job: %w", s.explainJobMiss(ctx, id))
	}
	return nil
}

// FinishJob moves a queued or running job to a terminal state, stamps
// finished_at with the database clock, and records the error code and
// message (nil clears them). state must be succeeded, failed or skipped;
// anything else is rejected before the database is touched. A queued job
// may finish without ever starting (skipped because the item was locked or
// the request was debounced). It returns ErrNotFound when no such job
// exists and ErrJobState when the job has already finished.
func (s *Store) FinishJob(ctx context.Context, jobID string, state JobState, errCode, errMsg *string) error {
	if !state.IsTerminal() {
		return fmt.Errorf("store: finish job: %q is not a terminal state", state)
	}
	id, ok := normalizeUUID(jobID)
	if !ok {
		return fmt.Errorf("store: finish job: malformed id: %w", ErrNotFound)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE sync_jobs
		SET state = $2, finished_at = now(), error_code = $3, error_message = $4
		WHERE job_id = $1 AND state IN ('queued', 'running')`,
		id, string(state), errCode, errMsg)
	if err != nil {
		return fmt.Errorf("store: finish job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: finish job: %w", s.explainJobMiss(ctx, id))
	}
	return nil
}

// explainJobMiss says why a state-guarded job UPDATE matched no row: the
// job does not exist (ErrNotFound) or it is in a state the transition does
// not start from (ErrJobState, naming the actual state). It costs a second
// round trip, but only on the failure path.
func (s *Store) explainJobMiss(ctx context.Context, id string) error {
	var state string
	err := s.pool.QueryRow(ctx, `SELECT state FROM sync_jobs WHERE job_id = $1`, id).Scan(&state)
	if err != nil {
		return notFoundIfNoRows(err)
	}
	return fmt.Errorf("%w: job is %s", ErrJobState, state)
}

// ListJobs returns the item's most recent jobs, newest first, at most limit
// of them (1..1000). An unknown item yields an empty list, not an error.
func (s *Store) ListJobs(ctx context.Context, itemID string, limit int) ([]Job, error) {
	if err := checkLimit(limit); err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	jobs, err := scanAllByName[Job](ctx, s.pool, `
		SELECT `+jobSelectColumns+`
		FROM sync_jobs
		WHERE item_id = $1
		ORDER BY created_at DESC, job_id
		LIMIT $2`,
		itemID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	return jobs, nil
}
