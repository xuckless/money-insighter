package jobs

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/plaid/plaidtest"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
	"plaidsync/internal/store/storetest"
	"plaidsync/internal/sync"
)

type harness struct {
	t      *testing.T
	ctx    context.Context
	store  *store.Store
	keys   *crypto.Keyring
	fake   *plaidtest.Fake
	runner *Runner
	done   chan *sync.Result
}

func newHarness(t *testing.T, cfg config.SyncConfig) *harness {
	t.Helper()
	key := make([]byte, 32)
	kr, err := crypto.NewKeyring(1, map[uint32]secret.Bytes{1: secret.NewBytes(key)})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, store: storetest.New(t), keys: kr, fake: plaidtest.New(), done: make(chan *sync.Result, 16)}
	h.ctx = storetest.Ctx(t)
	engine := sync.New(h.store, h.fake, kr, cfg, slog.New(slog.DiscardHandler))
	h.runner = New(h.store, engine, cfg, slog.New(slog.DiscardHandler))
	h.runner.afterRun = func(r *sync.Result) { h.done <- r }
	return h
}

func (h *harness) link(it *plaidtest.Item) *plaidtest.Item {
	h.t.Helper()
	h.fake.AddItem(it)
	blob, version, err := h.keys.Encrypt(it.AccessToken, sync.CredentialAAD(it.Info.ItemID))
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.UpsertItem(h.ctx, store.NewItem{ItemID: it.Info.ItemID, Credential: store.Credential{Ciphertext: blob, KeyVersion: version}, Raw: it.Info.Raw}); err != nil {
		h.t.Fatal(err)
	}
	return it
}

// start runs the runner in the background until the test ends.
func (h *harness) start() context.CancelFunc {
	ctx, cancel := context.WithCancel(h.ctx)
	stopped := make(chan struct{})
	go func() {
		_ = h.runner.Run(ctx)
		close(stopped)
	}()
	h.t.Cleanup(func() {
		cancel()
		<-stopped
	})
	return cancel
}

// waitJob polls until the job reaches a terminal state.
func (h *harness) waitJob(jobID string) *store.Job {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := h.store.GetJob(h.ctx, jobID)
		if err != nil {
			h.t.Fatal(err)
		}
		if job.State.IsTerminal() {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("job %s did not finish", jobID)
	return nil
}

func testConfig() config.SyncConfig {
	return config.SyncConfig{MinInterval: 15 * time.Minute, Interval: time.Hour, SchedulerEnabled: false,
		MaxAttempts: 2, RetryBase: time.Millisecond, RetryMax: time.Millisecond, Concurrency: 2}
}

func TestRunnerProcessesQueuedJob(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	h.start()

	job, err := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindInitial)
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitJob(job.JobID)
	if done.State != store.JobStateSucceeded || done.StartedAt == nil || done.FinishedAt == nil || done.ErrorCode != nil {
		t.Errorf("job = %+v", *done)
	}
	item, _ := h.store.GetItem(h.ctx, it.Info.ItemID)
	if item.Cursor == nil || *item.Cursor != "cursor-final" {
		t.Errorf("item not synced: %+v", *item)
	}
	runs, _ := h.store.ListSyncRuns(h.ctx, it.Info.ItemID, 5)
	if len(runs) != 1 || runs[0].JobID == nil || *runs[0].JobID != job.JobID || runs[0].Trigger != store.JobKindInitial {
		t.Errorf("runs = %+v", runs)
	}
}

func TestRunnerCoalescesQueuedJobs(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	// Not started: jobs stay queued.
	a, err := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindWebhook)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindManual)
	if err != nil {
		t.Fatal(err)
	}
	if a.JobID != b.JobID {
		t.Errorf("second enqueue created a new job: %s vs %s", a.JobID, b.JobID)
	}
	jobs, _ := h.store.ListJobs(h.ctx, it.Info.ItemID, 10)
	if len(jobs) != 1 {
		t.Errorf("jobs = %d, want 1", len(jobs))
	}
}

func TestRunnerEnqueueRefusals(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	if _, err := h.runner.Enqueue(h.ctx, "nope", store.JobKindManual); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown item = %v", err)
	}
	if err := h.store.SetItemStatus(h.ctx, it.Info.ItemID, store.ItemStatusLoginRequired, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindManual); !errors.Is(err, ErrNotSyncable) {
		t.Errorf("login required = %v", err)
	}
	if err := h.store.MarkItemRemoved(h.ctx, it.Info.ItemID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindManual); !errors.Is(err, ErrItemRemoved) {
		t.Errorf("removed = %v", err)
	}
}

