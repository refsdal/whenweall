package admin

// Ports src/server/admin/users.ts (listAdminUsers/getAdminUserDetail) plus the mutations that TS
// source never had — TS's lock/unlock is Better-Auth's admin plugin banUser/unbanUser (against
// `user.banned`/`user.banReason`, added by that plugin, not this codebase's own schema) and its
// delete is the raw `auth.api.deleteUser` call exercised by user-delete.workers.test.ts, whose
// cascade behavior lives in src/server/auth/personal-org.ts's deleteOrphanedOwnerOrganizations —
// this file ports that cascade by hand against Limen's organization/member schema (there is no
// Go analogue of a Better-Auth `databaseHooks.user.delete.before`).
//
// Limen's users table (migrations/00002_auth.sql) has no banned/locked column of its own — see
// migrations/00007_admin_locks.sql's own doc comment for why locking is instead a standalone
// `locked_users` table, enforced at the auth seam (internal/auth's resolveSession).

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/db"
)

// defaultUserListLimit/maxUserListLimit mirror audit.go's own list-limit convention (there is no
// TS analogue to port — listAdminUsers took a raw offset/limit pair with no cap of its own).
const (
	defaultUserListLimit = 50
	maxUserListLimit     = 200
)

// UserFilter narrows SearchUsers' result set. Query matches (case-insensitively) against the
// user's email or their first/last name; Cursor is opaque, always the next value a previous
// SearchUsers call returned; Limit <= 0 uses defaultUserListLimit, and anything over
// maxUserListLimit is clamped to it.
type UserFilter struct {
	Query  string
	Cursor string
	Limit  int
}

// AdminUserRow is one row of a SearchUsers page — the support console's find-a-person list.
// Mirrors AdminUserSummary (users.ts), minus `role` (TS's Better-Auth admin-plugin field): Staff
// plays that role here, sourced from staff_users the same way resolveSession's own Session.Staff
// is (session.go).
type AdminUserRow struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"emailVerified"`
	Staff         bool   `json:"staff"`
	Locked        bool   `json:"locked"`
	CreatedAt     string `json:"createdAt"`
}

// OrgMembership is one of AdminUserDetail's Orgs entries.
type OrgMembership struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Slug  string   `json:"slug"`
	Roles []string `json:"roles"`
}

// UserCounts mirrors AdminUserDetail's `counts` block (users.ts).
type UserCounts struct {
	Polls        int64 `json:"polls"`
	BookingPages int64 `json:"bookingPages"`
	Bookings     int64 `json:"bookings"`
}

// AdminUserDetail mirrors AdminUserDetail (users.ts): a user's summary row, plus the orgs they
// belong to, what they've created, and (if locked) why. RecentActions is dropped here — the
// console reads that from admin.List directly (audit.go), scoped to targetType "user"/targetId,
// rather than UserDetail re-deriving it.
type AdminUserDetail struct {
	AdminUserRow
	LockReason *string         `json:"lockReason"`
	Orgs       []OrgMembership `json:"orgs"`
	Counts     UserCounts      `json:"counts"`
}

// userSelectColumns is shared by SearchUsers and UserDetail: the row shape, minus filters/limits.
// Never widen this to `SELECT *` — users.password is credential material with no business
// reaching an admin console screen (users.ts's own SUMMARY_COLUMNS doc comment, ported verbatim).
const userSelectColumns = `
	id, email, first_name, last_name, email_verified_at, created_at,
	EXISTS(SELECT 1 FROM staff_users s WHERE s.user_id = users.id) AS staff,
	EXISTS(SELECT 1 FROM locked_users l WHERE l.user_id = users.id) AS locked
