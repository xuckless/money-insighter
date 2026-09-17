package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"plaidsync/internal/civil"
	"plaidsync/internal/money"
)

// seedItemWithAccount stores an active item with one account, written the
// way ApplySyncBatch writes it, and sets the item's cursor so it counts as
// synced.
func seedItemWithAccount(t *testing.T, s *Store, itemID, accountID, balance string) {
	t.Helper()
	ctx := testCtx(t)
	if err := s.UpsertItem(ctx, NewItem{ItemID: itemID, Credential: testCredential(itemID, 1)}); err != nil {
		t.Fatal(err)
	}
	bal := money.MustParse(balance)
	err := s.WithItemLock(ctx, itemID, func(ctx context.Context, tx ItemTx) error {
		_, err := upsertAccounts(ctx, tx.(*itemTx).tx, itemID, []Account{{
			AccountID: accountID, Name: "Chequing", Type: "depository", CurrentBalance: &bal,
			Raw: json.RawMessage(`{"account_id":"` + accountID + `"}`),
		}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	setCursor(t, s, itemID, "cursor-1")
}

func TestBalanceSnapshotTriggerKeepsOneRowPerDay(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItemWithAccount(t, s, "item-1", "acc-1", "100.50")

	if _, err := s.pool.Exec(ctx, `UPDATE plaid_accounts SET current_balance = 250.25 WHERE account_id = 'acc-1'`); err != nil {
		t.Fatal(err)
	}
	// A change to a column the trigger does not watch writes nothing new.
	if _, err := s.pool.Exec(ctx, `UPDATE plaid_accounts SET name = 'Renamed' WHERE account_id = 'acc-1'`); err != nil {
		t.Fatal(err)
	}
	var (
		n       int
		day     time.Time
		current string
	)
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) OVER (), day, current_balance::text
		FROM account_balance_snapshots WHERE account_id = 'acc-1'`).Scan(&n, &day, &current); err != nil {
		t.Fatal(err)
	}
	if n != 1 || current != "250.25" {
		t.Errorf("snapshots = %d rows, current %s; want 1 row at 250.25", n, current)
	}
	var today time.Time
	if err := s.pool.QueryRow(ctx, `SELECT current_date`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	if !day.Equal(today) {
		t.Errorf("snapshot day = %v, want %v", day, today)
	}
}

func testStream(id, accountID string) RecurringStream {
	avg := money.MustParse("11.99")
	return RecurringStream{
		StreamID: id, AccountID: accountID, Direction: StreamDirectionOutflow,
		Description: "SPOTIFY", Frequency: "MONTHLY", Status: "MATURE", IsActive: true,
		FirstDate: civil.MustParseDate("2026-01-19"), LastDate: civil.MustParseDate("2026-08-19"),
		AverageAmount: &avg, LastAmount: &avg,
		Raw: json.RawMessage(`{"stream_id":"` + id + `"}`),
	}
}

func TestReplaceRecurringStreams(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItemWithAccount(t, s, "item-1", "acc-1", "10")

	// A stream on an account the store has not seen is skipped rather than
	// failing the refresh.
	err := s.ReplaceRecurringStreams(ctx, "item-1", []RecurringStream{testStream("s-1", "acc-1"), testStream("s-2", "acc-unknown")})
	if err != nil {
		t.Fatal(err)
	}
	streams, err := s.ListRecurringStreams(ctx, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].StreamID != "s-1" || streams[0].ItemID != "item-1" || len(streams[0].TransactionIDs) != 0 {
		t.Fatalf("streams = %+v", streams)
	}
	c, err := s.GetRecurringCheck(ctx, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	wantRecent(t, "refreshed at", c.RefreshedAt)

	// A failure keeps the streams and records the error.
	if err := s.RecordRecurringFailure(ctx, "item-1", "PRODUCT_NOT_ENABLED", "not enabled"); err != nil {
		t.Fatal(err)
	}
	if c, err = s.GetRecurringCheck(ctx, "item-1"); err != nil {
		t.Fatal(err)
	}
	wantStr(t, "error code", c.ErrorCode, optStr("PRODUCT_NOT_ENABLED"))

	// An empty refresh removes the stream; a later one brings it back and
	// clears the error.
	if err := s.ReplaceRecurringStreams(ctx, "item-1", nil); err != nil {
		t.Fatal(err)
	}
	if streams, _ = s.ListRecurringStreams(ctx, "item-1"); streams[0].RemovedAt == nil {
		t.Error("stream not removed by an empty refresh")
	}
	if err := s.ReplaceRecurringStreams(ctx, "item-1", []RecurringStream{testStream("s-1", "acc-1")}); err != nil {
		t.Fatal(err)
	}
	if streams, _ = s.ListRecurringStreams(ctx, "item-1"); streams[0].RemovedAt != nil {
		t.Error("stream still removed after it came back")
	}
	if c, _ = s.GetRecurringCheck(ctx, "item-1"); c.ErrorCode != nil {
		t.Errorf("error code = %v after success", *c.ErrorCode)
	}

	if err := s.ReplaceRecurringStreams(ctx, "nope", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item = %v, want ErrNotFound", err)
	}
}

func TestItemsDueRecurringRefresh(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItemWithAccount(t, s, "item-fresh", "acc-fresh", "1")
	seedItemWithAccount(t, s, "item-never", "acc-never", "1")
	if err := s.UpsertItem(ctx, NewItem{ItemID: "item-unsynced", Credential: testCredential("u", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRecurringStreams(ctx, "item-fresh", nil); err != nil {
		t.Fatal(err)
	}

	ids, err := s.ItemsDueRecurringRefresh(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "item-never" {
		t.Errorf("due = %v, want [item-never]", ids)
	}
	if ids, _ = s.ItemsDueRecurringRefresh(ctx, time.Now().Add(time.Hour)); len(ids) != 2 {
		t.Errorf("due with a future cutoff = %v, want both synced items", ids)
	}
}
