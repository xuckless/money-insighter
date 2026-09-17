// Package jobs runs sync jobs. sync_jobs is the queue: the API, the
// webhook receiver and the scheduler enqueue rows, a pool of workers
// claims them one at a time with SKIP LOCKED and hands each to the sync
// engine, and the row's terminal state is what a client polling
// GET /v1/jobs/{id} sees. Because the queue lives in Postgres a queued job
// survives a restart of the binary, and a job that was running when the
// process stopped is put back in the queue at the next start.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	gosync "sync"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/store"
	"plaidsync/internal/sync"
)

// pollInterval is how often an idle worker checks the queue when nothing
// has woken it. Enqueue wakes workers immediately; the poll only covers
// jobs written by something this process did not see (an operator's SQL,
// a future second process).
const pollInterval = 5 * time.Second

// finishTimeout bounds the database writes that record a job's outcome
// after the request or shutdown context that started it has gone.
const finishTimeout = 10 * time.Second

// Error codes written to sync_jobs.error_code for skipped jobs.
const (
	// CodeDebounced means a manual sync arrived within
	// PLAIDSYNC_SYNC_MIN_INTERVAL of the last successful one.
	CodeDebounced = "debounced"
	// CodeLocked means another sync of the item was in progress.
	CodeLocked = "locked"
	// CodeItemRemoved means the item has been removed.
	CodeItemRemoved = "item_removed"
	// CodeNeedsReauth means the item is waiting for Link update mode.
	CodeNeedsReauth = "needs_reauth"
	// CodeInterrupted means the process stopped while the job ran; the
	// job was requeued and ran again.
	CodeInterrupted = "interrupted"
)

// ErrItemRemoved is returned by Enqueue for a removed item.
var ErrItemRemoved = errors.New("jobs: item is removed")

// ErrNotSyncable is returned by Enqueue for an item that is waiting for a
// human to run Link in update mode. The API turns it into a 409 so a
// client does not queue work that will only be skipped.
var ErrNotSyncable = errors.New("jobs: item needs Link update mode before it can sync")

// Runner owns the worker pool and the scheduler. Build it with New, call
// Run once in its own goroutine, and Enqueue from anywhere.
type Runner struct {
	store  *store.Store
	engine *sync.Engine
	cfg    config.SyncConfig
	log    *slog.Logger
	wake   chan struct{}

	mu       gosync.Mutex // serialises Enqueue's check-then-create per process
	now      func() time.Time
	afterRun func(*sync.Result) // test hook, called after each processed job

	// notReady counts, per item, the follow-up syncs queued after Plaid
	// answered "not ready"; guarded by mu. notReadyDelays paces them.
	notReady       map[string]int
	notReadyDelays []time.Duration
}

// NotReadyDelays paces the follow-up syncs after Plaid answers
// /transactions/sync with an empty cursor: the item's initial pull is still
// in progress on Plaid's side and nothing was written. A deployment with a
// webhook URL would hear SYNC_UPDATES_AVAILABLE; one without (the desktop
// app) would otherwise show an empty connection until the next scheduler
// sweep. Sandbox is usually ready within a minute, Production within a few;
// after the last delay the item is left to the scheduler.
var NotReadyDelays = []time.Duration{
	15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute,
	5 * time.Minute, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute,
}

// New builds a Runner. logger may be nil.
func New(st *store.Store, engine *sync.Engine, cfg config.SyncConfig, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		store:  st,
		engine: engine,
		cfg:    cfg,
		log:    logger.With("component", "jobs"),
		wake:   make(chan struct{}, 1),
		now:    time.Now,

		notReady:       make(map[string]int),
		notReadyDelays: NotReadyDelays,
	}
}

