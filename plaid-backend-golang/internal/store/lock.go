package store

import (
	"context"
	"errors"
	"fmt"
)

// advisoryLockNamespace is the first argument of the two-int form of
// pg_try_advisory_xact_lock. Hashing a fixed string rather than using 0
// keeps this service's locks from colliding with anything else that uses
// advisory locks on the same database (goose, for one, takes a session
// lock on a different key while migrating).
const advisoryLockNamespace = "plaidsync:item"

// WithItemLock runs fn inside one transaction that holds the item's
// advisory lock for its whole lifetime.
//
// It begins a transaction, tries pg_try_advisory_xact_lock on
// (hashtext('plaidsync:item'), hashtext(itemID)) and, when the lock is
// already held by another transaction, rolls back and returns ErrItemLocked
// at once: it never waits. Otherwise it loads the item with SELECT ... FOR
// NO KEY UPDATE (ErrNotFound when there is no such item, the lock released
// by the rollback) and calls fn with an ItemTx bound to the transaction. fn
// returning nil commits; any error, a panic, or a failed commit rolls back
// and the lock goes with the transaction. Errors are wrapped; test them with
// errors.Is.
//
// The row lock is FOR NO KEY UPDATE rather than FOR UPDATE on purpose. Both
// serialise against every UPDATE of the row (UpsertItem, SetItemStatus,
// MarkItemRemoved, UpdateCredential all wait for the sync to finish), but
// FOR UPDATE also conflicts with the FOR KEY SHARE lock that a foreign-key
// check takes on the parent row, so every INSERT into sync_jobs, sync_runs,
// plaid_accounts or transactions for the item from another session would
// hang for the whole pagination. That would defeat "already running, do
// not block": the second sync's locked audit row and the API's 202 job
// creation must go through while a sync is in flight.
//
// The transaction, and so the lock, deliberately stays open for everything
// fn does, including the caller's calls to Plaid: two syncs paginating the
// same item from the same cursor would corrupt its state, so the lock must
// cover the pagination, not only the write at the end. The pool is sized
// with that in mind.
func (s *Store) WithItemLock(ctx context.Context, itemID string, fn func(ctx context.Context, tx ItemTx) error) (err error) {
	if itemID == "" {
		return errors.New("store: with item lock: item id is empty")
	}

	tx, err := s.beginTx(ctx)
	if err != nil {
		return fmt.Errorf("store: with item lock: %w", err)
	}
	committed := false
	defer func() {
		// Runs on every exit including a panic in fn, so the lock is never
		// left behind: rolling back (or, at worst, returning a broken
		// connection to the pool, which closes it) ends the transaction.
		if !committed {
			s.rollback(ctx, tx)
		}
	}()

	var locked bool
	err = tx.QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext($1), hashtext($2))`,
		advisoryLockNamespace, itemID,
	).Scan(&locked)
	if err != nil {
		return fmt.Errorf("store: with item lock: acquire lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("store: with item lock: %w", ErrItemLocked)
	}

	item, err := scanOneByName[Item](ctx, tx,
		`SELECT `+itemSelectColumns+` FROM plaid_items WHERE item_id = $1 FOR NO KEY UPDATE`, itemID)
	if err != nil {
		return fmt.Errorf("store: with item lock: load item: %w", err)
	}

	itx := &itemTx{s: s, tx: tx, item: item}
	if err := fn(ctx, itx); err != nil {
		return fmt.Errorf("store: with item lock: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: with item lock: commit: %w", err)
	}
	committed = true
	return nil
}
