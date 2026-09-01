package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// claimBatchSize bounds how many jobs one RunOnce call claims, so a single poll iteration can't
// monopolize the table indefinitely if a large backlog piles up.
const claimBatchSize = 20

// defaultPollInterval is how often Run polls when PollInterval is left unset.
const defaultPollInterval = 5 * time.Second

// Handler processes one claimed job. Returning nil completes the job; returning an error fails
// it (Fail records the error and either schedules a backoff retry or, once the attempt budget is
// spent, leaves it dead-lettered).
type Handler func(ctx context.Context, job Job) error

// Worker polls scheduled_jobs and dispatches claimed rows to registered per-kind handlers.
//
// This is the poll-based replacement for Cloudflare Queues consumers and DO alarm callbacks: N
// replicas each poll independently, relying on ClaimDue's FOR UPDATE SKIP LOCKED to keep them
// from claiming the same row, rather than a push-based invocation model needing leader election.
type Worker struct {
	db           *sql.DB
	replicaID    string
	handlers     map[string]Handler
	log          *slog.Logger
	PollInterval time.Duration
}

// NewWorker builds a Worker with no handlers registered and a 5s PollInterval. An empty worker
// (nothing registered) is harmless: RunOnce still claims and dead-letters unknown-kind jobs.
func NewWorker(sqlDB *sql.DB, replicaID string, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		db:           sqlDB,
		replicaID:    replicaID,
		handlers:     make(map[string]Handler),
		log:          log,
		PollInterval: defaultPollInterval,
	}
}

// Register wires a handler for one job kind. Later registrations for the same kind replace
// earlier ones.
func (w *Worker) Register(kind string, h Handler) {
	w.handlers[kind] = h
}

// RunOnce claims up to claimBatchSize due jobs and runs each one's handler to completion,
// completing or failing it depending on the outcome. It is both the poll loop's body and a test
// seam: tests call it directly instead of waiting out a ticker.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	claimed, err := ClaimDue(ctx, w.db, w.replicaID, claimBatchSize)
	if err != nil {
		return 0, err
	}

	for _, job := range claimed {
		w.process(ctx, job)
	}
	return len(claimed), nil
}

// process runs one claimed job's handler and reconciles the outcome against the table. A
// handler error (including a recovered panic, or an unknown kind) fails the job; the dead-letter
// moment — willRetry == false — is logged at ERROR because that's when a human needs to look.
func (w *Worker) process(ctx context.Context, job Job) {
	if err := w.invoke(ctx, job); err != nil {
		willRetry, ferr := Fail(ctx, w.db, job.ID, err.Error())
		if ferr != nil {
			w.log.Error("jobs: failed to record job failure", "kind", job.Kind, "id", job.ID, "error", ferr)
			return
		}
		if willRetry {
			w.log.Warn("job failed, will retry", "kind", job.Kind, "id", job.ID, "attempts", job.Attempts, "error", err)
		} else {
			w.log.Error("job dead-lettered: attempt budget exhausted", "kind", job.Kind, "id", job.ID, "attempts", job.Attempts, "error", err)
		}
		return
	}

	if err := Complete(ctx, w.db, job.ID); err != nil {
		w.log.Error("jobs: failed to complete job", "kind", job.Kind, "id", job.ID, "error", err)
	}
}

// invoke dispatches to the registered handler, converting an unknown kind or a recovered panic
// into a plain error so process's Complete/Fail logic doesn't need to know about either.
func (w *Worker) invoke(ctx context.Context, job Job) (err error) {
	h, ok := w.handlers[job.Kind]
	if !ok {
		return fmt.Errorf("no handler registered for kind %q", job.Kind)
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panicked: %v", r)
		}
	}()
	return h(ctx, job)
}

// Run polls every PollInterval (defaulting to 5s) until ctx is done. This stays poll-only by
// design: Schedule already emits a NOTIFY on JobsChannel as a latency optimisation, but the poll
// loop is the source of truth and remains correct even if a NOTIFY is dropped. A LISTEN-based
// wake-up can ride on the rooms listener later if wanted.
func (w *Worker) Run(ctx context.Context) {
	interval := w.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil {
				w.log.Error("jobs: poll failed", "error", err)
			}
		}
	}
}
