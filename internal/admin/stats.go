package admin

import (
	"context"
	"fmt"

	"github.com/refsdal/whenweall/internal/db"
)

// Count mirrors stats.ts's own `Count` type: a total, plus how much of it arrived recently.
type Count struct {
	Total  int64 `json:"total"`
	Last7  int64 `json:"last7"`
	Last30 int64 `json:"last30"`
}

// DashboardStats mirrors AdminStats (stats.ts), minus its `revenue` block — billing is gone from
// this rewrite, so subscriptions/MRR/premium-org counts have no source of truth left to read from
// — and plus two fields the TS source had no way to express: on Cloudflare, mail delivery went
// through a managed Queue with no SQL-queryable depth; here it is scheduled_jobs rows (see
// internal/mailer's "mail:send" kind), so MailQueueDepth and FailedJobs (the dead-letter count,
// mirroring jobs.Dead's own attempts >= max_attempts predicate) are now first-class stats.
type DashboardStats struct {
	Users          Count `json:"users"`
	Orgs           Count `json:"orgs"`
	Polls          Count `json:"polls"` // live, non-signup polls — signup sheets are their own bucket below
	PollsFinalized int64 `json:"pollsFinalized"`
	SignupSheets   Count `json:"signupSheets"`
	Participants   Count `json:"participants"`
	BookingPages   Count `json:"bookingPages"`
	Bookings       Count `json:"bookings"`
	MailQueueDepth int64 `json:"mailQueueDepth"`
	FailedJobs     int64 `json:"failedJobs"`
}

// Stats gathers every number the admin dashboard shows, in one pass. Deliberately plain COUNT
// queries rather than any aggregation layer — ports stats.ts's own doc comment on
// getAdminStats verbatim: at current scale this is free, and pre-aggregating would be inventing a
// problem. If it ever gets slow, cache this function's output; do not restructure the data.
func Stats(ctx context.Context, tx db.DBTX) (*DashboardStats, error) {
	var s DashboardStats
	var err error

	if s.Users, err = growthCount(ctx, tx, "users", ""); err != nil {
		return nil, fmt.Errorf("admin: counting users: %w", err)
	}
	if s.Orgs, err = growthCount(ctx, tx, "organizations", ""); err != nil {
		return nil, fmt.Errorf("admin: counting organizations: %w", err)
	}
	if s.Polls, err = growthCount(ctx, tx, "polls", "deleted_at IS NULL AND type <> 'signup'"); err != nil {
		return nil, fmt.Errorf("admin: counting polls: %w", err)
	}
	if s.SignupSheets, err = growthCount(ctx, tx, "polls", "deleted_at IS NULL AND type = 'signup'"); err != nil {
		return nil, fmt.Errorf("admin: counting signup sheets: %w", err)
	}
	if s.Participants, err = growthCount(ctx, tx, "participants", ""); err != nil {
		return nil, fmt.Errorf("admin: counting participants: %w", err)
	}
	if s.BookingPages, err = growthCount(ctx, tx, "booking_pages", "deleted_at IS NULL"); err != nil {
		return nil, fmt.Errorf("admin: counting booking pages: %w", err)
	}
	if s.Bookings, err = growthCount(ctx, tx, "bookings", ""); err != nil {
		return nil, fmt.Errorf("admin: counting bookings: %w", err)
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM polls WHERE deleted_at IS NULL AND status = 'finalized'
	`).Scan(&s.PollsFinalized); err != nil {
		return nil, fmt.Errorf("admin: counting finalized polls: %w", err)
	}

	// "mail:send" is internal/mailer's job kind (its Enqueue schedules it) — this counts every
	// row that hasn't yet either succeeded (Fail/claim removes/updates it, so a delivered mail
	// leaves no row at all — see jobs.go's claim query) or exhausted its attempts (excluded here,
	// counted separately below as FailedJobs, exactly like a queue's own backlog vs its DLQ).
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM scheduled_jobs WHERE kind = 'mail:send' AND attempts < max_attempts
	`).Scan(&s.MailQueueDepth); err != nil {
		return nil, fmt.Errorf("admin: counting mail queue depth: %w", err)
	}

	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM scheduled_jobs WHERE attempts >= max_attempts
	`).Scan(&s.FailedJobs); err != nil {
		return nil, fmt.Errorf("admin: counting failed jobs: %w", err)
	}

	return &s, nil
}

// growthCount runs one query returning (total, last7, last30) for table, each a COUNT narrowed by
// whereExtra (a fixed SQL boolean expression — every call site above passes a literal, never
// caller-supplied text, so there is no injection surface) ANDed with the table's own created_at
// window. Collapses stats.ts's separate textCount/timestampCount (which existed only because
// Better-Auth's tables used epoch-ms integers where the app's own used ISO text) into one helper:
// every table here is timestamptz post-recut, so a single FILTER-based query replaces both.
func growthCount(ctx context.Context, tx db.DBTX, table, whereExtra string) (Count, error) {
	where := "true"
	if whereExtra != "" {
		where = whereExtra
	}
	query := fmt.Sprintf(`
		SELECT
			count(*) AS total,
			count(*) FILTER (WHERE created_at >= now() - interval '7 days') AS last7,
			count(*) FILTER (WHERE created_at >= now() - interval '30 days') AS last30
		FROM %s
		WHERE %s
	`, table, where)

	var c Count
	err := tx.QueryRowContext(ctx, query).Scan(&c.Total, &c.Last7, &c.Last30)
	return c, err
}
