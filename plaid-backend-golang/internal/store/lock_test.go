package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// lockTimeout is how long a WithItemLock call that must return "at once"
// is allowed to take. The contract says the lock is never waited on; a
// generous bound keeps the test honest without being flaky on a busy box.
const lockTimeout = 2 * time.Second

// holdLock runs WithItemLock on itemID in a goroutine whose fn blocks until
// release is closed. It returns once fn has been entered, plus a channel
// that carries WithItemLock's result after release.
func holdLock(t *testing.T, s *Store, itemID string, fn func(ctx context.Context, tx ItemTx) error) (release func(), done <-chan error) {
	t.Helper()
	entered := make(chan struct{})
	releaseCh := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- s.WithItemLock(testCtx(t), itemID, func(ctx context.Context, tx ItemTx) error {
			close(entered)
			<-releaseCh
			if fn != nil {
				return fn(ctx, tx)
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("WithItemLock returned before fn was entered: %v", err)
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for fn to be entered")
	}
	var once bool
	return func() {
		if !once {
			once = true
			close(releaseCh)
		}
	}, result
}

// timedLock calls WithItemLock with a no-op fn and reports the error and
// how long it took.
func timedLock(t *testing.T, s *Store, itemID string) (error, time.Duration) {
	t.Helper()
	start := time.Now()
	err := s.WithItemLock(testCtx(t), itemID, func(ctx context.Context, tx ItemTx) error { return nil })
	return err, time.Since(start)
}

func TestWithItemLockHeld(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-a")
	seedItem(t, s, "item-b")

	release, done := holdLock(t, s, "item-a", nil)
	defer release()

	// The same item: refused at once, twice.
	for i := 0; i < 2; i++ {
		err, took := timedLock(t, s, "item-a")
		if !errors.Is(err, ErrItemLocked) {
			t.Fatalf("second caller on held item: err = %v, want ErrItemLocked", err)
		}
		if took > lockTimeout {
			t.Fatalf("second caller blocked for %v, want an immediate ErrItemLocked", took)
		}
	}

	// A different item is not affected.
	if err, _ := timedLock(t, s, "item-b"); err != nil {
		t.Fatalf("different item while item-a is locked: %v", err)
	}

	// Holder still inside fn.
	select {
	case err := <-done:
		t.Fatalf("holder returned early: %v", err)
	default:
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("holder: %v", err)
	}
	if err, _ := timedLock(t, s, "item-a"); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestWithItemLockErrorRollsBack(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-rb")

	sentinel := errors.New("engine gave up")
	var applied ApplyResult
	err := s.WithItemLock(testCtx(t), "item-rb", func(ctx context.Context, tx ItemTx) error {
		var err error
		applied, err = tx.ApplySyncBatch(ctx, SyncBatch{
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{newTx("tx-1", "acc-1", "1", "2024-03-01")},
			NextCursor: "cursor-1",
		})
		if err != nil {
			return err
		}
		if _, err := tx.RecordSyncRun(ctx, SyncRun{
			ItemID: "item-rb", Trigger: JobKindManual, StartedAt: time.Now(), FinishedAt: time.Now(), Outcome: SyncOutcomeSuccess,
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the callback's error (wrapped)", err)
	}
	if applied.Inserted != 1 || applied.AccountsUpserted != 1 {
		t.Errorf("apply inside the tx reported %+v", applied)
	}

	if st := queryItem(t, s, "item-rb"); st.Cursor != nil || st.LastSuccessfulSyncAt != nil {
		t.Errorf("item changed after rollback: %+v", st)
	}
	for _, table := range []string{"plaid_accounts", "transactions", "sync_runs"} {
		if n := countRows(t, s, table, "item-rb"); n != 0 {
			t.Errorf("%s has %d rows after rollback, want 0", table, n)
		}
	}
	if err, _ := timedLock(t, s, "item-rb"); err != nil {
		t.Fatalf("lock not released after rollback: %v", err)
	}
}

func TestWithItemLockSuccessCommits(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-ok")

	var runID int64
	err := s.WithItemLock(testCtx(t), "item-ok", func(ctx context.Context, tx ItemTx) error {
		if tx.Item().ItemID != "item-ok" || tx.Item().Status != ItemStatusActive || tx.Item().Cursor != nil {
			t.Errorf("Item() = %+v", tx.Item())
		}
		if _, err := tx.ApplySyncBatch(ctx, SyncBatch{
			Accounts:   []Account{newAcct("acc-1")},
			Upserts:    []Transaction{newTx("tx-1", "acc-1", "1", "2024-03-01")},
			NextCursor: "cursor-1",
		}); err != nil {
			return err
		}
		var err error
		runID, err = tx.RecordSyncRun(ctx, SyncRun{
			ItemID: "item-ok", Trigger: JobKindInitial, StartedAt: time.Now(), FinishedAt: time.Now(),
			CursorAfter: strp("cursor-1"), Outcome: SyncOutcomeSuccess, Inserted: 1, AccountsSeen: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("WithItemLock: %v", err)
	}

	if st := queryItem(t, s, "item-ok"); derefStr(st.Cursor) != "cursor-1" || st.LastSuccessfulSyncAt == nil {
		t.Errorf("item after commit: %+v", st)
	}
	if n := countRows(t, s, "transactions", "item-ok"); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
	var gotRun int64
	if err := s.pool.QueryRow(testCtx(t), `SELECT run_id FROM sync_runs WHERE item_id = 'item-ok'`).Scan(&gotRun); err != nil {
		t.Fatalf("sync run after commit: %v", err)
	}
	if gotRun != runID {
		t.Errorf("run_id = %d, want %d", gotRun, runID)
	}
	if err, _ := timedLock(t, s, "item-ok"); err != nil {
		t.Fatalf("lock not released after commit: %v", err)
	}
}

func TestWithItemLockNotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)

	called := false
	err := s.WithItemLock(testCtx(t), "item-missing", func(ctx context.Context, tx ItemTx) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if called {
		t.Error("fn was called for an unknown item")
	}

	// The advisory lock taken before the lookup was released with the
	// rollback: creating the item and locking it now works.
	seedItem(t, s, "item-missing")
	if err, _ := timedLock(t, s, "item-missing"); err != nil {
		t.Fatalf("lock after ErrNotFound: %v", err)
	}

	if err := s.WithItemLock(testCtx(t), "", func(context.Context, ItemTx) error { return nil }); err == nil {
		t.Error("empty item id accepted")
	}
}

func TestWithItemLockContextCanceledInFn(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-cancel")

	// The engine sees its context canceled (shutdown) between Plaid pages
	// and returns; the transaction must be rolled back on a context that
	// still works, and the lock released.
	ctx, cancel := context.WithCancel(testCtx(t))
	err := s.WithItemLock(ctx, "item-cancel", func(ctx context.Context, tx ItemTx) error {
		if _, err := tx.ApplySyncBatch(ctx, SyncBatch{
			Accounts:   []Account{newAcct("acc-1")},
			NextCursor: "cursor-1",
		}); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if st := queryItem(t, s, "item-cancel"); st.Cursor != nil {
		t.Errorf("cursor persisted despite cancellation: %+v", st)
	}
	if n := countRows(t, s, "plaid_accounts", "item-cancel"); n != 0 {
		t.Errorf("accounts = %d after cancellation, want 0", n)
	}
	if err, _ := timedLock(t, s, "item-cancel"); err != nil {
		t.Fatalf("lock not released after cancellation: %v", err)
	}
}

func TestWithItemLockContextCanceledMidQuery(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-cancel-q")

	// Harder case: the context dies while a statement is in flight on the
	// locked connection. pgx's default context watcher closes the
	// connection; the server notices when the statement finishes (here a
	// short pg_sleep) and ends the transaction, which releases the lock.
	ctx, cancel := context.WithCancel(testCtx(t))
	err := s.WithItemLock(ctx, "item-cancel-q", func(ctx context.Context, tx ItemTx) error {
		if _, err := tx.ApplySyncBatch(ctx, SyncBatch{NextCursor: "cursor-1"}); err != nil {
			return err
		}
		time.AfterFunc(100*time.Millisecond, cancel)
		_, err := tx.(*itemTx).tx.Exec(ctx, `SELECT pg_sleep(2)`)
		if err == nil {
			return errors.New("pg_sleep finished, expected cancellation")
		}
		return err
	})
	if err == nil {
		t.Fatal("WithItemLock succeeded, want an error")
	}
	if st := queryItem(t, s, "item-cancel-q"); st.Cursor != nil {
		t.Errorf("cursor persisted despite cancellation: %+v", st)
	}

	// The server only notices the closed connection once pg_sleep returns,
	// so allow a few attempts, but it must come free well within the sleep
	// plus a margin.
	deadline := time.Now().Add(10 * time.Second)
	for {
		err, _ := timedLock(t, s, "item-cancel-q")
		if err == nil {
			break
		}
		if !errors.Is(err, ErrItemLocked) || time.Now().After(deadline) {
			t.Fatalf("lock not released after mid-query cancellation: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestWithItemLockPanicReleasesLock(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-panic")

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("panic did not propagate")
			}
		}()
		_ = s.WithItemLock(testCtx(t), "item-panic", func(ctx context.Context, tx ItemTx) error {
			if _, err := tx.ApplySyncBatch(ctx, SyncBatch{NextCursor: "cursor-1"}); err != nil {
				return err
			}
			panic("engine bug")
		})
	}()

	if st := queryItem(t, s, "item-panic"); st.Cursor != nil {
		t.Errorf("cursor persisted despite panic: %+v", st)
	}
	if err, _ := timedLock(t, s, "item-panic"); err != nil {
		t.Fatalf("lock not released after panic: %v", err)
	}
}

// TestWithItemLockRowLockDoesNotBlockReaders checks that the FOR NO KEY
// UPDATE row lock taken on the item does not stop plain reads, and that the
// advisory lock is transaction-scoped (visible in pg_locks while held, gone
// after).
func TestWithItemLockRowLockDoesNotBlockReaders(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-read")

	release, done := holdLock(t, s, "item-read", nil)
	defer release()

	ctx := testCtx(t)
	if _, err := s.GetItem(ctx, "item-read"); err != nil {
		t.Fatalf("GetItem while locked: %v", err)
	}
	var held int
	// pg_locks is cluster-wide and other tests run in parallel on their
	// own databases, so count only this database's advisory locks.
	const advisoryLocks = `
		SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory' AND granted
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`
	if err := s.pool.QueryRow(ctx, advisoryLocks).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 1 {
		t.Errorf("advisory locks held while inside fn = %d, want 1", held)
	}

	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, advisoryLocks).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Errorf("advisory locks held after commit = %d, want 0", held)
	}
}

// TestWithItemLockPoolConnectionReturned makes sure the connection used by
// a rolled-back or committed item transaction goes back to the pool in a
// usable state: a plain query afterwards must not see an aborted
// transaction or a closed connection.
func TestWithItemLockPoolConnectionReturned(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-pool")

	for i := 0; i < 3; i++ {
		_ = s.WithItemLock(testCtx(t), "item-pool", func(ctx context.Context, tx ItemTx) error {
			return errors.New("fail")
		})
		if err := s.WithItemLock(testCtx(t), "item-pool", func(ctx context.Context, tx ItemTx) error { return nil }); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	var one int
	if err := s.pool.QueryRow(testCtx(t), `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("pool unusable afterwards: %v", err)
	}
	if err := s.withTx(testCtx(t), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT 1`)
		return err
	}); err != nil {
		t.Fatalf("withTx afterwards: %v", err)
	}
}

// TestWithItemLockDoesNotBlockChildInserts pins the strength of the row
// lock taken on plaid_items. A foreign-key check on an INSERT into
// sync_jobs or sync_runs takes FOR KEY SHARE on the parent row, which
// conflicts with FOR UPDATE but not with FOR NO KEY UPDATE. If the row
// lock were FOR UPDATE, a second sync attempt's "locked" audit row and the
// API's job creation would hang until the running sync commits, which for
// an initial sync is minutes.
func TestWithItemLockDoesNotBlockChildInserts(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	seedItem(t, s, "item-child")

	release, done := holdLock(t, s, "item-child", nil)
	defer release()

	ctx, cancel := context.WithTimeout(testCtx(t), lockTimeout)
	defer cancel()

	start := time.Now()
	if _, err := s.CreateJob(ctx, "item-child", JobKindManual); err != nil {
		t.Fatalf("CreateJob while the item lock is held: %v (took %v)", err, time.Since(start))
	}
	start = time.Now()
	if _, err := s.RecordSyncRun(ctx, SyncRun{
		ItemID: "item-child", Trigger: JobKindManual,
		StartedAt: time.Now(), FinishedAt: time.Now(), Outcome: SyncOutcomeLocked,
	}); err != nil {
		t.Fatalf("RecordSyncRun while the item lock is held: %v (took %v)", err, time.Since(start))
	}

	// Holder still inside fn: the inserts really did run concurrently.
	select {
	case err := <-done:
		t.Fatalf("holder returned early: %v", err)
	default:
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("holder: %v", err)
	}
}
