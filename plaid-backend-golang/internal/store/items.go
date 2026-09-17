package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// itemSelectColumns is the column list that maps onto Item. It deliberately
// omits encrypted_access_token and key_version: an Item never carries the
// credential, so nothing that reads through this list can leak it.
const itemSelectColumns = `
	item_id, institution_id, institution_name, cursor, status,
	last_error_code, last_error_type, last_error_message, last_error_at,
	last_successful_sync_at, consent_expires_at, raw, created_at, updated_at`

// accountSelectColumns is the column list that maps onto Account.
const accountSelectColumns = `
	account_id, item_id, name, official_name, mask, type, subtype,
	current_balance, available_balance, credit_limit,
	iso_currency_code, unofficial_currency_code, balance_last_updated_at,
	raw, first_seen_at, last_seen_at, missing_since, updated_at`

// transactionSelectColumns is the column list that maps onto Transaction.
const transactionSelectColumns = `
	transaction_id, account_id, item_id, amount,
	iso_currency_code, unofficial_currency_code,
	date, authorized_date, datetime, authorized_datetime,
	name, merchant_name, merchant_entity_id,
	pending, pending_transaction_id,
	pfc_primary, pfc_detailed, pfc_confidence, payment_channel, transaction_code,
	raw, superseded_by, superseded_at, removed_at, first_seen_at, updated_at`

// validItemStatus reports whether s is one of the statuses the schema's
// CHECK constraint allows. The database enforces this too; checking first
// gives a clearer error than a constraint violation.
func validItemStatus(s ItemStatus) bool {
	switch s {
	case ItemStatusActive, ItemStatusLoginRequired, ItemStatusPendingExpiration,
		ItemStatusPermissionRevoked, ItemStatusError, ItemStatusRemoved:
		return true
	}
	return false
}

// validateCredential checks that c can be stored: a non-empty blob and a
// key version greater than zero. The schema only forbids NULL, and an empty
// ciphertext would satisfy it while being undecryptable.
func validateCredential(c Credential) error {
	if len(c.Ciphertext) == 0 {
		return errors.New("store: credential ciphertext is empty")
	}
	if c.KeyVersion == 0 {
		return errors.New("store: credential key version must be greater than zero")
	}
	return nil
}

// UpsertItem records a newly linked (or re-linked) item. On first sight it
// inserts an active row. When the item already exists, whatever its status,
// it replaces the credential and consent expiry, resets the status to
// active and clears last_error_*, which is what a successful Link update
// mode flow means. It keeps the sync cursor and last_successful_sync_at:
// Plaid keeps the item's history across re-authentication, so the next sync
// carries on from where it left off.
//
// InstitutionID, InstitutionName and Raw are replaced only when the caller
// supplies them; a nil value leaves the stored one in place rather than
// wiping known information.
func (s *Store) UpsertItem(ctx context.Context, n NewItem) error {
	if n.ItemID == "" {
		return errors.New("store: upsert item: item id is empty")
	}
	if err := validateCredential(n.Credential); err != nil {
		return fmt.Errorf("store: upsert item: %w", err)
	}

	const q = `
		INSERT INTO plaid_items (
			item_id, institution_id, institution_name,
			encrypted_access_token, key_version, consent_expires_at, raw, status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active')
		ON CONFLICT (item_id) DO UPDATE SET
			institution_id         = COALESCE(EXCLUDED.institution_id, plaid_items.institution_id),
			institution_name       = COALESCE(EXCLUDED.institution_name, plaid_items.institution_name),
			encrypted_access_token = EXCLUDED.encrypted_access_token,
			key_version            = EXCLUDED.key_version,
			consent_expires_at     = EXCLUDED.consent_expires_at,
			raw                    = COALESCE(EXCLUDED.raw, plaid_items.raw),
			status                 = 'active',
			last_error_code        = NULL,
			last_error_type        = NULL,
			last_error_message     = NULL,
			last_error_at          = NULL`
	_, err := s.pool.Exec(ctx, q,
		n.ItemID, n.InstitutionID, n.InstitutionName,
		n.Credential.Ciphertext, n.Credential.KeyVersion, n.ConsentExpiresAt, n.Raw,
	)
	if err != nil {
		return fmt.Errorf("store: upsert item: %w", err)
	}
	return nil
}

// GetItem returns the item with the given id in any status, without its
// credential. It returns ErrNotFound when no such row exists.
func (s *Store) GetItem(ctx context.Context, itemID string) (*Item, error) {
	item, err := scanOneByName[Item](ctx, s.pool,
		`SELECT `+itemSelectColumns+` FROM plaid_items WHERE item_id = $1`, itemID)
	if err != nil {
		return nil, fmt.Errorf("store: get item: %w", err)
	}
	return item, nil
}

