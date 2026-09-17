package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrItemRemoved is returned by ItemTx.ApplySyncBatch when the locked item
// has status removed. A removed item has no credential and must never be
// synced or resurrected; the caller should treat it as a fatal condition
// for that item rather than retry.
var ErrItemRemoved = errors.New("store: item removed")

// itemTx is the ItemTx handed to WithItemLock's callback. It carries the
// transaction that holds the advisory lock and the item as loaded inside
// it. It is not safe for concurrent use: one transaction, one goroutine.
type itemTx struct {
	s    *Store
	tx   pgx.Tx
	item *Item
}

var _ ItemTx = (*itemTx)(nil)

// Item returns the item as loaded under the lock. After a successful
// ApplySyncBatch it reflects the new cursor, status and sync time.
func (t *itemTx) Item() *Item {
	return t.item
}

// Credential reads the encrypted access token inside the transaction. It
// returns ErrNotFound when the item has been removed (its credential is
// NULL). The row is already locked FOR NO KEY UPDATE, so the value cannot
// change under the caller for the life of the transaction.
func (t *itemTx) Credential(ctx context.Context) (*Credential, error) {
	c, err := scanOneByName[Credential](ctx, t.tx, `
		SELECT encrypted_access_token, key_version
		FROM plaid_items
		WHERE item_id = $1 AND encrypted_access_token IS NOT NULL`, t.item.ItemID)
	if err != nil {
		return nil, fmt.Errorf("store: credential: %w", err)
	}
	return c, nil
}

// updateItemCursorSQL is the final step of ApplySyncBatch: the cursor and
// the rows commit together or not at all. It also records the successful
// sync and clears any earlier error. The status guard means a row that was
// removed can never be flipped back to active here; the caller checks the
// status first so this is belt and braces.
const updateItemCursorSQL = `
	UPDATE plaid_items
	SET cursor = $2,
	    last_successful_sync_at = clock_timestamp(),
	    status = 'active',
	    last_error_code = NULL,
	    last_error_type = NULL,
	    last_error_message = NULL,
	    last_error_at = NULL
	WHERE item_id = $1 AND status <> 'removed'
	RETURNING ` + itemSelectColumns

// ApplySyncBatch writes one complete pagination of /transactions/sync in
// the item's transaction, in this order:
//
//  1. Upsert the accounts Plaid listed (deduped by id, last wins).
//  2. If the batch listed any account, flag the item's other accounts as
//     missing and report every currently flagged account id.
//  3. Upsert added and modified transactions (deduped by id, last wins);
//     an identical replay is a no-op counted as Unchanged.
//  4. Soft-delete removed transactions; unknown ids are ignored and a
//     second removal keeps the original removed_at.
//  5. Link pending rows to their posted successors, in both arrival
//     orders.
//  6. Save the cursor, stamp last_successful_sync_at, set the status to
//     active and clear last_error_*.
//
// Nothing is persisted until WithItemLock commits, so a failure anywhere,
// including between step 5 and step 6, leaves the cursor and the rows
// exactly as they were: there is no window in which rows are committed
// without their cursor or the cursor without its rows.
//
// Timestamps: every column this method stamps (last_successful_sync_at,
// first_seen_at, last_seen_at, missing_since, removed_at, superseded_at)
// uses clock_timestamp(), the wall clock at the statement, rather than
// now(). In Postgres now() is the transaction start, which for an
// item-locked transaction is the moment WithItemLock began, before any
// Plaid page was fetched; a 24-month initial sync would otherwise record a
// "successful sync" minutes before it finished, and the step-6 debounce
// would under-count the interval. The trigger-maintained updated_at
// columns still use now() and so carry the transaction start.
//
// It returns ErrItemRemoved without writing anything when the item's
// status is removed.
func (t *itemTx) ApplySyncBatch(ctx context.Context, b SyncBatch) (ApplyResult, error) {
	var res ApplyResult

	if t.item.Status == ItemStatusRemoved {
		return res, fmt.Errorf("store: apply sync batch: %w", ErrItemRemoved)
	}
	if b.NextCursor == "" {
		return res, errors.New("store: apply sync batch: next cursor is empty")
	}
	itemID := t.item.ItemID

	// 1. Accounts.
	accounts := dedupeAccounts(b.Accounts)
	n, err := upsertAccounts(ctx, t.tx, itemID, accounts)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("store: apply sync batch: %w", err)
	}
	res.AccountsUpserted = n

	// 2. Missing accounts, only when the batch actually described accounts.
	if len(accounts) > 0 {
		seen := make([]string, 0, len(accounts))
		for _, a := range accounts {
			seen = append(seen, a.AccountID)
		}
		res.MissingAccountIDs, err = markMissingAccounts(ctx, t.tx, itemID, seen)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("store: apply sync batch: %w", err)
		}
	}

	// 3. Transactions.
	upserts := dedupeTransactions(b.Upserts)
	counts, err := upsertTransactions(ctx, t.tx, itemID, upserts)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("store: apply sync batch: %w", err)
	}
	res.Inserted, res.Updated, res.Unchanged = counts.inserted, counts.updated, counts.unchanged

	// 4. Soft deletes.
	res.Removed, err = softDeleteTransactions(ctx, t.tx, itemID, b.Removed)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("store: apply sync batch: %w", err)
	}

	// 5. Pending reconciliation.
	res.Superseded, err = linkPendingTransactions(ctx, t.tx, itemID, upserts)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("store: apply sync batch: %w", err)
	}

	if t.s.testHookBeforeCursorUpdate != nil {
		if err := t.s.testHookBeforeCursorUpdate(ctx); err != nil {
			return ApplyResult{}, fmt.Errorf("store: apply sync batch: %w", err)
		}
	}

	// 6. Cursor, in the same transaction as everything above.
	updated, err := scanOneByName[Item](ctx, t.tx, updateItemCursorSQL, itemID, b.NextCursor)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The row is locked FOR NO KEY UPDATE, so it cannot have been
			// removed underneath us; this only fires if the status check
			// above was somehow bypassed.
			err = ErrItemRemoved
		}
		return ApplyResult{}, fmt.Errorf("store: apply sync batch: update cursor: %w", err)
	}
	*t.item = *updated

	t.s.log.DebugContext(ctx, "applied sync batch",
		"item_id", itemID,
		"accounts_upserted", res.AccountsUpserted,
		"accounts_missing", len(res.MissingAccountIDs),
		"inserted", res.Inserted,
		"updated", res.Updated,
		"unchanged", res.Unchanged,
		"removed", res.Removed,
		"superseded", res.Superseded,
	)
	return res, nil
}

// RecordSyncRun inserts the audit row for this run inside the item's
// transaction, so it commits with the rows and cursor it describes, and
// returns the assigned run_id. Runs that never reach a transaction (lock
// held, item not found, Plaid failures before the write) use
// Store.RecordSyncRun instead. Both paths share insertSyncRun (runs.go).
func (t *itemTx) RecordSyncRun(ctx context.Context, r SyncRun) (int64, error) {
	return insertSyncRun(ctx, t.tx, r)
}
