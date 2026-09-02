// Package admin ports src/server/admin/* (the staff-only console): the audit log
// (audit.ts/audit-query.ts), the dashboard's aggregate stats (stats.ts), and — in later tasks of
// this plan — user management and the HTTP surface itself.
package admin

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/db"
)

// isoLayout matches JS's Date.prototype.toISOString(): UTC, millisecond precision, literal "Z"
// suffix. Every timestamp this package hands back goes through it (see internal/polls/timeutil.go
// for the identical convention in that package).
const isoLayout = "2006-01-02T15:04:05.000Z"

func formatISO(t time.Time) string {
	return t.UTC().Format(isoLayout)
}

// AuditEntry mirrors AdminAuditLogRow (src/server/db/schema.ts's adminAuditLog), the TS
// viewmodel the console reads directly — camelCase JSON so the frontend (a later plan) needs no
// translation layer.
type AuditEntry struct {
	ID          string          `json:"id"`
	ActorUserID *string         `json:"actorUserId"` // nil once the actor's own user row is gone (FK ON DELETE SET NULL)
	ActorEmail  string          `json:"actorEmail"`
	Action      string          `json:"action"`
	TargetType  string          `json:"targetType"`
	TargetID    *string         `json:"targetId"`
	Reason      *string         `json:"reason"`
	Metadata    json.RawMessage `json:"metadata"` // raw JSON, or nil — never re-encoded, never inspected for values
	CreatedAt   string          `json:"createdAt"`
}

