// Package bookings — this file (bookings.go) is a behavioral port of
// src/server/bookings/bookings.ts: booking creation, the manage flow (cancel/reschedule/view), and
// the org-manager booking list — plus src/lib/tokens.ts's manage-token scheme. Availability itself
// (src/lib/availability.ts) is availability.go's pure Slots/IsSlotAvailable.
//
// THE atomicity contract (spec §9's double-book proof, the booking analog of plan 4's poll-claim
// proof — see internal/polls/claims.go's own doc comment for the sibling pattern):
//
// Book's and Reschedule's invariant is per-page: no two confirmed bookings on the same page may
// have overlapping [start, end) intervals (once each candidate's own buffer padding is applied).
// The DB-backed half of that invariant — the actual availability recheck and the winning
// booking's INSERT/UPDATE — runs entirely inside one transaction and takes exactly ONE row lock —
// the page's own row (`SELECT ... FROM booking_pages WHERE ... FOR UPDATE`,
// queries.GetBookingPageByOrgSlugForUpdate/GetBookingPageForUpdate) — taken BEFORE recomputing the
// candidate slot's validity, and held until the winning booking's write and the transaction
// commits. Every other concurrent book/reschedule attempt against THE SAME PAGE blocks on that
// same row lock, so the "read-live-bookings, check-availability, write" sequence below can never
// interleave across two transactions: whichever transaction acquires the lock first sees every
// booking the other has already committed, and the loser's own re-check (against that now-current
// busy list, read fresh via q — the tx-bound *queries.Queries, never the pre-lock snapshot below)
// correctly fails with ErrSlotTaken. See TestBookRacingClaimsExactlyOneWinner (bookings_test.go)
// for the proof. Only one lock is ever taken (unlike Claim's two, option-then-participant) because
// there is only one invariant to protect here — a booking has no per-visitor budget to serialize
// separately — so there is no fixed lock ORDER to reason about, and no deadlock risk between
// Book/Reschedule calls on different pages (each only ever locks its own page row).
//
// I1: a page's Google Calendar freebusy (googleBusyForPage, google.go) is best-effort and, unlike
// a live booking row, never itself protected by the page lock — merging it in is cheap CPU work,
// but *producing* it is one outbound HTTPS call that could stall for the full 15s
// googleHTTPClient timeout. Both methods therefore resolve the page read-only and call
// googleBusyForPage BEFORE ever opening the transaction that takes the lock: the slow network hop
// happens with no lock held at all (so it can never make every other concurrent booking attempt
// against this page queue up behind it), and only the fast part — re-reading the page, the live
// bookings, and checking the merged busy list — happens once the lock is held. A page/booking
// that changed between this pre-lock read and the lock itself (paused, googleSync toggled,
// rescheduled again) is never a correctness problem: the pre-lock read only ever feeds the
// best-effort Google half of the busy list, and the DB-authoritative half (live bookings, the
// page's own current rules, PAGE_PAUSED/BOOKING_PAST) is always re-read fresh, in-lock, exactly as
// this doc comment's own proof above requires.
package bookings

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/bookings/queries"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/rooms"
)

// CancelledBy mirrors the TS schema's CancelledBy union.
type CancelledBy string

const (
	CancelledByVisitor   CancelledBy = "visitor"
	CancelledByOrganiser CancelledBy = "organiser"
)

// BookInput carries a visitor's booking request — ported from createBooking's CreateBookingInput
// plus the busy-interval parameter createBooking takes separately (bookings.ts): Busy is a
// caller-supplied list of additional busy intervals (e.g. a Google Calendar freebusy lookup) to
// merge with the page's own live bookings before checking the slot — see the "rejects a slot
// blocked by a caller-supplied busy interval" case this ports from bookings.workers.test.ts.
type BookInput struct {
	StartAt  time.Time
	Name     string
	Email    string
	Note     *string
	Locale   *string
	Timezone string
	Busy     []Interval
}

