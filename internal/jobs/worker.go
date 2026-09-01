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

// jobTimeout bounds a single handler invocation, well under lockTimeout (5 minutes, jobs.go).
// RunOnce claims a whole batch (claimBatchSize, 20 jobs) and runs their handlers serially in one
// goroutine before any of them is marked complete, so a single slow or stuck handler holds up
// every job claimed after it too. Without a per-job bound, one hung handler (a stalled SMTP dial,
// say) can keep its row locked past lockTimeout; once that happens, another replica's poll sees
// the row as abandoned (locked_at < now() - lockTimeout) and claims and runs it again while the
// first invocation is still in flight — a self-inflicted duplicate mail:send. jobTimeout keeps
// each handler well inside that budget so a stuck one fails fast (ctx.Done) and the batch moves
// on, instead of the lock aging out from under it.
const jobTimeout = 2 * time.Minute

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

	// JobTimeout bounds a single handler invocation; see jobTimeout's doc comment for why. It's
	// exported, like PollInterval, purely so a test can shrink it instead of waiting out the real
	// 2-minute default to exercise the timeout path; NewWorker seeds it to jobTimeout and nothing
	// outside tests should need to touch it.
	JobTimeout time.Duration
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
		JobTimeout:   jobTimeout,
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
//
// The handler runs under a per-job jobTimeout, not the bare ctx RunOnce was called with — see
// jobTimeout's doc comment for why an unbounded handler is a duplicate-send hazard, not just a
// slow one.
func (w *Worker) process(ctx context.Context, job Job) {
	timeout := w.JobTimeout
	if timeout <= 0 {
		timeout = jobTimeout
	}
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := w.invoke(jobCtx, job); err != nil {
		willRetry, rowFound, ferr := fail(ctx, w.db, job.ID, err.Error())
		if ferr != nil {
			w.log.Error("jobs: failed to record job failure", "kind", job.Kind, "id", job.ID, "error", ferr)
			return
		}
		switch {
		case willRetry:
			w.log.Warn("job failed, will retry", "kind", job.Kind, "id", job.ID, "attempts", job.Attempts, "error", err)
		case !rowFound:
			// The row is gone, not exhausted: the handler errored after already completing or
			// rescheduling itself under a fresh id (see fail's doc comment). Routine, not a
			// dead-letter — no human needs to look, so this stays well below ERROR.
			w.log.Debug("jobs: job row gone (completed or rescheduled elsewhere)", "kind", job.Kind, "id", job.ID, "error", err)
		default:
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
