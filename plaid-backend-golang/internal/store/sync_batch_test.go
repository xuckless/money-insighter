package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"plaidsync/internal/civil"
	"plaidsync/internal/money"
)

// ---- helpers -----------------------------------------------------------

// newAcct builds an Account for tests. Raw is derived from the fields after
// the mutators run, so two accounts with different fields have different
// raw payloads.
func newAcct(id string, mut ...func(*Account)) Account {
	cad := "CAD"
	a := Account{
		AccountID:       id,
		Name:            "Account " + id,
		Type:            "credit",
		ISOCurrencyCode: &cad,
	}
	for _, m := range mut {
		m(&a)
	}
	a.Raw = mustJSON(map[string]any{
		"account_id": a.AccountID,
		"name":       a.Name,
		"type":       a.Type,
		"balances": map[string]any{
			"current":   a.CurrentBalance,
			"available": a.AvailableBalance,
			"limit":     a.CreditLimit,
		},
	})
	return a
}

// newTx builds a Transaction for tests. Raw is derived from the fields
// after the mutators run: the upsert's no-op check compares raw, so a test
// that changes a field gets a different raw payload for free.
func newTx(id, accountID, amount, date string, mut ...func(*Transaction)) Transaction {
	cad := "CAD"
	tr := Transaction{
		TransactionID:   id,
		AccountID:       accountID,
		Amount:          money.MustParse(amount),
		ISOCurrencyCode: &cad,
		Date:            civil.MustParseDate(date),
		Name:            "Transaction " + id,
		Pending:         false,
	}
	for _, m := range mut {
		m(&tr)
	}
	tr.Raw = mustJSON(map[string]any{
		"transaction_id":           tr.TransactionID,
		"account_id":               tr.AccountID,
		"amount":                   tr.Amount,
		"iso_currency_code":        tr.ISOCurrencyCode,
		"unofficial_currency_code": tr.UnofficialCurrencyCode,
		"date":                     tr.Date,
		"authorized_date":          tr.AuthorizedDate,
		"name":                     tr.Name,
		"merchant_name":            tr.MerchantName,
		"pending":                  tr.Pending,
		"pending_transaction_id":   tr.PendingTransactionID,
	})
	return tr
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func strp(s string) *string { return &s }

func amtp(s string) *money.Amount {
	a := money.MustParse(s)
	return &a
}

// tryApply applies b to itemID through WithItemLock.
func tryApply(t *testing.T, s *Store, itemID string, b SyncBatch) (ApplyResult, error) {
	t.Helper()
	var res ApplyResult
	err := s.WithItemLock(testCtx(t), itemID, func(ctx context.Context, tx ItemTx) error {
		var err error
		res, err = tx.ApplySyncBatch(ctx, b)
		return err
	})
	return res, err
}

// apply is tryApply for batches that must succeed.
func apply(t *testing.T, s *Store, itemID string, b SyncBatch) ApplyResult {
	t.Helper()
	res, err := tryApply(t, s, itemID, b)
	if err != nil {
		t.Fatalf("ApplySyncBatch(cursor %q): %v", b.NextCursor, err)
	}
	return res
}

// itemState is the part of plaid_items a batch changes.
type itemState struct {
	Cursor               *string
	Status               string
	LastSuccessfulSyncAt *time.Time
	LastErrorCode        *string
}

func queryItem(t *testing.T, s *Store, itemID string) itemState {
	t.Helper()
	var st itemState
	err := s.pool.QueryRow(testCtx(t), `
		SELECT cursor, status, last_successful_sync_at, last_error_code
		FROM plaid_items WHERE item_id = $1`, itemID).
		Scan(&st.Cursor, &st.Status, &st.LastSuccessfulSyncAt, &st.LastErrorCode)
	if err != nil {
		t.Fatalf("query item %s: %v", itemID, err)
	}
	return st
}

// txRow is a transactions row as the database renders it, read with raw
// SQL so the assertions do not depend on the store's own scanners.
type txRow struct {
	ItemID, AccountID      string
	AmountText, DateText   string
	AuthorizedDateText     *string
	ISOCurrencyCode        *string
	UnofficialCurrencyCode *string
	Name                   string
	Pending                bool
	PendingTransactionID   *string
	SupersededBy           *string
	SupersededAt           *time.Time
	RemovedAt              *time.Time
	FirstSeenAt, UpdatedAt time.Time
	Raw                    string
}

func queryTx(t *testing.T, s *Store, id string) txRow {
	t.Helper()
	var r txRow
	err := s.pool.QueryRow(testCtx(t), `
		SELECT item_id, account_id, amount::text, date::text, authorized_date::text,
		       iso_currency_code, unofficial_currency_code, name, pending, pending_transaction_id,
		       superseded_by, superseded_at, removed_at, first_seen_at, updated_at, raw::text
		FROM transactions WHERE transaction_id = $1`, id).
		Scan(&r.ItemID, &r.AccountID, &r.AmountText, &r.DateText, &r.AuthorizedDateText,
			&r.ISOCurrencyCode, &r.UnofficialCurrencyCode, &r.Name, &r.Pending, &r.PendingTransactionID,
			&r.SupersededBy, &r.SupersededAt, &r.RemovedAt, &r.FirstSeenAt, &r.UpdatedAt, &r.Raw)
	if err != nil {
		t.Fatalf("query transaction %s: %v", id, err)
	}
	return r
}

// acctRow is a plaid_accounts row as the database renders it.
type acctRow struct {
	ItemID                 string
	CurrentText            *string
	AvailableText          *string
	LimitText              *string
	ISOCurrencyCode        *string
	FirstSeenAt            time.Time
	LastSeenAt             time.Time
	MissingSince           *time.Time
	BalanceLastUpdatedAtTZ *time.Time
}

func queryAcct(t *testing.T, s *Store, id string) acctRow {
	t.Helper()
	var r acctRow
	err := s.pool.QueryRow(testCtx(t), `
		SELECT item_id, current_balance::text, available_balance::text, credit_limit::text,
		       iso_currency_code, first_seen_at, last_seen_at, missing_since, balance_last_updated_at
		FROM plaid_accounts WHERE account_id = $1`, id).
		Scan(&r.ItemID, &r.CurrentText, &r.AvailableText, &r.LimitText,
			&r.ISOCurrencyCode, &r.FirstSeenAt, &r.LastSeenAt, &r.MissingSince, &r.BalanceLastUpdatedAtTZ)
	if err != nil {
		t.Fatalf("query account %s: %v", id, err)
	}
	return r
}

func countRows(t *testing.T, s *Store, table, itemID string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(testCtx(t), `SELECT count(*) FROM `+table+` WHERE item_id = $1`, itemID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func derefStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// ---- ApplySyncBatch ----------------------------------------------------

func TestApplySyncBatchFreshItem(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-fresh")

	res := apply(t, s, "item-fresh", SyncBatch{
		Accounts: []Account{
			newAcct("acc-1", func(a *Account) {
				a.CurrentBalance = amtp("1234.56")
				a.AvailableBalance = amtp("-0.5")
				a.CreditLimit = amtp("5000")
			}),
			newAcct("acc-2"),
		},
		Upserts: []Transaction{
			newTx("tx-1", "acc-1", "12.34", "2024-03-01"),
			newTx("tx-2", "acc-1", "-20", "2024-03-02"),
			newTx("tx-3", "acc-2", "0.99", "2024-03-03"),
		},
		NextCursor: "cursor-1",
		Added:      3,
	})

	if res.Inserted != 3 || res.Updated != 0 || res.Unchanged != 0 || res.Removed != 0 || res.Superseded != 0 || res.AccountsUpserted != 2 || len(res.MissingAccountIDs) != 0 {
		t.Errorf("ApplyResult = %+v, want Inserted 3, AccountsUpserted 2, nothing else", res)
	}

	st := queryItem(t, s, "item-fresh")
	if derefStr(st.Cursor) != "cursor-1" {
		t.Errorf("cursor = %s, want cursor-1", derefStr(st.Cursor))
	}
	if st.Status != "active" {
		t.Errorf("status = %s, want active", st.Status)
	}
	if st.LastSuccessfulSyncAt == nil {
		t.Error("last_successful_sync_at is NULL after a successful batch")
	}
	if got := countRows(t, s, "transactions", "item-fresh"); got != 3 {
		t.Errorf("transactions rows = %d, want 3", got)
	}
	if got := countRows(t, s, "plaid_accounts", "item-fresh"); got != 2 {
		t.Errorf("accounts rows = %d, want 2", got)
	}

	// The store stamps item_id itself; callers left it empty.
	tr := queryTx(t, s, "tx-1")
	if tr.ItemID != "item-fresh" || tr.AccountID != "acc-1" {
		t.Errorf("tx-1 item/account = %s/%s, want item-fresh/acc-1", tr.ItemID, tr.AccountID)
	}
	if tr.AmountText != "12.34" || tr.DateText != "2024-03-01" {
		t.Errorf("tx-1 amount/date = %s/%s", tr.AmountText, tr.DateText)
	}
	if tr.RemovedAt != nil || tr.SupersededBy != nil {
		t.Errorf("fresh row has removed_at=%v superseded_by=%v", tr.RemovedAt, tr.SupersededBy)
	}

	acc := queryAcct(t, s, "acc-1")
	if acc.ItemID != "item-fresh" {
		t.Errorf("acc-1 item_id = %s", acc.ItemID)
	}
	if derefStr(acc.CurrentText) != "1234.56" || derefStr(acc.AvailableText) != "-0.50" || derefStr(acc.LimitText) != "5000.00" {
		t.Errorf("acc-1 balances = %s/%s/%s", derefStr(acc.CurrentText), derefStr(acc.AvailableText), derefStr(acc.LimitText))
	}
	if acc.MissingSince != nil {
		t.Errorf("acc-1 missing_since = %v, want NULL", acc.MissingSince)
	}
	acc2 := queryAcct(t, s, "acc-2")
	if acc2.CurrentText != nil || acc2.AvailableText != nil || acc2.LimitText != nil {
		t.Errorf("acc-2 nil balances stored as %s/%s/%s, want NULL", derefStr(acc2.CurrentText), derefStr(acc2.AvailableText), derefStr(acc2.LimitText))
	}
}

// TestApplySyncBatchTimestampsAreNotTransactionStart checks that the
// timestamps a batch writes reflect when the batch was applied, not when
// the item transaction began. The sync transaction is open for the whole
// Plaid pagination, so now() (Postgres' transaction start) would stamp
// last_successful_sync_at and the row audit columns minutes early on a
// long initial sync.
func TestApplySyncBatchTimestampsAreNotTransactionStart(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-clock")

	// Batch 1 seeds a pending row and two accounts so batch 2 can exercise
	// the soft-delete, pending link and missing-account paths.
	apply(t, s, "item-clock", SyncBatch{
		Accounts: []Account{newAcct("acc-1"), newAcct("acc-2")},
		Upserts: []Transaction{
			newTx("tx-pending", "acc-1", "5", "2024-03-01", func(tr *Transaction) { tr.Pending = true }),
			newTx("tx-gone", "acc-1", "7", "2024-03-01"),
		},
		NextCursor: "cursor-1",
	})

	const pause = 300 * time.Millisecond
	var txStart time.Time
	err := s.WithItemLock(testCtx(t), "item-clock", func(ctx context.Context, tx ItemTx) error {
		if err := tx.(*itemTx).tx.QueryRow(ctx, `SELECT now()`).Scan(&txStart); err != nil {
			return err
		}
		time.Sleep(pause) // stands in for the pagination
		_, err := tx.ApplySyncBatch(ctx, SyncBatch{
			Accounts: []Account{newAcct("acc-1"), newAcct("acc-3")},
			Upserts: []Transaction{
				newTx("tx-posted", "acc-1", "5", "2024-03-02", func(tr *Transaction) { tr.PendingTransactionID = strp("tx-pending") }),
			},
			Removed:    []string{"tx-gone"},
			NextCursor: "cursor-2",
		})
		return err
	})
	if err != nil {
		t.Fatalf("WithItemLock: %v", err)
	}
	earliest := txStart.Add(pause)

	check := func(name string, ts *time.Time) {
		t.Helper()
		if ts == nil {
			t.Errorf("%s is NULL", name)
			return
		}
		if ts.Before(earliest) {
			t.Errorf("%s = %v, before tx start + pause (%v): stamped with the transaction start", name, ts, earliest)
		}
	}
	check("last_successful_sync_at", queryItem(t, s, "item-clock").LastSuccessfulSyncAt)
	posted := queryTx(t, s, "tx-posted")
	check("first_seen_at", &posted.FirstSeenAt)
	check("superseded_at", queryTx(t, s, "tx-pending").SupersededAt)
	check("removed_at", queryTx(t, s, "tx-gone").RemovedAt)
	acc1 := queryAcct(t, s, "acc-1")
	check("account last_seen_at", &acc1.LastSeenAt)
	check("account missing_since", queryAcct(t, s, "acc-2").MissingSince)
	acc3 := queryAcct(t, s, "acc-3")
	check("account first_seen_at", &acc3.FirstSeenAt)
}

func TestApplySyncBatchReplayIsNoOp(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-replay")

	batch := SyncBatch{
		Accounts: []Account{newAcct("acc-1")},
		Upserts: []Transaction{
			newTx("tx-1", "acc-1", "12.34", "2024-03-01"),
			newTx("tx-2", "acc-1", "-20", "2024-03-02"),
		},
		NextCursor: "cursor-1",
	}
	apply(t, s, "item-replay", batch)
	before1, before2 := queryTx(t, s, "tx-1"), queryTx(t, s, "tx-2")

	batch.NextCursor = "cursor-2"
	res := apply(t, s, "item-replay", batch)
	if res.Inserted != 0 || res.Updated != 0 || res.Unchanged != 2 {
		t.Errorf("replay: inserted=%d updated=%d unchanged=%d, want 0/0/2", res.Inserted, res.Updated, res.Unchanged)
	}

	after1, after2 := queryTx(t, s, "tx-1"), queryTx(t, s, "tx-2")
	for _, pair := range []struct {
		id            string
		before, after txRow
	}{{"tx-1", before1, after1}, {"tx-2", before2, after2}} {
		if !pair.after.FirstSeenAt.Equal(pair.before.FirstSeenAt) {
			t.Errorf("%s first_seen_at changed on replay: %v -> %v", pair.id, pair.before.FirstSeenAt, pair.after.FirstSeenAt)
		}
		if !pair.after.UpdatedAt.Equal(pair.before.UpdatedAt) {
			t.Errorf("%s updated_at changed on replay (the row was written): %v -> %v", pair.id, pair.before.UpdatedAt, pair.after.UpdatedAt)
		}
	}
	if st := queryItem(t, s, "item-replay"); derefStr(st.Cursor) != "cursor-2" {
		t.Errorf("cursor after replay = %s, want cursor-2", derefStr(st.Cursor))
	}
}

func TestApplySyncBatchModifiedForUnseenAndSeen(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-mod")

	apply(t, s, "item-mod", SyncBatch{
		Accounts:   []Account{newAcct("acc-1")},
		Upserts:    []Transaction{newTx("tx-seen", "acc-1", "10", "2024-03-01")},
		NextCursor: "cursor-1",
	})
	seenBefore := queryTx(t, s, "tx-seen")

	// A "modified" pagination: one id we have (with new data) and one we
	// have never seen. The store treats both as upserts.
	res := apply(t, s, "item-mod", SyncBatch{
		Accounts: []Account{newAcct("acc-1")},
		Upserts: []Transaction{
			newTx("tx-seen", "acc-1", "11.50", "2024-03-01", func(tr *Transaction) {
				tr.Name = "Renamed"
				tr.MerchantName = strp("Merchant")
			}),
			newTx("tx-unseen", "acc-1", "3", "2024-03-05"),
		},
		NextCursor: "cursor-2",
		Modified:   2,
	})
	if res.Inserted != 1 || res.Updated != 1 || res.Unchanged != 0 {
		t.Errorf("modified batch: inserted=%d updated=%d unchanged=%d, want 1/1/0", res.Inserted, res.Updated, res.Unchanged)
	}

	seen := queryTx(t, s, "tx-seen")
	if seen.AmountText != "11.50" || seen.Name != "Renamed" {
		t.Errorf("tx-seen after modify: amount=%s name=%s", seen.AmountText, seen.Name)
	}
	if !seen.FirstSeenAt.Equal(seenBefore.FirstSeenAt) {
		t.Errorf("tx-seen first_seen_at changed on update: %v -> %v", seenBefore.FirstSeenAt, seen.FirstSeenAt)
	}
	if !seen.UpdatedAt.After(seenBefore.UpdatedAt) {
		t.Errorf("tx-seen updated_at not bumped on update: %v -> %v", seenBefore.UpdatedAt, seen.UpdatedAt)
	}
	if unseen := queryTx(t, s, "tx-unseen"); unseen.AmountText != "3.00" {
		t.Errorf("tx-unseen amount = %s, want 3.00", unseen.AmountText)
	}
}

func TestApplySyncBatchAmountAndDateFidelity(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-fid")

	// A late evening in Vancouver on 2024-03-31: in UTC that is already
	// 2024-04-01, which is exactly the day shift a DATE column must not
	// suffer. The expectation is built explicitly; nothing here reads the
	// machine's zone.
	vancouver := time.FixedZone("PDT", -7*60*60)
	dt := time.Date(2024, 3, 31, 23, 30, 0, 0, vancouver)
	authDate := civil.MustParseDate("2024-03-30")

	amounts := map[string]string{ // id suffix -> amount
		"a": "12.34",
		"b": "-0.01",
		"c": "1500",
		"d": "9999999999.99",
		"e": "0",
		"f": "-999999999999.99",
	}
	numericText := map[string]string{ // what NUMERIC(14,2) renders
		"a": "12.34",
		"b": "-0.01",
		"c": "1500.00",
		"d": "9999999999.99",
		"e": "0.00",
		"f": "-999999999999.99",
	}
	var upserts []Transaction
	for suffix, amt := range amounts {
		upserts = append(upserts, newTx("tx-"+suffix, "acc-1", amt, "2024-03-31", func(tr *Transaction) {
			tr.AuthorizedDate = &authDate
			tr.DateTime = &dt
			tr.AuthorizedDateTime = &dt
		}))
	}
	upserts = append(upserts, newTx("tx-nulls", "acc-1", "1", "2024-02-29"))
	apply(t, s, "item-fid", SyncBatch{
		Accounts:   []Account{newAcct("acc-1")},
		Upserts:    upserts,
		NextCursor: "cursor-1",
	})

	for suffix, amt := range amounts {
		id := "tx-" + suffix
		r := queryTx(t, s, id)
		if r.AmountText != numericText[suffix] {
			t.Errorf("%s: NUMERIC text = %q, want %q", id, r.AmountText, numericText[suffix])
		}
		if r.DateText != "2024-03-31" || derefStr(r.AuthorizedDateText) != "2024-03-30" {
			t.Errorf("%s: DATE text = %q / %q, want 2024-03-31 / 2024-03-30", id, r.DateText, derefStr(r.AuthorizedDateText))
		}

		// And through the store's own scanners (money.Amount, civil.Date).
		got, err := s.GetTransaction(testCtx(t), id)
		if err != nil {
			t.Fatalf("GetTransaction(%s): %v", id, err)
		}
		if !got.Amount.Equal(money.MustParse(amt)) {
			t.Errorf("%s: scanned amount %s, want %s", id, got.Amount, amt)
		}
		if got.Date != civil.MustParseDate("2024-03-31") {
			t.Errorf("%s: scanned date %v, want 2024-03-31", id, got.Date)
		}
		if got.AuthorizedDate == nil || *got.AuthorizedDate != authDate {
			t.Errorf("%s: scanned authorized_date %v, want %v", id, got.AuthorizedDate, authDate)
		}
		if got.DateTime == nil || !got.DateTime.Equal(dt) {
			t.Errorf("%s: scanned datetime %v, want instant %v", id, got.DateTime, dt)
		}
		if got.AuthorizedDateTime == nil || !got.AuthorizedDateTime.Equal(dt) {
			t.Errorf("%s: scanned authorized_datetime %v, want instant %v", id, got.AuthorizedDateTime, dt)
		}
		if !json.Valid(got.Raw) {
			t.Errorf("%s: scanned raw is not valid JSON: %s", id, got.Raw)
		}
	}

	nulls, err := s.GetTransaction(testCtx(t), "tx-nulls")
	if err != nil {
		t.Fatalf("GetTransaction(tx-nulls): %v", err)
	}
	if nulls.AuthorizedDate != nil || nulls.DateTime != nil || nulls.AuthorizedDateTime != nil {
		t.Errorf("tx-nulls: NULL columns scanned as %v / %v / %v, want nil", nulls.AuthorizedDate, nulls.DateTime, nulls.AuthorizedDateTime)
	}
	if nulls.Date != civil.MustParseDate("2024-02-29") {
		t.Errorf("tx-nulls: date %v, want 2024-02-29", nulls.Date)
	}

	// Read the same row in sessions pinned to zones on both sides of UTC,
	// in both result formats. The DATE must not move and the TIMESTAMPTZ
	// must be the same instant regardless.
	for _, zone := range []string{"America/Vancouver", "Pacific/Kiritimati", "UTC"} {
		for name, format := range map[string]pgx.QueryResultFormats{
			"binary": {pgx.BinaryFormatCode},
			"text":   {pgx.TextFormatCode},
		} {
			t.Run(zone+"/"+name, func(t *testing.T) {
				ctx := testCtx(t)
				err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `SET LOCAL TIME ZONE '`+zone+`'`); err != nil {
						return err
					}
					var d civil.Date
					var ad *civil.Date
					var ts time.Time
					var amt money.Amount
					err := tx.QueryRow(ctx,
						`SELECT date, authorized_date, datetime, amount FROM transactions WHERE transaction_id = 'tx-a'`,
						format).Scan(&d, &ad, &ts, &amt)
					if err != nil {
						return err
					}
					if d != civil.MustParseDate("2024-03-31") {
						t.Errorf("date read in %s/%s = %v, want 2024-03-31", zone, name, d)
					}
					if ad == nil || *ad != authDate {
						t.Errorf("authorized_date read in %s/%s = %v, want %v", zone, name, ad, authDate)
					}
					if !ts.Equal(dt) {
						t.Errorf("datetime read in %s/%s = %v, want instant %v", zone, name, ts, dt)
					}
					if !amt.Equal(money.MustParse("12.34")) {
						t.Errorf("amount read in %s/%s = %s, want 12.34", zone, name, amt)
					}
					return nil
				})
				if err != nil {
					t.Fatalf("read in %s/%s: %v", zone, name, err)
				}
			})
		}
	}
}