// BookingResult is Book's and Reschedule's shared return value. ManageToken is only ever
// populated by Book — the visitor's manage token for this booking, deterministically derived (see
// Service.manageToken below) rather than randomly minted, so it is never itself persisted —
// Reschedule never mints a new one either (a booking's manage token is fixed for its whole
// lifetime, derived from its own id), so its ManageToken is always "". PreviousStartAt/Changed are
// only meaningful coming from Reschedule (ported from rescheduleBooking's own {changed,
// previousStartAt} return); Book always reports Changed: true with a zero PreviousStartAt (there
// was no previous slot to report).
type BookingResult struct {
	BookingID       string
	ManageToken     string
	PreviousStartAt time.Time
	Changed         bool
}

// manageTokenHMACPrefix namespaces the HMAC input below — see Service.manageToken's own doc
// comment for why a plain HMAC(secret, bookingID) alone isn't quite enough.
const manageTokenHMACPrefix = "booking-manage:"

// manageToken derives bookingID's own visitor manage token: base64url (no padding) of
// HMAC-SHA256(s.manageSecret, "booking-manage:"+bookingID) — I4's replacement for the prior
// random-token-plus-stored-hash scheme (tokens.ts's own generateToken/hashToken, and this
// package's own history before this fix). Deterministic and never persisted: Book "mints" a
// token by computing it (there is no bookings.manage_token_hash column left to write it to), and
// Cancel/Reschedule/ManagedBooking verify a caller-supplied one the same way, by recomputing and
// comparing — see verifyManageToken. The prefix keeps this HMAC's input namespaced to this one
// purpose, so the same (secret, id) pair used for some future, differently-purposed token (were
// one ever added) can never collide with a booking's own manage token by accident. Panics only if
// s.manageSecret is empty and the caller didn't already guard for that — every call site either
// checks s.manageSecret first (Book) or only ever reaches here once a booking already exists,
// which Book could not have created without that same non-empty secret.
func (s *Service) manageToken(bookingID string) string {
	mac := hmac.New(sha256.New, []byte(s.manageSecret))
	mac.Write([]byte(manageTokenHMACPrefix + bookingID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyManageToken reports whether token is bookingID's own manage token — recomputed via
// manageToken and compared in constant time (hmac.Equal), never against a stored hash. false for
// an empty secret or an empty token (rather than treating a misconfigured, empty-secret service
// as though every token were valid against manageToken("")'s own degenerate output).
func (s *Service) verifyManageToken(bookingID, token string) bool {
	if s.manageSecret == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(s.manageToken(bookingID)), []byte(token))
}

// pageRulesFrom ports pageRulesFrom (bookings.ts): the PageRules Slots/IsSlotAvailable need, read
// off a stored page row.
func pageRulesFrom(page queries.BookingPage) (PageRules, error) {
	var availability Availability
	if err := json.Unmarshal(page.Availability, &availability); err != nil {
		return PageRules{}, err
	}
	var dateOverrides DateOverrides
	if page.DateOverrides.Valid {
		if err := json.Unmarshal(page.DateOverrides.RawMessage, &dateOverrides); err != nil {
			return PageRules{}, err
		}
	}
	return PageRules{
		Timezone:        page.Timezone,
		SlotDurationMin: int(page.SlotDurationMin),
		BufferBeforeMin: int(page.BufferBeforeMin),
		BufferAfterMin:  int(page.BufferAfterMin),
		MinNoticeMin:    int(page.MinNoticeMin),
		MaxDaysAhead:    int(page.MaxDaysAhead),
		Availability:    availability,
		DateOverrides:   dateOverrides,
	}, nil
}

// bookingWindow is the one-day-either-side window createBooking/rescheduleBooking (bookings.ts)
// use around a candidate start time to bound the bookedIntervalsForPage lookup.
func bookingWindow(start time.Time) (from, to time.Time) {
	return start.Add(-24 * time.Hour), start.Add(24 * time.Hour)
}

// bookedIntervalsForPage ports bookedIntervalsForPage (bookings.ts): confirmed bookings on pageID
// overlapping [from, to) as their *raw* stored [start, end) — no buffer applied here. Slots is the
// single place a page's buffers are applied (padding whichever *candidate* slot it's checking):
// expanding a booking's interval by the buffer here too would double it. excludeBookingID drops
// one booking (its own prior interval) so a reschedule doesn't collide with the slot it's moving
// away from.
func bookedIntervalsForPage(ctx context.Context, q *queries.Queries, pageID string, from, to time.Time, excludeBookingID string) ([]Interval, error) {
	rows, err := q.ListConfirmedBookingsInRange(ctx, queries.ListConfirmedBookingsInRangeParams{
		PageID: pageID, RangeFrom: from, RangeTo: to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Interval, 0, len(rows))
	for _, b := range rows {
		if b.ID == excludeBookingID {
			continue
		}
		out = append(out, Interval{Start: b.StartAt, End: b.EndAt})
	}
	return out, nil
}

// BookedIntervals ports the exported bookedIntervals (bookings.ts): every confirmed booking on
// pageID overlapping [from, to), as raw stored intervals. Returns an empty slice (not an error)
// for an unknown pageID, matching the TS source's own `if (!page) return []`.
func (s *Service) BookedIntervals(ctx context.Context, pageID string, from, to time.Time) ([]Interval, error) {
	if _, err := s.q.GetBookingPage(ctx, pageID); errors.Is(err, sql.ErrNoRows) {
		return []Interval{}, nil
	} else if err != nil {
		return nil, err
	}
	return bookedIntervalsForPage(ctx, s.q, pageID, from, to, "")
}

// PublicAvailability ports the availability half of getPublicAvailability (bookings.functions.ts):
// resolves the public page and generates its bookable slots over [from, to] against its own live
// bookings (no caller-supplied Google busy list here — this port has no HTTP layer to source one
// from yet). Returns (nil, nil) for an unknown org/page/paused/deleted page, matching
// getPublicAvailability's own "no such page" -> null result (a paused page still has a public row,
// per GetPublicPage's doc comment, but generateSlots against it would find nothing bookable
// anyway — callers that need to show a "paused" message should call GetPublicPage directly).
func (s *Service) PublicAvailability(ctx context.Context, orgSlug, pageSlug string, from, to time.Time) ([]time.Time, error) {
	org, err := s.q.GetOrganizationBySlug(ctx, orgSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // mirrors getPublicAvailability's null for an unknown handle
	}
	if err != nil {
		return nil, err
	}

	page, err := s.q.GetBookingPageByOrgSlug(ctx, queries.GetBookingPageByOrgSlugParams{
		OrganizationID: org.ID, Slug: pageSlug,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // mirrors getPublicAvailability's null for an unknown/deleted page
	}
	if err != nil {
		return nil, err
	}

	rules, err := pageRulesFrom(page)
	if err != nil {
		return nil, err
	}
	busy, err := bookedIntervalsForPage(ctx, s.q, page.ID, from, to, "")
	if err != nil {
		return nil, err
	}
	busy = googleBusyForPage(ctx, s.google, page, from, to, busy)

	return Slots(rules, busy, time.Now().UTC(), from, to), nil
}

// Book ports createBooking (bookings.ts) — see this file's package doc comment for the atomicity
// contract. orgSlug/pageSlug resolve the public `/book/<handle>/<slug>` page the same way
// GetPublicPage does; unlike GetPublicPage, an unknown org/page here is ErrNotFound (there is no
// slot to book against a page that doesn't exist), matching createBooking's own `if (!page ...)
// throw NOT_FOUND`.
func (s *Service) Book(ctx context.Context, orgSlug, pageSlug string, in BookInput) (*BookingResult, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	// I4: a booking's manage token is derived from s.manageSecret (manageToken above) — an empty
	// secret (config.Load's own AuthSecret >= 32 chars rule means this is a misconfiguration, not
	// a real runtime state) must fail loudly here rather than mint every booking a manage token
	// that would fail verifyManageToken's own same-check for the rest of that booking's life.
	if s.manageSecret == "" {
		return nil, errors.New("bookings: manage token secret is not configured")
	}

	// I1: resolve the org/page read-only, and run the (network-bound) Google Calendar freebusy
	// lookup, BEFORE ever opening the transaction that takes the page row lock below — see this
	// file's package doc comment for why. org.ID is reused as-is once the lock is taken (an org's
	// own id never changes for a live slug); everything else read here is only ever used to
	// decide whether/how to call Google — the authoritative page/status/rules checks all re-run
	// against a fresh, LOCKED read a few lines down.
	org, err := s.q.GetOrganizationBySlug(ctx, orgSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	preLockPage, err := s.q.GetBookingPageByOrgSlug(ctx, queries.GetBookingPageByOrgSlugParams{
		OrganizationID: org.ID, Slug: pageSlug,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	from, to := bookingWindow(in.StartAt)
	var googleBusy []Interval
	if preLockPage.Status != "paused" {
		googleBusy = googleBusyForPage(ctx, s.google, preLockPage, from, to, nil)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	// Lock the page row now, before recomputing the candidate slot's validity below — see this
	// file's package doc comment for why the ordering matters. Re-read fresh (never the
	// preLockPage snapshot above): this is the authoritative row every DB-backed check from here
	// on is checked against.
	page, err := q.GetBookingPageByOrgSlugForUpdate(ctx, queries.GetBookingPageByOrgSlugForUpdateParams{
		OrganizationID: org.ID, Slug: pageSlug,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if page.Status == "paused" {
		return nil, ErrPagePaused
	}

	now := time.Now().UTC()
	if in.StartAt.Before(now) {
		return nil, ErrBookingPast
	}

	rules, err := pageRulesFrom(page)
	if err != nil {
		return nil, err
	}
	existing, err := bookedIntervalsForPage(ctx, q, page.ID, from, to, "")
	if err != nil {
		return nil, err
	}
	busy := make([]Interval, 0, len(in.Busy)+len(existing)+len(googleBusy))
	busy = append(busy, in.Busy...)
	busy = append(busy, existing...)
	busy = append(busy, googleBusy...)

	if !IsSlotAvailable(rules, in.StartAt, now, busy) {
		return nil, ErrSlotTaken
	}

	endAt := in.StartAt.Add(time.Duration(page.SlotDurationMin) * time.Minute)
	bookingID := db.NewID()

	if err := q.InsertBooking(ctx, queries.InsertBookingParams{
		ID:              bookingID,
		PageID:          page.ID,
		StartAt:         in.StartAt,
		EndAt:           endAt,
		VisitorName:     strings.TrimSpace(in.Name),
		VisitorEmail:    strings.TrimSpace(in.Email),
		VisitorNote:     optionalTrimmedString(in.Note),
		VisitorLocale:   optionalTrimmedString(in.Locale),
		VisitorTimezone: in.Timezone,
		Status:          "confirmed",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		return nil, err
	}

	if err := rooms.Emit(ctx, tx, "booking:"+page.ID, "page.changed", nil); err != nil {
		return nil, err
	}

	// Mail + the reminder timer are enqueued/armed inside this same transaction (Task 4): a
	// booking whose commit fails must not leave a stray confirmation job or reminder behind, and
	// one whose commit succeeds must never lose either — matching internal/polls/timers.go's own
	// enqueueMailPoll/armDeadline call sites.
	if err := enqueueMailBooking(ctx, tx, "confirmed", bookingID, nil); err != nil {
		return nil, err
	}
	if page.Reminders {
		if err := armBookingReminder(ctx, tx, bookingID, in.StartAt); err != nil {
			return nil, err
		}
	}

	// The Google Calendar event is created post-commit, via a "google:sync" job enqueued in this
	// same transaction (Task 5) — never inline: an API stall must not fail a booking that already
	// succeeded. See google.go's package doc comment.
	if page.GoogleSync {
		if err := enqueueGoogleSync(ctx, tx, "insert", bookingID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &BookingResult{BookingID: bookingID, ManageToken: s.manageToken(bookingID), Changed: true}, nil
}

// Cancel ports the auth-check half of getBookingForManage plus cancelBooking (bookings.ts),
// merged into one atomic call — this port's fixed signature (bookingID, manageToken, byOrganiser)
// carries no org/user identity to check an organiser's *role* against; byOrganiser is the caller's
// already-established "this is the page owner, not a token-bearing visitor" fact — Task 6's HTTP
// layer establishes it via RequireManageableBooking (authz.go) before calling Cancel with
// byOrganiser: true. A wrong manageToken (byOrganiser: false) is ErrInvalidToken — the booking
// itself was found, but this credential doesn't open it (Task 6's accumulated requirement (d);
// see ErrInvalidToken's own doc comment in errors.go for why this changed from an earlier,
// simpler ErrNotFound). Idempotent: cancelling an already-cancelled booking is a no-op, matching
// cancelBooking's own doc comment.
func (s *Service) Cancel(ctx context.Context, bookingID, manageToken string, byOrganiser bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	booking, err := q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !byOrganiser && !s.verifyManageToken(bookingID, manageToken) {
		return ErrInvalidToken
	}

	if booking.Status == "cancelled" {
		return tx.Commit()
	}

	by := CancelledByVisitor
	if byOrganiser {
		by = CancelledByOrganiser
	}
	if err := q.UpdateBookingStatus(ctx, queries.UpdateBookingStatusParams{
		ID:          bookingID,
		Status:      "cancelled",
		CancelledBy: sql.NullString{String: string(by), Valid: true},
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		return err
	}

	if err := rooms.Emit(ctx, tx, "booking:"+booking.PageID, "page.changed", nil); err != nil {
		return err
	}

	// Mail + cancelling the reminder timer are done inside this same transaction (Task 4) — see
	// Book's own comment on why this must not be a separate, possibly-partial step.
	if err := enqueueMailBooking(ctx, tx, "cancelled", bookingID, nil); err != nil {
		return err
	}
	if err := cancelBookingReminder(ctx, tx, bookingID); err != nil {
		return err
	}

	// A known Google Calendar event is always cleaned up, regardless of the page's CURRENT
	// googleSync toggle (spec finding 7, ported from google-sync.ts's own comment): the event was
	// created while sync was on, so it still needs deleting even if sync has since been turned
	// off.
	if booking.GoogleEventID.Valid {
		if err := enqueueGoogleSync(ctx, tx, "delete", bookingID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Reschedule ports rescheduleBooking (bookings.ts) — see this file's package doc comment for the
// atomicity contract (the same page-row lock Book takes, re-acquired here). Always requires
// manageToken to match (ErrInvalidToken otherwise — see ErrInvalidToken's own doc comment in
// errors.go for Task 6's requirement (d) that changed this from an earlier ErrNotFound) — this
// port's fixed signature carries no byOrganiser flag the way Cancel's does, so there is no
// separate owner-forced path here; an owner-initiated reschedule (were one ever added) would need
// its own method, the same way UnclaimFor sits beside Unclaim in internal/polls/claims.go. Unlike
// createBooking's own busy parameter, this port's fixed signature takes no caller-supplied
// Google-freebusy list — there is no external caller here the way Book's HTTP handler is; instead
// this method does its own internal prefetch (see this file's package doc comment's own I1 note)
// of the page's Google Calendar freebusy, merged in alongside the page's own live bookings.
func (s *Service) Reschedule(ctx context.Context, bookingID, manageToken string, newStart time.Time) (*BookingResult, error) {
	// I1: resolve the booking/page read-only, and run the Google Calendar freebusy lookup, BEFORE
	// ever opening the transaction that takes the page row lock below — see Book's identical
	// prefetch, and this file's package doc comment, for why. Every value read in this pass is
	// used only to decide whether/how to call Google; the authoritative token/status/page checks
	// all re-run against a fresh, LOCKED read a few lines down.
	preLockBooking, err := s.q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.verifyManageToken(bookingID, manageToken) {
		return nil, ErrInvalidToken
	}
	from, to := bookingWindow(newStart)
	var googleBusy []Interval
	if preLockBooking.Status != "cancelled" {
		preLockPage, pageErr := s.q.GetBookingPage(ctx, preLockBooking.PageID)
		if pageErr != nil && !errors.Is(pageErr, sql.ErrNoRows) {
			return nil, pageErr
		}
		if pageErr == nil && preLockPage.Status != "paused" {
			googleBusy = googleBusyForPage(ctx, s.google, preLockPage, from, to, nil)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	booking, err := q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.verifyManageToken(bookingID, manageToken) {
		return nil, ErrInvalidToken
	}
	if booking.Status == "cancelled" {
		return nil, ErrConflict
	}

	// Lock the page row now, before recomputing the candidate slot's validity below — see this
	// file's package doc comment for why the ordering matters. Re-read fresh (never the
	// preLockPage snapshot above): this is the authoritative row every DB-backed check from here
	// on is checked against.
	page, err := q.GetBookingPageForUpdate(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if page.Status == "paused" {
		return nil, ErrPagePaused
	}

	now := time.Now().UTC()
	if newStart.Before(now) {
		return nil, ErrBookingPast
	}

	rules, err := pageRulesFrom(page)
	if err != nil {
		return nil, err
	}
	// excludeBookingID drops this booking's own prior interval so a reschedule to a slot it
	// already occupies (or one abutting it) doesn't self-block — see bookedIntervalsForPage's
	// doc comment, and the "rescheduling to the exact same slot succeeds" case this ports.
	existing, err := bookedIntervalsForPage(ctx, q, page.ID, from, to, bookingID)
	if err != nil {
		return nil, err
	}
	busy := make([]Interval, 0, len(existing)+len(googleBusy))
	busy = append(busy, existing...)
	busy = append(busy, googleBusy...)

	if !IsSlotAvailable(rules, newStart, now, busy) {
		return nil, ErrSlotTaken
	}

	endAt := newStart.Add(time.Duration(page.SlotDurationMin) * time.Minute)
	previousStartAt := booking.StartAt

	if err := q.UpdateBookingSchedule(ctx, queries.UpdateBookingScheduleParams{
		ID: bookingID, StartAt: newStart, EndAt: endAt, UpdatedAt: now,
	}); err != nil {
		return nil, err
	}

	if err := rooms.Emit(ctx, tx, "booking:"+page.ID, "page.changed", nil); err != nil {
		return nil, err
	}

	// Mail + the reminder timer, inside this same transaction (Task 4) — see Book's own comment.
	// The reminder is re-armed at the NEW start when the page still wants one, or cancelled
	// outright when it doesn't — ports BookingRoom.reschedule's own if/else exactly (unlike Book,
	// which only ever arms, never cancels, since a fresh booking has no prior reminder to clear).
	if err := enqueueMailBooking(ctx, tx, "rescheduled", bookingID, &previousStartAt); err != nil {
		return nil, err
	}
	if page.Reminders {
		if err := armBookingReminder(ctx, tx, bookingID, newStart); err != nil {
			return nil, err
		}
	} else {
		if err := cancelBookingReminder(ctx, tx, bookingID); err != nil {
			return nil, err
		}
	}

	// Google Calendar delete-then-recreate, post-commit, via one "reschedule" google:sync job
	// (Task 5) — see googleSyncReschedule's own doc comment for the sequencing contract. Enqueued
	// whenever there's a known event to clean up OR sync is (now) on; a no-op job (neither) is
	// skipped rather than scheduled for nothing.
	if booking.GoogleEventID.Valid || page.GoogleSync {
		if err := enqueueGoogleSync(ctx, tx, "reschedule", bookingID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &BookingResult{BookingID: bookingID, PreviousStartAt: previousStartAt, Changed: true}, nil
}

// toBookingView ports toBookingView (bookings.ts).
func toBookingView(b queries.Booking) BookingView {
	return BookingView{
		ID:              b.ID,
		PageID:          b.PageID,
		StartAt:         formatISO(b.StartAt),
		EndAt:           formatISO(b.EndAt),
		VisitorName:     b.VisitorName,
		VisitorEmail:    b.VisitorEmail,
		VisitorNote:     nullStringPtr(b.VisitorNote),
		VisitorTimezone: b.VisitorTimezone,
		VisitorLocale:   nullStringPtr(b.VisitorLocale),
		Status:          b.Status,
		CancelledBy:     nullStringPtr(b.CancelledBy),
		CreatedAt:       formatISO(b.CreatedAt),
	}
}

// ManagedBooking ports the token-authenticated half of getBookingForManage (bookings.ts) — this
// port's fixed signature (bookingID, manageToken) carries no org/user identity, so the
// org-manager auth branch (requireOrgPage-shaped: same-org check + canManageContent) isn't
// reachable here, the same deviation GetOwnedPage/ListPageBookings already document; Task 6's own
// HTTP endpoint table keeps GET .../manage token-only (unlike POST .../cancel, whose organiser
// fallback is this task's accumulated requirement (e)), so this stays token-only too. A wrong
// token is ErrInvalidToken (see ErrInvalidToken's own doc comment in errors.go for Task 6's
// requirement (d) that changed this from an earlier ErrNotFound).
func (s *Service) ManagedBooking(ctx context.Context, bookingID, manageToken string) (*ManagedBookingView, error) {
	booking, err := s.q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !s.verifyManageToken(bookingID, manageToken) {
		return nil, ErrInvalidToken
	}

	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	org, err := s.q.GetOrganization(ctx, page.OrganizationID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var handle *string
	ownerName := ""
	if err == nil {
		h := org.Slug
		handle = &h
		ownerName = org.Name
	}

	view := &ManagedBookingView{
		BookingView: toBookingView(booking),
		Page: ManagedBookingPageView{
			ID:              page.ID,
			Handle:          handle,
			Slug:            page.Slug,
			Title:           page.Title,
			Location:        nullStringPtr(page.Location),
			Timezone:        page.Timezone,
			SlotDurationMin: int(page.SlotDurationMin),
			Owner:           PublicPageOwnerView{Name: ownerName},
		},
	}
	return view, nil
}

// ListPageBookings ports listBookings (bookings.ts). This port's fixed signature (pageID, orgID)
// carries no userID, so — like GetOwnedPage (pages.go) — only the org-scoping half of the TS
// source's auth (page must belong to orgID) is reachable here; the creator-or-manager
// canManageContent check is deferred to a future HTTP layer, the same deviation requireOrgPage's
// own doc comment already covers.
func (s *Service) ListPageBookings(ctx context.Context, pageID, orgID string, from, to time.Time) ([]BookingView, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}
	page, err := s.q.GetBookingPage(ctx, pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if page.OrganizationID != orgIDInt {
		return nil, ErrNotFound
	}

	rows, err := s.q.ListBookingsInRange(ctx, queries.ListBookingsInRangeParams{
		PageID: pageID, RangeFrom: from, RangeTo: to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]BookingView, 0, len(rows))
	for _, b := range rows {
		out = append(out, toBookingView(b))
	}
	return out, nil
}
