package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/thecodearcher/limen/plugins/organization"
)

// ErrPasswordRequired: the account has a password and the caller supplied none.
var ErrPasswordRequired = errors.New("auth: current password required")

// ErrPasswordMismatch: the supplied current password does not verify.
var ErrPasswordMismatch = errors.New("auth: current password does not match")

// CheckOwnPassword re-verifies a signed-in user's current password before a destructive action
// (DELETE /api/v1/me). An account with no password at all (OAuth-only) passes with any input — a
// credential that does not exist cannot be re-entered. Verification uses Limen's own Argon2id
// hasher through credential-password's exported API (ComparePassword), never a re-implementation.
func (s *Service) CheckOwnPassword(ctx context.Context, userID, password string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrNoSuchUser
	}
	var hash sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT password FROM users WHERE id = $1`, uid).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchUser
	}
	if err != nil {
		return fmt.Errorf("auth: loading password hash: %w", err)
	}
	if !hash.Valid || hash.String == "" {
		return nil
	}
	if password == "" {
		return ErrPasswordRequired
	}
	ok, err := s.passwords.ComparePassword(password, &hash.String)
	if err != nil {
		return fmt.Errorf("auth: comparing password: %w", err)
	}
	if !ok {
		return ErrPasswordMismatch
	}
	return nil
}

// DeleteOwnAccount deletes the caller's own account with exactly admin.DeleteUser's cascade
// (CascadeDeleteUser) — sole-owned organizations and their content go, co-owned ones survive. The
// HTTP layer is responsible for CheckOwnPassword first. Limen sessions are revoked best-effort
// before the transaction (same as the admin path); the transaction's own DELETE FROM sessions is
// the hard guarantee. The in-process personal-org cache is cleared so a re-registration under the
// same id (impossible with BIGSERIAL, but cheap to be correct about) is never skipped.
func (s *Service) DeleteOwnAccount(ctx context.Context, userID string) error {
	_ = s.RevokeUserSessions(ctx, userID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin delete-account tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := CascadeDeleteUser(ctx, tx, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit delete-account tx: %w", err)
	}
	s.personalOrgEnsured.Delete(userID)
	return nil
}

// OrgSummary is one row of ListUserOrganizations — what the SPA's org switcher renders. ID is the
// stringified organization id (the seam's convention), because Limen's own GET /organizations/
// serializes organizations WITHOUT an id (SerializeModel drops non-string ids), which is why the
// switcher cannot be built on Limen's routes alone.
type OrgSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Active bool   `json:"active"`
}

// ListUserOrganizations lists every organization sess's user belongs to, by name, flagging the
// session's active one.
func (s *Service) ListUserOrganizations(ctx context.Context, sess *Session) ([]OrgSummary, error) {
	uid, err := strconv.ParseInt(sess.UserID, 10, 64)
	if err != nil {
		return nil, ErrNoSuchUser
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.name, o.slug
		FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY o.name, o.id
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("auth: listing organizations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]OrgSummary, 0)
	for rows.Next() {
		var (
			id         int64
			name, slug string
		)
		if err := rows.Scan(&id, &name, &slug); err != nil {
			return nil, fmt.Errorf("auth: scanning organization: %w", err)
		}
		idStr := strconv.FormatInt(id, 10)
		out = append(out, OrgSummary{ID: idStr, Name: name, Slug: slug, Active: idStr == sess.ActiveOrgID})
	}
	return out, rows.Err()
}

// SwitchOrganization makes orgID the active organization of the Limen session carried by r,
// after verifying membership. Takes w/r rather than a ctx because it needs Limen's own session
// (GetSession) and must forward any re-issued session cookie. Errors: ErrUnauthenticated (no
// Limen session), ErrForbidden (not a member, unknown or malformed org id), ErrInternal (wrapped)
// otherwise.
func (s *Service) SwitchOrganization(w http.ResponseWriter, r *http.Request, orgID string) error {
	validated, err := s.limen.GetSession(r)
	if err != nil || validated == nil || validated.User == nil || validated.Session == nil {
		return ErrUnauthenticated
	}
	id, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return ErrForbidden
	}
	if err := s.orgs.CheckMemberExistsInOrganization(r.Context(), id, validated.User.ID); err != nil {
		if errors.Is(err, organization.ErrMemberNotInOrganization) {
			return ErrForbidden
		}
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	_, result, err := s.orgs.SwitchOrganization(r.Context(), validated.Session, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if result != nil && result.Cookie != nil {
		http.SetCookie(w, result.Cookie)
	}
	return nil
}
