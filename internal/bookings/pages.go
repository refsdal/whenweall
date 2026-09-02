// Package bookings is a behavioral port of src/server/bookings/{pages,schemas,viewmodel}.ts: the
// booking-page domain (an org's public scheduling pages). This file (pages.go) covers Task 2 —
// CreatePage/UpdatePage/DeletePage/ListMyPages/GetOwnedPage/GetPublicPage/SetOrgSlug. Booking
// creation itself (src/server/bookings/bookings.ts) is a later task.
package bookings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sqlc-dev/pqtype"

	"github.com/refsdal/whenweall/internal/bookings/queries"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
)

// Service is the booking-page domain service; every exported method below is a behavioral port of
// one function from src/server/bookings/pages.ts.
type Service struct {
	db *sql.DB
	q  *queries.Queries

	// manageSecret derives every booking's HMAC manage token (bookings.go's Service.manageToken/
	// verifyManageToken) — set from cfg.AuthSecret by NewService. Empty only if cfg itself carries
	// an empty AuthSecret, which config.Load's own >= 32 chars rule never allows in a running
	// server; Book still checks for it explicitly (its own doc comment) rather than trusting that.
	manageSecret string

	// google is nil ("sync off") unless SetGoogleSync wires in a real one (NewGoogleSync,
	// google.go) — every Google-touching code path in this package treats a nil google exactly
	// like a page with googleSync off, so PublicAvailability/Book/Cancel/Reschedule behave
	// unchanged for every existing caller/test that never calls SetGoogleSync.
	google GoogleSync
}

// NewService builds a Service bound to sqlDB, with Google Calendar sync off (see SetGoogleSync)
// and its manage-token secret set from cfg.AuthSecret (I4 — see manageSecret's own doc comment).
func NewService(cfg *config.Config, sqlDB *sql.DB) *Service {
	return &Service{db: sqlDB, q: queries.New(sqlDB), manageSecret: cfg.AuthSecret}
}

// SetGoogleSync wires g (typically NewGoogleSync's result) into s. A setter rather than a
// NewService parameter so every existing call site — production wiring not yet built, and every
// test that predates Task 5 — keeps compiling and behaving unchanged; g may be nil (sync stays
// off), the same value NewGoogleSync itself returns when the capability isn't configured.
func (s *Service) SetGoogleSync(g GoogleSync) {
	s.google = g
}

// isSlugConflict reports whether err is a raw Postgres unique-violation on the partial unique
// index booking_pages_org_slug_uidx (organization_id, slug WHERE deleted_at IS NULL) — the
// (org, live-slug) collision CreatePage/UpdatePage translate into ErrSlugTaken. Scoped to the
// index by name (mirroring internal/auth.isOrganizationSlugConflict's own reasoning), not just
// SQLSTATE 23505, so an unrelated unique-constraint violation is never mistaken for this one.
func isSlugConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "booking_pages_org_slug_uidx"
}

// isHandleConflict reports whether err is a raw Postgres unique-violation on
// organizations.slug (idx_organizations_slug) — SetOrgSlug's own conflict, translated to
// ErrHandleTaken.
func isHandleConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_organizations_slug"
}

// requireOrgPage fetches pageID (queries.GetBookingPage already excludes soft-deleted rows) and
// checks it belongs to orgID — the org-scoping half of requireManagedPage (pages.ts).
//
// Wrong-org page -> ErrNotFound, matching the TS source's own leak-avoidance intent: a page id's
// existence must never be revealed outside its own org. An orgID that doesn't parse as a bigint
// also -> ErrNotFound (there is no such org to belong to).
//
// Deviation from the TS source, required by the brief's exact method signatures: UpdatePage/
// DeletePage/GetOwnedPage carry an orgID but no userID, so the creator-or-manager half of
// requireManagedPage (canManageContent: "a same-org member who didn't create it gets FORBIDDEN")
// cannot be checked here — there is no identity to check it against. Matches
// internal/polls/service.go's own requireOrgPoll, which defers that identity-aware check to a
// later task's HTTP handler layer (RequireManageable) for the same signature reason.
func requireOrgPage(ctx context.Context, q *queries.Queries, pageID, orgID string) (queries.BookingPage, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return queries.BookingPage{}, ErrNotFound
	}
	page, err := q.GetBookingPage(ctx, pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return queries.BookingPage{}, ErrNotFound
	}
	if err != nil {
		return queries.BookingPage{}, err
	}
	if page.OrganizationID != orgIDInt {
		return queries.BookingPage{}, ErrNotFound
	}
	return page, nil
}

