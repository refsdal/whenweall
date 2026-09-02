package httpserver

// Task 5's e2e seed endpoint: a straight port of src/routes/api/test/seed.ts onto this stack's own
// auth/polls/bookings seams. Playwright's e2e/fixtures.ts POSTs the same JSON body this file
// accepts (see its own `seed` helper) — {name, withPoll, withSignup, withBookingPage, role} — and
// expects back {email, password, name, pollId, pageId, handle, slug}, exactly like the TS route
// did, so the existing fixtures need no changes to point at this stack.
//
// Two differences from the TS route, both deliberate:
//   - No `plan: 'premium'` support: billing/subscriptions have no home in this rewrite (see
//     internal/admin/stats.go's own doc comment), so there is nothing left to seed a subscription
//     row into.
//   - No manual "set email_verified" step: Limen's credential-password plugin signs a fresh
//     signup straight in (autoSignInOnSignUp, the buildLimenConfig default) with no email-
//     verification gate on sign-in at all, unlike the old Better-Auth config this replaced — see
//     internal/auth/auth_test.go's TestSignupSigninMeFlow, which never touches the DB to unblock
//     a fresh signup either.
//
// Wiring cannot import internal/polls or internal/bookings directly: both already import this
// package (handlers.go in each), so the reverse edge would be a compile-time cycle — the same
// constraint internal/rooms/endpoints.go's own doc comment describes for its narrow
// PollService/BookingService seams. SeedPolls/SeedBookings below are this file's version of that
// same pattern: a narrow interface built entirely from primitive types, satisfied structurally by
// *polls.Service/*bookings.Service (whose own CreateSamplePoll/CreateSampleSignup/
// CreateSampleBookingPage methods — internal/polls/seed.go, internal/bookings/seed.go — build the
// actual sample-data literals, since only those packages can reference their own
// CreatePollInput/PageInput types) without this package ever importing either.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
)

// SeedPolls is the narrow seam RegisterTestRoutes needs from *polls.Service.
type SeedPolls interface {
	CreateSamplePoll(ctx context.Context, orgID, userID string) (pollID string, err error)
	CreateSampleSignup(ctx context.Context, orgID, userID string) (pollID string, err error)
}

// SeedBookings is the narrow seam RegisterTestRoutes needs from *bookings.Service.
type SeedBookings interface {
	CreateSampleBookingPage(ctx context.Context, orgID, userID string) (pageID, handle, slug string, err error)
}

// seedDefaultPassword mirrors the old TS route's own default ("correct horse battery staple") in
// spirit, not literally: buildLimenConfig sets the credential-password plugin's policy to
// length-only (>= 12, no character-class requirement — see its own doc comment), matching
// Better-Auth's laxer default the old route relied on, so this could be any string long enough;
// it stays a mixed-case-plus-digit phrase for realism, not because the policy demands it.
const seedDefaultPassword = "Str0ngPassw0rd!"

// seedRequest mirrors seed.ts's SeedBody. Role only ever recognizes "staff" (mirroring the TS
// route's own `role?: 'staff'`); Plan is intentionally absent — see this file's package doc
// comment.
type seedRequest struct {
	Email           string `json:"email"`
	Name            string `json:"name"`
	Password        string `json:"password"`
	WithPoll        bool   `json:"withPoll"`
	WithSignup      bool   `json:"withSignup"`
	WithBookingPage bool   `json:"withBookingPage"`
	Role            string `json:"role"`
}

// seedResult mirrors seed.ts's Response.json({email, password, name, pollId, pageId, handle,
// slug}) byte-for-byte, including null (not "omitted") for whichever of pollId/pageId/handle/slug
// this call didn't ask for — e2e/fixtures.ts's SeededUser reads these as optional, which a JSON
// null satisfies exactly the same as a missing key would.
type seedResult struct {
	Email    string  `json:"email"`
	Password string  `json:"password"`
	Name     string  `json:"name"`
	PollID   *string `json:"pollId"`
	PageID   *string `json:"pageId"`
	Handle   *string `json:"handle"`
	Slug     *string `json:"slug"`
}

// RegisterTestRoutes mounts POST /api/test/seed — the caller (cmd/whenweall) must only call this
// when cfg.EnableTestRoutes is true (config.Load already hard-fails boot if that's set alongside
// APP_ENV=production, so there is no production code path that can reach here).
func RegisterTestRoutes(mux *http.ServeMux, cfg *config.Config, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings) {
	mux.HandleFunc("POST /api/test/seed", handleSeed(cfg, authSvc, polls, bookings))
}

