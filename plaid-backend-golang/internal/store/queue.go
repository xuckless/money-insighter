package store

import (
	"context"
	"fmt"
)

// The methods in this file make sync_jobs a work queue for the in-process
// runner: jobs are created queued (CreateJob), claimed one at a time with
// SKIP LOCKED so several workers never take the same job, and a restart
// puts interrupted jobs back in the queue. The table is the only queue
// state, so a queued job survives a restart of the binary.

// ClaimJob moves the oldest queued job to running, stamps started_at with
// the database clock, and returns it. Concurrent claimers each get a
// different job (FOR UPDATE SKIP LOCKED). It returns ErrNotFound when no
// job is queued.
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	job, err := scanOneByName[Job](ctx, s.pool, `
		UPDATE sync_jobs
		SET state = 'running', started_at = now()
		WHERE job_id = (
			SELECT job_id FROM sync_jobs
			WHERE state = 'queued'
			ORDER BY created_at, job_id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+jobSelectColumns)
	if err != nil {
		return nil, fmt.Errorf("store: claim job: %w", err)
	}
	return job, nil
}

// QueuedJob returns the item's oldest queued job, so a second trigger for
// an item that is already waiting can be answered with the existing job
// instead of a duplicate. It returns ErrNotFound when none is queued.
func (s *Store) QueuedJob(ctx context.Context, itemID string) (*Job, error) {
	job, err := scanOneByName[Job](ctx, s.pool, `
		SELECT `+jobSelectColumns+`
		FROM sync_jobs
		WHERE item_id = $1 AND state = 'queued'
		ORDER BY created_at, job_id
		LIMIT 1`, itemID)
	if err != nil {
		return nil, fmt.Errorf("store: queued job: %w", err)
	}
	return job, nil
}

// RequeueRunningJobs puts every running job back to queued and clears its
// started_at, returning how many it touched. It is for process start: the
// deployment runs one plaidsync process, so a job still marked running
// when the process starts was interrupted by the previous process's exit
// and nobody else can be working on it. Re-running a sync is safe (the
// engine restarts from the saved cursor and every write is an upsert).
func (s *Store) RequeueRunningJobs(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sync_jobs
		SET state = 'queued', started_at = NULL
		WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("store: requeue running jobs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ListSyncableItems returns the items the scheduler should sync: those in
// status active or error. Items waiting for a human (the re-auth states)
// and removed items are left out.
func (s *Store) ListSyncableItems(ctx context.Context) ([]Item, error) {
	items, err := scanAllByName[Item](ctx, s.pool,
		`SELECT `+itemSelectColumns+` FROM plaid_items WHERE status IN ('active', 'error') ORDER BY created_at, item_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list syncable items: %w", err)
	}
	return items, nil
}
