package mailer

// The mail_unsubscribes suppression list — read on every notification send, written by the
// unsubscribe endpoints (internal/httpserver/unsubscribe.go).
//
// Hand-written SQL rather than sqlc, matching internal/jobs and internal/admin/stats.go: three
// statements against a two-column table, in a package that has no sqlc target of its own and
// wants none for this. The generated models still cover the table (sqlc reads every migration),
// which is why `sqlc diff` stays clean.
//
// Every function takes db.DBTX, so a caller can suppress inside its own transaction if it ever
// needs to; today's callers all pass the pool.

import (
	"context"
	"time"

	"github.com/refsdal/whenweall/internal/db"
)

// Unsubscribe sources, stored verbatim in mail_unsubscribes.source.
const (
	// SourceLink is the footer link in the mail, confirmed by a person on a page.
	SourceLink = "link"
	// SourceOneClick is RFC 8058's List-Unsubscribe-Post — the button Gmail and Yahoo render
	// beside the sender, which POSTs without ever showing our page.
	SourceOneClick = "one-click"
)

// IsSuppressed reports whether notification mail to email has been unsubscribed. The address is
// normalised first, so it can be passed exactly as it is stored on a participant or user row.
func IsSuppressed(ctx context.Context, tx db.DBTX, email string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM mail_unsubscribes WHERE email = $1)`,
		NormalizeEmail(email),
	).Scan(&exists)
	return exists, err
}

// Suppress records that email no longer wants notification mail. Idempotent: clicking the link
// twice, or a provider firing its one-click POST more than once, updates the existing row rather
// than failing on the primary key. A repeat keeps the ORIGINAL created_at — the date the person
// actually withdrew consent is the one worth being able to show — while letting source reflect
// however they last did it.
func Suppress(ctx context.Context, tx db.DBTX, email, source string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO mail_unsubscribes (email, source, created_at) VALUES ($1, $2, $3)
		 ON CONFLICT (email) DO UPDATE SET source = EXCLUDED.source`,
		NormalizeEmail(email), source, time.Now().UTC(),
	)
	return err
}

// Resubscribe removes email from the suppression list. A no-op (not an error) when it was never
// on it: a stale tab, a double-click or a re-opened link must not produce a failure page.
func Resubscribe(ctx context.Context, tx db.DBTX, email string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM mail_unsubscribes WHERE email = $1`, NormalizeEmail(email))
	return err
}