// toPageView ports toPageView (pages.ts).
func toPageView(page queries.BookingPage) (*PageView, error) {
	var availability Availability
	if err := json.Unmarshal(page.Availability, &availability); err != nil {
		return nil, err
	}
	var dateOverrides DateOverrides
	if page.DateOverrides.Valid {
		if err := json.Unmarshal(page.DateOverrides.RawMessage, &dateOverrides); err != nil {
			return nil, err
		}
	}
	return &PageView{
		ID:              page.ID,
		Slug:            page.Slug,
		Title:           page.Title,
		Description:     nullStringPtr(page.Description),
		Location:        nullStringPtr(page.Location),
		Timezone:        page.Timezone,
		SlotDurationMin: int(page.SlotDurationMin),
		BufferBeforeMin: int(page.BufferBeforeMin),
		BufferAfterMin:  int(page.BufferAfterMin),
		MinNoticeMin:    int(page.MinNoticeMin),
		MaxDaysAhead:    int(page.MaxDaysAhead),
		Availability:    availability,
		DateOverrides:   dateOverrides,
		GoogleSync:      page.GoogleSync,
		Reminders:       page.Reminders,
		Status:          page.Status,
		CreatedAt:       formatISO(page.CreatedAt),
		UpdatedAt:       formatISO(page.UpdatedAt),
	}, nil
}

// CreatePage ports createPage. memberUserId is not a parameter here (unlike the TS PageOwner
// type's optional override) — this port always defaults it to the creator (userID), matching
// createPage's own default and this task's brief signature, which carries no separate override
// input. Reassigning a page's calendar owner is left to a later task.
func (s *Service) CreatePage(ctx context.Context, orgID, userID string, in PageInput) (*PageView, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}

	availabilityJSON, err := json.Marshal(in.Availability)
	if err != nil {
		return nil, err
	}
	dateOverridesJSON, err := marshalDateOverrides(in.DateOverrides)
	if err != nil {
		return nil, err
	}

	id := db.NewID()
	now := time.Now().UTC()
	createdBy := sql.NullInt64{Int64: userIDInt, Valid: true}
	memberUserID := sql.NullInt64{Int64: userIDInt, Valid: true}

	page := queries.BookingPage{
		ID:              id,
		OrganizationID:  orgIDInt,
		CreatedBy:       createdBy,
		MemberUserID:    memberUserID,
		Slug:            in.Slug,
		Title:           strings.TrimSpace(in.Title),
		Description:     optionalTrimmedString(in.Description),
		Location:        optionalTrimmedString(in.Location),
		Timezone:        in.Timezone,
		SlotDurationMin: int32(in.SlotDurationMin),
		BufferBeforeMin: int32(in.BufferBeforeMin),
		BufferAfterMin:  int32(in.BufferAfterMin),
		MinNoticeMin:    int32(in.MinNoticeMin),
		MaxDaysAhead:    int32(in.MaxDaysAhead),
		Availability:    availabilityJSON,
		DateOverrides:   dateOverridesJSON,
		GoogleSync:      in.GoogleSync,
		Reminders:       in.Reminders,
		Status:          "active",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.q.InsertBookingPage(ctx, queries.InsertBookingPageParams{
		ID:              page.ID,
		OrganizationID:  page.OrganizationID,
		CreatedBy:       page.CreatedBy,
		MemberUserID:    page.MemberUserID,
		Slug:            page.Slug,
		Title:           page.Title,
		Description:     page.Description,
		Location:        page.Location,
		Timezone:        page.Timezone,
		SlotDurationMin: page.SlotDurationMin,
		BufferBeforeMin: page.BufferBeforeMin,
		BufferAfterMin:  page.BufferAfterMin,
		MinNoticeMin:    page.MinNoticeMin,
		MaxDaysAhead:    page.MaxDaysAhead,
		Availability:    page.Availability,
		DateOverrides:   page.DateOverrides,
		GoogleSync:      page.GoogleSync,
		Reminders:       page.Reminders,
		Status:          page.Status,
		CreatedAt:       page.CreatedAt,
		UpdatedAt:       page.UpdatedAt,
	}); err != nil {
		if isSlugConflict(err) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}

	return toPageView(page)
}