func TestApplySyncBatchCurrencyCodes(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-ccy")

	apply(t, s, "item-ccy", SyncBatch{
		Accounts: []Account{newAcct("amex-cad")}, // newAcct sets CAD
		Upserts: []Transaction{
			newTx("tx-usd", "amex-cad", "9.99", "2024-03-01", func(tr *Transaction) {
				tr.ISOCurrencyCode = strp("USD")
				tr.UnofficialCurrencyCode = nil
			}),
			newTx("tx-unofficial", "amex-cad", "0.5", "2024-03-01", func(tr *Transaction) {
				tr.ISOCurrencyCode = nil
				tr.UnofficialCurrencyCode = strp("BTC")
			}),
		},
		NextCursor: "cursor-1",
	})

	if acc := queryAcct(t, s, "amex-cad"); derefStr(acc.ISOCurrencyCode) != "CAD" {
		t.Errorf("account iso_currency_code = %s, want CAD", derefStr(acc.ISOCurrencyCode))
	}
	usd := queryTx(t, s, "tx-usd")
	if derefStr(usd.ISOCurrencyCode) != "USD" || usd.UnofficialCurrencyCode != nil {
		t.Errorf("tx-usd codes = %s / %s, want USD / NULL", derefStr(usd.ISOCurrencyCode), derefStr(usd.UnofficialCurrencyCode))
	}
	un := queryTx(t, s, "tx-unofficial")
	if un.ISOCurrencyCode != nil || derefStr(un.UnofficialCurrencyCode) != "BTC" {
		t.Errorf("tx-unofficial codes = %s / %s, want NULL / BTC", derefStr(un.ISOCurrencyCode), derefStr(un.UnofficialCurrencyCode))
	}
}

