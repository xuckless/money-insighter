package sync

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/plaid"
	"plaidsync/internal/plaid/plaidtest"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
	"plaidsync/internal/store/storetest"
)

// harness wires a fresh database, a keyring, a fake Plaid and an engine
// whose sleeps are recorded instead of waited.
type harness struct {
	t      *testing.T
	ctx    context.Context
	store  *store.Store
	keys   *crypto.Keyring
	fake   *plaidtest.Fake
	engine *Engine
	sleeps []time.Duration
}

func testKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	kr, err := crypto.NewKeyring(1, map[uint32]secret.Bytes{1: secret.NewBytes(key)})
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func testSyncConfig() config.SyncConfig {
	return config.SyncConfig{
		MinInterval: 15 * time.Minute, Interval: 6 * time.Hour, SchedulerEnabled: true,
		MaxAttempts: 3, RetryBase: 2 * time.Second, RetryMax: 2 * time.Minute, Concurrency: 2,
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, store: storetest.New(t), keys: testKeyring(t), fake: plaidtest.New()}
	h.ctx = storetest.Ctx(t)
	h.engine = New(h.store, h.fake, h.keys, testSyncConfig(), slog.New(slog.DiscardHandler))
	h.engine.sleep = func(ctx context.Context, d time.Duration) error {
		h.sleeps = append(h.sleeps, d)
		return ctx.Err()
	}
	h.engine.jitter = func() float64 { return 1 }
	return h
}

