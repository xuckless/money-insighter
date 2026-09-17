package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"plaidsync/internal/money"
)

// This file holds the account half of ApplySyncBatch: the upsert of the
// accounts Plaid listed and the flagging of accounts it no longer lists.
// Both run only inside an item-locked transaction, so they take pgx.Tx.

// upsertAccountSQL writes one account. On conflict every mutable column is
// replaced, last_seen_at is bumped and missing_since is cleared, so an
// account that vanished and came back is healthy again; first_seen_at is
// left alone and updated_at is maintained by the trigger. NUMERIC columns
// receive money.Amount values as text (their driver.Valuer form), which
// Postgres parses exactly. Timestamps use clock_timestamp(), not now():
// see ApplySyncBatch.
const upsertAccountSQL = `
	INSERT INTO plaid_accounts (
		account_id, item_id, name, official_name, mask, type, subtype,
		current_balance, available_balance, credit_limit,
		iso_currency_code, unofficial_currency_code, balance_last_updated_at,
		raw, first_seen_at, last_seen_at, missing_since
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, clock_timestamp(), clock_timestamp(), NULL)
	ON CONFLICT (account_id) DO UPDATE SET
		item_id                  = EXCLUDED.item_id,
		name                     = EXCLUDED.name,
		official_name            = EXCLUDED.official_name,
		mask                     = EXCLUDED.mask,
		type                     = EXCLUDED.type,
		subtype                  = EXCLUDED.subtype,
		current_balance          = EXCLUDED.current_balance,
		available_balance        = EXCLUDED.available_balance,
		credit_limit             = EXCLUDED.credit_limit,
		iso_currency_code        = EXCLUDED.iso_currency_code,
		unofficial_currency_code = EXCLUDED.unofficial_currency_code,
		balance_last_updated_at  = EXCLUDED.balance_last_updated_at,
		raw                      = EXCLUDED.raw,
		last_seen_at             = clock_timestamp(),
		missing_since            = NULL`

// markMissingAccountsSQL flags every account of the item that the batch did
// not list. COALESCE keeps the original missing_since on rows flagged by an
// earlier run; RETURNING lists them all, newly and previously flagged, so
// the engine can log the situation on every run rather than once.
const markMissingAccountsSQL = `
	UPDATE plaid_accounts
	SET missing_since = COALESCE(missing_since, clock_timestamp())
	WHERE item_id = $1 AND account_id <> ALL($2::text[])
	RETURNING account_id`

// validateAccount checks the fields the schema or the batch logic need
// before the row reaches the database, so a bad input produces a clear
// error rather than a constraint violation. Balances are checked for scale
// as a sanity bound: the columns are unconstrained NUMERIC, so nothing is
// rounded, but a value with more than six fractional digits is not money
// Plaid sends and points at a decoding bug.
func validateAccount(a Account) error {
	if a.AccountID == "" {
		return errors.New("account id is empty")
	}
	if len(a.Raw) == 0 {
		return fmt.Errorf("account %s: raw payload is empty", a.AccountID)
	}
	for _, f := range []struct {
		name string
		v    *money.Amount
	}{
		{"current balance", a.CurrentBalance},
		{"available balance", a.AvailableBalance},
		{"credit limit", a.CreditLimit},
	} {
		if err := validateAmountScale(f.name, f.v); err != nil {
			return fmt.Errorf("account %s: %w", a.AccountID, err)
		}
	}
	return nil
}

// maxAmountScale is the most fractional digits the store accepts. The
// money columns are unconstrained NUMERIC (migration 00002; they began as
// NUMERIC(14,2), which Plaid's investment balances exceed), so this is not
// about rounding: it is a sanity bound on what Plaid can plausibly send. Postgres rounds anything finer on insert without an
// error, so the store refuses it instead: the normalised column must never
// disagree with raw.
const maxAmountScale = 6

// validateAmountScale rejects a non-nil amount with more fractional digits
// than maxAmountScale. name labels the field in the error.
func validateAmountScale(name string, v *money.Amount) error {
	if v == nil {
		return nil
	}
	if sc := v.Scale(); sc > maxAmountScale {
		return fmt.Errorf("%s %s has %d fractional digits, more than the %d the schema stores", name, v, sc, maxAmountScale)
	}
	return nil
}

// dedupeAccounts returns accounts with each AccountID appearing once. When
// an id repeats, the LAST occurrence wins (a multi-row upsert must not
// touch one row twice, and later data is fresher); it keeps the position
// of the first occurrence so the output order is stable.
func dedupeAccounts(accounts []Account) []Account {
	out := make([]Account, 0, len(accounts))
	index := make(map[string]int, len(accounts))
	for _, a := range accounts {
		if i, seen := index[a.AccountID]; seen {
			out[i] = a
			continue
		}
		index[a.AccountID] = len(out)
		out = append(out, a)
	}
	return out
}

// upsertAccounts writes accounts (already deduped) for the item in one
// pipelined batch and returns how many rows it wrote. The item_id column is
// always the locked item's, whatever the caller left in Account.ItemID.
func upsertAccounts(ctx context.Context, tx pgx.Tx, itemID string, accounts []Account) (int, error) {
	if len(accounts) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, a := range accounts {
		if err := validateAccount(a); err != nil {
			return 0, err
		}
		batch.Queue(upsertAccountSQL,
			a.AccountID, itemID, a.Name, a.OfficialName, a.Mask, a.Type, a.Subtype,
			a.CurrentBalance, a.AvailableBalance, a.CreditLimit,
			a.ISOCurrencyCode, a.UnofficialCurrencyCode, a.BalanceLastUpdatedAt,
			a.Raw,
		)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return 0, fmt.Errorf("upsert accounts: %w", err)
	}
	return len(accounts), nil
}

// markMissingAccounts flags every account of the item whose id is not in
// seen and returns, sorted, the ids of all accounts currently flagged.
// Callers must not pass an empty seen list: that would flag every account,
// which is why ApplySyncBatch skips this step for a batch with no accounts.
func markMissingAccounts(ctx context.Context, tx pgx.Tx, itemID string, seen []string) ([]string, error) {
	rows, err := tx.Query(ctx, markMissingAccountsSQL, itemID, seen)
	if err != nil {
		return nil, fmt.Errorf("mark missing accounts: %w", err)
	}
	missing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("mark missing accounts: %w", err)
	}
	sort.Strings(missing)
	return missing, nil
}