func TestApplySyncBatchRemoved(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-rm")
	seedItem(t, s, "item-other")

	apply(t, s, "item-rm", SyncBatch{
		Accounts: []Account{newAcct("acc-1")},
		Upserts: []Transaction{
			newTx("tx-1", "acc-1", "1", "2024-03-01"),
			newTx("tx-2", "acc-1", "2", "2024-03-02"),
		},
		NextCursor: "cursor-1",
	})

	// Removing one known id, one unknown id, and a duplicate of the known
	// one: exactly one row becomes removed.
	res := apply(t, s, "item-rm", SyncBatch{
		Removed:    []string{"tx-1", "tx-does-not-exist", "tx-1"},
		NextCursor: "cursor-2",
	})
	if res.Removed != 1 {
		t.Errorf("first removal: Removed = %d, want 1", res.Removed)
	}
	first := queryTx(t, s, "tx-1")
	if first.RemovedAt == nil {
		t.Fatal("tx-1 removed_at is NULL after removal")
	}
	if other := queryTx(t, s, "tx-2"); other.RemovedAt != nil {
		t.Errorf("tx-2 removed_at = %v, want NULL", other.RemovedAt)
	}

	// Removing again is a no-op that keeps the original timestamp.
	res = apply(t, s, "item-rm", SyncBatch{Removed: []string{"tx-1"}, NextCursor: "cursor-3"})
	if res.Removed != 0 {
		t.Errorf("second removal: Removed = %d, want 0", res.Removed)
	}
	if again := queryTx(t, s, "tx-1"); !again.RemovedAt.Equal(*first.RemovedAt) {
		t.Errorf("tx-1 removed_at changed on second removal: %v -> %v", *first.RemovedAt, *again.RemovedAt)
	}

	// Another item cannot remove this item's rows.
	res = apply(t, s, "item-other", SyncBatch{Removed: []string{"tx-2"}, NextCursor: "cursor-x"})
	if res.Removed != 0 {
		t.Errorf("cross-item removal: Removed = %d, want 0", res.Removed)
	}
	if other := queryTx(t, s, "tx-2"); other.RemovedAt != nil {
		t.Errorf("tx-2 removed by another item's batch: removed_at = %v", other.RemovedAt)
	}

	// A re-delivered row for a removed transaction refreshes the data but
	// never clears removed_at: Plaid does not resurrect.
	res = apply(t, s, "item-rm", SyncBatch{
		Upserts:    []Transaction{newTx("tx-1", "acc-1", "1.25", "2024-03-01")},
		NextCursor: "cursor-4",
	})
	if res.Updated != 1 {
		t.Errorf("re-delivery: Updated = %d, want 1", res.Updated)
	}
	redelivered := queryTx(t, s, "tx-1")
	if redelivered.AmountText != "1.25" {
		t.Errorf("re-delivered amount = %s, want 1.25", redelivered.AmountText)
	}
	if redelivered.RemovedAt == nil || !redelivered.RemovedAt.Equal(*first.RemovedAt) {
		t.Errorf("re-delivery changed removed_at: %v -> %v", *first.RemovedAt, redelivered.RemovedAt)
	}
}

