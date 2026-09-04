package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// CascadeDeleteUser removes userID and everything that must go with them, inside the caller's
// transaction. Shared by internal/admin.DeleteUser (staff-initiated) and Service.DeleteOwnAccount
// (self-service) so the two can never drift. Ports user-delete.workers.test.ts's semantics
// against Limen's organization/member schema: every organization where userID is the sole
// `owner`-role member is handed to another member (promoting the oldest remaining one, by
// membership created_at) or, if userID was the org's last member of any role, deleted outright,
// taking its polls/booking pages with it via their own ON DELETE CASCADE from organizations. An
// org with another owner survives untouched, its content untouched, and userID's own
// `created_by`/`member_user_id` references in it simply go null via their own ON DELETE SET NULL.
//
// Returns ErrNoSuchUser (after the cascade ran to no effect) when no users row matched. The
// caller commits; the caller also revokes Limen sessions best-effort beforehand (see
// Service.RevokeUserSessions) — the DELETE FROM sessions here is the hard FK enforcement.
func CascadeDeleteUser(ctx context.Context, tx *sql.Tx, userID string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrNoSuchUser
	}

	if err := cascadeOrphanedOwnerOrganizations(ctx, tx, uid); err != nil {
		return fmt.Errorf("auth: cascading organizations owned by user %s: %w", userID, err)
	}

	// accounts and sessions reference users(id) ON DELETE RESTRICT (migrations/00002), unlike
	// organization_members and user_preferences (CASCADE, which the DELETE FROM users below
	// triggers on its own) — so they must be cleared explicitly first, or that statement fails a
	// foreign key check. (two_factors used to be in this list; migration 00010 dropped the table.)
	for _, stmt := range []string{
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM accounts WHERE user_id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, uid); err != nil {
			return fmt.Errorf("auth: clearing dependent rows for user %s: %w", userID, err)
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, uid)
	if err != nil {
		return fmt.Errorf("auth: deleting user %s: %w", userID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchUser
	}
	return nil
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