// UpdatePage ports updatePage. Unlike the TS source's partial update, this replaces every editable
// field with in's value (see PageInput's doc comment) — id/organizationId/createdBy/memberUserId/
// createdAt are carried over from the existing row untouched.
func (s *Service) UpdatePage(ctx context.Context, pageID, orgID string, in PageInput) (*PageView, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	existing, err := requireOrgPage(ctx, s.q, pageID, orgID)
	if err != nil {
		return nil, err
	}

	availabilityJSON, err := json.Marshal(in.Availability)
	if err != nil {
		return nil, err
	}
	dateOverridesJSON, err := marshalDateOverrides(in.DateOverrides)
	if err != nil {
		return nil, err
	}

	status := in.Status
	if status == "" {
		status = "active"
	}
	now := time.Now().UTC()

	if err := s.q.UpdateBookingPage(ctx, queries.UpdateBookingPageParams{
		ID:              pageID,
		Slug:            in.Slug,
		Title:           strings.TrimSpace(in.Title),
		Description:     optionalTrimmedString(in.Description),
		Location:        optionalTrimmedString(in.Location),
		Timezone:        in.Timezone,
		SlotDurationMin: int32(in.SlotDurationMin),
		BufferBeforeMin: int32(in.BufferBeforeMin),
		BufferAfterMin:  int32(in.BufferAfterMin),
		MinNoticeMin:    int32(in.MinNoticeMin),
		MaxDaysAhead:    int32(in.MaxDaysAhead),
		Availability:    availabilityJSON,
		DateOverrides:   dateOverridesJSON,
		GoogleSync:      in.GoogleSync,
		Reminders:       in.Reminders,
		Status:          status,
		UpdatedAt:       now,
	}); err != nil {
		if isSlugConflict(err) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}

	existing.Slug = in.Slug
	existing.Title = strings.TrimSpace(in.Title)
	existing.Description = optionalTrimmedString(in.Description)
	existing.Location = optionalTrimmedString(in.Location)
	existing.Timezone = in.Timezone
	existing.SlotDurationMin = int32(in.SlotDurationMin)
	existing.BufferBeforeMin = int32(in.BufferBeforeMin)
	existing.BufferAfterMin = int32(in.BufferAfterMin)
	existing.MinNoticeMin = int32(in.MinNoticeMin)
	existing.MaxDaysAhead = int32(in.MaxDaysAhead)
	existing.Availability = availabilityJSON
	existing.DateOverrides = dateOverridesJSON
	existing.GoogleSync = in.GoogleSync
	existing.Reminders = in.Reminders
	existing.Status = status
	existing.UpdatedAt = now

	return toPageView(existing)
}

// DeletePage ports deletePage: a soft delete (deleted_at set). Freeing the page's slug for reuse
// happens implicitly — booking_pages_org_slug_uidx only covers live (deleted_at IS NULL) rows.
func (s *Service) DeletePage(ctx context.Context, pageID, orgID string) error {
	if _, err := requireOrgPage(ctx, s.q, pageID, orgID); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.q.SoftDeleteBookingPage(ctx, queries.SoftDeleteBookingPageParams{
		ID:        pageID,
		DeletedAt: sql.NullTime{Time: now, Valid: true},
	})
}

