package jobs

import (
	"context"
	"database/sql"
	"time"
)

// housekeepingStallAfter is how far past its run_at a housekeeping job may be before it counts as
// stalled. The worker claims due jobs within seconds, so anything this late means the chain — or
// the worker itself — has stopped.
const housekeepingStallAfter = 15 * time.Minute

// Health is the background-work signal an external monitor alerts on (#43), served by
// GET /healthz/jobs (internal/httpserver). Counts and kind names only: the route is
// unauthenticated, so no payload, id or error text ever goes in it.
type Health struct {
	// DeadLetteredLast24h counts jobs that exhausted their attempts in the last 24 hours. Older
	// ones stay on the admin console's failed-jobs screen until retried or swept, but no longer
	// hold an alert open.
	DeadLetteredLast24h int `json:"deadLetteredLast24h"`
	// StalledHousekeeping names housekeeping chains overdue by more than housekeepingStallAfter.
	// They can never dead-letter (housekeepingMaxAttempts), so this is the only way a stopped one
	// shows: the retention and cleanup it enforces simply stops happening.
	StalledHousekeeping []string `json:"stalledHousekeeping"`
}

// OK reports whether nothing in h needs a human.
func (h Health) OK() bool {
	return h.DeadLetteredLast24h == 0 && len(h.StalledHousekeeping) == 0
}

// CheckHealth reads the current Health. Dead-letter age is measured on run_at, the same "died at"
// stand-in sweepDeadLetters uses.
func CheckHealth(ctx context.Context, sqlDB *sql.DB) (Health, error) {
	h := Health{StalledHousekeeping: []string{}}
	if err := sqlDB.QueryRowContext(ctx, `
		SELECT count(*) FROM scheduled_jobs
		WHERE attempts >= max_attempts AND run_at > now() - interval '24 hours'`,
	).Scan(&h.DeadLetteredLast24h); err != nil {
		return Health{}, err
	}

	rows, err := sqlDB.QueryContext(ctx, `
		SELECT kind FROM scheduled_jobs
		WHERE room_key = $1 AND kind = ANY($2) AND run_at < $3
		ORDER BY kind`,
		housekeepingRoomKey, housekeepingKinds(), time.Now().Add(-housekeepingStallAfter))
	if err != nil {
		return Health{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return Health{}, err
		}
		h.StalledHousekeeping = append(h.StalledHousekeeping, kind)
	}
	return h, rows.Err()
}