// link registers it with the fake and stores it in the database with its
// access token encrypted under the keyring, as the API's exchange does.
func (h *harness) link(it *plaidtest.Item) *plaidtest.Item {
	h.t.Helper()
	h.fake.AddItem(it)
	blob, version, err := h.keys.Encrypt(it.AccessToken, CredentialAAD(it.Info.ItemID))
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.store.UpsertItem(h.ctx, store.NewItem{
		ItemID:           it.Info.ItemID,
		InstitutionID:    it.Info.InstitutionID,
		InstitutionName:  it.Info.InstitutionName,
		Credential:       store.Credential{Ciphertext: blob, KeyVersion: version},
		ConsentExpiresAt: it.Info.ConsentExpiresAt,
		Raw:              it.Info.Raw,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return it
}

func (h *harness) item(id string) *store.Item {
	h.t.Helper()
	it, err := h.store.GetItem(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return it
}

func (h *harness) runs(id string) []store.SyncRun {
	h.t.Helper()
	runs, err := h.store.ListSyncRuns(h.ctx, id, 10)
	if err != nil {
		h.t.Fatal(err)
	}
	return runs
}

func TestSyncItemHappyPath(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindInitial, nil)
	if !res.Succeeded() || res.Err != nil {
		t.Fatalf("result = %+v", *res)
	}
	if res.Attempts != 1 || res.Pages != 2 || res.Added != 4 || res.Modified != 1 || res.Removed != 1 {
		t.Errorf("counts = attempts %d pages %d added %d modified %d removed %d", res.Attempts, res.Pages, res.Added, res.Modified, res.Removed)
	}
	// Four distinct transactions were upserted (the grocery row twice, last
	// wins), one was soft-deleted, and the pending ride was linked to its
	// posted successor.
	if res.Apply.Inserted != 4 || res.Apply.Removed != 1 || res.Apply.Superseded != 1 || res.Apply.AccountsUpserted != 2 {
		t.Errorf("apply = %+v", res.Apply)
	}
	if res.UpdateStatus != plaid.UpdateStatusHistoricalComplete || res.NotReady {
		t.Errorf("update status = %q not ready %v", res.UpdateStatus, res.NotReady)
	}

	item := h.item(it.Info.ItemID)
	if item.Cursor == nil || *item.Cursor != "cursor-final" || item.Status != store.ItemStatusActive || item.LastSuccessfulSyncAt == nil {
		t.Errorf("item = %+v", *item)
	}
	txs, err := h.store.ListTransactions(h.ctx, it.Info.ItemID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 4 {
		t.Fatalf("transactions = %d", len(txs))
	}
	byID := map[string]store.Transaction{}
	for _, tx := range txs {
		byID[tx.TransactionID] = tx
	}
	if g := byID["txn-groceries-1"]; g.MerchantName == nil || *g.MerchantName != "Loblaws Supermarket" {
		t.Errorf("modified row not applied: %+v", g)
	}
	if p := byID["txn-uber-pending"]; p.RemovedAt == nil || p.SupersededBy == nil || *p.SupersededBy != "txn-uber-posted" {
		t.Errorf("pending row = removed %v superseded_by %v", p.RemovedAt, p.SupersededBy)
	}
	if pay := byID["txn-payroll"]; pay.Amount.String() != "-2500" {
		t.Errorf("payroll amount = %s", pay.Amount)
	}
	accts, err := h.store.ListAccounts(h.ctx, it.Info.ItemID)
	if err != nil || len(accts) != 2 || accts[0].MissingSince != nil {
		t.Errorf("accounts = %+v, %v", accts, err)
	}

	runs := h.runs(it.Info.ItemID)
	if len(runs) != 1 || runs[0].RunID != res.RunID {
		t.Fatalf("runs = %+v", runs)
	}
	r := runs[0]
	if r.Outcome != store.SyncOutcomeSuccess || r.Pages != 2 || r.CursorBefore != nil || r.CursorAfter == nil || *r.CursorAfter != "cursor-final" || r.Trigger != store.JobKindInitial {
		t.Errorf("run = %+v", r)
	}
	if r.Inserted != 4 || r.Superseded != 1 || r.AccountsSeen != 2 || r.AccountsMissing != 0 || r.JobID != nil {
		t.Errorf("run counts = %+v", r)
	}
	if calls := h.fake.CallsTo(plaidtest.OpGetAccounts); len(calls) != 1 {
		t.Errorf("accounts calls = %d, want 1", len(calls))
	}

	// A second sync starts from the saved cursor. Script an empty page for
	// it and check the cursor advances without touching the rows.
	it.Pages["cursor-final"] = &plaid.SyncPage{NextCursor: "cursor-final-2", UpdateStatus: plaid.UpdateStatusHistoricalComplete}
	res = h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindScheduled, nil)
	if !res.Succeeded() || res.Pages != 1 || res.Apply.Inserted != 0 {
		t.Fatalf("second result = %+v", *res)
	}
	if calls := h.fake.CallsTo(plaidtest.OpSyncTransactions); calls[len(calls)-1].Cursor != "cursor-final" {
		t.Errorf("second sync started from %q", calls[len(calls)-1].Cursor)
	}
	if item := h.item(it.Info.ItemID); *item.Cursor != "cursor-final-2" {
		t.Errorf("cursor after second sync = %q", *item.Cursor)
	}
}

func TestSyncItemRestartsFromOriginalCursorOnRetryableError(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())

	// Page 1 succeeds, page 2 fails with a mutation error, then both pages
	// succeed on the second attempt.
	h.fake.FailNext(plaidtest.OpSyncTransactions, nil) // placeholder consumed by page 1
	h.fake.FailNext(plaidtest.OpSyncTransactions, plaidtest.PlaidError(plaidtest.FixtureErrorMutation, "/transactions/sync", 400))

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil)
	if !res.Succeeded() {
		t.Fatalf("result = %+v", *res)
	}
	if res.Attempts != 2 || res.Pages != 2 {
		t.Errorf("attempts %d pages %d", res.Attempts, res.Pages)
	}
	calls := h.fake.CallsTo(plaidtest.OpSyncTransactions)
	cursors := make([]string, len(calls))
	for i, c := range calls {
		cursors[i] = c.Cursor
	}
	want := []string{"", "cursor-page-2", "", "cursor-page-2"}
	if len(cursors) != len(want) {
		t.Fatalf("cursors = %v, want %v", cursors, want)
	}
	for i := range want {
		if cursors[i] != want[i] {
			t.Errorf("cursors = %v, want %v", cursors, want)
			break
		}
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 2*time.Second {
		t.Errorf("sleeps = %v, want [2s]", h.sleeps)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 1 || runs[0].Outcome != store.SyncOutcomeSuccess {
		t.Errorf("runs = %+v", runs)
	}
	if txs, _ := h.store.ListTransactions(h.ctx, it.Info.ItemID, 100, 0); len(txs) != 4 {
		t.Errorf("transactions = %d, want 4 (no duplicates from the failed attempt)", len(txs))
	}
}

func TestSyncItemGivesUpAfterMaxAttempts(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	for i := 0; i < 3; i++ {
		h.fake.FailNext(plaidtest.OpSyncTransactions, plaidtest.PlaidError(plaidtest.FixtureErrorRateLimit, "/transactions/sync", 429))
	}

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindScheduled, nil)
	if res.Succeeded() || res.Outcome != store.SyncOutcomeRetryableError || res.Attempts != 3 {
		t.Fatalf("result = %+v", *res)
	}
	if len(h.sleeps) != 2 || h.sleeps[0] != 2*time.Second || h.sleeps[1] != 4*time.Second {
		t.Errorf("sleeps = %v", h.sleeps)
	}
	item := h.item(it.Info.ItemID)
	if item.Status != store.ItemStatusActive || item.LastErrorCode == nil || *item.LastErrorCode != "TRANSACTIONS_SYNC_LIMIT" || item.Cursor != nil {
		t.Errorf("item = %+v", *item)
	}
	runs := h.runs(it.Info.ItemID)
	if len(runs) != 1 || runs[0].Outcome != store.SyncOutcomeRetryableError || runs[0].ErrorCode == nil || *runs[0].ErrorCode != "TRANSACTIONS_SYNC_LIMIT" || runs[0].RequestID == nil {
		t.Errorf("runs = %+v", runs)
	}
	if txs, _ := h.store.ListTransactions(h.ctx, it.Info.ItemID, 100, 0); len(txs) != 0 {
		t.Errorf("transactions = %d, want 0", len(txs))
	}
}

func TestSyncItemNeedsReauth(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	if err := h.fake.SandboxResetLogin(h.ctx, it.AccessToken); err != nil {
		t.Fatal(err)
	}

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindWebhook, nil)
	if res.Outcome != store.SyncOutcomeNeedsReauth || res.Attempts != 1 || len(h.sleeps) != 0 {
		t.Fatalf("result = %+v sleeps %v", *res, h.sleeps)
	}
	item := h.item(it.Info.ItemID)
	if item.Status != store.ItemStatusLoginRequired || item.LastErrorCode == nil || *item.LastErrorCode != "ITEM_LOGIN_REQUIRED" || item.LastErrorAt == nil {
		t.Errorf("item = %+v", *item)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 1 || runs[0].Outcome != store.SyncOutcomeNeedsReauth {
		t.Errorf("runs = %+v", runs)
	}

	// While the item waits for a human, the engine does not call Plaid.
	before := len(h.fake.CallsTo(plaidtest.OpSyncTransactions))
	res = h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindScheduled, nil)
	if !res.Skipped || res.Outcome != store.SyncOutcomeNeedsReauth || len(h.fake.CallsTo(plaidtest.OpSyncTransactions)) != before {
		t.Errorf("second result = %+v", *res)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 1 {
		t.Errorf("a skipped sync must not add a run: %d", len(runs))
	}

	// After update mode, UpsertItem resets the status and syncing resumes.
	h.fake.RepairLogin(it.Info.ItemID)
	h.link(it)
	if res = h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil); !res.Succeeded() {
		t.Errorf("after repair = %+v", *res)
	}
}