func TestApplySyncBatchPendingToPostedOrderA(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-pend")

	res := apply(t, s, "item-pend", SyncBatch{
		Accounts: []Account{newAcct("acc-1")},
		Upserts: []Transaction{
			newTx("tx-pending", "acc-1", "10", "2024-03-01", func(tr *Transaction) { tr.Pending = true }),
		},
		NextCursor: "cursor-1",
	})
	if res.Superseded != 0 || res.Inserted != 1 {
		t.Errorf("batch 1: %+v", res)
	}

	posted := SyncBatch{
		Accounts: []Account{newAcct("acc-1")},
		Upserts: []Transaction{
			newTx("tx-posted", "acc-1", "10", "2024-03-03", func(tr *Transaction) {
				tr.Pending = false
				tr.PendingTransactionID = strp("tx-pending")
			}),
		},
		Removed:    []string{"tx-pending"},
		NextCursor: "cursor-2",
	}
	res = apply(t, s, "item-pend", posted)
	if res.Inserted != 1 || res.Removed != 1 || res.Superseded != 1 {
		t.Errorf("batch 2: inserted=%d removed=%d superseded=%d, want 1/1/1", res.Inserted, res.Removed, res.Superseded)
	}

	old := queryTx(t, s, "tx-pending")
	if derefStr(old.SupersededBy) != "tx-posted" {
		t.Errorf("pending row superseded_by = %s, want tx-posted", derefStr(old.SupersededBy))
	}
	if old.SupersededAt == nil || old.RemovedAt == nil {
		t.Errorf("pending row superseded_at=%v removed_at=%v, want both set", old.SupersededAt, old.RemovedAt)
	}
	if !old.Pending {
		t.Error("pending row lost pending=true")
	}
	newRow := queryTx(t, s, "tx-posted")
	if newRow.Pending || derefStr(newRow.PendingTransactionID) != "tx-pending" || newRow.SupersededBy != nil {
		t.Errorf("posted row: pending=%v pending_transaction_id=%s superseded_by=%s", newRow.Pending, derefStr(newRow.PendingTransactionID), derefStr(newRow.SupersededBy))
	}

	posted.NextCursor = "cursor-3"
	res = apply(t, s, "item-pend", posted)
	if res.Superseded != 0 || res.Removed != 0 || res.Unchanged != 1 {
		t.Errorf("batch 2 replay: superseded=%d removed=%d unchanged=%d, want 0/0/1", res.Superseded, res.Removed, res.Unchanged)
	}
	if again := queryTx(t, s, "tx-pending"); !again.SupersededAt.Equal(*old.SupersededAt) {
		t.Errorf("superseded_at changed on replay: %v -> %v", *old.SupersededAt, *again.SupersededAt)
	}
}