// ListMyPages ports listMyPages.
func (s *Service) ListMyPages(ctx context.Context, orgID string) ([]PageSummary, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}

	rows, err := s.q.ListBookingPagesByOrg(ctx, orgIDInt)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	out := make([]PageSummary, 0, len(rows))
	for _, p := range rows {
		count, err := s.q.CountUpcomingConfirmedBookings(ctx, queries.CountUpcomingConfirmedBookingsParams{
			PageID: p.ID, StartAt: now,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, PageSummary{
			ID:            p.ID,
			Slug:          p.Slug,
			Title:         p.Title,
			Status:        p.Status,
			UpcomingCount: int(count),
			CreatedAt:     formatISO(p.CreatedAt),
			UpdatedAt:     formatISO(p.UpdatedAt),
		})
	}
	return out, nil
}

// GetOwnedPage ports the requireManagedPage+toPageView half of getOwnedPage (pages.ts) that this
// port's signature can reach — see requireOrgPage's doc comment for what's deliberately not
// checked here (the creator-or-manager FORBIDDEN case).
func (s *Service) GetOwnedPage(ctx context.Context, pageID, orgID string) (*PageView, error) {
	page, err := requireOrgPage(ctx, s.q, pageID, orgID)
	if err != nil {
		return nil, err
	}
	return toPageView(page)
}

// GetPublicPage ports getPublicPage: a lookup for `/book/<handle>/<slug>`. Returns (nil, nil) — not
// an error — for an unknown handle, unknown slug, or a soft-deleted page, matching the TS source
// returning null. A PAUSED page IS still returned (with its "paused" status) so the route can show
// a "not currently accepting bookings" message — verified against
// src/server/bookings/__tests__/pages.workers.test.ts's "still returns a paused page" case; this
// diverges from the task brief's own shorthand ("404 ... on paused"), which does not match either
// the pages.ts source or its own test.
func (s *Service) GetPublicPage(ctx context.Context, orgSlug, pageSlug string) (*PublicPageView, error) {
	org, err := s.q.GetOrganizationBySlug(ctx, orgSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // mirrors getPublicPage returning null for an unknown handle
	}
	if err != nil {
		return nil, err
	}

	page, err := s.q.GetBookingPageByOrgSlug(ctx, queries.GetBookingPageByOrgSlugParams{
		OrganizationID: org.ID, Slug: pageSlug,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // mirrors getPublicPage returning null for an unknown slug/deleted page
	}
	if err != nil {
		return nil, err
	}

	return &PublicPageView{
		ID:              page.ID,
		Handle:          org.Slug,
		Slug:            page.Slug,
		Title:           page.Title,
		Description:     nullStringPtr(page.Description),
		Location:        nullStringPtr(page.Location),
		Timezone:        page.Timezone,
		SlotDurationMin: int(page.SlotDurationMin),
		MaxDaysAhead:    int(page.MaxDaysAhead),
		Status:          page.Status,
		Owner:           PublicPageOwnerView{Name: org.Name},
	}, nil
}

// SetOrgSlug ports setOrgSlug: the org's public handle (organizations.slug, globally unique —
// Limen's own idx_organizations_slug), not a per-org booking-page slug.
func (s *Service) SetOrgSlug(ctx context.Context, orgID, slug string) error {
	if err := validateHandle(slug); err != nil {
		return err
	}
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return ErrNotFound
	}
	if err := s.q.UpdateOrganizationSlug(ctx, queries.UpdateOrganizationSlugParams{
		ID: orgIDInt, Slug: slug,
	}); err != nil {
		if isHandleConflict(err) {
			return ErrHandleTaken
		}
		return err
	}
	return nil
}

// marshalDateOverrides converts a possibly-nil DateOverrides into the nullable jsonb param
// UpdateBookingPage/InsertBookingPage expect: nil map -> SQL NULL (matching dateOverrides being
// omitted/absent in the TS source), a non-nil (possibly empty) map -> the marshaled JSON object.
func marshalDateOverrides(overrides DateOverrides) (pqtype.NullRawMessage, error) {
	if overrides == nil {
		return pqtype.NullRawMessage{}, nil
	}
	b, err := json.Marshal(overrides)
	if err != nil {
		return pqtype.NullRawMessage{}, err
	}
	return pqtype.NullRawMessage{RawMessage: b, Valid: true}, nil
}
