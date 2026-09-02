// Package bookings (this file, google.go) is a behavioral port of src/server/google/calendar.ts
// (the Google Calendar v3 client + token resolution) fused with src/server/bookings/google-sync.ts
// (how a booking's lifecycle drives it) — Task 5's optional Google Calendar sync.
//
// Architecture, and how it differs from the TS source:
//
//   - TS's getGoogleAccessToken resolves a token proactively via Better-Auth's own
//     getAccessToken (which refreshes off the stored expiry before the call is even made). This
//     port owns the Limen accounts row directly (GetGoogleAccount/UpdateGoogleAccountToken below)
//     and has no such proactive path, so it refreshes reactively instead: every call tries the
//     access token it already has, and only refreshes (once) on an actual 401 — see
//     doWithRefresh's own doc comment.
//
//   - TS calls the Google API inline, on the request that triggered it (bookings.functions.ts),
//     with a separate best-effort mailer.queue.ts retry only for the resulting notice mail. This
//     port instead enqueues one "google:sync" job in the SAME transaction as the domain write
//     that needs it (Book/Cancel/Reschedule, bookings.go) — mirroring emails.go's own
//     "mail:booking" job — and the job handler (handleGoogleSyncJob, below) does the actual API
//     call after that transaction has committed. A slow or down Google API can therefore never
//     make a booking/cancel/reschedule request hang or fail.
//
//   - A "hard" sync failure (any error other than "no usable Google connection") is reported by
//     enqueueing a "mail:booking" job with kind "sync_failed" (handleMailBookingJob,
//     emails.go) rather than sending the notice directly — reusing the exact same queue,
//     re-read-fresh, retry-on-SMTP-hiccup machinery every other lifecycle mail already gets,
//     rather than a bespoke one-shot send. Ports sendGoogleSyncFailedNotice's contract (emails.ts)
//     of "at most one notice per failed operation."
//
//   - "No usable Google connection" (no accounts row for the page's member, or a refresh that
//     can't produce a usable token) is ErrGoogleNotConnected (errors.go) — every caller here
//     treats it identically to TS's own getGoogleAccessToken returning null: a silent no-op,
//     never a hard failure/notice.
package bookings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/oauth2"

	"github.com/refsdal/whenweall/internal/bookings/queries"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
)

// GoogleCalendarBaseURL and GoogleOAuthTokenURL are Google's real endpoints. Exported as mutable
// package-level vars — rather than threaded through NewGoogleSync's own parameters — purely so
// tests can point them at an httptest.Server stub, mirroring
// internal/httpserver.TurnstileSiteverifyURL's own precedent. Production code must never assign
// to these.
var (
	GoogleCalendarBaseURL = "https://www.googleapis.com/calendar/v3"
	GoogleOAuthTokenURL   = "https://oauth2.googleapis.com/token"
)

// googleHTTPClient's Timeout is a fail-safe upper bound on a Calendar API round trip — this
// package's calls only ever run inside the async "google:sync" job handler (never on a request
// goroutine), but an unbounded call would still tie up a worker slot indefinitely on a hung
// Google (or stub) response.
var googleHTTPClient = &http.Client{Timeout: 15 * time.Second}

// GoogleSync is the Google Calendar sync surface bookings.go/emails.go compose against — see
// NewGoogleSync. A nil GoogleSync (the capability off) is always a valid value: every call site
// in this package treats it as "sync off" rather than checking the capability itself, so there is
// exactly one on/off switch.
type GoogleSync interface {
	// Busy returns userID's Google Calendar freebusy intervals over [from, to) — the extra busy
	// list PublicAvailability/Book/Reschedule merge into a page's own bookings when it has sync
	// on. Returns (nil, ErrGoogleNotConnected) when userID has no usable Google connection;
	// callers treat any non-nil error identically (silent degrade to "no extra busy intervals" —
	// ports computeBusy's own try/catch, bookings.functions.ts).
	Busy(ctx context.Context, userID string, from, to time.Time) ([]Interval, error)

	// InsertEvent creates a Google Calendar event for booking b on userID's primary calendar,
	// returning its event id. b.PageID is used to read the event's summary/description/timezone
	// off the current booking_pages row (not carried on BookingView itself). Returns ("",
	// ErrGoogleNotConnected) when userID has no usable Google connection.
	InsertEvent(ctx context.Context, userID string, b *BookingView) (eventID string, err error)

	// DeleteEvent deletes eventID from userID's primary calendar. A 404/410 (the event is already
	// gone, however that happened) is success, not an error — mirrors deleteEvent's own
	// rationale (calendar.ts). Returns ErrGoogleNotConnected when userID has no usable Google
	// connection.
	DeleteEvent(ctx context.Context, userID, eventID string) error
}