func TestApplySyncBatchPendingToPostedOrderB(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-pend-b")

	// Same batch, posted row first.
	res := apply(t, s, "item-pend-b", SyncBatch{
		Accounts: []Account{newAcct("acc-1")},
		Upserts: []Transaction{
			newTx("tx-posted", "acc-1", "10", "2024-03-03", func(tr *Transaction) {
				tr.PendingTransactionID = strp("tx-pending")
			}),
			newTx("tx-pending", "acc-1", "10", "2024-03-01", func(tr *Transaction) { tr.Pending = true }),
		},
		NextCursor: "cursor-1",
	})
	if res.Inserted != 2 || res.Superseded != 1 {
		t.Errorf("same batch, posted first: inserted=%d superseded=%d, want 2/1", res.Inserted, res.Superseded)
	}
	if old := queryTx(t, s, "tx-pending"); derefStr(old.SupersededBy) != "tx-posted" || old.SupersededAt == nil {
		t.Errorf("pending row superseded_by=%s superseded_at=%v", derefStr(old.SupersededBy), old.SupersededAt)
	}

	// Later batch: the pending row arrives after its posted successor was
	// stored in an earlier batch. Only the reverse link can catch this.
	res = apply(t, s, "item-pend-b", SyncBatch{
		Upserts: []Transaction{
			newTx("tx-posted-2", "acc-1", "20", "2024-03-05", func(tr *Transaction) {
				tr.PendingTransactionID = strp("tx-pending-2")
			}),
		},
		NextCursor: "cursor-2",
	})
	if res.Superseded != 0 {
		t.Errorf("posted-only batch: superseded=%d, want 0 (pending row not stored yet)", res.Superseded)
	}
	res = apply(t, s, "item-pend-b", SyncBatch{
		Upserts: []Transaction{
			newTx("tx-pending-2", "acc-1", "20", "2024-03-04", func(tr *Transaction) { tr.Pending = true }),
		},
		NextCursor: "cursor-3",
	})
	if res.Inserted != 1 || res.Superseded != 1 {
		t.Errorf("late pending batch: inserted=%d superseded=%d, want 1/1", res.Inserted, res.Superseded)
	}
	if old := queryTx(t, s, "tx-pending-2"); derefStr(old.SupersededBy) != "tx-posted-2" {
		t.Errorf("late pending row superseded_by=%s, want tx-posted-2", derefStr(old.SupersededBy))
	}

	// A modification of an already-linked pending row does not re-count it.
	res = apply(t, s, "item-pend-b", SyncBatch{
		Upserts: []Transaction{
			newTx("tx-pending-2", "acc-1", "20", "2024-03-04", func(tr *Transaction) {
				tr.Pending = true
				tr.Name = "renamed pending"
			}),
		},
		NextCursor: "cursor-4",
	})
	if res.Updated != 1 || res.Superseded != 0 {
		t.Errorf("modified linked pending row: updated=%d superseded=%d, want 1/0", res.Updated, res.Superseded)
	}
}

