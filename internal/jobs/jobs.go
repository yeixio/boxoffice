// Package jobs runs Box Office's background work from a queue kept in the
// database, so work survives a restart and Activity can show all of it.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yeixio/boxoffice/internal/store"
)

// Result is how a job ended.
type Result struct {
	Status  string // done, skipped or failed
	Message string
}

func Done(format string, a ...any) Result    { return Result{"done", fmt.Sprintf(format, a...)} }
func Skipped(format string, a ...any) Result { return Result{"skipped", fmt.Sprintf(format, a...)} }

// Handler does one kind of job, given its payload.
type Handler func(ctx context.Context, payload string) (Result, error)

// Runner takes jobs off the queue one at a time and runs them.
type Runner struct {
	Store *store.Store
	Log   *slog.Logger

	mu       sync.Mutex
	handlers map[string]Handler
	wake     chan struct{}
}

func New(st *store.Store, log *slog.Logger) *Runner {
	return &Runner{Store: st, Log: log, handlers: map[string]Handler{}, wake: make(chan struct{}, 1)}
}

// Handle says how to run jobs of a kind.
func (r *Runner) Handle(kind string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[kind] = h
}

// Queue adds a job and wakes the runner.
func (r *Runner) Queue(ctx context.Context, kind, subject, payload string) (store.Job, error) {
	j, err := r.Store.QueueJob(ctx, kind, subject, payload)
	if err == nil {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	return j, err
}

// Run works through the queue until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	if err := r.Store.RequeueRunning(ctx); err != nil {
		r.Log.Warn("couldn't requeue interrupted jobs", "err", err)
	}
	prune := time.NewTicker(6 * time.Hour)
	defer prune.Stop()
	for {
		for r.runOne(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-time.After(30 * time.Second):
		case <-prune.C:
			r.Store.PruneJobs(ctx, time.Now().AddDate(0, 0, -14))
		}
	}
}

// RunAll runs every queued job now, for tests.
func (r *Runner) RunAll(ctx context.Context) {
	for r.runOne(ctx) {
	}
}

func (r *Runner) runOne(ctx context.Context) bool {
	j, err := r.Store.NextJob(ctx)
	if errors.Is(err, store.ErrNotFound) || ctx.Err() != nil {
		return false
	}
	if err != nil {
		r.Log.Warn("couldn't read the job queue", "err", err)
		return false
	}
	r.mu.Lock()
	h := r.handlers[j.Kind]
	r.mu.Unlock()
	res := Result{"failed", "nothing knows how to do this job"}
	start := time.Now()
	r.Log.Debug("job started", "kind", j.Kind, "subject", j.Subject, "id", j.ID)
	if h != nil {
		res, err = safely(ctx, h, j.Payload)
		if err != nil {
			res = Result{"failed", err.Error()}
		}
	}
	if res.Status == "failed" {
		r.Log.Warn("job failed", "kind", j.Kind, "subject", j.Subject, "err", res.Message, "took", time.Since(start).Round(time.Millisecond).String())
	} else {
		r.Log.Info("job finished", "kind", j.Kind, "subject", j.Subject, "status", res.Status, "result", res.Message,
			"took", time.Since(start).Round(time.Millisecond).String())
	}
	// Record the outcome even when ctx just ended.
	if err := r.Store.FinishJob(context.WithoutCancel(ctx), j.ID, res.Status, res.Message); err != nil {
		r.Log.Warn("couldn't record a job's outcome", "err", err)
	}
	return true
}

func safely(ctx context.Context, h Handler, payload string) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the job crashed: %v", p)
		}
	}()
	return h(ctx, payload)
}
