// Package jobs is the scheduled_jobs table: timers and the mail queue.
//
// This replaces two Cloudflare mechanisms at once. Durable Object alarms provided per-object
// timers (PollRoom's digest/deadline/reminder, BookingRoom's per-booking reminders) and
// Cloudflare Queues provided at-least-once mail delivery with retries and a dead-letter queue.
// Underneath they were the same shape — "run this later, retry it a bounded number of times" — so
// they are one table here rather than two subsystems.
//
// Jobs are claimed with FOR UPDATE SKIP LOCKED, which is what makes this safe with any number of
// replicas and without leader election: two workers cannot claim the same row, and a busy row is
// skipped rather than blocking the claimer.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/refsdal/whenweall/internal/db"
)

// lockTimeout is how long a claimed job may stay locked before another worker may reclaim it.
// It bounds the damage of a replica that is killed mid-job: the work is retried rather than
// stranded. It must exceed the slowest plausible job (a digest fanning out mail), or healthy
// jobs get run twice.
const lockTimeout = "interval '5 minutes'"

// JobsChannel is the Postgres channel used to wake idle workers the moment a job is inserted,
// instead of waiting out the poll interval. The poll loop is still the source of truth — NOTIFY
// is a latency optimisation and is allowed to be missed.
const JobsChannel = "whenweall_jobs"

// maxLastErrorLen caps how much of a failure's error text is persisted, so one enormous stack
// trace can't bloat the table.
const maxLastErrorLen = 2000

// Job is one row of scheduled_jobs.
type Job struct {
	ID          string
	Kind        string
	RoomKey     *string
	RunAt       time.Time
	Payload     json.RawMessage // nil when none
	Attempts    int
	MaxAttempts int
}

// ScheduleInput describes a job to schedule.
type ScheduleInput struct {
	Kind string
	// RoomKey is nil for jobs that are not per-room (each queued mail is independent);
	// non-nil upserts the one pending job for that (kind, room) pair.
	RoomKey     *string
	RunAt       time.Time
	Payload     any // JSON-marshalled; nil allowed
	MaxAttempts int // 0 -> 5
}

// Schedule schedules a job.
//
// For room-scoped jobs this is an upsert, not an append: PollRoom kept exactly one pending alarm
// per object and re-armed it, so scheduling poll.digest twice for the same poll must leave one
// job, not two. The partial unique index on (kind, room_key) enforces that, and the conflict
// target below resolves to it.
//
// Re-scheduling deliberately resets attempts: the caller is asking for a fresh run at a new time,
// not resuming a failing one.
func Schedule(ctx context.Context, tx db.DBTX, in ScheduleInput) error {
	maxAttempts := in.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 5
	}

	var payloadArg any
	if in.Payload != nil {
		b, err := json.Marshal(in.Payload)
		if err != nil {
			return fmt.Errorf("jobs: marshal payload: %w", err)
		}
		payloadArg = string(b)
	}

	if in.RoomKey == nil {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO scheduled_jobs (id, kind, room_key, run_at, payload, max_attempts)
			VALUES ($1, $2, NULL, $3, $4::jsonb, $5)
		`, db.NewID(), in.Kind, in.RunAt, payloadArg, maxAttempts)
		if err != nil {
			return err
		}
	} else {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO scheduled_jobs (id, kind, room_key, run_at, payload, max_attempts)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6)
			ON CONFLICT (kind, room_key) WHERE room_key IS NOT NULL
			DO UPDATE SET
				run_at = EXCLUDED.run_at,
				payload = EXCLUDED.payload,
				max_attempts = EXCLUDED.max_attempts,
				attempts = 0,
				locked_by = NULL,
				locked_at = NULL,
				last_error = NULL
		`, db.NewID(), in.Kind, *in.RoomKey, in.RunAt, payloadArg, maxAttempts)
		if err != nil {
			return err
		}
	}

	_, err := tx.ExecContext(ctx, `SELECT pg_notify($1, '')`, JobsChannel)
	return err
}

