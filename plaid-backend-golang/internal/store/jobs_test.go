package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// wantCanonicalUUID checks that id is a lowercase, dashed, version 4 UUID.
func wantCanonicalUUID(t *testing.T, what, id string) {
	t.Helper()
	norm, ok := normalizeUUID(id)
	if !ok {
		t.Fatalf("%s = %q, not a canonical UUID", what, id)
	}
	if norm != id {
		t.Errorf("%s = %q, want lowercase %q", what, id, norm)
	}
	if id[14] != '4' {
		t.Errorf("%s = %q, version nibble is %c, want 4", what, id, id[14])
	}
	if v := id[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Errorf("%s = %q, variant nibble is %c, want 8|9|a|b", what, id, v)
	}
}

func TestJobLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_jobs")

	created, err := s.CreateJob(ctx, "item_jobs", JobKindInitial)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	wantCanonicalUUID(t, "JobID", created.JobID)
	if created.ItemID != "item_jobs" || created.Kind != JobKindInitial || created.State != JobStateQueued {
		t.Errorf("created job = %+v", created)
	}
	wantRecent(t, "CreatedAt", &created.CreatedAt)
	if created.StartedAt != nil || created.FinishedAt != nil || created.ErrorCode != nil || created.ErrorMessage != nil {
		t.Errorf("fresh job has non-nil optional fields: %+v", created)
	}

	// GetJob returns exactly what CreateJob returned, and the UUID survives
	// the trip through the native UUID column unchanged.
	got, err := s.GetJob(ctx, created.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !reflect.DeepEqual(got, created) {
		t.Errorf("GetJob = %+v, want %+v", got, created)
	}
	// An uppercase spelling of the same id is the same job.
	if upper, err := s.GetJob(ctx, strings.ToUpper(created.JobID)); err != nil || upper.JobID != created.JobID {
		t.Errorf("GetJob(uppercase id) = %+v, %v; want the same job", upper, err)
	}

	// queued -> running.
	if err := s.StartJob(ctx, created.JobID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	got, err = s.GetJob(ctx, created.JobID)
	if err != nil {
		t.Fatalf("GetJob after start: %v", err)
	}
	if got.State != JobStateRunning {
		t.Errorf("State after StartJob = %q, want running", got.State)
	}
	wantRecent(t, "StartedAt", got.StartedAt)
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt = %v after StartJob, want nil", *got.FinishedAt)
	}

	// running -> succeeded, no error.
	if err := s.FinishJob(ctx, created.JobID, JobStateSucceeded, nil, nil); err != nil {
		t.Fatalf("FinishJob(succeeded): %v", err)
	}
	got, err = s.GetJob(ctx, created.JobID)
	if err != nil {
		t.Fatalf("GetJob after finish: %v", err)
	}
	if got.State != JobStateSucceeded {
		t.Errorf("State after FinishJob = %q, want succeeded", got.State)
	}
	wantRecent(t, "FinishedAt", got.FinishedAt)
	if got.StartedAt == nil || got.FinishedAt.Before(*got.StartedAt) {
		t.Errorf("FinishedAt %v is before StartedAt %v", got.FinishedAt, got.StartedAt)
	}
	if got.ErrorCode != nil || got.ErrorMessage != nil {
		t.Errorf("succeeded job carries an error: %v %v", got.ErrorCode, got.ErrorMessage)
	}

	// A second job: queued -> running -> failed with an error.
	failed, err := s.CreateJob(ctx, "item_jobs", JobKindManual)
	if err != nil {
		t.Fatalf("CreateJob(manual): %v", err)
	}
	if failed.JobID == created.JobID {
		t.Fatalf("two jobs share the id %s", failed.JobID)
	}
	if err := s.StartJob(ctx, failed.JobID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if err := s.FinishJob(ctx, failed.JobID, JobStateFailed, optStr("INSTITUTION_DOWN"), optStr("institution is down")); err != nil {
		t.Fatalf("FinishJob(failed): %v", err)
	}
	got, err = s.GetJob(ctx, failed.JobID)
	if err != nil {
		t.Fatalf("GetJob(failed): %v", err)
	}
	if got.State != JobStateFailed || got.Kind != JobKindManual {
		t.Errorf("failed job = %+v", got)
	}
	wantStr(t, "ErrorCode", got.ErrorCode, optStr("INSTITUTION_DOWN"))
	wantStr(t, "ErrorMessage", got.ErrorMessage, optStr("institution is down"))

	// A third job: queued -> skipped without ever starting.
	skipped, err := s.CreateJob(ctx, "item_jobs", JobKindWebhook)
	if err != nil {
		t.Fatalf("CreateJob(webhook): %v", err)
	}
	if err := s.FinishJob(ctx, skipped.JobID, JobStateSkipped, optStr("ITEM_LOCKED"), nil); err != nil {
		t.Fatalf("FinishJob(skipped from queued): %v", err)
	}
	got, err = s.GetJob(ctx, skipped.JobID)
	if err != nil {
		t.Fatalf("GetJob(skipped): %v", err)
	}
	if got.State != JobStateSkipped || got.StartedAt != nil || got.FinishedAt == nil {
		t.Errorf("skipped job = %+v", got)
	}
	wantStr(t, "ErrorCode", got.ErrorCode, optStr("ITEM_LOCKED"))
	wantStr(t, "ErrorMessage", got.ErrorMessage, nil)

	// Every kind is accepted.
	if _, err := s.CreateJob(ctx, "item_jobs", JobKindScheduled); err != nil {
		t.Errorf("CreateJob(scheduled): %v", err)
	}
}

func TestGetJobGarbageIDIsNotFound(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	for _, id := range []string{
		"",
		"garbage",
		"not-a-uuid-at-all-but-36-chars-long!",
		"0123456789abcdef0123456789abcdef",       // bare hex, no dashes
		"{a3bb189e-8bf9-3888-9912-ace4e6543002}", // braced
		"a3bb189e-8bf9-3888-9912-ace4e654300",    // one short
		"a3bb189e-8bf9-3888-9912-ace4e65430022",  // one long
		"a3bb189e-8bf9-3888-9912-ace4e654300g",   // non-hex
		"a3bb189e-8bf9-3888-9912-ace4e6543002",   // well-formed, unknown
		"'; DROP TABLE sync_jobs; --",
	} {
		job, err := s.GetJob(ctx, id)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("GetJob(%q) = %+v, %v; want ErrNotFound", id, job, err)
		}
		if err := s.StartJob(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("StartJob(%q) = %v, want ErrNotFound", id, err)
		}
		if err := s.FinishJob(ctx, id, JobStateSucceeded, nil, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("FinishJob(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestFinishJobRejectsNonTerminalState(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_finish")

	job, err := s.CreateJob(ctx, "item_finish", JobKindManual)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s.StartJob(ctx, job.JobID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}

	for _, st := range []JobState{JobStateQueued, JobStateRunning, JobState(""), JobState("done"), JobState("SUCCEEDED")} {
		err := s.FinishJob(ctx, job.JobID, st, nil, nil)
		if err == nil {
			t.Errorf("FinishJob(%q) succeeded, want error", st)
			continue
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrJobState) {
			t.Errorf("FinishJob(%q) = %v, want a plain validation error", st, err)
		}
	}

	// The job is untouched.
	got, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.State != JobStateRunning || got.FinishedAt != nil {
		t.Errorf("job changed by rejected FinishJob calls: %+v", got)
	}
}

func TestJobTransitionsAreGuarded(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_guard")

	job, err := s.CreateJob(ctx, "item_guard", JobKindScheduled)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s.StartJob(ctx, job.JobID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	// Starting twice: the first started_at must survive.
	if err := s.StartJob(ctx, job.JobID); !errors.Is(err, ErrJobState) {
		t.Errorf("second StartJob = %v, want ErrJobState", err)
	}
	if err := s.FinishJob(ctx, job.JobID, JobStateSucceeded, nil, nil); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}
	finished, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	// A finished job is frozen.
	if err := s.FinishJob(ctx, job.JobID, JobStateFailed, optStr("LATE"), optStr("too late")); !errors.Is(err, ErrJobState) {
		t.Errorf("FinishJob on finished job = %v, want ErrJobState", err)
	}
	if err := s.StartJob(ctx, job.JobID); !errors.Is(err, ErrJobState) {
		t.Errorf("StartJob on finished job = %v, want ErrJobState", err)
	}
	again, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !reflect.DeepEqual(again, finished) {
		t.Errorf("finished job changed by rejected transitions:\n got %+v\nwant %+v", again, finished)
	}
	// ErrJobState and ErrNotFound are distinct.
	if errors.Is(ErrJobState, ErrNotFound) || errors.Is(ErrNotFound, ErrJobState) {
		t.Error("ErrJobState and ErrNotFound must not match each other")
	}
}

func TestCreateJobRejections(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_cj")

	if _, err := s.CreateJob(ctx, "item_unknown", JobKindManual); !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateJob(unknown item) = %v, want ErrNotFound", err)
	}
	if _, err := s.CreateJob(ctx, "", JobKindManual); err == nil {
		t.Error("CreateJob(empty item) succeeded, want error")
	}
	for _, kind := range []JobKind{"", "cron", "MANUAL"} {
		if _, err := s.CreateJob(ctx, "item_cj", kind); err == nil {
			t.Errorf("CreateJob(kind %q) succeeded, want error", kind)
		}
	}
	jobs, err := s.ListJobs(ctx, "item_cj", 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("rejected CreateJob calls left %d rows", len(jobs))
	}

	// Jobs may be created for a removed item: the store records, the
	// caller decides whether to run.
	if err := s.MarkItemRemoved(ctx, "item_cj"); err != nil {
		t.Fatalf("MarkItemRemoved: %v", err)
	}
	if _, err := s.CreateJob(ctx, "item_cj", JobKindManual); err != nil {
		t.Errorf("CreateJob(removed item) = %v, want nil", err)
	}
}

func TestListJobsNewestFirst(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_list")
	seedItem(t, s, "item_other")

	empty, err := s.ListJobs(ctx, "item_list", 10)
	if err != nil {
		t.Fatalf("ListJobs on empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("ListJobs on empty = %#v, want empty non-nil slice", empty)
	}

	var ids []string
	for i, kind := range []JobKind{JobKindInitial, JobKindWebhook, JobKindScheduled, JobKindManual} {
		job, err := s.CreateJob(ctx, "item_list", kind)
		if err != nil {
			t.Fatalf("CreateJob %d: %v", i, err)
		}
		// Spread created_at out explicitly rather than trusting the clock
		// to tick between inserts.
		if _, err := s.pool.Exec(ctx, `UPDATE sync_jobs SET created_at = now() - make_interval(mins => $2) WHERE job_id = $1`,
			job.JobID, 10-i); err != nil {
			t.Fatalf("backdate job %d: %v", i, err)
		}
		ids = append(ids, job.JobID)
	}
	if _, err := s.CreateJob(ctx, "item_other", JobKindManual); err != nil {
		t.Fatalf("CreateJob(other item): %v", err)
	}

	jobs, err := s.ListJobs(ctx, "item_list", 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var got []string
	for _, j := range jobs {
		got = append(got, j.JobID)
		if j.ItemID != "item_list" {
			t.Errorf("ListJobs returned a job of %q", j.ItemID)
		}
	}
	want := []string{ids[3], ids[2], ids[1], ids[0]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListJobs order = %v, want %v (newest first)", got, want)
	}
	for i := 1; i < len(jobs); i++ {
		if jobs[i].CreatedAt.After(jobs[i-1].CreatedAt) {
			t.Errorf("jobs[%d].CreatedAt %v is after jobs[%d].CreatedAt %v", i, jobs[i].CreatedAt, i-1, jobs[i-1].CreatedAt)
		}
	}

	limited, err := s.ListJobs(ctx, "item_list", 2)
	if err != nil {
		t.Fatalf("ListJobs(limit 2): %v", err)
	}
	if len(limited) != 2 || limited[0].JobID != ids[3] || limited[1].JobID != ids[2] {
		t.Errorf("ListJobs(limit 2) = %v", limited)
	}
	for _, bad := range []int{0, -1, 1001} {
		if _, err := s.ListJobs(ctx, "item_list", bad); err == nil {
			t.Errorf("ListJobs(limit %d) succeeded, want error", bad)
		}
	}
}

// TestJobTimestampsComeFromTheDatabase pins that started_at and
// finished_at are stamped by now() on the server, not by a Go clock the
// caller could get wrong, and land as TIMESTAMPTZ instants.
func TestJobTimestampsComeFromTheDatabase(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_clock")

	job, err := s.CreateJob(ctx, "item_clock", JobKindManual)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s.StartJob(ctx, job.JobID); err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if err := s.FinishJob(ctx, job.JobID, JobStateSucceeded, nil, nil); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}

	var dbCreated, dbStarted, dbFinished time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT created_at, started_at, finished_at FROM sync_jobs WHERE job_id = $1`, job.JobID).
		Scan(&dbCreated, &dbStarted, &dbFinished); err != nil {
		t.Fatalf("read timestamps: %v", err)
	}
	got, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !got.CreatedAt.Equal(dbCreated) || !got.StartedAt.Equal(dbStarted) || !got.FinishedAt.Equal(dbFinished) {
		t.Errorf("Job timestamps %v %v %v differ from the columns %v %v %v",
			got.CreatedAt, got.StartedAt, got.FinishedAt, dbCreated, dbStarted, dbFinished)
	}
	if dbStarted.Before(dbCreated) || dbFinished.Before(dbStarted) {
		t.Errorf("timestamps out of order: created %v, started %v, finished %v", dbCreated, dbStarted, dbFinished)
	}
}
