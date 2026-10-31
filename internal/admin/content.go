package admin

// Content moderation (#48): find one poll, sign-up sheet or booking page from whatever an abuse
// report gave staff, and suspend or lift it — audited, with a reason, like lock/unlock. What a
// suspension does to the item itself is the suspended_at column's business
// (migrations/00013_content_suspension.sql, docs/abuse-reports.md); this file only sets it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
)

// Content kinds, as they appear in the route (/api/v1/admin/content/{kind}/{id}) and as an audit
// row's target_type.
const (
	ContentKindPoll        = "poll"
	ContentKindBookingPage = "booking-page"
)

// ErrContentNotFound is returned when a kind/id names nothing, deleted rows included.
var ErrContentNotFound = errors.New("admin: content not found")

// ErrContentUnchanged is returned by SetContentSuspended when the item is already in the asked-for
// state — a 409, so a double click never writes a second audit row for a change that did not
// happen.
var ErrContentUnchanged = errors.New("admin: content already in that state")

// ContentItem is what the console shows about one item before staff act on it. Deleted and
// suspended items are included on purpose: the console is the only place to lift a suspension,
// and an owner deleting reported content after the fact is worth seeing.
type ContentItem struct {
	Kind             string     `json:"kind"`
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	PollType         *string    `json:"pollType"` // "dates" | "text" | "signup"; nil for a booking page
	OrganizationName string     `json:"organizationName"`
	OrganizationSlug string     `json:"organizationSlug"`
	CreatorEmail     *string    `json:"creatorEmail"` // nil once the creator's account is gone
	PublicPath       string     `json:"publicPath"`
	CreatedAt        time.Time  `json:"createdAt"`
	DeletedAt        *time.Time `json:"deletedAt"`
	SuspendedAt      *time.Time `json:"suspendedAt"`
}

// contentTables maps a kind to its table. Only these two values ever reach the SQL below.
var contentTables = map[string]string{
	ContentKindPoll:        "polls",
	ContentKindBookingPage: "booking_pages",
}

// LookupContent resolves q — a poll link (/p/<id>), a booking link (/book/<handle>/<slug>), or a
// bare id as in a "Report: poll <id>" subject — to the one item it names. A bare id is tried as a
// poll first, then as a booking page; ids are random and never shared between the two tables.
// Returns (nil, nil) when nothing matches.
func LookupContent(ctx context.Context, sqlDB *sql.DB, q string) (*ContentItem, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	path := q
	if u, err := url.Parse(q); err == nil && (u.Scheme != "" || strings.HasPrefix(q, "/")) {
		path = u.Path
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")

	switch {
	case len(segments) >= 2 && segments[0] == "p":
		return getContent(ctx, sqlDB, ContentKindPoll, "p.id = $1", segments[1])
	case len(segments) >= 3 && segments[0] == "book":
		return getContent(ctx, sqlDB, ContentKindBookingPage,
			"o.slug = $1 AND bp.slug = $2 AND bp.deleted_at IS NULL", segments[1], segments[2])
	case len(segments) == 1:
		item, err := getContent(ctx, sqlDB, ContentKindPoll, "p.id = $1", segments[0])
		if item != nil || err != nil {
			return item, err
		}
		return getContent(ctx, sqlDB, ContentKindBookingPage, "bp.id = $1", segments[0])
	}
	return nil, nil
}

// getContent reads one item of kind matching where (a fixed predicate from this file, never caller
// input) against args.
func getContent(ctx context.Context, sqlDB *sql.DB, kind, where string, args ...any) (*ContentItem, error) {
	var query string
	if kind == ContentKindPoll {
		query = `
			SELECT p.id, p.title, p.type, '', o.name, o.slug, u.email, p.created_at, p.deleted_at, p.suspended_at
			FROM polls p
			JOIN organizations o ON o.id = p.organization_id
			LEFT JOIN users u ON u.id = p.created_by
			WHERE ` + where
	} else {
		query = `
			SELECT bp.id, bp.title, NULL, bp.slug, o.name, o.slug, u.email, bp.created_at, bp.deleted_at, bp.suspended_at
			FROM booking_pages bp
			JOIN organizations o ON o.id = bp.organization_id
			LEFT JOIN users u ON u.id = bp.created_by
			WHERE ` + where + ` ORDER BY bp.created_at DESC LIMIT 1`
	}

	item := ContentItem{Kind: kind}
	var pollType, creator sql.NullString
	var deleted, suspended sql.NullTime
	var pageSlug string
	err := sqlDB.QueryRowContext(ctx, query, args...).Scan(&item.ID, &item.Title, &pollType, &pageSlug,
		&item.OrganizationName, &item.OrganizationSlug, &creator, &item.CreatedAt, &deleted, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pollType.Valid {
		item.PollType = &pollType.String
	}
	if creator.Valid {
		item.CreatorEmail = &creator.String
	}
	if deleted.Valid {
		item.DeletedAt = &deleted.Time
	}
	if suspended.Valid {
		item.SuspendedAt = &suspended.Time
	}

	item.PublicPath = "/p/" + item.ID
	if kind == ContentKindBookingPage {
		item.PublicPath = "/book/" + item.OrganizationSlug + "/" + pageSlug
	}
	return &item, nil
}

// SetContentSuspended suspends (suspend = true) or lifts a suspension on kind/id, writing the
// audit row in the same transaction. ErrContentNotFound for an unknown kind or id;
// ErrContentUnchanged when it is already in that state.
func SetContentSuspended(ctx context.Context, sqlDB *sql.DB, actor *auth.Session, kind, id string, suspend bool, reason string) error {
	if actor == nil {
		return errors.New("admin: SetContentSuspended requires a non-nil actor session")
	}
	table, ok := contentTables[kind]
	if !ok {
		return ErrContentNotFound
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin: begin suspend tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrContentNotFound
	}

	update := `UPDATE ` + table + ` SET suspended_at = now() WHERE id = $1 AND suspended_at IS NULL`
	action := ActionSuspendContent
	if !suspend {
		update = `UPDATE ` + table + ` SET suspended_at = NULL WHERE id = $1 AND suspended_at IS NOT NULL`
		action = ActionUnsuspendContent
	}
	res, err := tx.ExecContext(ctx, update, id)
	if err != nil {
		return fmt.Errorf("admin: %s %s %s: %w", action, kind, id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrContentUnchanged
	}

	if err := Record(ctx, tx, actor, action, kind, id, reason, nil); err != nil {
		return fmt.Errorf("admin: recording audit for %s: %w", action, err)
	}
	return tx.Commit()
}