func TestSyncItemFatalMarksItemError(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	h.fake.FailNext(plaidtest.OpSyncTransactions, plaidtest.PlaidError(plaidtest.FixtureErrorTrialLimit, "/transactions/sync", 429))

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil)
	if res.Outcome != store.SyncOutcomeFatal || res.Attempts != 1 {
		t.Fatalf("result = %+v", *res)
	}
	if item := h.item(it.Info.ItemID); item.Status != store.ItemStatusError || *item.LastErrorCode != "TRIAL_CONNECTION_LIMIT" {
		t.Errorf("item = %+v", *item)
	}
	// An unclassified failure (a plain error from the client) is an error
	// outcome and also marks the item.
	h.fake.FailNext(plaidtest.OpSyncTransactions, errors.New("decode: something odd"))
	res = h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil)
	if res.Outcome != store.SyncOutcomeError {
		t.Fatalf("result = %+v", *res)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 2 || runs[0].ErrorCode != nil || runs[0].ErrorMessage == nil {
		t.Errorf("runs = %+v", runs)
	}
}

func TestSyncItemAccountsFailureIsClassified(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	h.fake.FailNext(plaidtest.OpGetAccounts, plaidtest.PlaidError(plaidtest.FixtureErrorLoginRequired, "/accounts/get", 400))

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil)
	if res.Outcome != store.SyncOutcomeNeedsReauth || res.Pages != 2 {
		t.Fatalf("result = %+v", *res)
	}
	if item := h.item(it.Info.ItemID); item.Status != store.ItemStatusLoginRequired || item.Cursor != nil {
		t.Errorf("item = %+v", *item)
	}
	if txs, _ := h.store.ListTransactions(h.ctx, it.Info.ItemID, 100, 0); len(txs) != 0 {
		t.Errorf("nothing may be committed when /accounts/get fails: %d rows", len(txs))
	}
}

