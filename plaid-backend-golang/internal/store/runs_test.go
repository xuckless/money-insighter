package store

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// fullSyncRun returns a SyncRun with every nullable field set and every
// count distinct, so a column swap in the INSERT or SELECT would show up.
func fullSyncRun(itemID, jobID string, startedAt time.Time) SyncRun {
	return SyncRun{
		ItemID:          itemID,
		JobID:           optStr(jobID),
		Trigger:         JobKindWebhook,
		StartedAt:       startedAt,
		FinishedAt:      startedAt.Add(3*time.Second + 250*time.Microsecond),
		CursorBefore:    optStr("cursor-before"),
		CursorAfter:     optStr("cursor-after"),
		Pages:           3,
		Added:           41,
		Modified:        5,
		Removed:         2,
		Inserted:        40,
		Updated:         4,
		Superseded:      1,
		AccountsSeen:    2,
		AccountsMissing: 1,
		Outcome:         SyncOutcomeSuccess,
		ErrorCode:       optStr("NONE"),
		ErrorType:       optStr("NONE_TYPE"),
		ErrorMessage:    optStr("no error, but the columns are filled"),
		RequestID:       optStr("req_abc123"),
	}
}

// wantSyncRunEqual compares two runs field by field, treating timestamps
// as instants (pgx returns them in the local zone).
func wantSyncRunEqual(t *testing.T, got, want SyncRun) {
	t.Helper()
	if !got.StartedAt.Equal(want.StartedAt.Truncate(time.Microsecond)) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if !got.FinishedAt.Equal(want.FinishedAt.Truncate(time.Microsecond)) {
		t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, want.FinishedAt)
	}
	// Neutralise the timestamps and compare everything else at once.
	got.StartedAt, want.StartedAt = time.Time{}, time.Time{}
	got.FinishedAt, want.FinishedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SyncRun mismatch:\n got %s\nwant %s", describeSyncRun(got), describeSyncRun(want))
	}
}

// describeSyncRun renders a run with pointers dereferenced, for failure
// messages.
func describeSyncRun(r SyncRun) string {
	deref := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	return "{RunID:" + strconv.FormatInt(r.RunID, 10) + " ItemID:" + r.ItemID + " JobID:" + deref(r.JobID) +
		" Trigger:" + string(r.Trigger) + " CursorBefore:" + deref(r.CursorBefore) +
		" CursorAfter:" + deref(r.CursorAfter) + " Outcome:" + string(r.Outcome) +
		" ErrorCode:" + deref(r.ErrorCode) + " ErrorType:" + deref(r.ErrorType) +
		" ErrorMessage:" + deref(r.ErrorMessage) + " RequestID:" + deref(r.RequestID) + "}"
}

