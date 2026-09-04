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
//   - The seeded user IS marked verified (users.email_verified_at) before any session is minted:
//     plan A restored the e-mail verification gate (RequireSession/WithOrgSession 403
//     `email_unverified`), so an unverified seed would be useless to every fixture. Sign-in is
//     explicit (POST /signin/credential) rather than relying on signup's own Set-Cookie, so it
//     keeps working whether or not autoSignInOnSignUp is enabled.
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
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/thecodearcher/limen"
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
	// FailedJob inserts one already dead-lettered scheduled_jobs row (attempts == max_attempts) so
	// the admin console's Jobs page has something to list and retry — see seedDeadLetter.
	FailedJob bool   `json:"failedJob"`
	Role      string `json:"role"`
}

// seedResult mirrors seed.ts's Response.json({email, password, name, pollId, pageId, handle,
// slug}) byte-for-byte, including null (not "omitted") for whichever of pollId/pageId/handle/slug
// this call didn't ask for — e2e/fixtures.ts's SeededUser reads these as optional, which a JSON
// null satisfies exactly the same as a missing key would.
type seedResult struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Name        string  `json:"name"`
	PollID      *string `json:"pollId"`
	PageID      *string `json:"pageId"`
	Handle      *string `json:"handle"`
	Slug        *string `json:"slug"`
	FailedJobID *string `json:"failedJobId"`
}

// RegisterTestRoutes mounts POST /api/test/seed — the caller (cmd/whenweall) must only call this
// when cfg.EnableTestRoutes is true (config.Load already hard-fails boot if that's set alongside
// APP_ENV=production, so there is no production code path that can reach here). sqlDB is needed
// for the two things Limen's HTTP surface can't do for us: marking the fresh user verified and
// inserting a dead-lettered job.
func RegisterTestRoutes(mux *http.ServeMux, cfg *config.Config, sqlDB *sql.DB, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings) {
	mux.HandleFunc("POST /api/test/seed", handleSeed(cfg, sqlDB, authSvc, polls, bookings))
}

func handleSeed(cfg *config.Config, sqlDB *sql.DB, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings) http.HandlerFunc {
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
			// Lowercased: db.NewID()'s alphabet is mixed-case, but Limen stores/looks up email
			// normalized (limen.NormalizeEmail — lowercased, trimmed). Without this, the address
			// this response claims would differ from users.email's actual stored value for any
			// caller that queries by exact string instead of going through Limen's own normalized
			// lookups (e.g. this route's own seedMarkVerified/seedDeadLetter tests below).
			email = fmt.Sprintf("test-%s@example.com", strings.ToLower(db.NewID()))
		}
		name := body.Name
		if name == "" {
			name = "Test User"
		}
		password := body.Password
		if password == "" {
			password = seedDefaultPassword
		}

		if err := seedSignUp(authSvc, email, password, name); err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signup failed: "+err.Error(), nil)
			return
		}

		// Verified BEFORE the first session is minted (a Session is built from the user row as it
		// stands — the a442f9f lesson for the staff role applies to EmailVerified too).
		if err := seedMarkVerified(r.Context(), sqlDB, email); err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: marking verified failed: "+err.Error(), nil)
			return
		}
		if body.Role == "staff" {
			if err := authSvc.MakeStaff(r.Context(), email); err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: MakeStaff failed: "+err.Error(), nil)
				return
			}
		}

		cookies, err := seedSignIn(authSvc, email, password)
		if err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signin failed: "+err.Error(), nil)
			return
		}
		// Every signup gets a personal organization, but (unlike the old TS route's synchronous
		// better-auth hook) this stack only creates it lazily, on the first request that passes
		// through authSvc.Middleware (see auth.Service's ensurePersonalOrgOnce doc comment) — so
		// seed content needs one authenticated round trip before an org id exists to create
		// anything in.
		sess := seedTriggerSession(authSvc, cookies)
		if sess == nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: no session established after signin", nil)
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
		if body.FailedJob {
			id, err := seedDeadLetter(r.Context(), sqlDB)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: inserting dead-lettered job failed: "+err.Error(), nil)
				return
			}
			result.FailedJobID = &id
		}

		JSON(w, http.StatusOK, result)
	}
}

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

// seedSignUp drives Limen's own signup route in-process (authSvc.Handler(), the exact handler
// internal/httpserver.Server mounts at "/api/v1/auth/") via httptest, exactly the way a browser's
// POST to /api/v1/auth/signup/credential would. `name` rides along in the body for plan A's
// after-signup hook, which persists it as the display name (GetProfile). The response's cookies
// are deliberately NOT used: seedSignIn mints the session after the user is marked verified.
func seedSignUp(authSvc *auth.Service, email, password, name string) error {
	payload, err := json.Marshal(map[string]string{"email": email, "password": password, "name": name})
	if err != nil {
		return fmt.Errorf("marshal signup body: %w", err)
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
		return fmt.Errorf("signup/credential: status %d: %s", res.StatusCode, respBody)
	}
	return nil
}

// seedSignIn is seedSignUp's counterpart for POST /signin/credential — the same route every
// Playwright `signIn` helper submits to — returning the session cookies Limen sets.
func seedSignIn(authSvc *auth.Service, email, password string) ([]*http.Cookie, error) {
	payload, err := json.Marshal(map[string]string{"credential": email, "password": password})
	if err != nil {
		return nil, fmt.Errorf("marshal signin body: %w", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signin/credential", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = nextSeedRemoteAddr()
	rec := httptest.NewRecorder()
	authSvc.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("signin/credential: status %d: %s", res.StatusCode, respBody)
	}
	return res.Cookies(), nil
}

// seedMarkVerified sets users.email_verified_at for a freshly signed-up user. COALESCE keeps an
// already-set timestamp (e.g. if a hook marked the user verified), so this is safe to run twice.
//
// limen.NormalizeEmail matches the case Limen itself stores email in (lowercased, trimmed —
// utils.go's own NormalizeEmail, the same helper auth.Service.MarkEmailVerified and GetProfile
// use): the default seed email embeds db.NewID(), whose alphabet is mixed-case, so looking this
// row up by the exact caller-supplied string would silently update zero rows for most seeds.
func seedMarkVerified(ctx context.Context, sqlDB *sql.DB, email string) error {
	res, err := sqlDB.ExecContext(ctx,
		`UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE email = $1`,
		limen.NormalizeEmail(email))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("expected to update 1 user row for %s, updated %d", email, n)
	}
	return nil
}

// seedDeadLetterKind is a real mail kind so the row reads like a genuine dead letter on the admin
// console's Jobs page. If the console retries it, the mailer handler fails to decode the payload
// and the worker walks it back to dead-lettered after its retry budget — harmless noise, and
// exactly what a retry of a broken mail job does in production.
const seedDeadLetterKind = "mail:send"

// seedDeadLetter inserts one scheduled_jobs row with its attempt budget already spent (the
// dead-letter condition internal/jobs.Dead selects on: attempts >= max_attempts). The job id is
// embedded in last_error so an e2e spec can find exactly its own row in a shared table.
func seedDeadLetter(ctx context.Context, sqlDB *sql.DB) (string, error) {
	id := db.NewID()
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO scheduled_jobs (id, kind, room_key, run_at, payload, attempts, max_attempts, last_error)
		VALUES ($1, $2, NULL, now() - interval '1 hour', '{"e2e": true}'::jsonb, 5, 5, $3)
	`, id, seedDeadLetterKind, "e2e: seeded dead-lettered job "+id)
	if err != nil {
		return "", err
	}
	return id, nil
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