// googleSync is the real GoogleSync: an httptest-stubbable Google Calendar v3 client (via
// GoogleCalendarBaseURL) backed by the Limen accounts-table token store (via GoogleOAuthTokenURL
// for a refresh).
type googleSync struct {
	q            *queries.Queries
	clientID     string
	clientSecret string
}

// NewGoogleSync returns nil when cfg.Capabilities.Google is off (a half- or un-configured
// GOOGLE_CLIENT_ID/SECRET pair — see config.Load's own pair() reasoning) — every caller in this
// package treats a nil GoogleSync as "sync off."
func NewGoogleSync(cfg *config.Config, sqlDB *sql.DB) GoogleSync {
	if !cfg.Capabilities.Google {
		return nil
	}
	return &googleSync{
		q:            queries.New(sqlDB),
		clientID:     cfg.GoogleClientID,
		clientSecret: cfg.GoogleClientSecret,
	}
}

// googleAccountRow is the subset of one Limen accounts row a Calendar sync needs.
type googleAccountRow struct {
	id           int64
	accessToken  string
	refreshToken sql.NullString
}

// account resolves userID's linked Google account row, or ErrGoogleNotConnected when userID
// doesn't parse as Limen's bigint user id or there is no such row — both are "not connected",
// same as TS's getGoogleAccessToken returning null for either case (calendar.ts).
func (g *googleSync) account(ctx context.Context, userID string) (*googleAccountRow, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return nil, ErrGoogleNotConnected
	}
	row, err := g.q.GetGoogleAccount(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGoogleNotConnected
	}
	if err != nil {
		return nil, err
	}
	return &googleAccountRow{id: row.ID, accessToken: row.AccessToken, refreshToken: row.RefreshToken}, nil
}