func TestRecordSyncRunRoundTripWithEveryFieldSet(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_runs")
	job, err := s.CreateJob(ctx, "item_runs", JobKindWebhook)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	startedAt := time.Date(2024, time.March, 31, 23, 59, 58, 123456789, time.FixedZone("PDT", -7*3600))
	want := fullSyncRun("item_runs", job.JobID, startedAt)
	want.RunID = 999 // must be ignored on input

	runID, err := s.RecordSyncRun(ctx, want)
	if err != nil {
		t.Fatalf("RecordSyncRun: %v", err)
	}
	if runID <= 0 {
		t.Fatalf("RecordSyncRun returned run_id %d, want > 0", runID)
	}
	if runID == 999 {
		t.Errorf("RecordSyncRun used the caller's RunID instead of the sequence")
	}

	runs, err := s.ListSyncRuns(ctx, "item_runs", 10)
	if err != nil {
		t.Fatalf("ListSyncRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("ListSyncRuns returned %d runs, want 1", len(runs))
	}
	want.RunID = runID
	wantSyncRunEqual(t, runs[0], want)

	// The UUID went into the native column and came back canonical; the
	// timestamps kept their instants at microsecond precision.
	var jobIDText, startedText string
	if err := s.pool.QueryRow(ctx, `SELECT job_id::text, (started_at AT TIME ZONE 'UTC')::text FROM sync_runs WHERE run_id = $1`, runID).Scan(&jobIDText, &startedText); err != nil {
		t.Fatalf("read text forms: %v", err)
	}
	if jobIDText != job.JobID {
		t.Errorf("job_id stored as %q, want %q", jobIDText, job.JobID)
	}
	if startedText != "2024-04-01 06:59:58.123456" {
		t.Errorf("started_at stored as %q (UTC), want 2024-04-01 06:59:58.123456", startedText)
	}
}

func TestRecordSyncRunRoundTripWithNils(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_nils")

	now := time.Now()
	want := SyncRun{
		ItemID:     "item_nils",
		Trigger:    JobKindScheduled,
		StartedAt:  now,
		FinishedAt: now, // a zero-length run is legal
		Outcome:    SyncOutcomeLocked,
	}
	runID, err := s.RecordSyncRun(ctx, want)
	if err != nil {
		t.Fatalf("RecordSyncRun: %v", err)
	}
	runs, err := s.ListSyncRuns(ctx, "item_nils", 10)
	if err != nil {
		t.Fatalf("ListSyncRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("ListSyncRuns returned %d runs, want 1", len(runs))
	}
	want.RunID = runID
	wantSyncRunEqual(t, runs[0], want)
	got := runs[0]
	if got.JobID != nil || got.CursorBefore != nil || got.CursorAfter != nil ||
		got.ErrorCode != nil || got.ErrorType != nil || got.ErrorMessage != nil || got.RequestID != nil {
		t.Errorf("nil inputs came back non-nil: %s", describeSyncRun(got))
	}
	if got.Pages != 0 || got.Added != 0 || got.Inserted != 0 || got.AccountsMissing != 0 {
		t.Errorf("zero counts came back non-zero: %+v", got)
	}

	// Every outcome and trigger is accepted by both the store and the
	// database.
	for _, o := range []SyncOutcome{SyncOutcomeSuccess, SyncOutcomeRetryableError, SyncOutcomeNeedsReauth, SyncOutcomeFatal, SyncOutcomeLocked, SyncOutcomeCanceled, SyncOutcomeError} {
		for _, tr := range []JobKind{JobKindInitial, JobKindManual, JobKindWebhook, JobKindScheduled} {
			r := want
			r.Outcome, r.Trigger = o, tr
			if _, err := s.RecordSyncRun(ctx, r); err != nil {
				t.Errorf("RecordSyncRun(outcome %q, trigger %q): %v", o, tr, err)
			}
		}
	}
}

func TestRecordSyncRunRejections(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_rej")
	job, err := s.CreateJob(ctx, "item_rej", JobKindManual)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	base := fullSyncRun("item_rej", job.JobID, time.Now())

	notFound := map[string]func(r *SyncRun){
		"unknown item":      func(r *SyncRun) { r.ItemID = "item_unknown" },
		"unknown job":       func(r *SyncRun) { r.JobID = optStr("a3bb189e-8bf9-4888-9912-ace4e6543002") },
		"malformed job id":  func(r *SyncRun) { r.JobID = optStr("job-1") },
		"empty job id text": func(r *SyncRun) { r.JobID = optStr("") },
	}
	for name, mutate := range notFound {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			if _, err := s.RecordSyncRun(ctx, r); !errors.Is(err, ErrNotFound) {
				t.Errorf("RecordSyncRun = %v, want ErrNotFound", err)
			}
		})
	}

	invalid := map[string]func(r *SyncRun){
		"empty item id":               func(r *SyncRun) { r.ItemID = "" },
		"unknown trigger":             func(r *SyncRun) { r.Trigger = "cron" },
		"empty trigger":               func(r *SyncRun) { r.Trigger = "" },
		"unknown outcome":             func(r *SyncRun) { r.Outcome = "ok" },
		"empty outcome":               func(r *SyncRun) { r.Outcome = "" },
		"zero started_at":             func(r *SyncRun) { r.StartedAt = time.Time{} },
		"zero finished_at":            func(r *SyncRun) { r.FinishedAt = time.Time{} },
		"finished before started":     func(r *SyncRun) { r.FinishedAt = r.StartedAt.Add(-time.Second) },
		"negative pages":              func(r *SyncRun) { r.Pages = -1 },
		"negative added":              func(r *SyncRun) { r.Added = -1 },
		"negative modified":           func(r *SyncRun) { r.Modified = -1 },
		"negative removed":            func(r *SyncRun) { r.Removed = -1 },
		"negative inserted":           func(r *SyncRun) { r.Inserted = -1 },
		"negative updated":            func(r *SyncRun) { r.Updated = -1 },
		"negative superseded":         func(r *SyncRun) { r.Superseded = -1 },
		"negative accounts_seen":      func(r *SyncRun) { r.AccountsSeen = -1 },
		"negative accounts_missing":   func(r *SyncRun) { r.AccountsMissing = -1 },
		"outcome with trailing space": func(r *SyncRun) { r.Outcome = "success " },
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			_, err := s.RecordSyncRun(ctx, r)
			if err == nil {
				t.Fatal("RecordSyncRun succeeded, want error")
			}
			if errors.Is(err, ErrNotFound) {
				t.Errorf("RecordSyncRun = %v, want a validation error, not ErrNotFound", err)
			}
		})
	}

	runs, err := s.ListSyncRuns(ctx, "item_rej", 100)
	if err != nil {
		t.Fatalf("ListSyncRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("rejected calls recorded %d runs", len(runs))
	}
}