// Record writes one audit row in the same transaction as the action it records — see the
// package's callers, which always pass their own tx rather than the pool. There is deliberately
// no counterpart that edits or removes a row: the trail is the only thing separating legitimate
// support access from misuse, so it must not be erasable from inside the application (ports
// audit.ts's own doc comment, and the "exports no way to change or remove a recorded action" test
// that guards it in the TS source — mirrored here as TestRecord_HasNoMutatorCounterpart).
func Record(ctx context.Context, tx db.DBTX, actor *auth.Session, action, targetType, targetID, reason string, metadata any) error {
	if actor == nil {
		return errors.New("admin: Record requires a non-nil actor session")
	}
	actorUserID, err := strconv.ParseInt(actor.UserID, 10, 64)
	if err != nil {
		return fmt.Errorf("admin: actor session has a non-numeric user id %q: %w", actor.UserID, err)
	}

	var metadataJSON []byte
	if metadata != nil {
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("admin: marshalling audit metadata: %w", err)
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO admin_audit_log (id, actor_user_id, actor_email, action, target_type, target_id, reason, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, now())
	`, db.NewID(), actorUserID, actor.Email, action, targetType, targetID, reason, nullableJSON(metadataJSON))
	return err
}

// nullableJSON returns nil (a SQL NULL) for an empty/absent metadata payload, and b otherwise —
// pgx scans a nil []byte as NULL for a jsonb column, but an empty non-nil slice would be invalid
// JSON.
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// defaultListLimit/maxListLimit match fetchAdminAuditLog's own validator in admin.functions.ts
// (z.number().int().min(1).max(200).default(100)).
const (
	defaultListLimit = 100
	maxListLimit     = 200
)

// AuditFilter narrows List's result set. Every field is optional; the zero value lists everything,
// newest first.
type AuditFilter struct {
	Action     string
	ActorEmail string
	TargetType string
	TargetID   string
	// Cursor is opaque — always the nextCursor a previous List call returned. Empty starts from
	// the newest row.
	Cursor string
	// Limit caps the page size; <= 0 uses defaultListLimit, and anything over maxListLimit is
	// clamped to it.
	Limit int
}

// auditWhereConditions builds the WHERE conditions List and Count share — every AuditFilter field
// except Cursor (List-only: Count reports the filtered set's true size, not one page of it) and
// Limit (not a WHERE condition at all). Returns the conditions alongside the args already bound
// for them; a caller that needs more placeholders (List's own cursor condition, its LIMIT) keeps
// extending the same slice with its own arg() closure, continuing the placeholder numbering from
// len(args).
func auditWhereConditions(f AuditFilter) (where []string, args []any) {
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Action != "" {
		where = append(where, "action = "+arg(f.Action))
	}
	if f.ActorEmail != "" {
		where = append(where, "actor_email = "+arg(f.ActorEmail))
	}
	if f.TargetType != "" {
		where = append(where, "target_type = "+arg(f.TargetType))
	}
	if f.TargetID != "" {
		where = append(where, "target_id = "+arg(f.TargetID))
	}
	return where, args
}

// List reads the audit log newest first, walking it as a (created_at, id) keyset so concurrent
// inserts can never shift a page underneath the caller (a straight OFFSET would). nextCursor is
// "" once the last page has been reached.
//
// Fetches limit+1 rows rather than exactly limit: the naive "nextCursor whenever we got a full
// page" version wrongly set one whenever the result happened to land exactly on a page boundary
// (e.g. precisely 100 rows left and Limit 100), pointing at a next page that then came back empty
// — not a data bug (the walk still terminated, just with one wasted empty round trip), but the
// wrong contract for nextCursor's own promise of "" meaning "no more rows". Requesting one extra
// row and trimming it off (keeping only its own cursor value, if a next page truly exists) makes
// the boundary case and the ordinary case the same code path.
func List(ctx context.Context, tx db.DBTX, f AuditFilter) ([]AuditEntry, string, error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = defaultListLimit
	case limit > maxListLimit:
		limit = maxListLimit
	}

	where, args := auditWhereConditions(f)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Cursor != "" {
		cursorCreatedAt, cursorID, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("admin: invalid audit cursor: %w", err)
		}
		where = append(where, fmt.Sprintf("(created_at, id) < (%s, %s)", arg(cursorCreatedAt), arg(cursorID)))
	}

	query := `
		SELECT id, actor_user_id, actor_email, action, target_type, target_id, reason, metadata, created_at
		FROM admin_audit_log
	`
	if len(where) > 0 {
		query += "WHERE " + strings.Join(where, " AND ") + "\n"
	}
	query += fmt.Sprintf("ORDER BY created_at DESC, id DESC LIMIT %s", arg(limit+1))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()

	entries := make([]AuditEntry, 0, limit+1)
	createdAts := make([]time.Time, 0, limit+1)
	for rows.Next() {
		var (
			e           AuditEntry
			actorUserID sql.NullInt64
			targetID    sql.NullString
			reason      sql.NullString
			metadata    []byte
			createdAt   time.Time
		)
		if err := rows.Scan(&e.ID, &actorUserID, &e.ActorEmail, &e.Action, &e.TargetType, &targetID, &reason, &metadata, &createdAt); err != nil {
			return nil, "", err
		}
		if actorUserID.Valid {
			s := strconv.FormatInt(actorUserID.Int64, 10)
			e.ActorUserID = &s
		}
		if targetID.Valid {
			e.TargetID = &targetID.String
		}
		if reason.Valid {
			e.Reason = &reason.String
		}
		if len(metadata) > 0 {
			e.Metadata = metadata
		}
		e.CreatedAt = formatISO(createdAt)
		entries = append(entries, e)
		createdAts = append(createdAts, createdAt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(entries) > limit {
		entries = entries[:limit]
		createdAts = createdAts[:limit]
		nextCursor = encodeCursor(createdAts[limit-1], entries[limit-1].ID)
	}
	return entries, nextCursor, nil
}

// CountAuditLog reports how many rows f's filter matches, ignoring Cursor/Limit entirely — the
// support console's "N results" alongside a cursor-paginated List, not itself paginated. A second
// query over the same WHERE List uses (rather than a window function folded into List's own
// query) is the simplest correct answer at the row counts this console will ever see; if that
// ever stops being true, revisit. Named CountAuditLog, not Count, to not collide with this
// package's existing Count type (stats.go's Total/Last7/Last30 viewmodel).
func CountAuditLog(ctx context.Context, tx db.DBTX, f AuditFilter) (int64, error) {
	where, args := auditWhereConditions(f)

	query := "SELECT count(*) FROM admin_audit_log"
	if len(where) > 0 {
		query += "\nWHERE " + strings.Join(where, " AND ")
	}

	var total int64
	err := tx.QueryRowContext(ctx, query, args...).Scan(&total)
	return total, err
}

// encodeCursor/decodeCursor make the (created_at, id) keyset opaque to callers — List's contract
// is "pass back whatever nextCursor you were given", not a documented format.
func encodeCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return t, parts[1], nil
}
