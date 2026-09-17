package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNoTopperSchema is returned by [Store.RequireTopperSchema] when schema
// topper does not exist. The schema is created by the operator (see
// deploy/postgres-init/topper.sql), not by the service, because creating it
// needs privileges the topper role deliberately lacks.
var ErrNoTopperSchema = errors.New(`store: schema "topper" does not exist; run deploy/postgres-init/topper.sql as the plaidsync superuser (see README)`)

// plaidSentinelTable is the plaidsync table whose presence proves that
// plaidsync's migrations have run. It is the table of plaidsync's newest
// migration the views read (00004), so seeing it means every table the
// catalog and the views need is there.
const plaidSentinelTable = "public.plaid_recurring_streams"

// waitPollInterval is how often WaitForPlaidSchema re-checks.
const waitPollInterval = time.Second

// RequireTopperSchema fails with [ErrNoTopperSchema] when the topper schema
// is missing. Connection errors are returned as they are.
func (s *Store) RequireTopperSchema(ctx context.Context) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regnamespace('topper') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("store: check topper schema: %w", err)
	}
	if !exists {
		return ErrNoTopperSchema
	}
	return nil
}

// WaitForPlaidSchema blocks until plaidsync's tables exist, polling once a
// second for at most timeout. Connection failures are retried too, so the
// topper can start before Postgres and plaidsync are ready. A zero timeout
// checks exactly once.
func (s *Store) WaitForPlaidSchema(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for attempt := 1; ; attempt++ {
		var exists bool
		err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, plaidSentinelTable).Scan(&exists)
		switch {
		case err == nil && exists:
			if attempt > 1 {
				s.log.InfoContext(ctx, "plaidsync schema present", "attempts", attempt)
			}
			return nil
		case err == nil:
			lastErr = fmt.Errorf("table %s does not exist", plaidSentinelTable)
		case ctx.Err() != nil:
			return fmt.Errorf("store: wait for plaidsync schema: %w", ctx.Err())
		default:
			lastErr = err
		}
		if attempt == 1 {
			s.log.WarnContext(ctx, "waiting for plaidsync schema", "timeout", timeout, "reason", lastErr)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("store: plaidsync schema not present after %s (is plaidsync running its migrations?): %w", timeout, lastErr)
		}
		wait := min(waitPollInterval, remaining)
		select {
		case <-ctx.Done():
			return fmt.Errorf("store: wait for plaidsync schema: %w", ctx.Err())
		case <-time.After(wait):
		}
	}
}