// Enqueue creates a queued sync job for the item and wakes a worker. An
// item that already has a queued job gets that job back instead of a
// second one, so a burst of webhooks or clicks collapses into one sync.
// It returns store.ErrNotFound for an unknown item, ErrItemRemoved for a
// removed one and ErrNotSyncable for one in a re-auth state, so callers
// can answer precisely without a job that would only be skipped.
func (r *Runner) Enqueue(ctx context.Context, itemID string, kind store.JobKind) (*store.Job, error) {
	item, err := r.store.GetItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	switch item.Status {
	case store.ItemStatusRemoved:
		return nil, ErrItemRemoved
	case store.ItemStatusLoginRequired, store.ItemStatusPendingExpiration, store.ItemStatusPermissionRevoked:
		return nil, fmt.Errorf("%w (status %s)", ErrNotSyncable, item.Status)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if job, err := r.store.QueuedJob(ctx, itemID); err == nil {
		r.log.DebugContext(ctx, "sync already queued", "item_id", itemID, "job_id", job.JobID, "requested_kind", string(kind))
		return job, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	job, err := r.store.CreateJob(ctx, itemID, kind)
	if err != nil {
		return nil, err
	}
	r.log.InfoContext(ctx, "sync job queued", "item_id", itemID, "job_id", job.JobID, "kind", string(kind))
	r.signal()
	return job, nil
}

// signal wakes one idle worker without blocking.
func (r *Runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run requeues jobs interrupted by the previous process, then runs
// cfg.Concurrency workers and, when enabled, the scheduler, until ctx is
// canceled. It returns after every worker has stopped. A sync in flight
// when ctx is canceled is canceled too; its job stays running and is
// requeued at the next start, exactly like a crash.
func (r *Runner) Run(ctx context.Context) error {
	n, err := r.store.RequeueRunningJobs(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		r.log.Warn("requeued jobs interrupted by the previous process", "count", n)
	}

	var wg gosync.WaitGroup
	for i := 0; i < r.cfg.Concurrency; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			r.worker(ctx, worker)
		}(i + 1)
	}
	if r.cfg.SchedulerEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.scheduler(ctx)
		}()
	}
	if r.cfg.RecurringEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.refreshStaleRecurring(ctx)
		}()
	}
	r.log.Info("job runner started", "workers", r.cfg.Concurrency, "scheduler_enabled", r.cfg.SchedulerEnabled, "interval", r.cfg.Interval)
	<-ctx.Done()
	wg.Wait()
	r.log.Info("job runner stopped")
	return nil
}