func handleSeed(cfg *config.Config, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Belt-and-braces (seed.ts's own doc comment: "Gated so it can never be reachable in
		// production even if ENABLE_TEST_ROUTES is accidentally left set"): the real gate is
		// RegisterTestRoutes only ever being called when cfg.EnableTestRoutes is true
		// (cmd/whenweall's serve()), so this route structurally doesn't exist otherwise — this
		// second check only matters if a future refactor ever calls RegisterTestRoutes
		// unconditionally.
		if !cfg.EnableTestRoutes {
			Err(w, http.StatusNotFound, "not_found", "not found", nil)
			return
		}

		var body seedRequest
		// A missing/malformed body is exactly seed.ts's own `.catch(() => ({}))` — every field
		// defaults rather than 400ing, so an e2e spec that only wants `{}` (a plain verified user)
		// doesn't have to send an empty JSON object explicitly. json.Decode on an empty r.Body
		// (Content-Length 0) returns io.EOF, which — unlike DecodeJSON's own "malformed JSON"
		// 400 — this route treats as "no body", the same as seed.ts's catch does.
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				Err(w, http.StatusBadRequest, "invalid", "malformed JSON body", nil)
				return
			}
		}
		_ = r.Body.Close()

		email := body.Email
		if email == "" {
			email = fmt.Sprintf("test-%s@example.com", db.NewID())
		}
		name := body.Name
		if name == "" {
			name = "Test User"
		}
		password := body.Password
		if password == "" {
			password = seedDefaultPassword
		}

		cookies, err := seedSignUp(authSvc, email, password)
		if err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signup failed: "+err.Error(), nil)
			return
		}

		// Set before any session is used: a session is minted from the user row as it stands at
		// that moment, so a role granted afterwards would not apply to it (the a442f9f lesson —
		// forward the staff role, and set it before the one call below that actually establishes
		// this seed's own session).
		if body.Role == "staff" {
			if err := authSvc.MakeStaff(r.Context(), email); err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: MakeStaff failed: "+err.Error(), nil)
				return
			}
		}

		// Every signup gets a personal organization, but (unlike the old TS route's synchronous
		// better-auth hook) this stack only creates it lazily, on the first request that passes
		// through authSvc.Middleware (see auth.Service's ensurePersonalOrgOnce doc comment) — so
		// seed content needs one authenticated round trip before an org id exists to create
		// anything in.
		sess := seedTriggerSession(authSvc, cookies)
		if sess == nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: no session established after signup", nil)
			return
		}

		result := seedResult{Email: email, Password: password, Name: name}

		if body.WithPoll {
			id, err := polls.CreateSamplePoll(r.Context(), sess.ActiveOrgID, sess.UserID)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: creating poll failed: "+err.Error(), nil)
				return
			}
			result.PollID = &id
		}
		// Mirrors seed.ts exactly: `withSignup` reuses the same `pollId` field `withPoll` does
		// (both write into one `let pollId` there) — a caller asking for both gets the sign-up
		// sheet's id back, not the datetime poll's, since this branch runs second.
		if body.WithSignup {
			id, err := polls.CreateSampleSignup(r.Context(), sess.ActiveOrgID, sess.UserID)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: creating sign-up sheet failed: "+err.Error(), nil)
				return
			}
			result.PollID = &id
		}
		if body.WithBookingPage {
			pageID, handle, slug, err := bookings.CreateSampleBookingPage(r.Context(), sess.ActiveOrgID, sess.UserID)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: creating booking page failed: "+err.Error(), nil)
				return
			}
			result.PageID = &pageID
			result.Handle = &handle
			result.Slug = &slug
		}

		JSON(w, http.StatusOK, result)
	}
}

// seedSignUp drives Limen's own signup route in-process (authSvc.Handler(), the exact handler
// internal/httpserver.Server mounts at "/api/v1/auth/") via httptest, exactly the way a browser's
// POST to /api/v1/auth/signup/credential would, so the created user's password hash, session
// token and Set-Cookie are all whatever Limen itself produces — nothing here reaches into
// Limen's own tables directly. autoSignInOnSignUp (buildLimenConfig's default, unchanged) means
// signup alone mints a session; there is no separate signin call to make.
// seedRemoteAddrCounter backs nextSeedRemoteAddr — see its own doc comment.
var seedRemoteAddrCounter atomic.Uint32

// nextSeedRemoteAddr hands each seedSignUp call a distinct synthetic client address, rather than
// letting every one of them share httptest.NewRequest's fixed default ("192.0.2.1:1234"). Belt-
// and-braces alongside the two real fixes for the rate-limiter-trips-on-repeated-seeding problem
// this address sharing would otherwise cause (internal/auth.httpConfigOptions disables Limen's
// own rate limiter, and this package's routes() skips its Postgres-backed one, both gated on
// EnableTestRoutes exactly like this route is) — belt-and-braces because a future change to
// either of those two gates should not silently reintroduce "every seeded signup looks like the
// same IP" as a second, independent way to trip a limiter keyed on it.
func nextSeedRemoteAddr() string {
	n := seedRemoteAddrCounter.Add(1)
	return fmt.Sprintf("10.%d.%d.%d:%d", (n>>16)&0xff, (n>>8)&0xff, n&0xff, 40000+(n%20000))
}

func seedSignUp(authSvc *auth.Service, email, password string) ([]*http.Cookie, error) {
	payload, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return nil, fmt.Errorf("marshal signup body: %w", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup/credential", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = nextSeedRemoteAddr()
	rec := httptest.NewRecorder()
	authSvc.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("signup/credential: status %d: %s", res.StatusCode, respBody)
	}
	return res.Cookies(), nil
}

// seedTriggerSession is this file's own version of internal/auth's triggerSessionResolution test
// helper (personal_org_test.go): a request carrying cookies (the signup's own Set-Cookie) through
// authSvc.Middleware in-process, so resolveSession actually runs — creating the personal org on
// its first pass for a brand-new user (ensurePersonalOrgOnce) and resolving ActiveOrgID — and the
// resulting *auth.Session is captured directly via auth.FromContext rather than parsed back out of
// a JSON response body (nothing here needs Limen's own /me route at all).
func seedTriggerSession(authSvc *auth.Service, cookies []*http.Cookie) *auth.Session {
	var got *auth.Session
	handler := authSvc.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/test/seed/_session", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return got
}