func TestApplySyncBatchDuplicateTransactionID(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-dup")

	res := apply(t, s, "item-dup", SyncBatch{
		Accounts: []Account{newAcct("acc-1"), newAcct("acc-1", func(a *Account) { a.Name = "Renamed account" })},
		Upserts: []Transaction{
			newTx("tx-1", "acc-1", "10", "2024-03-01", func(tr *Transaction) { tr.Name = "first" }),
			newTx("tx-2", "acc-1", "5", "2024-03-01"),
			newTx("tx-1", "acc-1", "20", "2024-03-01", func(tr *Transaction) { tr.Name = "second" }),
			newTx("tx-1", "acc-1", "30", "2024-03-02", func(tr *Transaction) { tr.Name = "third" }),
		},
		NextCursor: "cursor-1",
		Added:      2,
		Modified:   2,
	})
	if res.Inserted != 2 || res.Updated != 0 || res.AccountsUpserted != 1 {
		t.Errorf("duplicates: inserted=%d updated=%d accounts=%d, want 2/0/1", res.Inserted, res.Updated, res.AccountsUpserted)
	}
	r := queryTx(t, s, "tx-1")
	if r.AmountText != "30.00" || r.Name != "third" || r.DateText != "2024-03-02" {
		t.Errorf("last occurrence did not win: amount=%s name=%s date=%s", r.AmountText, r.Name, r.DateText)
	}
	var name string
	if err := s.pool.QueryRow(testCtx(t), `SELECT name FROM plaid_accounts WHERE account_id = 'acc-1'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed account" {
		t.Errorf("last account occurrence did not win: name=%q", name)
	}
}

func TestApplySyncBatchAccountVanish(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-van")
	seedItem(t, s, "item-bystander")

	// Another item's account must never be flagged by this item's batches.
	apply(t, s, "item-bystander", SyncBatch{Accounts: []Account{newAcct("acc-bystander")}, NextCursor: "c"})

	res := apply(t, s, "item-van", SyncBatch{
		Accounts:   []Account{newAcct("acc-a"), newAcct("acc-b")},
		NextCursor: "cursor-1",
	})
	if len(res.MissingAccountIDs) != 0 {
		t.Errorf("batch 1 missing = %v, want none", res.MissingAccountIDs)
	}

	res = apply(t, s, "item-van", SyncBatch{Accounts: []Account{newAcct("acc-a")}, NextCursor: "cursor-2"})
	if !reflect.DeepEqual(res.MissingAccountIDs, []string{"acc-b"}) {
		t.Errorf("batch 2 missing = %v, want [acc-b]", res.MissingAccountIDs)
	}
	b := queryAcct(t, s, "acc-b")
	if b.MissingSince == nil {
		t.Fatal("acc-b missing_since is NULL after vanishing")
	}
	if a := queryAcct(t, s, "acc-a"); a.MissingSince != nil {
		t.Errorf("acc-a missing_since = %v, want NULL", a.MissingSince)
	}
	if by := queryAcct(t, s, "acc-bystander"); by.MissingSince != nil {
		t.Errorf("other item's account flagged missing: %v", by.MissingSince)
	}

	// Still missing on the next run: reported again, original timestamp kept.
	res = apply(t, s, "item-van", SyncBatch{Accounts: []Account{newAcct("acc-a")}, NextCursor: "cursor-3"})
	if !reflect.DeepEqual(res.MissingAccountIDs, []string{"acc-b"}) {
		t.Errorf("batch 3 missing = %v, want [acc-b] (previously flagged accounts are reported every run)", res.MissingAccountIDs)
	}
	if again := queryAcct(t, s, "acc-b"); !again.MissingSince.Equal(*b.MissingSince) {
		t.Errorf("acc-b missing_since changed on second run: %v -> %v", *b.MissingSince, *again.MissingSince)
	}

	// It comes back.
	res = apply(t, s, "item-van", SyncBatch{Accounts: []Account{newAcct("acc-a"), newAcct("acc-b")}, NextCursor: "cursor-4"})
	if len(res.MissingAccountIDs) != 0 {
		t.Errorf("batch 4 missing = %v, want none", res.MissingAccountIDs)
	}
	back := queryAcct(t, s, "acc-b")
	if back.MissingSince != nil {
		t.Errorf("acc-b missing_since = %v after reappearing, want NULL", back.MissingSince)
	}
	if !back.LastSeenAt.After(b.LastSeenAt) {
		t.Errorf("acc-b last_seen_at not bumped: %v -> %v", b.LastSeenAt, back.LastSeenAt)
	}
	if !back.FirstSeenAt.Equal(b.FirstSeenAt) {
		t.Errorf("acc-b first_seen_at changed: %v -> %v", b.FirstSeenAt, back.FirstSeenAt)
	}

	// No account information at all: nothing is marked missing.
	res = apply(t, s, "item-van", SyncBatch{NextCursor: "cursor-5"})
	if len(res.MissingAccountIDs) != 0 || res.AccountsUpserted != 0 {
		t.Errorf("empty accounts batch: %+v", res)
	}
	for _, id := range []string{"acc-a", "acc-b"} {
		if r := queryAcct(t, s, id); r.MissingSince != nil {
			t.Errorf("%s flagged missing by a batch with no accounts: %v", id, r.MissingSince)
		}
	}
}

func TestApplySyncBatchAtomicity(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-atomic")

	apply(t, s, "item-atomic", SyncBatch{
		Accounts:   []Account{newAcct("acc-1")},
		Upserts:    []Transaction{newTx("tx-old", "acc-1", "1", "2024-03-01")},
		NextCursor: "cursor-1",
	})
	before := queryItem(t, s, "item-atomic")

	boom := errors.New("simulated crash between rows and cursor")
	hookCalls := 0
	s.testHookBeforeCursorUpdate = func(ctx context.Context) error {
		hookCalls++
		// The rows are already written in the transaction at this point.
		return boom
	}

	_, err := tryApply(t, s, "item-atomic", SyncBatch{
		Accounts: []Account{newAcct("acc-1"), newAcct("acc-2")},
		Upserts: []Transaction{
			newTx("tx-old", "acc-1", "1.50", "2024-03-01"),
			newTx("tx-new", "acc-2", "2", "2024-03-02"),
		},
		Removed:    []string{"tx-old"},
		NextCursor: "cursor-2",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the injected failure", err)
	}
	if hookCalls != 1 {
		t.Errorf("hook called %d times, want 1", hookCalls)
	}

	after := queryItem(t, s, "item-atomic")
	if !reflect.DeepEqual(after, before) {
		t.Errorf("item changed despite failure: %+v -> %+v", before, after)
	}
	if got := countRows(t, s, "plaid_accounts", "item-atomic"); got != 1 {
		t.Errorf("accounts = %d, want 1 (acc-2 must not persist)", got)
	}
	if got := countRows(t, s, "transactions", "item-atomic"); got != 1 {
		t.Errorf("transactions = %d, want 1 (tx-new must not persist)", got)
	}
	old := queryTx(t, s, "tx-old")
	if old.AmountText != "1.00" || old.RemovedAt != nil {
		t.Errorf("tx-old changed despite failure: amount=%s removed_at=%v", old.AmountText, old.RemovedAt)
	}

	// With the hook gone the same batch goes through.
	s.testHookBeforeCursorUpdate = nil
	res := apply(t, s, "item-atomic", SyncBatch{
		Accounts:   []Account{newAcct("acc-1"), newAcct("acc-2")},
		Upserts:    []Transaction{newTx("tx-new", "acc-2", "2", "2024-03-02")},
		NextCursor: "cursor-2",
	})
	if res.Inserted != 1 {
		t.Errorf("after hook removed: %+v", res)
	}
	if st := queryItem(t, s, "item-atomic"); derefStr(st.Cursor) != "cursor-2" {
		t.Errorf("cursor = %s, want cursor-2", derefStr(st.Cursor))
	}
}

func TestApplySyncBatchRemovedItem(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-gone")
	ctx := testCtx(t)

	apply(t, s, "item-gone", SyncBatch{
		Accounts:   []Account{newAcct("acc-1")},
		NextCursor: "cursor-1",
	})
	if _, err := s.pool.Exec(ctx, `
		UPDATE plaid_items SET status = 'removed', encrypted_access_token = NULL, key_version = NULL
		WHERE item_id = 'item-gone'`); err != nil {
		t.Fatal(err)
	}
	before := queryItem(t, s, "item-gone")

	err := s.WithItemLock(ctx, "item-gone", func(ctx context.Context, tx ItemTx) error {
		if tx.Item().Status != ItemStatusRemoved {
			t.Errorf("Item().Status = %s, want removed", tx.Item().Status)
		}
		if _, err := tx.Credential(ctx); !errors.Is(err, ErrNotFound) {
			t.Errorf("Credential on removed item: %v, want ErrNotFound", err)
		}
		_, err := tx.ApplySyncBatch(ctx, SyncBatch{
			Accounts:   []Account{newAcct("acc-1"), newAcct("acc-2")},
			Upserts:    []Transaction{newTx("tx-1", "acc-1", "1", "2024-03-01")},
			NextCursor: "cursor-2",
		})
		if !errors.Is(err, ErrItemRemoved) {
			t.Errorf("ApplySyncBatch on removed item: %v, want ErrItemRemoved", err)
		}
		// Nothing was written even inside the still-open transaction.
		var n int
		if err := tx.(*itemTx).tx.QueryRow(ctx, `SELECT count(*) FROM transactions`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("%d transactions written in-tx for a removed item", n)
		}
		if err := tx.(*itemTx).tx.QueryRow(ctx, `SELECT count(*) FROM plaid_accounts`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("%d accounts in-tx for a removed item, want the 1 from before", n)
		}
		return nil // commit: still nothing to persist
	})
	if err != nil {
		t.Fatalf("WithItemLock: %v", err)
	}
	if after := queryItem(t, s, "item-gone"); !reflect.DeepEqual(after, before) {
		t.Errorf("removed item changed: %+v -> %+v", before, after)
	}
	if got := countRows(t, s, "transactions", "item-gone"); got != 0 {
		t.Errorf("transactions = %d, want 0", got)
	}
}

func TestApplySyncBatchClearsPreviousError(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-err")
	ctx := testCtx(t)

	if _, err := s.pool.Exec(ctx, `
		UPDATE plaid_items
		SET status = 'error', last_error_code = 'INSTITUTION_DOWN', last_error_type = 'INSTITUTION_ERROR',
		    last_error_message = 'down', last_error_at = now()
		WHERE item_id = 'item-err'`); err != nil {
		t.Fatal(err)
	}

	var itemAfter *Item
	err := s.WithItemLock(ctx, "item-err", func(ctx context.Context, tx ItemTx) error {
		if tx.Item().Status != ItemStatusError {
			t.Errorf("Item().Status before apply = %s, want error", tx.Item().Status)
		}
		if _, err := tx.ApplySyncBatch(ctx, SyncBatch{NextCursor: "cursor-1"}); err != nil {
			return err
		}
		itemAfter = tx.Item()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if itemAfter.Status != ItemStatusActive || derefStr(itemAfter.Cursor) != "cursor-1" || itemAfter.LastSuccessfulSyncAt == nil || itemAfter.LastErrorCode != nil {
		t.Errorf("Item() not refreshed after apply: %+v", itemAfter)
	}
	st := queryItem(t, s, "item-err")
	if st.Status != "active" || st.LastErrorCode != nil || derefStr(st.Cursor) != "cursor-1" {
		t.Errorf("item after successful sync: %+v", st)
	}
	var msg, typ *string
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT last_error_type, last_error_message, last_error_at FROM plaid_items WHERE item_id = 'item-err'`).Scan(&typ, &msg, &at); err != nil {
		t.Fatal(err)
	}
	if typ != nil || msg != nil || at != nil {
		t.Errorf("last_error_* not cleared: %v %v %v", typ, msg, at)
	}
}