func TestSyncItemNotReady(t *testing.T) {
	h := newHarness(t)
	it := plaidtest.CIBCItem()
	it.SetPages() // a single NOT_READY page
	h.link(it)

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindInitial, nil)
	if !res.Succeeded() || !res.NotReady || res.Pages != 1 || res.UpdateStatus != plaid.UpdateStatusNotReady {
		t.Fatalf("result = %+v", *res)
	}
	item := h.item(it.Info.ItemID)
	if item.Cursor != nil || item.LastSuccessfulSyncAt != nil || item.Status != store.ItemStatusActive {
		t.Errorf("item = %+v", *item)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 1 || runs[0].Outcome != store.SyncOutcomeSuccess || runs[0].CursorAfter != nil {
		t.Errorf("runs = %+v", runs)
	}
	if len(h.fake.CallsTo(plaidtest.OpGetAccounts)) != 0 {
		t.Error("accounts must not be fetched for a not-ready item")
	}
}

func TestSyncItemSkipsRemovedAndUnknown(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	if err := h.store.MarkItemRemoved(h.ctx, it.Info.ItemID); err != nil {
		t.Fatal(err)
	}
	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil)
	if !res.Skipped || res.Outcome != store.SyncOutcomeFatal || len(h.fake.Calls()) != 0 {
		t.Errorf("removed = %+v", *res)
	}
	res = h.engine.SyncItem(h.ctx, "no-such-item", store.JobKindManual, nil)
	if res.Outcome != store.SyncOutcomeError || !errors.Is(res.Err, store.ErrNotFound) || res.RunID != 0 {
		t.Errorf("unknown = %+v", *res)
	}
}

func TestSyncItemLocked(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())

	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = h.store.WithItemLock(h.ctx, it.Info.ItemID, func(ctx context.Context, tx store.ItemTx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindWebhook, nil)
	close(release)
	if res.Outcome != store.SyncOutcomeLocked || res.RunID == 0 {
		t.Fatalf("result = %+v", *res)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 1 || runs[0].Outcome != store.SyncOutcomeLocked {
		t.Errorf("runs = %+v", runs)
	}
	if item := h.item(it.Info.ItemID); item.Status != store.ItemStatusActive || item.LastErrorAt != nil {
		t.Errorf("a locked run must not touch the item: %+v", *item)
	}
}

func TestSyncItemCanceled(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	ctx, cancel := context.WithCancel(h.ctx)
	h.fake.FailNext(plaidtest.OpSyncTransactions, context.Canceled)
	cancel()

	res := h.engine.SyncItem(ctx, it.Info.ItemID, store.JobKindScheduled, nil)
	if res.Outcome != store.SyncOutcomeCanceled {
		t.Fatalf("result = %+v", *res)
	}
	if runs := h.runs(it.Info.ItemID); len(runs) != 0 {
		t.Errorf("a canceled sync leaves no run: %+v", runs)
	}
	if item := h.item(it.Info.ItemID); item.Status != store.ItemStatusActive || item.LastErrorAt != nil {
		t.Errorf("a canceled sync must not touch the item: %+v", *item)
	}
}

func TestSyncItemUndecryptableCredential(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(200 - i)
	}
	kr, _ := crypto.NewKeyring(1, map[uint32]secret.Bytes{1: secret.NewBytes(other)})
	h.engine.keys = kr

	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, nil)
	if res.Outcome != store.SyncOutcomeFatal || len(h.fake.Calls()) != 0 {
		t.Fatalf("result = %+v", *res)
	}
	if item := h.item(it.Info.ItemID); item.Status != store.ItemStatusError {
		t.Errorf("item = %+v", *item)
	}
}

func TestSyncItemRecordsJobID(t *testing.T) {
	h := newHarness(t)
	it := h.link(plaidtest.CIBCItem())
	job, err := h.store.CreateJob(h.ctx, it.Info.ItemID, store.JobKindManual)
	if err != nil {
		t.Fatal(err)
	}
	res := h.engine.SyncItem(h.ctx, it.Info.ItemID, store.JobKindManual, &job.JobID)
	if !res.Succeeded() {
		t.Fatalf("result = %+v", *res)
	}
	if runs := h.runs(it.Info.ItemID); runs[0].JobID == nil || *runs[0].JobID != job.JobID {
		t.Errorf("run job id = %v, want %s", runs[0].JobID, job.JobID)
	}
}

func TestBackoff(t *testing.T) {
	e := &Engine{cfg: config.SyncConfig{RetryBase: time.Second, RetryMax: 10 * time.Second}, jitter: func() float64 { return 1 }}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		if got := e.backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	e.jitter = func() float64 { return 1.5 }
	if got := e.backoff(4); got != 10*time.Second {
		t.Errorf("jittered backoff above max = %v", got)
	}
	e.jitter = func() float64 { return 0.5 }
	if got := e.backoff(1); got != 500*time.Millisecond {
		t.Errorf("jittered backoff = %v", got)
	}
}