func TestRunnerDebouncesManualSyncs(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	h.start()

	first, _ := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindManual)
	if job := h.waitJob(first.JobID); job.State != store.JobStateSucceeded {
		t.Fatalf("first job = %+v", *job)
	}
	<-h.done

	second, _ := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindManual)
	job := h.waitJob(second.JobID)
	if job.State != store.JobStateSkipped || job.ErrorCode == nil || *job.ErrorCode != CodeDebounced {
		t.Errorf("second job = %+v", *job)
	}
	if calls := h.fake.CallsTo(plaidtest.OpSyncTransactions); len(calls) != 2 {
		t.Errorf("Plaid was called %d times; the debounced job must not call it", len(calls))
	}

	// A webhook-triggered sync is never debounced.
	it.Pages["cursor-final"] = plaidtest.SyncPage(plaidtest.FixtureSyncNotReady)
	it.Pages["cursor-final"].NextCursor = "cursor-final"
	third, _ := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindWebhook)
	if job := h.waitJob(third.JobID); job.State != store.JobStateSucceeded {
		t.Errorf("webhook job = %+v", *job)
	}
	<-h.done
}

func TestRunnerRecordsFailures(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	h.fake.FailNext(plaidtest.OpSyncTransactions, plaidtest.PlaidError(plaidtest.FixtureErrorLoginRequired, "/transactions/sync", 400))
	h.start()

	job, _ := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindWebhook)
	done := h.waitJob(job.JobID)
	if done.State != store.JobStateFailed || done.ErrorCode == nil || *done.ErrorCode != string(store.SyncOutcomeNeedsReauth) || done.ErrorMessage == nil {
		t.Errorf("job = %+v", *done)
	}
	<-h.done
	// The item is now waiting for a human; a new job is skipped, not run.
	if _, err := h.runner.Enqueue(h.ctx, it.Info.ItemID, store.JobKindWebhook); !errors.Is(err, ErrNotSyncable) {
		t.Errorf("enqueue after reauth = %v", err)
	}
	queued, err := h.store.CreateJob(h.ctx, it.Info.ItemID, store.JobKindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	h.runner.signal()
	if job := h.waitJob(queued.JobID); job.State != store.JobStateSkipped || *job.ErrorCode != CodeNeedsReauth {
		t.Errorf("direct job = %+v", *job)
	}
}

func TestRunnerRequeuesInterruptedJobs(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	job, _ := h.store.CreateJob(h.ctx, it.Info.ItemID, store.JobKindManual)
	if err := h.store.StartJob(h.ctx, job.JobID); err != nil {
		t.Fatal(err)
	}
	n, err := h.store.RequeueRunningJobs(h.ctx)
	if err != nil || n != 1 {
		t.Fatalf("requeue = %d, %v", n, err)
	}
	got, _ := h.store.GetJob(h.ctx, job.JobID)
	if got.State != store.JobStateQueued || got.StartedAt != nil {
		t.Errorf("job = %+v", *got)
	}
	// Run picks it up.
	h.start()
	if done := h.waitJob(job.JobID); done.State != store.JobStateSucceeded {
		t.Errorf("job after restart = %+v", *done)
	}
}

func TestRunnerScheduleAll(t *testing.T) {
	h := newHarness(t, testConfig())
	a := h.link(plaidtest.CIBCItem())
	b := plaidtest.CIBCItem()
	b.Info.ItemID = "item-b"
	b.AccessToken = secret.NewToken("access-b")
	h.link(b)
	c := plaidtest.CIBCItem()
	c.Info.ItemID = "item-c"
	c.AccessToken = secret.NewToken("access-c")
	h.link(c)
	if err := h.store.SetItemStatus(h.ctx, c.Info.ItemID, store.ItemStatusLoginRequired, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetItemStatus(h.ctx, b.Info.ItemID, store.ItemStatusError, &store.ItemError{Code: "X", Message: "earlier"}); err != nil {
		t.Fatal(err)
	}

	if n := h.runner.ScheduleAll(h.ctx); n != 2 {
		t.Errorf("scheduled %d, want 2 (active and error, not login_required)", n)
	}
	for _, id := range []string{a.Info.ItemID, b.Info.ItemID} {
		if _, err := h.store.QueuedJob(h.ctx, id); err != nil {
			t.Errorf("%s has no queued job: %v", id, err)
		}
	}
	if _, err := h.store.QueuedJob(h.ctx, c.Info.ItemID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("login_required item was scheduled: %v", err)
	}
	// A second sweep does not duplicate.
	if n := h.runner.ScheduleAll(h.ctx); n != 2 {
		t.Errorf("second sweep = %d", n)
	}
	jobs, _ := h.store.ListJobs(h.ctx, a.Info.ItemID, 10)
	if len(jobs) != 1 {
		t.Errorf("duplicate jobs after second sweep: %d", len(jobs))
	}
}

func TestClaimJobIsExclusive(t *testing.T) {
	h := newHarness(t, testConfig())
	it := h.link(plaidtest.CIBCItem())
	for i := 0; i < 3; i++ {
		if _, err := h.store.CreateJob(h.ctx, it.Info.ItemID, store.JobKindScheduled); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		job, err := h.store.ClaimJob(h.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if seen[job.JobID] || job.State != store.JobStateRunning || job.StartedAt == nil {
			t.Errorf("claim %d = %+v", i, *job)
		}
		seen[job.JobID] = true
	}
	if _, err := h.store.ClaimJob(h.ctx); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("empty queue = %v", err)
	}
}