func TestListSyncRunsNewestFirst(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_order")
	seedItem(t, s, "item_other")

	empty, err := s.ListSyncRuns(ctx, "item_order", 10)
	if err != nil {
		t.Fatalf("ListSyncRuns on empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("ListSyncRuns on empty = %#v, want empty non-nil slice", empty)
	}

	// Insert out of chronological order so insertion order and run_id
	// order both differ from started_at order.
	t0 := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	offsets := []time.Duration{2 * time.Hour, 0, 3 * time.Hour, time.Hour}
	ids := make([]int64, len(offsets))
	for i, off := range offsets {
		r := SyncRun{ItemID: "item_order", Trigger: JobKindScheduled, StartedAt: t0.Add(off), FinishedAt: t0.Add(off + time.Minute), Outcome: SyncOutcomeSuccess, Pages: i}
		id, err := s.RecordSyncRun(ctx, r)
		if err != nil {
			t.Fatalf("RecordSyncRun %d: %v", i, err)
		}
		ids[i] = id
	}
	if _, err := s.RecordSyncRun(ctx, SyncRun{ItemID: "item_other", Trigger: JobKindManual, StartedAt: t0.Add(10 * time.Hour), FinishedAt: t0.Add(11 * time.Hour), Outcome: SyncOutcomeError}); err != nil {
		t.Fatalf("RecordSyncRun(other item): %v", err)
	}

	runs, err := s.ListSyncRuns(ctx, "item_order", 10)
	if err != nil {
		t.Fatalf("ListSyncRuns: %v", err)
	}
	var got []int64
	for _, r := range runs {
		got = append(got, r.RunID)
		if r.ItemID != "item_order" {
			t.Errorf("ListSyncRuns returned a run of %q", r.ItemID)
		}
	}
	want := []int64{ids[2], ids[0], ids[3], ids[1]} // 3h, 2h, 1h, 0h
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListSyncRuns order = %v, want %v (started_at DESC)", got, want)
	}

	// Ties on started_at break on run_id DESC (the later insert first).
	tie1, err := s.RecordSyncRun(ctx, SyncRun{ItemID: "item_order", Trigger: JobKindManual, StartedAt: t0.Add(5 * time.Hour), FinishedAt: t0.Add(5 * time.Hour), Outcome: SyncOutcomeLocked})
	if err != nil {
		t.Fatalf("RecordSyncRun tie 1: %v", err)
	}
	tie2, err := s.RecordSyncRun(ctx, SyncRun{ItemID: "item_order", Trigger: JobKindManual, StartedAt: t0.Add(5 * time.Hour), FinishedAt: t0.Add(5 * time.Hour), Outcome: SyncOutcomeLocked})
	if err != nil {
		t.Fatalf("RecordSyncRun tie 2: %v", err)
	}
	top, err := s.ListSyncRuns(ctx, "item_order", 2)
	if err != nil {
		t.Fatalf("ListSyncRuns(limit 2): %v", err)
	}
	if len(top) != 2 || top[0].RunID != tie2 || top[1].RunID != tie1 {
		t.Errorf("ListSyncRuns(limit 2) = %v, want run ids %d, %d", top, tie2, tie1)
	}
	for _, bad := range []int{0, -1, 1001} {
		if _, err := s.ListSyncRuns(ctx, "item_order", bad); err == nil {
			t.Errorf("ListSyncRuns(limit %d) succeeded, want error", bad)
		}
	}
}

// TestInsertSyncRunInsideTransaction proves insertSyncRun works over a
// pgx.Tx, which is how ItemTx.RecordSyncRun uses it: the audit row is
// visible only if the transaction commits, and the returned run_id is the
// one that lands.
func TestInsertSyncRunInsideTransaction(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_tx_run")

	run := SyncRun{ItemID: "item_tx_run", Trigger: JobKindInitial, StartedAt: time.Now(), FinishedAt: time.Now(), Outcome: SyncOutcomeSuccess, Inserted: 12}

	// Rolled back: nothing persists.
	boom := errors.New("boom")
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		id, err := insertSyncRun(ctx, tx, run)
		if err != nil {
			return err
		}
		if id <= 0 {
			t.Errorf("insertSyncRun in tx returned run_id %d", id)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("withTx = %v, want boom", err)
	}
	runs, err := s.ListSyncRuns(ctx, "item_tx_run", 10)
	if err != nil {
		t.Fatalf("ListSyncRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("rolled-back run persisted: %d rows", len(runs))
	}

	// Committed: exactly that row, with the id insertSyncRun returned.
	var committedID int64
	err = s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		id, err := insertSyncRun(ctx, tx, run)
		committedID = id
		return err
	})
	if err != nil {
		t.Fatalf("withTx commit: %v", err)
	}
	runs, err = s.ListSyncRuns(ctx, "item_tx_run", 10)
	if err != nil {
		t.Fatalf("ListSyncRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != committedID || runs[0].Inserted != 12 {
		t.Errorf("after commit: %v, want one run with id %d", runs, committedID)
	}

	// A foreign key failure inside the tx surfaces as ErrNotFound and, like
	// any error, aborts the transaction.
	err = s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		bad := run
		bad.ItemID = "item_unknown"
		_, err := insertSyncRun(ctx, tx, bad)
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("insertSyncRun(unknown item) in tx = %v, want ErrNotFound", err)
	}
}