// refresh exchanges acct's refresh token for a fresh access token via GoogleOAuthTokenURL,
// persists it (and, when Google issued a new one, the new refresh token — it doesn't always)
// back onto the accounts row, and returns the new access token. ErrGoogleNotConnected when there
// is no refresh token to use, or the token endpoint itself rejects it (an unlinked/revoked
// connection) — both degrade the same way a missing account row does.
func (g *googleSync) refresh(ctx context.Context, acct *googleAccountRow) (string, error) {
	if !acct.refreshToken.Valid || acct.refreshToken.String == "" {
		return "", ErrGoogleNotConnected
	}

	conf := &oauth2.Config{
		ClientID:     g.clientID,
		ClientSecret: g.clientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: GoogleOAuthTokenURL},
	}
	src := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: acct.refreshToken.String})
	tok, err := src.Token()
	if err != nil {
		return "", ErrGoogleNotConnected
	}

	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		// Google doesn't always reissue a refresh token on a plain refresh — keep the one already
		// on file rather than clobbering it with an empty string.
		newRefresh = acct.refreshToken.String
	}
	if err := g.q.UpdateGoogleAccountToken(ctx, queries.UpdateGoogleAccountTokenParams{
		ID:                   acct.id,
		AccessToken:          tok.AccessToken,
		RefreshToken:         sql.NullString{String: newRefresh, Valid: true},
		AccessTokenExpiresAt: sql.NullTime{Time: tok.Expiry, Valid: !tok.Expiry.IsZero()},
		UpdatedAt:            time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	acct.accessToken = tok.AccessToken
	return tok.AccessToken, nil
}

// doWithRefresh resolves userID's account, calls fn with its current access token, and — only on
// a 401 response — refreshes the token exactly once and retries fn a single additional time. This
// port's own reactive-refresh contract (see this file's package doc comment for how it differs
// from TS's proactive one): a non-401 response (including any other error status) is returned to
// the caller as-is, for it to translate into success or a googleAPIError.
func (g *googleSync) doWithRefresh(ctx context.Context, userID string, fn func(accessToken string) (*http.Response, error)) (*http.Response, error) {
	acct, err := g.account(ctx, userID)
	if err != nil {
		return nil, err
	}

	resp, err := fn(acct.accessToken)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	_ = resp.Body.Close()

	newToken, err := g.refresh(ctx, acct)
	if err != nil {
		return nil, err
	}
	return fn(newToken)
}

// googleAPIError is a non-2xx (and, for a delete, non-404/410) Google Calendar API response —
// ports GoogleApiError (calendar.ts). Never wraps the response body: Google's own error payloads
// can echo back request data (an attendee email, say), and nothing here needs more than the
// status to decide hard-failure-vs-not.
type googleAPIError struct{ status int }

func (e *googleAPIError) Error() string {
	return fmt.Sprintf("bookings: google calendar api error (%d)", e.status)
}

// Busy ports calendar.ts's getFreeBusy plus computeBusy's Google half (bookings.functions.ts).
func (g *googleSync) Busy(ctx context.Context, userID string, from, to time.Time) ([]Interval, error) {
	reqBody, err := json.Marshal(map[string]any{
		"timeMin": from.UTC().Format(time.RFC3339),
		"timeMax": to.UTC().Format(time.RFC3339),
		"items":   []map[string]string{{"id": "primary"}},
	})
	if err != nil {
		return nil, err
	}

	resp, err := g.doWithRefresh(ctx, userID, func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, GoogleCalendarBaseURL+"/freeBusy", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return googleHTTPClient.Do(req)
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &googleAPIError{status: resp.StatusCode}
	}

	var data struct {
		Calendars struct {
			Primary struct {
				Busy []struct{ Start, End string } `json:"busy"`
			} `json:"primary"`
		} `json:"calendars"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	out := make([]Interval, 0, len(data.Calendars.Primary.Busy))
	for _, b := range data.Calendars.Primary.Busy {
		start, err1 := time.Parse(time.RFC3339, b.Start)
		end, err2 := time.Parse(time.RFC3339, b.End)
		if err1 != nil || err2 != nil {
			continue // malformed entry — skip rather than fail the whole lookup over one bad row
		}
		out = append(out, Interval{Start: start, End: end})
	}
	return out, nil
}

// InsertEvent ports calendar.ts's createEvent plus syncGoogleEventCreate's request-shaping
// (google-sync.ts): summary/description/timezone come off the current booking_pages row (read
// here via b.PageID, since BookingView itself carries none of them), start/end/attendee off b.
func (g *googleSync) InsertEvent(ctx context.Context, userID string, b *BookingView) (string, error) {
	page, err := g.q.GetBookingPage(ctx, b.PageID)
	if err != nil {
		return "", err
	}

	reqBody, err := json.Marshal(map[string]any{
		"summary":     page.Title,
		"description": nullString(page.Description),
		"start":       map[string]string{"dateTime": b.StartAt, "timeZone": page.Timezone},
		"end":         map[string]string{"dateTime": b.EndAt, "timeZone": page.Timezone},
		"attendees":   []map[string]string{{"email": b.VisitorEmail}},
	})
	if err != nil {
		return "", err
	}

	resp, err := g.doWithRefresh(ctx, userID, func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			GoogleCalendarBaseURL+"/calendars/primary/events?sendUpdates=all", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		return googleHTTPClient.Do(req)
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &googleAPIError{status: resp.StatusCode}
	}

	var data struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return data.ID, nil
}

// DeleteEvent ports calendar.ts's deleteEvent: a 404/410 (the event is already gone, whether from
// a prior delete or the organiser removing it by hand) counts as success, never a sync failure to
// report.
func (g *googleSync) DeleteEvent(ctx context.Context, userID, eventID string) error {
	resp, err := g.doWithRefresh(ctx, userID, func(token string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
			GoogleCalendarBaseURL+"/calendars/primary/events/"+url.PathEscape(eventID), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return googleHTTPClient.Do(req)
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil
	}
	return &googleAPIError{status: resp.StatusCode}
}

// SetGoogleEventID persists booking's known Google Calendar event id — nil clears it (a
// successful delete), a non-nil value records a freshly created one (a successful insert). Ports
// bookingService.setGoogleEventId (bookings.ts).
func (s *Service) SetGoogleEventID(ctx context.Context, bookingID string, eventID *string) error {
	return s.q.UpdateBookingGoogleEventID(ctx, queries.UpdateBookingGoogleEventIDParams{
		ID:            bookingID,
		GoogleEventID: optionalTrimmedString(eventID),
		UpdatedAt:     time.Now().UTC(),
	})
}

// --- Composition with Book/PublicAvailability/Reschedule (bookings.go) --------------------------

// googleBusyForPage best-effort merges page's own Google Calendar freebusy over [from, to) into
// existing — ports computeBusy's Google half (bookings.functions.ts). A no-op (no network call at
// all) unless google is wired, the page has sync on, and it has an assigned member — and any Busy
// error past that (no connection, a hard API failure) degrades silently to existing alone: Google
// Calendar availability is always best-effort, never something that can block a booking or its
// own availability display.
func googleBusyForPage(ctx context.Context, google GoogleSync, page queries.BookingPage, from, to time.Time, existing []Interval) []Interval {
	if google == nil || !page.GoogleSync || !page.MemberUserID.Valid {
		return existing
	}
	extra, err := google.Busy(ctx, strconv.FormatInt(page.MemberUserID.Int64, 10), from, to)
	if err != nil {
		return existing
	}
	return append(existing, extra...)
}

// jobKindGoogleSync is the scheduled_jobs kind Book/Cancel/Reschedule enqueue (in their own
// transaction — see enqueueGoogleSync) whenever a Google Calendar create/delete might be needed;
// handleGoogleSyncJob does the actual API call once that transaction has committed.
const jobKindGoogleSync = "google:sync"

// googleSyncMaxAttempts bounds retries for a "google:sync" job's own bug-shaped failures (a
// payload that fails to decode, an unknown kind, a local DB error persisting the result) — a real
// Google Calendar API failure is never retried by the job system itself (see this file's package
// doc comment): it's reported once via a "mail:booking"/"sync_failed" job and the handler returns
// nil either way.
const googleSyncMaxAttempts = 5

// googleSyncPayload is the "google:sync" job's payload: kind plus the booking id, in the same
// ids-only spirit as mailBookingPayload (emails.go) — the handler re-reads the booking and its
// page fresh, so a page whose googleSync toggle (or member) changed between enqueue and run is
// reflected correctly rather than acting on stale data.
type googleSyncPayload struct {
	Kind      string `json:"kind"` // "insert" | "delete" | "reschedule"
	BookingID string `json:"bookingId"`
}

// enqueueGoogleSync schedules one "google:sync" job. Not room-scoped (RoomKey nil): mirrors
// enqueueMailBooking's own reasoning (emails.go) — each queued sync stands for one specific
// lifecycle event, never collapsed/upserted with another.
func enqueueGoogleSync(ctx context.Context, tx db.DBTX, kind, bookingID string) error {
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:        jobKindGoogleSync,
		RunAt:       time.Now(),
		Payload:     googleSyncPayload{Kind: kind, BookingID: bookingID},
		MaxAttempts: googleSyncMaxAttempts,
	})
}

// handleGoogleSyncJob is "google:sync"'s body: re-read the booking/page fresh (either missing is
// a silent no-op — the world has moved on since this was scheduled, same as
// handleMailBookingJob's own contract), then dispatch to the kind-specific step below. s.google
// being nil here (the capability was turned off, or never configured, since this job was
// enqueued) is likewise a silent no-op — there's no client left to make the call with.
func (s *Service) handleGoogleSyncJob(ctx context.Context, job jobs.Job) error {
	var payload googleSyncPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("bookings: decode google:sync payload: %w", err)
	}
	if s.google == nil {
		return nil
	}

	booking, err := s.q.GetBooking(ctx, payload.BookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	switch payload.Kind {
	case "insert":
		return s.googleSyncInsert(ctx, booking, page)
	case "delete":
		_, err := s.googleSyncDelete(ctx, booking, page)
		return err
	case "reschedule":
		return s.googleSyncReschedule(ctx, booking, page)
	default:
		return fmt.Errorf("bookings: unknown google:sync kind %q", payload.Kind)
	}
}

// googleSyncInsert creates booking's Google Calendar event and stores its id — ports
// syncGoogleEventCreate (google-sync.ts). A no-op when the booking's since been cancelled, the
// page's sync toggle is (now) off, or the page has no assigned member — all re-checked fresh here
// rather than trusting whatever was true when this was enqueued. ErrGoogleNotConnected (no usable
// account) is likewise a silent no-op; any other error enqueues the "sync_failed" notice.
func (s *Service) googleSyncInsert(ctx context.Context, booking queries.Booking, page queries.BookingPage) error {
	if booking.Status == "cancelled" || !page.GoogleSync || !page.MemberUserID.Valid {
		return nil
	}

	view := toBookingView(booking)
	eventID, err := s.google.InsertEvent(ctx, strconv.FormatInt(page.MemberUserID.Int64, 10), &view)
	if err != nil {
		if errors.Is(err, ErrGoogleNotConnected) {
			return nil
		}
		return enqueueMailBooking(ctx, s.db, "sync_failed", booking.ID, nil)
	}
	return s.SetGoogleEventID(ctx, booking.ID, &eventID)
}

// googleSyncDelete deletes booking's known Google Calendar event, if any — ports
// syncGoogleEventDelete (google-sync.ts). Returns true when there was nothing to delete, no
// organiser account to hold a token, the event was already gone, or the delete succeeded; false
// only on a real hard failure (for which the "sync_failed" notice has already been enqueued) —
// googleSyncReschedule uses this to decide whether it's safe to create the replacement event, the
// same way syncGoogleEventsForReschedule's own bool return does.
func (s *Service) googleSyncDelete(ctx context.Context, booking queries.Booking, page queries.BookingPage) (bool, error) {
	if !booking.GoogleEventID.Valid {
		return true, nil
	}
	if !page.MemberUserID.Valid {
		// No organiser account to hold a token — ports the TS source's own `if (!token) return
		// true` branch: nothing more to do, and (like TS) googleEventId is deliberately left
		// as-is rather than cleared, since nothing was actually cleaned up.
		return true, nil
	}

	err := s.google.DeleteEvent(ctx, strconv.FormatInt(page.MemberUserID.Int64, 10), booking.GoogleEventID.String)
	if err == nil {
		return true, s.SetGoogleEventID(ctx, booking.ID, nil)
	}
	if errors.Is(err, ErrGoogleNotConnected) {
		return true, nil
	}
	return false, enqueueMailBooking(ctx, s.db, "sync_failed", booking.ID, nil)
}

// googleSyncReschedule ports syncGoogleEventsForReschedule (google-sync.ts): delete the old event
// (if any), and only create the replacement when that delete actually succeeded — a failed delete
// leaves googleEventId pointed at the still-real old event rather than overwriting it with an
// orphaned one.
func (s *Service) googleSyncReschedule(ctx context.Context, booking queries.Booking, page queries.BookingPage) error {
	ok, err := s.googleSyncDelete(ctx, booking, page)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.googleSyncInsert(ctx, booking, page)
}