func TestApplySyncBatchRejectsBadInput(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-bad")

	cases := map[string]SyncBatch{
		"empty cursor": {Accounts: []Account{newAcct("acc-1")}},
		"account without id": {
			Accounts:   []Account{newAcct("")},
			NextCursor: "c",
		},
		"account without raw": {
			Accounts:   []Account{{AccountID: "acc-1", Name: "n", Type: "t"}},
			NextCursor: "c",
		},
		"transaction without id": {
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{newTx("", "acc-1", "1", "2024-03-01")},
			NextCursor: "c",
		},
		"transaction without account": {
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{newTx("tx-1", "", "1", "2024-03-01")},
			NextCursor: "c",
		},
		"transaction without date": {
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{{TransactionID: "tx-1", AccountID: "acc-1", Name: "n", Raw: json.RawMessage(`{}`)}},
			NextCursor: "c",
		},
		"transaction without raw": {
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{{TransactionID: "tx-1", AccountID: "acc-1", Name: "n", Date: civil.MustParseDate("2024-03-01")}},
			NextCursor: "c",
		},
		"transaction on unknown account": {
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{newTx("tx-1", "acc-unknown", "1", "2024-03-01")},
			NextCursor: "c",
		},
		// NUMERIC(14,2) would silently round a sub-cent amount on the way
		// in, leaving the column disagreeing with raw; the store refuses.
		"transaction amount with three decimals": {
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{newTx("tx-1", "acc-1", "12.345", "2024-03-01")},
			NextCursor: "c",
		},
		"account balance with three decimals": {
			Accounts:   []Account{newAcct("acc-1", func(a *Account) { a.CurrentBalance = amtp("0.001") })},
			NextCursor: "c",
		},
		"account limit with three decimals": {
			Accounts:   []Account{newAcct("acc-1", func(a *Account) { a.CreditLimit = amtp("-0.005") })},
			NextCursor: "c",
		},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tryApply(t, s, "item-bad", b); err == nil {
				t.Fatal("ApplySyncBatch succeeded, want error")
			}
			if st := queryItem(t, s, "item-bad"); st.Cursor != nil || st.LastSuccessfulSyncAt != nil {
				t.Errorf("item touched by a rejected batch: %+v", st)
			}
			if n := countRows(t, s, "plaid_accounts", "item-bad") + countRows(t, s, "transactions", "item-bad"); n != 0 {
				t.Errorf("%d rows persisted by a rejected batch", n)
			}
		})
	}
}

// TestApplySyncBatchLargeBatch pushes enough rows through the pipelined
// upsert to be sure the batch path (not just a handful of statements)
// works, and that the counts stay exact.
func TestApplySyncBatchLargeBatch(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-large")

	const n = 2500
	upserts := make([]Transaction, 0, n)
	for i := 0; i < n; i++ {
		upserts = append(upserts, newTx(fmt.Sprintf("tx-%04d", i), "acc-1", fmt.Sprintf("%d.%02d", i, i%100), "2024-01-01"))
	}
	res := apply(t, s, "item-large", SyncBatch{
		Accounts:   []Account{newAcct("acc-1")},
		Upserts:    upserts,
		NextCursor: "cursor-1",
	})
	if res.Inserted != n {
		t.Errorf("Inserted = %d, want %d", res.Inserted, n)
	}

	// Modify the first half, replay the second half.
	for i := 0; i < n/2; i++ {
		upserts[i] = newTx(upserts[i].TransactionID, "acc-1", "0.01", "2024-01-02")
	}
	res = apply(t, s, "item-large", SyncBatch{
		Accounts:   []Account{newAcct("acc-1")},
		Upserts:    upserts,
		NextCursor: "cursor-2",
	})
	if res.Inserted != 0 || res.Updated != n/2 || res.Unchanged != n/2 {
		t.Errorf("second pass: inserted=%d updated=%d unchanged=%d, want 0/%d/%d", res.Inserted, res.Updated, res.Unchanged, n/2, n/2)
	}
	if got := countRows(t, s, "transactions", "item-large"); got != n {
		t.Errorf("rows = %d, want %d", got, n)
	}
}

