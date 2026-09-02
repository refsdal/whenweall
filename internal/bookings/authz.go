// Package bookings (this file, authz.go) is Task 6's port of org-roles.ts's canManageContent —
// the identity-aware "creator, or an owner/admin of the org" gate that pages.go's/bookings.go's
// own requireOrgPage-shaped checks can't reach themselves (their brief-pinned signatures carry an
// orgID but no userID — see requireOrgPage's own doc comment in pages.go). Mirrors
// internal/polls/service.go's RequireManageable/canManagePoll pair exactly, generalized to a
// name that isn't poll-specific since this task's HTTP handler layer (handlers.go) needs the same
// gate in front of THREE different resource shapes: a booking page directly (UpdatePage/
// DeletePage/GetOwnedPage/ListPageBookings), a booking reached via its page (the organiser-cancel
// path, requirement (e)), and the org itself, which has no per-resource "creator" at all
// (SetOrgSlug/org-handle, requirement (a)).
package bookings

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/refsdal/whenweall/internal/bookings/queries"
)

// canManageContent ports org-roles.ts's canManageContent: does userID belong to organizationID AT
// ALL (any role) first — no membership is an unconditional false, checked before anything else,
// exactly like internal/polls's canManagePoll (participants.go) already does for the identical
// TS predicate — and, only for an actual member, either createdBy's own match or holding an
// 'owner'/'admin' role in organizationID. userID == "" (no signed-in identity at all) is always
// false. createdBy.Valid == false (no creator to match at all — SetOrgSlug's own call site, since
// an organization isn't "created by" a member the way a page or a booking is) simply means that
// half of the OR can never fire, so this reduces to the plain role check for that caller.
//
// The membership-first ordering matters for the same reason canManagePoll's own doc comment gives:
// someone who created a page and later left the org (or was removed) must lose the ability to
// manage it — being its creator is not itself membership.
func (s *Service) canManageContent(ctx context.Context, organizationID int64, createdBy sql.NullInt64, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return false, nil
	}
	isMember, err := s.q.IsOrgMember(ctx, queries.IsOrgMemberParams{
		OrganizationID: organizationID, UserID: userIDInt,
	})
	if err != nil {
		return false, err
	}
	if !isMember {
		return false, nil
	}
	if createdBy.Valid && createdBy.Int64 == userIDInt {
		return true, nil
	}
	return s.q.MemberHasManagingRole(ctx, queries.MemberHasManagingRoleParams{
		OrganizationID: organizationID, UserID: userIDInt,
	})
}

// RequireManageablePage ports the identity-aware half of requireManagedPage (pages.ts) that
// requireOrgPage's own signature can't reach: NOT_FOUND for a missing or wrong-org page (see
// requireOrgPage's doc comment in pages.go), then FORBIDDEN unless userID is the page's own
// creator or an owner/admin of orgID. Task 6's HTTP handler layer (handlers.go) calls this before
// GetOwnedPage/UpdatePage/DeletePage/ListPageBookings — the four T2/T3 "managing" methods whose
// own brief-pinned signatures carry no identity to check this against themselves — per this
// task's accumulated requirement (a); mirrors internal/polls/service.go's RequireManageable
// exactly.
func (s *Service) RequireManageablePage(ctx context.Context, pageID, orgID, userID string) error {
	page, err := requireOrgPage(ctx, s.q, pageID, orgID)
	if err != nil {
		return err
	}
	canManage, err := s.canManageContent(ctx, page.OrganizationID, page.CreatedBy, userID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrForbidden
	}
	return nil
}

// RequireManageableBooking is RequireManageablePage's counterpart for a booking reached by id
// rather than its page directly — the organiser-cancel path's own auth gate (requirement (e)):
// loads bookingID's own page and defers to RequireManageablePage against it. An unknown booking id
// is ErrNotFound, matching every other lookup in this package.
func (s *Service) RequireManageableBooking(ctx context.Context, bookingID, orgID, userID string) error {
	booking, err := s.q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.RequireManageablePage(ctx, booking.PageID, orgID, userID)
}

// RequireManageableOrg is RequireManageablePage's counterpart for the org itself — SetOrgSlug's
// own auth gate (requirement (a) explicitly folds "org-handle" into this same creator-or-manager
// gate, rather than porting setHandle's own stricter requireOwnerRole, TS's "owner only, not
// admin" rule for this one setting — see pages.functions.ts's own doc comment on why the TS
// source treats it differently; this Go port's task brief does not, so an org admin (not just its
// owner) can change the handle here). There is no "creator" to match for an organization itself
// (createdBy passed as sql.NullInt64{} — always invalid), so this reduces to "is userID a member
// of orgID holding the owner/admin role" — the plain half of canManageContent.
func (s *Service) RequireManageableOrg(ctx context.Context, orgID, userID string) error {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return ErrNotFound
	}
	canManage, err := s.canManageContent(ctx, orgIDInt, sql.NullInt64{}, userID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrForbidden
	}
	return nil
}
