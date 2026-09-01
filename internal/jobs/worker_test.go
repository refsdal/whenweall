package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestWorkerProcessesJob(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	done := make(chan string, 1)
	w.Register("t:ok", func(_ context.Context, job jobs.Job) error {
		done <- job.ID
		return nil
	})

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{Kind: "t:ok", RunAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}

	select {
	case <-done:
	default:
		t.Error("handler was not invoked")
	}

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (job completed and removed)", count)
	}
}

func TestWorkerFailureSchedulesRetry(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	w.Register("t:fail", func(_ context.Context, _ jobs.Job) error {
		return errors.New("boom")
	})

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "t:fail", RunAt: time.Now().Add(-time.Second), MaxAttempts: 5,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}

	var attempts int
	var lastError string
	if err := d.QueryRowContext(ctx, "SELECT attempts, last_error FROM scheduled_jobs").Scan(&attempts, &lastError); err != nil {
		t.Fatalf("select: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if !strings.Contains(lastError, "boom") {
		t.Errorf("last_error = %q, want to contain %q", lastError, "boom")
	}
}

func TestWorkerRecoversPanic(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	w.Register("t:panic", func(_ context.Context, _ jobs.Job) error {
		panic("kaboom")
	})

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "t:panic", RunAt: time.Now().Add(-time.Second), MaxAttempts: 5,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce should recover the panic and return nil error, got: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}

	var attempts int
	var lastError string
	if err := d.QueryRowContext(ctx, "SELECT attempts, last_error FROM scheduled_jobs").Scan(&attempts, &lastError); err != nil {
		t.Fatalf("select: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (job failed, not lost)", attempts)
	}
	if !strings.Contains(lastError, "kaboom") {
		t.Errorf("last_error = %q, want to contain %q", lastError, "kaboom")
	}
}

func TestWorkerUnknownKindDeadLettersEventually(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	// Deliberately no handler registered for "t:unknown".

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "t:unknown", RunAt: time.Now().Add(-time.Second), MaxAttempts: 2,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := d.ExecContext(ctx, "UPDATE scheduled_jobs SET run_at = now() - interval '1 second'"); err != nil {
			t.Fatalf("force due: %v", err)
		}
		processed, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce iteration %d: %v", i, err)
		}
		if processed != 1 {
			t.Fatalf("iteration %d: processed = %d, want 1", i, processed)
		}
	}

	dead, err := jobs.Dead(ctx, d, 10)
	if err != nil {
		t.Fatalf("Dead: %v", err)
	}
	if len(dead) != 1 {
		t.Fatalf("len(dead) = %d, want 1", len(dead))
	}

	var lastError string
	if err := d.QueryRowContext(ctx, "SELECT last_error FROM scheduled_jobs").Scan(&lastError); err != nil {
		t.Fatalf("select last_error: %v", err)
	}
	if !strings.Contains(lastError, "no handler registered") {
		t.Errorf("last_error = %q, want to contain %q", lastError, "no handler registered")
	}
}