// ---- ItemTx.RecordSyncRun and Credential --------------------------------

func TestItemTxRecordSyncRun(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-run")
	ctx := testCtx(t)

	jobID := "0f7a4c2e-9b1d-4e3a-8c5f-1a2b3c4d5e6f"
	if _, err := s.pool.Exec(ctx, `INSERT INTO sync_jobs (job_id, item_id, kind, state) VALUES ($1, 'item-run', 'manual', 'running')`, jobID); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2024, 3, 31, 10, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Second)

	full := SyncRun{
		ItemID: "item-run", JobID: &jobID, Trigger: JobKindManual,
		StartedAt: started, FinishedAt: finished,
		CursorBefore: strp("c0"), CursorAfter: strp("c1"),
		Pages: 3, Added: 10, Modified: 2, Removed: 1, Inserted: 9, Updated: 3, Superseded: 1,
		AccountsSeen: 2, AccountsMissing: 1,
		Outcome: SyncOutcomeSuccess, ErrorCode: strp("E"), ErrorType: strp("T"), ErrorMessage: strp("m"), RequestID: strp("req-1"),
	}
	var runFull, runNils int64
	err := s.WithItemLock(ctx, "item-run", func(ctx context.Context, tx ItemTx) error {
		if _, err := tx.ApplySyncBatch(ctx, SyncBatch{NextCursor: "c1"}); err != nil {
			return err
		}
		var err error
		if runFull, err = tx.RecordSyncRun(ctx, full); err != nil {
			return err
		}
		if runNils, err = tx.RecordSyncRun(ctx, SyncRun{
			ItemID: "item-run", Trigger: JobKindScheduled, StartedAt: started, FinishedAt: finished, Outcome: SyncOutcomeLocked,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runFull <= 0 || runNils <= runFull {
		t.Errorf("run ids = %d, %d; want increasing positive ids", runFull, runNils)
	}

	var got SyncRun
	var jobText *string
	err = s.pool.QueryRow(ctx, `
		SELECT item_id, job_id::text, trigger, started_at, finished_at, cursor_before, cursor_after,
		       pages, added, modified, removed, inserted, updated, superseded, accounts_seen, accounts_missing,
		       outcome, error_code, error_type, error_message, request_id
		FROM sync_runs WHERE run_id = $1`, runFull).Scan(
		&got.ItemID, &jobText, &got.Trigger, &got.StartedAt, &got.FinishedAt, &got.CursorBefore, &got.CursorAfter,
		&got.Pages, &got.Added, &got.Modified, &got.Removed, &got.Inserted, &got.Updated, &got.Superseded, &got.AccountsSeen, &got.AccountsMissing,
		&got.Outcome, &got.ErrorCode, &got.ErrorType, &got.ErrorMessage, &got.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	got.JobID = jobText
	if !got.StartedAt.Equal(started) || !got.FinishedAt.Equal(finished) {
		t.Errorf("sync run times = %v / %v, want %v / %v", got.StartedAt, got.FinishedAt, started, finished)
	}
	got.StartedAt, got.FinishedAt = time.Time{}, time.Time{}
	want := full
	want.StartedAt, want.FinishedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sync run round trip:\n got %+v\nwant %+v", got, want)
	}

	var nilJob *string
	var outcome string
	if err := s.pool.QueryRow(ctx, `SELECT job_id::text, outcome FROM sync_runs WHERE run_id = $1`, runNils).Scan(&nilJob, &outcome); err != nil {
		t.Fatal(err)
	}
	if nilJob != nil || outcome != "locked" {
		t.Errorf("nil-field run: job_id=%v outcome=%s", nilJob, outcome)
	}

	// Bad inputs.
	err = s.WithItemLock(ctx, "item-run", func(ctx context.Context, tx ItemTx) error {
		if _, err := tx.RecordSyncRun(ctx, SyncRun{ItemID: "item-run", JobID: strp("not-a-uuid"), Trigger: JobKindManual, StartedAt: started, FinishedAt: finished, Outcome: SyncOutcomeError}); err == nil {
			t.Error("malformed job id accepted")
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("expected the abort error")
	}
	err = s.WithItemLock(ctx, "item-run", func(ctx context.Context, tx ItemTx) error {
		_, err := tx.RecordSyncRun(ctx, SyncRun{ItemID: "item-run", JobID: strp("00000000-0000-4000-8000-000000000000"), Trigger: JobKindManual, StartedAt: started, FinishedAt: finished, Outcome: SyncOutcomeError})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown job id: %v, want ErrNotFound", err)
		}
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("expected the abort error")
	}
}

func TestItemTxCredential(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-cred")

	err := s.WithItemLock(testCtx(t), "item-cred", func(ctx context.Context, tx ItemTx) error {
		c, err := tx.Credential(ctx)
		if err != nil {
			return err
		}
		if string(c.Ciphertext) != "dummy-ciphertext:item-cred" || c.KeyVersion != 1 {
			t.Errorf("Credential = %q v%d", c.Ciphertext, c.KeyVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---- unit tests for the pure helpers -----------------------------------

func TestDedupeKeepsLast(t *testing.T) {
	t.Parallel()
	txs := dedupeTransactions([]Transaction{
		{TransactionID: "a", Name: "a1"},
		{TransactionID: "b", Name: "b1"},
		{TransactionID: "a", Name: "a2"},
		{TransactionID: "c", Name: "c1"},
		{TransactionID: "b", Name: "b2"},
		{TransactionID: "a", Name: "a3"},
	})
	want := []Transaction{{TransactionID: "a", Name: "a3"}, {TransactionID: "b", Name: "b2"}, {TransactionID: "c", Name: "c1"}}
	if !reflect.DeepEqual(txs, want) {
		t.Errorf("dedupeTransactions = %+v, want %+v", txs, want)
	}
	if got := dedupeTransactions(nil); len(got) != 0 {
		t.Errorf("dedupeTransactions(nil) = %v", got)
	}

	accts := dedupeAccounts([]Account{{AccountID: "x", Name: "1"}, {AccountID: "y"}, {AccountID: "x", Name: "2"}})
	wantAccts := []Account{{AccountID: "x", Name: "2"}, {AccountID: "y"}}
	if !reflect.DeepEqual(accts, wantAccts) {
		t.Errorf("dedupeAccounts = %+v, want %+v", accts, wantAccts)
	}
}