// ListItems returns every item, including removed ones, oldest first.
// Callers that only want syncable items filter on Status.
func (s *Store) ListItems(ctx context.Context) ([]Item, error) {
	items, err := scanAllByName[Item](ctx, s.pool,
		`SELECT `+itemSelectColumns+` FROM plaid_items ORDER BY created_at, item_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list items: %w", err)
	}
	return items, nil
}

// GetCredential returns the item's encrypted access token. It returns
// ErrNotFound when the item does not exist or has been removed (its
// credential is NULL), so callers cannot distinguish the two and do not
// need to: neither can be synced.
func (s *Store) GetCredential(ctx context.Context, itemID string) (*Credential, error) {
	c, err := scanOneByName[Credential](ctx, s.pool, `
		SELECT encrypted_access_token, key_version
		FROM plaid_items
		WHERE item_id = $1 AND encrypted_access_token IS NOT NULL`, itemID)
	if err != nil {
		return nil, fmt.Errorf("store: get credential: %w", err)
	}
	return c, nil
}

// UpdateCredential replaces the item's encrypted access token, for
// re-encryption after a KEK rotation. It changes nothing else. It returns
// ErrNotFound when the item does not exist or has been removed; a removed
// item has no credential to rotate and must never get one back this way.
func (s *Store) UpdateCredential(ctx context.Context, itemID string, c Credential) error {
	if err := validateCredential(c); err != nil {
		return fmt.Errorf("store: update credential: %w", err)
	}
	err := rowsAffectedOrNotFound(s.pool.Exec(ctx, `
		UPDATE plaid_items
		SET encrypted_access_token = $2, key_version = $3
		WHERE item_id = $1 AND encrypted_access_token IS NOT NULL`,
		itemID, c.Ciphertext, c.KeyVersion))
	if err != nil {
		return fmt.Errorf("store: update credential: %w", err)
	}
	return nil
}

// SetItemStatus sets the item's status and, when e is not nil, records it
// in last_error_* (e.At defaults to now when zero). With e nil the
// last_error_* columns are left as they are, so moving an item back to
// active keeps the record of what last went wrong until the next
// successful sync clears it.
//
// The status must not be removed: use MarkItemRemoved, which also purges
// the credential (the schema ties the two together). It returns ErrNotFound
// when the item does not exist or has already been removed; a removed item
// has no lifecycle left.
func (s *Store) SetItemStatus(ctx context.Context, itemID string, status ItemStatus, e *ItemError) error {
	if !validItemStatus(status) {
		return fmt.Errorf("store: set item status: unknown status %q", status)
	}
	if status == ItemStatusRemoved {
		return errors.New("store: set item status: use MarkItemRemoved to remove an item")
	}

	var err error
	if e == nil {
		err = rowsAffectedOrNotFound(s.pool.Exec(ctx, `
			UPDATE plaid_items
			SET status = $2
			WHERE item_id = $1 AND status <> 'removed'`,
			itemID, status))
	} else {
		var at *time.Time
		if !e.At.IsZero() {
			t := e.At
			at = &t
		}
		err = rowsAffectedOrNotFound(s.pool.Exec(ctx, `
			UPDATE plaid_items
			SET status = $2,
			    last_error_code = $3,
			    last_error_type = $4,
			    last_error_message = $5,
			    last_error_at = COALESCE($6, now())
			WHERE item_id = $1 AND status <> 'removed'`,
			itemID, status, e.Code, e.Type, e.Message, at))
	}
	if err != nil {
		return fmt.Errorf("store: set item status: %w", err)
	}
	return nil
}

// MarkItemRemoved records that /item/remove succeeded: the status becomes
// removed and the encrypted access token and key version are set to NULL.
// The row itself, its cursor, its last error and every account and
// transaction stay for history. It returns ErrNotFound when the item does
// not exist. Removing an already removed item is a no-op that succeeds.
func (s *Store) MarkItemRemoved(ctx context.Context, itemID string) error {
	err := rowsAffectedOrNotFound(s.pool.Exec(ctx, `
		UPDATE plaid_items
		SET status = 'removed', encrypted_access_token = NULL, key_version = NULL
		WHERE item_id = $1`,
		itemID))
	if err != nil {
		return fmt.Errorf("store: mark item removed: %w", err)
	}
	return nil
}

// ListAccounts returns every account of the item, including ones currently
// flagged missing, ordered by account_id. An unknown item yields an empty
// list, not an error.
func (s *Store) ListAccounts(ctx context.Context, itemID string) ([]Account, error) {
	accounts, err := scanAllByName[Account](ctx, s.pool,
		`SELECT `+accountSelectColumns+` FROM plaid_accounts WHERE item_id = $1 ORDER BY account_id`,
		itemID)
	if err != nil {
		return nil, fmt.Errorf("store: list accounts: %w", err)
	}
	return accounts, nil
}

// GetTransaction returns one transaction by id, whether or not it has been
// removed or superseded. It returns ErrNotFound when no such row exists.
func (s *Store) GetTransaction(ctx context.Context, transactionID string) (*Transaction, error) {
	tx, err := scanOneByName[Transaction](ctx, s.pool,
		`SELECT `+transactionSelectColumns+` FROM transactions WHERE transaction_id = $1`,
		transactionID)
	if err != nil {
		return nil, fmt.Errorf("store: get transaction: %w", err)
	}
	return tx, nil
}

// ListTransactions returns a page of the item's transactions, newest date
// first with transaction_id as the tie-breaker so paging is stable. Removed
// and superseded rows are included; consumers filter on RemovedAt and
// SupersededBy. limit must be in 1..1000 and offset non-negative.
func (s *Store) ListTransactions(ctx context.Context, itemID string, limit, offset int) ([]Transaction, error) {
	if err := checkLimit(limit); err != nil {
		return nil, fmt.Errorf("store: list transactions: %w", err)
	}
	if err := checkOffset(offset); err != nil {
		return nil, fmt.Errorf("store: list transactions: %w", err)
	}
	txs, err := scanAllByName[Transaction](ctx, s.pool, `
		SELECT `+transactionSelectColumns+`
		FROM transactions
		WHERE item_id = $1
		ORDER BY date DESC, transaction_id
		LIMIT $2 OFFSET $3`,
		itemID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list transactions: %w", err)
	}
	return txs, nil
}