// Cancel cancels a room's pending job of one kind. The DO equivalent was deleting a storage key
// and re-arming; here there is nothing to re-arm. Cancelling a job that isn't there is not an
// error.
func Cancel(ctx context.Context, tx db.DBTX, kind, roomKey string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM scheduled_jobs WHERE kind = $1 AND room_key = $2`, kind, roomKey)
	return err
}

// ClaimDue claims up to limit due jobs for this worker.
//
// The CTE selects candidate rows with FOR UPDATE SKIP LOCKED and the outer UPDATE stamps them,
// so the selection and the claim happen in one statement — two workers polling simultaneously get
// disjoint sets rather than fighting over the same rows.
func ClaimDue(ctx context.Context, tx db.DBTX, replicaID string, limit int) ([]Job, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH due AS (
			SELECT id FROM scheduled_jobs
			WHERE run_at <= now()
			  AND attempts < max_attempts
			  AND (locked_by IS NULL OR locked_at < now() - `+lockTimeout+`)
			ORDER BY run_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE scheduled_jobs j
		SET locked_by = $2, locked_at = now(), attempts = j.attempts + 1
		FROM due
		WHERE j.id = due.id
		RETURNING j.id, j.kind, j.room_key, j.run_at, j.payload, j.attempts, j.max_attempts
	`, limit, replicaID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	claimed := make([]Job, 0)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, j)
	}
	return claimed, rows.Err()
}

// Complete removes a finished job.
func Complete(ctx context.Context, tx db.DBTX, id string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM scheduled_jobs WHERE id = $1`, id)
	return err
}

// Fail records a failure and schedules the retry.
//
// attempts was already incremented at claim time, so a job that crashes the worker outright
// still counts against its budget instead of being retried forever. Once the budget is spent the
// row is left in place with its error: attempts >= max_attempts is the dead-letter queue, which
// is where genuinely undeliverable work accumulates and needs a human — the same contract the
// Cloudflare DLQ had, minus a second piece of infrastructure.
func Fail(ctx context.Context, tx db.DBTX, id, errMsg string) (bool, error) {
	errMsg = truncateLastError(errMsg)

	row := tx.QueryRowContext(ctx, `
		UPDATE scheduled_jobs
		SET locked_by = NULL,
		    locked_at = NULL,
		    last_error = $2,
		    run_at = CASE
		        WHEN attempts < max_attempts
		        -- Exponential backoff capped at an hour: 1m, 2m, 4m, 8m, ...
		        THEN now() + LEAST(power(2, attempts) * interval '1 minute', interval '1 hour')
		        ELSE run_at
		    END
		WHERE id = $1
		RETURNING attempts < max_attempts AS will_retry
	`, id, errMsg)

	var willRetry bool
	if err := row.Scan(&willRetry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return willRetry, nil
}

// Dead is the dead-letter view: jobs that exhausted their attempts. Nothing reclaims these.
func Dead(ctx context.Context, tx db.DBTX, limit int) ([]Job, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, kind, room_key, run_at, payload, attempts, max_attempts
		FROM scheduled_jobs
		WHERE attempts >= max_attempts
		ORDER BY run_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]Job, 0)
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Retry resurrects a dead-lettered job for the admin console: it clears the failure state and
// budget so the next poll can claim it again.
func Retry(ctx context.Context, tx db.DBTX, id string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE scheduled_jobs
		SET attempts = 0, locked_by = NULL, locked_at = NULL, last_error = NULL, run_at = now()
		WHERE id = $1
	`, id)
	return err
}

// truncateLastError caps errMsg at maxLastErrorLen bytes without splitting a multi-byte rune.
// A naive byte-slice (errMsg[:maxLastErrorLen]) can land mid-rune — error text (SMTP bounce
// bodies, foreign-language content) is exactly where non-ASCII shows up — and Postgres rejects
// an UPDATE carrying invalid UTF-8 with "invalid byte sequence for encoding UTF8", which would
// make Fail itself fail and leave the job locked until LOCK_TIMEOUT instead of retrying or
// dead-lettering promptly.
func truncateLastError(s string) string {
	if len(s) <= maxLastErrorLen {
		return s
	}
	cut := maxLastErrorLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// rowScanner is the shape both *sql.Row and *sql.Rows share, so scanJob works for either.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(s rowScanner) (Job, error) {
	var j Job
	var roomKey sql.NullString
	var payload []byte
	if err := s.Scan(&j.ID, &j.Kind, &roomKey, &j.RunAt, &payload, &j.Attempts, &j.MaxAttempts); err != nil {
		return Job{}, err
	}
	if roomKey.Valid {
		j.RoomKey = &roomKey.String
	}
	if payload != nil {
		j.Payload = json.RawMessage(payload)
	}
	return j, nil
}