// worker claims and processes jobs until ctx is canceled.
func (r *Runner) worker(ctx context.Context, n int) {
	log := r.log.With("worker", n)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := r.store.ClaimJob(ctx)
		switch {
		case err == nil:
			r.process(ctx, log, job)
			continue // there may be more; claim again before sleeping
		case errors.Is(err, store.ErrNotFound):
		case ctx.Err() != nil:
			return
		default:
			log.Error("claim job", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

// process runs one claimed job to its terminal state.
func (r *Runner) process(ctx context.Context, log *slog.Logger, job *store.Job) {
	log = log.With("job_id", job.JobID, "item_id", job.ItemID, "kind", string(job.Kind))

	item, err := r.store.GetItem(ctx, job.ItemID)
	if err != nil {
		r.finish(ctx, log, job, store.JobStateFailed, "item_lookup", err.Error())
		return
	}
	switch item.Status {
	case store.ItemStatusRemoved:
		r.finish(ctx, log, job, store.JobStateSkipped, CodeItemRemoved, "item is removed")
		return
	case store.ItemStatusLoginRequired, store.ItemStatusPendingExpiration, store.ItemStatusPermissionRevoked:
		r.finish(ctx, log, job, store.JobStateSkipped, CodeNeedsReauth, "item is "+string(item.Status)+"; Link update mode is required")
		return
	}
	if job.Kind == store.JobKindManual && r.cfg.MinInterval > 0 && item.LastSuccessfulSyncAt != nil {
		if since := r.now().Sub(*item.LastSuccessfulSyncAt); since < r.cfg.MinInterval {
			r.finish(ctx, log, job, store.JobStateSkipped, CodeDebounced,
				fmt.Sprintf("last successful sync was %s ago; manual syncs are limited to one per %s", since.Round(time.Second), r.cfg.MinInterval))
			return
		}
	}

	res := r.engine.SyncItem(ctx, job.ItemID, job.Kind, &job.JobID)
	if r.afterRun != nil {
		defer r.afterRun(res)
	}
	switch res.Outcome {
	case store.SyncOutcomeSuccess:
		r.finish(ctx, log, job, store.JobStateSucceeded, "", "")
		if res.NotReady {
			r.retryNotReady(ctx, log, job.ItemID)
		} else {
			r.mu.Lock()
			delete(r.notReady, job.ItemID)
			r.mu.Unlock()
		}
	case store.SyncOutcomeLocked:
		r.finish(ctx, log, job, store.JobStateSkipped, CodeLocked, "another sync of this item was in progress")
	case store.SyncOutcomeCanceled:
		// Shutdown. Leave the job running; RequeueRunningJobs picks it up
		// at the next start.
		log.Info("job interrupted by shutdown; it will be requeued at the next start")
	default:
		code := string(res.Outcome)
		msg := "sync failed"
		if res.Skipped {
			msg = res.Reason
		} else if res.Err != nil {
			msg = res.Err.Error()
		}
		r.finish(ctx, log, job, store.JobStateFailed, code, msg)
	}
}

// refreshStaleRecurring refreshes, one item at a time, the recurring
// streams of every syncable item never checked or last checked more than
// one scheduler interval ago. It runs once at start, which is what makes
// turning the add-on on take effect without waiting for the next sync
// (a manual sync right after a restart would be debounced). Failures are
// recorded on the item by the engine; this only logs a lookup failure.
func (r *Runner) refreshStaleRecurring(ctx context.Context) {
	ids, err := r.store.ItemsDueRecurringRefresh(ctx, r.now().Add(-r.cfg.Interval))
	if err != nil {
		if ctx.Err() == nil {
			r.log.Error("list items due a recurring refresh", "error", err)
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		_ = r.engine.RefreshRecurring(ctx, id)
	}
}

// retryNotReady queues one more initial sync for the item after the next
// delay in notReadyDelays, or gives up on follow-ups (until the scheduler
// or a manual trigger) once they are exhausted. The wait is in-process:
// a restart drops it, and the next scheduler sweep covers that case.
func (r *Runner) retryNotReady(ctx context.Context, log *slog.Logger, itemID string) {
	r.mu.Lock()
	attempt := r.notReady[itemID]
	if attempt >= len(r.notReadyDelays) {
		r.mu.Unlock()
		log.Warn("item still not ready after every follow-up; leaving it to the scheduler", "follow_ups", attempt)
		return
	}
	r.notReady[itemID] = attempt + 1
	delay := r.notReadyDelays[attempt]
	r.mu.Unlock()

	log.Info("item not ready; a follow-up sync is scheduled", "delay", delay, "follow_up", attempt+1)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if _, err := r.Enqueue(ctx, itemID, store.JobKindInitial); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("could not queue the not-ready follow-up", "error", err)
		}
	}()
}

// finish writes the job's terminal state, outliving a canceled ctx so the
// outcome of work that already happened is never lost.
func (r *Runner) finish(ctx context.Context, log *slog.Logger, job *store.Job, state store.JobState, code, msg string) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	var codeP, msgP *string
	if code != "" {
		codeP = &code
	}
	if msg != "" {
		msgP = &msg
	}
	if err := r.store.FinishJob(wctx, job.JobID, state, codeP, msgP); err != nil {
		log.Error("finish job", "state", string(state), "error", err)
		return
	}
	attrs := []any{"state", string(state)}
	if code != "" {
		attrs = append(attrs, "code", code)
	}
	log.Info("job finished", attrs...)
}

// scheduler enqueues a scheduled sync of every syncable item once per
// cfg.Interval. It does not run at start: webhooks and manual triggers
// cover the gap, and a restart must not cause a burst of Plaid calls.
func (r *Runner) scheduler(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ScheduleAll(ctx)
		}
	}
}

// ScheduleAll enqueues a scheduled sync for every item in status active or
// error and returns how many jobs it queued. It is what the scheduler
// tick does, exposed so tests and operators can trigger a sweep.
func (r *Runner) ScheduleAll(ctx context.Context) int {
	items, err := r.store.ListSyncableItems(ctx)
	if err != nil {
		r.log.ErrorContext(ctx, "scheduler: list items", "error", err)
		return 0
	}
	queued := 0
	for _, it := range items {
		if _, err := r.Enqueue(ctx, it.ItemID, store.JobKindScheduled); err != nil {
			r.log.WarnContext(ctx, "scheduler: enqueue", "item_id", it.ItemID, "error", err)
			continue
		}
		queued++
	}
	r.log.InfoContext(ctx, "scheduler sweep", "items", len(items), "queued", queued)
	return queued
}