`

// scanUserRow scans one userSelectColumns row and returns both its viewmodel and the raw
// created_at (needed, un-rounded, for SearchUsers' own cursor — see its doc comment on why
// round-tripping through the ISO-formatted, millisecond-truncated CreatedAt string instead would
// risk silently dropping a row).
func scanUserRow(scan func(dest ...any) error) (AdminUserRow, time.Time, error) {
	var (
		id                  int64
		email               string
		firstName, lastName sql.NullString
		emailVerifiedAt     sql.NullTime
		createdAt           time.Time
		staff, locked       bool
	)
	if err := scan(&id, &email, &firstName, &lastName, &emailVerifiedAt, &createdAt, &staff, &locked); err != nil {
		return AdminUserRow{}, time.Time{}, err
	}
	return AdminUserRow{
		ID:            strconv.FormatInt(id, 10),
		Email:         email,
		Name:          composeUserName(firstName, lastName, email),
		EmailVerified: emailVerifiedAt.Valid,
		Staff:         staff,
		Locked:        locked,
		CreatedAt:     formatISO(createdAt),
	}, createdAt, nil
}

// composeUserName builds a display name from Limen's nullable first_name/last_name (there is no
// single `name` column, unlike Better-Auth's `user.name` — same gap internal/polls/notifications.go
// and internal/bookings/emails.go's own displayName helpers fill for mail recipients). Falls back
// to the email's local part, then the raw email, if both are blank.
func composeUserName(firstName, lastName sql.NullString, email string) string {
	first := strings.TrimSpace(firstName.String)
	last := strings.TrimSpace(lastName.String)
	name := strings.TrimSpace(strings.TrimSpace(first + " " + last))
	if name != "" {
		return name
	}
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
}

// SearchUsers is the support console's find-a-person query: a substring match on email or name
// (both ILIKE, so it's case-insensitive without needing to lower-case either side), newest first,
// walked as a (created_at, id) keyset exactly like admin.List (audit.go) — see that function's own
// doc comment for why a keyset beats OFFSET here. nextCursor is "" once the last page is reached.
func SearchUsers(ctx context.Context, tx db.DBTX, f UserFilter) ([]AdminUserRow, string, error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = defaultUserListLimit
	case limit > maxUserListLimit:
		limit = maxUserListLimit
	}

	var (
		where []string
		args  []any
	)
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if q := strings.TrimSpace(f.Query); q != "" {
		term := "%" + q + "%"
		placeholder := arg(term)
		where = append(where, fmt.Sprintf(
			"(email ILIKE %s OR (coalesce(first_name, '') || ' ' || coalesce(last_name, '')) ILIKE %s)",
			placeholder, placeholder,
		))
	}
	if f.Cursor != "" {
		cursorCreatedAt, cursorID, err := decodeUserCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("admin: invalid user cursor: %w", err)
		}
		where = append(where, fmt.Sprintf("(created_at, id) < (%s, %s)", arg(cursorCreatedAt), arg(cursorID)))
	}

	query := "SELECT " + userSelectColumns + " FROM users"
	if len(where) > 0 {
		query += "\nWHERE " + strings.Join(where, " AND ")
	}
	query += fmt.Sprintf("\nORDER BY created_at DESC, id DESC LIMIT %s", arg(limit))

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()

	users := make([]AdminUserRow, 0)
	var lastCreatedAt time.Time
	var lastID string
	for rows.Next() {
		u, createdAt, err := scanUserRow(rows.Scan)
		if err != nil {
			return nil, "", err
		}
		users = append(users, u)
		lastID = u.ID
		lastCreatedAt = createdAt
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(users) == limit {
		nextCursor = encodeUserCursor(lastCreatedAt, lastID)
	}
	return users, nextCursor, nil
}

// encodeUserCursor/decodeUserCursor mirror audit.go's encodeCursor/decodeCursor, keyed on a bigint
// user id rather than a text nanoid.
func encodeUserCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeUserCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, 0, err
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, err
	}
	return t, id, nil
}

// UserDetail returns nil (not an error) for an unknown or malformed id — a stale link in a ticket
// is an ordinary occurrence, not an exceptional one (ports getAdminUserDetail's own doc comment).
func UserDetail(ctx context.Context, tx db.DBTX, userID string) (*AdminUserDetail, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return nil, nil
	}

	row := tx.QueryRowContext(ctx, "SELECT "+userSelectColumns+" FROM users WHERE id = $1", uid)
	summary, _, err := scanUserRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("admin: loading user %s: %w", userID, err)
	}

	var lockReason sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT reason FROM locked_users WHERE user_id = $1", uid).Scan(&lockReason); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("admin: loading lock reason for user %s: %w", userID, err)
	}

	orgRows, err := tx.QueryContext(ctx, `
		SELECT o.id, o.name, o.slug,
			coalesce(string_agg(DISTINCT mr.role, ',' ORDER BY mr.role) FILTER (WHERE mr.role IS NOT NULL), '')
		FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		LEFT JOIN organization_member_roles mr ON mr.member_id = m.id
		WHERE m.user_id = $1
		GROUP BY o.id, o.name, o.slug
		ORDER BY o.name, o.id
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("admin: loading orgs for user %s: %w", userID, err)
	}
	defer func() { _ = orgRows.Close() }()

	orgs := make([]OrgMembership, 0)
	for orgRows.Next() {
		var (
			orgID    int64
			name     string
			slug     string
			rolesCSV string
		)
		if err := orgRows.Scan(&orgID, &name, &slug, &rolesCSV); err != nil {
			return nil, fmt.Errorf("admin: scanning org row for user %s: %w", userID, err)
		}
		var roles []string
		if rolesCSV != "" {
			roles = strings.Split(rolesCSV, ",")
		}
		orgs = append(orgs, OrgMembership{ID: strconv.FormatInt(orgID, 10), Name: name, Slug: slug, Roles: roles})
	}
	if err := orgRows.Err(); err != nil {
		return nil, fmt.Errorf("admin: reading orgs for user %s: %w", userID, err)
	}

	var counts UserCounts
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*) FROM polls WHERE created_by = $1 AND deleted_at IS NULL", uid,
	).Scan(&counts.Polls); err != nil {
		return nil, fmt.Errorf("admin: counting polls for user %s: %w", userID, err)
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT count(*) FROM booking_pages WHERE created_by = $1 AND deleted_at IS NULL", uid,
	).Scan(&counts.BookingPages); err != nil {
		return nil, fmt.Errorf("admin: counting booking pages for user %s: %w", userID, err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM bookings b JOIN booking_pages bp ON bp.id = b.page_id WHERE bp.created_by = $1
	`, uid).Scan(&counts.Bookings); err != nil {
		return nil, fmt.Errorf("admin: counting bookings for user %s: %w", userID, err)
	}

	detail := &AdminUserDetail{AdminUserRow: summary, Orgs: orgs, Counts: counts}
	if lockReason.Valid {
		detail.LockReason = &lockReason.String
	}
	return detail, nil
}

// LockUser flags userID as locked (audited, in the same tx) and then revokes their existing Limen
// sessions through the auth seam — see migrations/00007_admin_locks.sql's doc comment for why both
// halves matter. authSvc may be nil in a test that only cares about the lock/audit row itself; a
// nil authSvc simply skips the revoke.
func LockUser(ctx context.Context, sqlDB *sql.DB, authSvc *auth.Service, actor *auth.Session, userID, reason string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return fmt.Errorf("admin: invalid user id %q: %w", userID, err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin: begin lock-user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO locked_users (user_id, reason, created_at) VALUES ($1, NULLIF($2, ''), now())
		ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason
	`, uid, reason); err != nil {
		return fmt.Errorf("admin: locking user %s: %w", userID, err)
	}

	if err := Record(ctx, tx, actor, "lock-user", "user", userID, reason, nil); err != nil {
		return fmt.Errorf("admin: recording audit for lock-user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("admin: commit lock-user tx: %w", err)
	}

	// Deliberately after the commit, and deliberately log-and-continue rather than returning the
	// error: the lock itself (and resolveSession's own locked_users check) is already the
	// enforcement point the moment the row above is durable, so a revoke failure here can't leave
	// a locked user able to keep using the app — it only means their (now-useless) session rows
	// linger in Limen's sessions table a little longer than necessary. Surfacing this as LockUser's
	// own error would wrongly read as "the lock didn't take" to a caller, when it did.
	if authSvc != nil {
		if err := authSvc.RevokeUserSessions(ctx, userID); err != nil {
			slog.Default().Error("admin: revoking sessions for locked user failed", "user_id", userID, "error", err)
		}
	}
	return nil
}

// UnlockUser clears userID's lock (audited, in the same tx). Unlike LockUser there is nothing to
// revoke — removing a restriction never needs to invalidate anything the user already holds.
func UnlockUser(ctx context.Context, sqlDB *sql.DB, actor *auth.Session, userID, reason string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return fmt.Errorf("admin: invalid user id %q: %w", userID, err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin: begin unlock-user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM locked_users WHERE user_id = $1`, uid); err != nil {
		return fmt.Errorf("admin: unlocking user %s: %w", userID, err)
	}

	if err := Record(ctx, tx, actor, "unlock-user", "user", userID, reason, nil); err != nil {
		return fmt.Errorf("admin: recording audit for unlock-user: %w", err)
	}

	return tx.Commit()
}

// DeleteUser ports user-delete.workers.test.ts's semantics against Limen's organization/member
// schema: every organization where userID is the sole `owner`-role member is handed to another
// member (promoting the oldest remaining one, by membership created_at) or, if userID was the
// org's last member of any role, deleted outright, taking its polls/booking pages with it via their
// own ON DELETE CASCADE from organizations. An org with another owner survives untouched, its
// content untouched, and userID's own `created_by`/`member_user_id` references in it simply go
// null via their own ON DELETE SET NULL — this function never touches those rows itself, exactly
// as personal-org.ts's own doc comment describes for the TS original.
func DeleteUser(ctx context.Context, sqlDB *sql.DB, authSvc *auth.Service, actor *auth.Session, userID, reason string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return fmt.Errorf("admin: invalid user id %q: %w", userID, err)
	}

	// Best-effort, before the tx below: reaches whatever Limen-side session bookkeeping lives
	// beyond the raw `sessions` table (see RevokeUserSessions' own doc comment). Not required for
	// correctness — the tx's own DELETE FROM sessions a few lines down is what actually satisfies
	// sessions.user_id's ON DELETE RESTRICT — so a failure here is not fatal to the delete.
	if authSvc != nil {
		_ = authSvc.RevokeUserSessions(ctx, userID)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin: begin delete-user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := cascadeOrphanedOwnerOrganizations(ctx, tx, uid); err != nil {
		return fmt.Errorf("admin: cascading organizations owned by user %s: %w", userID, err)
	}

	// accounts/sessions/two_factors all reference users(id) ON DELETE RESTRICT (migrations/00002),
	// unlike organization_members (CASCADE, which the DELETE FROM users below triggers on its
	// own) — so they must be cleared explicitly first, or that statement fails a foreign key
	// check. This is also what actually revokes any session left over from the best-effort
	// RevokeUserSessions call above.
	for _, stmt := range []string{
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM accounts WHERE user_id = $1`,
		`DELETE FROM two_factors WHERE user_id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, uid); err != nil {
			return fmt.Errorf("admin: clearing dependent rows for user %s: %w", userID, err)
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, uid)
	if err != nil {
		return fmt.Errorf("admin: deleting user %s: %w", userID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("admin: no user with id %q", userID)
	}

	if err := Record(ctx, tx, actor, "delete-user", "user", userID, reason, nil); err != nil {
		return fmt.Errorf("admin: recording audit for delete-user: %w", err)
	}

	return tx.Commit()
}

// cascadeOrphanedOwnerOrganizations is deleteOrphanedOwnerOrganizations (personal-org.ts) ported
// against Limen's schema: an organization's owners are its organization_members rows that have an
// 'owner'-named row in organization_member_roles, rather than a single `member.role` column.
func cascadeOrphanedOwnerOrganizations(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.organization_id
		FROM organization_members m
		JOIN organization_member_roles mr ON mr.member_id = m.id AND mr.role = 'owner'
		WHERE m.user_id = $1
	`, userID)
	if err != nil {
		return err
	}
	var ownedOrgIDs []int64
	for rows.Next() {
		var orgID int64
		if err := rows.Scan(&orgID); err != nil {
			_ = rows.Close()
			return err
		}
		ownedOrgIDs = append(ownedOrgIDs, orgID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	for _, orgID := range ownedOrgIDs {
		var otherOwnerExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM organization_members m2
				JOIN organization_member_roles mr2 ON mr2.member_id = m2.id AND mr2.role = 'owner'
				WHERE m2.organization_id = $1 AND m2.user_id <> $2
			)
		`, orgID, userID).Scan(&otherOwnerExists); err != nil {
			return err
		}
		if otherOwnerExists {
			continue
		}

		var oldestOtherMemberID sql.NullInt64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM organization_members
			WHERE organization_id = $1 AND user_id <> $2
			ORDER BY created_at ASC LIMIT 1
		`, orgID, userID).Scan(&oldestOtherMemberID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if oldestOtherMemberID.Valid {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO organization_member_roles (organization_id, member_id, role)
				VALUES ($1, $2, 'owner')
				ON CONFLICT (member_id, role) DO NOTHING
			`, orgID, oldestOtherMemberID.Int64); err != nil {
				return err
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM organizations WHERE id = $1`, orgID); err != nil {
			return err
		}
	}
	return nil
}
