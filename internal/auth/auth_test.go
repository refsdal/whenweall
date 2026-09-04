package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

// capturedMail is a test-only Enqueuer: it appends every mailer.Message it's handed instead of
// writing to scheduled_jobs, so tests can assert on exactly what would have been sent.
type capturedMail struct {
	mu   sync.Mutex
	msgs []mailer.Message
}

func (c *capturedMail) enqueue(_ context.Context, _ db.DBTX, msg mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
	return nil
}

func (c *capturedMail) all() []mailer.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]mailer.Message, len(c.msgs))
	copy(out, c.msgs)
	return out
}

// find returns the first captured message with the given template name.
func (c *capturedMail) find(template string) (mailer.Message, bool) {
	for _, m := range c.all() {
		if m.Template == template {
			return m, true
		}
	}
	return mailer.Message{}, false
}

// testService wires a real Service (real Postgres via testdb, real Limen HTTP handler) behind an
// httptest.Server, with Middleware wrapping the mux so FromContext works in the probe routes
// below, plus a cookie-jar client so a signin's Set-Cookie carries into later requests the same
// way a browser would.
type testService struct {
	svc    *Service
	mail   *capturedMail
	server *httptest.Server
	client *http.Client
}

// newTestService builds a testService whose Service has Google/OIDC capabilities off (the
// default zero value) — TestOAuthRoutesAbsentWithoutConfig relies on that.
func newTestService(t *testing.T) *testService {
	t.Helper()
	return newTestServiceWithConfig(t, &config.Config{
		AppURL:      "http://app.example",
		LimenSecret: make([]byte, 32),
	})
}

func newTestServiceWithConfig(t *testing.T, cfg *config.Config) *testService {
	t.Helper()
	sqlDB := testdb.New(t)

	mail := &capturedMail{}
	svc, err := newService(cfg, sqlDB, mail.enqueue)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}

	mux := http.NewServeMux()
	// AuthMountGuard wraps the Limen mount here the same way internal/httpserver.Server.routes
	// wraps it in production — TestEmailVerificationGate (verification_test.go) exercises the
	// Limen-mount half of the gate (e.g. GET /api/v1/auth/organizations/active) through this same
	// testService, which would otherwise reach Limen's handler completely unguarded.
	mux.Handle("/api/v1/auth/", svc.AuthMountGuard(svc.Handler()))

	// /probe is this test's own route (not part of the seam's contract) exercising
	// FromContext/RequireSession/RequireStaff the same way a later plan's handlers would.
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := FromContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"anonymous": true})
			return
		}
		_ = json.NewEncoder(w).Encode(sess)
	})
	mux.HandleFunc("/probe/session", svc.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux.HandleFunc("/probe/staff", svc.RequireStaff(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux.HandleFunc("/probe/session-unverified-ok", svc.RequireSessionAllowUnverified(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux.HandleFunc("/probe/switch", svc.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		switch err := svc.SwitchOrganization(w, r, r.URL.Query().Get("org")); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrForbidden):
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	server := httptest.NewServer(svc.Middleware(mux))
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}

	return &testService{svc: svc, mail: mail, server: server, client: &http.Client{Jar: jar}}
}

func (ts *testService) url(path string) string {
	return ts.server.URL + path
}

func (ts *testService) postJSON(t *testing.T, path string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	resp, err := ts.client.Post(ts.url(path), "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (ts *testService) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := ts.client.Get(ts.url(path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func requireStatus2xx(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: status %d: %s", what, resp.StatusCode, body)
	}
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return body
}

// signUpVerifiedAndSignIn is the "give me a usable account" helper: signup (which mints no session
// since auto-sign-in is off), MarkEmailVerified (the gate refuses unverified sessions everywhere
// that matters), then signin on ts.client's cookie jar.
func (ts *testService) signUpVerifiedAndSignIn(t *testing.T, email string) {
	t.Helper()
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	if err := ts.svc.MarkEmailVerified(context.Background(), email); err != nil {
		t.Fatalf("MarkEmailVerified(%q): %v", email, err)
	}
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   signupPassword,
	}), "signin")
}

// signupPassword satisfies credential-password's default validation: min 8 chars, at least one
// uppercase letter and one number (symbols not required).
const signupPassword = "Str0ngPassw0rd"

func TestSignupSigninMeFlow(t *testing.T) {
	ts := newTestService(t)
	email := "ada@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	msg, ok := ts.mail.find("verify_email")
	if !ok {
		t.Fatal("no verify_email mail captured after signup")
	}
	if msg.To != email {
		t.Errorf("verify_email To = %q, want %q", msg.To, email)
	}
	url, _ := msg.Data["URL"].(string)
	wantPrefix := "http://app.example/verify-email?token="
	if !strings.HasPrefix(url, wantPrefix) || url == wantPrefix {
		t.Errorf("verify_email URL = %q, want prefix %q plus a token", url, wantPrefix)
	}

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   signupPassword,
	}), "signin")

	meResp := ts.get(t, "/api/v1/auth/me")
	if meResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(meResp.Body)
		_ = meResp.Body.Close()
		t.Fatalf("GET /me: status %d: %s", meResp.StatusCode, body)
	}
	meBody := decodeJSON(t, meResp)
	user, _ := meBody["user"].(map[string]any)
	if got, _ := user["email"].(string); got != email {
		t.Errorf("/me user.email = %q, want %q", got, email)
	}
	// The frontend's self-ownership checks need a usable id here — see sessionTransformer's doc
	// comment in auth.go for why Limen's own serialization drops it otherwise. It must come back
	// as a string of digits (the Go-string-id convention used everywhere else in this seam), not
	// whatever numeric type the driver happens to hand back.
	userIDStr, ok := user["id"].(string)
	if !ok || userIDStr == "" {
		t.Fatalf("/me user.id = %#v, want a non-empty string", user["id"])
	}
	if _, err := strconv.ParseInt(userIDStr, 10, 64); err != nil {
		t.Errorf("/me user.id = %q, want a string of digits: %v", userIDStr, err)
	}
	if isStaff, _ := user["isStaff"].(bool); isStaff {
		t.Errorf("/me user.isStaff = true, want false for a fresh signup")
	}

	probeResp := ts.get(t, "/probe")
	probeBody := decodeJSON(t, probeResp)
	if anon, _ := probeBody["anonymous"].(bool); anon {
		t.Fatalf("probe reported anonymous after signin: %+v", probeBody)
	}
	userID, _ := probeBody["UserID"].(string)
	if userID == "" {
		t.Errorf("probe Session.UserID is empty")
	}
	if staff, _ := probeBody["Staff"].(bool); staff {
		t.Errorf("probe Session.Staff = true, want false for a fresh signup")
	}
	if gotEmail, _ := probeBody["Email"].(string); gotEmail != email {
		t.Errorf("probe Session.Email = %q, want %q", gotEmail, email)
	}
}

// TestMeReflectsStaffFlagAfterMakeStaff covers sessionTransformer's isStaff addition to /me:
// false right after a fresh signup, true once MakeStaff has run — the same pattern
// TestStaffFlagAndRequireStaff already exercises for RequireStaff, here for the /me payload the
// frontend actually reads instead of the seam's internal Session.
func TestMeReflectsStaffFlagAfterMakeStaff(t *testing.T) {
	ts := newTestService(t)
	email := "future-staffer@example.com"

	ts.signUpVerifiedAndSignIn(t, email)

	meBody := decodeJSON(t, ts.get(t, "/api/v1/auth/me"))
	user, _ := meBody["user"].(map[string]any)
	if isStaff, _ := user["isStaff"].(bool); isStaff {
		t.Fatalf("/me user.isStaff = true before MakeStaff, want false: %+v", user)
	}

	if err := ts.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff: %v", err)
	}

	meBody2 := decodeJSON(t, ts.get(t, "/api/v1/auth/me"))
	user2, _ := meBody2["user"].(map[string]any)
	if isStaff, _ := user2["isStaff"].(bool); !isStaff {
		t.Errorf("/me user.isStaff = false after MakeStaff, want true: %+v", user2)
	}
	// The id must survive unchanged across that transition too.
	if got, _ := user2["id"].(string); got == "" {
		t.Errorf("/me user.id went missing after MakeStaff: %+v", user2)
	}
}

func TestRequireSessionRejectsAnonymous(t *testing.T) {
	ts := newTestService(t)

	resp := ts.get(t, "/probe/session")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	errObj, _ := body["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "unauthenticated" {
		t.Errorf("error.code = %q, want %q (body: %+v)", code, "unauthenticated", body)
	}
}

func TestPasswordResetEnqueuesMail(t *testing.T) {
	ts := newTestService(t)
	email := "reset-me@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/passwords/request-reset", map[string]any{
		"email": email,
	}), "request-reset")

	msg, ok := ts.mail.find("reset_password")
	if !ok {
		t.Fatal("no reset_password mail captured")
	}
	url, _ := msg.Data["URL"].(string)
	const marker = "token="
	idx := strings.Index(url, marker)
	if idx < 0 {
		t.Fatalf("reset_password URL = %q, missing %q", url, marker)
	}
	token := url[idx+len(marker):]
	if token == "" {
		t.Fatal("reset_password URL carries an empty token")
	}

	newPassword := "Ev3nStrongerPassw0rd"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/passwords/reset", map[string]any{
		"token":        token,
		"new_password": newPassword,
	}), "reset")

	// A separate cookie-jar client: the reset flow above may already have signed this session in
	// via the reset response, so signing in again on a fresh client is the real assertion that
	// the new password now works.
	fresh := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	fresh.client = &http.Client{Jar: jar}

	requireStatus2xx(t, fresh.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   newPassword,
	}), "signin with new password")
}

func TestStaffFlagAndRequireStaff(t *testing.T) {
	ts := newTestService(t)
	email := "staffer@example.com"

	ts.signUpVerifiedAndSignIn(t, email)

	resp := ts.get(t, "/probe/staff")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("probe/staff before MakeStaff: status %d, want 403: %s", resp.StatusCode, body)
	}

	if err := ts.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff: %v", err)
	}

	// The session was resolved (and its Staff flag cached in the Session value) before
	// MakeStaff ran, so a fresh request — not a fresh session — is what should observe it,
	// since resolveSession re-queries staff_users on every request.
	resp2 := ts.get(t, "/probe/staff")
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("probe/staff after MakeStaff: status %d, want 200: %s", resp2.StatusCode, body)
	}
}

// TestSessionCookieSecureMatchesAppURLScheme is I6's regression test: the session cookie's
// Secure attribute must track cfg.AppURL's scheme, not be hardcoded true. An http-served
// deployment (local/dev, or behind a proxy that terminates TLS and hands us plain http) that
// still got Secure would have the browser silently refuse to ever send the cookie back — an
// always-broken login, not just a weakened one — so this asserts both directions explicitly
// rather than only the https one.
func TestSessionCookieSecureMatchesAppURLScheme(t *testing.T) {
	for _, tc := range []struct {
		name       string
		appURL     string
		wantSecure bool
	}{
		{"http AppURL", "http://app.example", false},
		{"https AppURL", "https://app.example", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestServiceWithConfig(t, &config.Config{
				AppURL:      tc.appURL,
				LimenSecret: make([]byte, 32),
			})
			email := "cookie-secure@example.com"

			requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
				"email":    email,
				"password": signupPassword,
			}), "signup")

			resp := ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
				"credential": email,
				"password":   signupPassword,
			})
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode/100 != 2 {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("signin: status %d: %s", resp.StatusCode, body)
			}

			var sessionCookie string
			for _, c := range resp.Header.Values("Set-Cookie") {
				if strings.Contains(c, "limen_session=") {
					sessionCookie = c
					break
				}
			}
			if sessionCookie == "" {
				t.Fatalf("no limen_session Set-Cookie header found; got %v", resp.Header.Values("Set-Cookie"))
			}

			if gotSecure := strings.Contains(sessionCookie, "Secure"); gotSecure != tc.wantSecure {
				t.Errorf("Set-Cookie = %q\nSecure present = %v, want %v (AppURL %q)",
					sessionCookie, gotSecure, tc.wantSecure, tc.appURL)
			}
		})
	}
}

func TestOAuthRoutesAbsentWithoutConfig(t *testing.T) {
	ts := newTestService(t)

	// The verified path: oauth's PluginHTTPConfig base path is "/oauth" (see
	// plugins/oauth/plugin.go), mounted under the auth base path — so the full route is
	// /api/v1/auth/oauth/google/authorize, not /api/v1/auth/google/authorize.
	resp := ts.get(t, "/api/v1/auth/oauth/google/authorize")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("oauth route responded %d with Google/OIDC unconfigured, want 4xx: %s", resp.StatusCode, body)
	}
}

func TestRequireOrgMemberRejectsAnonymous(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background() // no Session stashed — same as an anonymous request

	if _, err := ts.svc.RequireOrgMember(ctx, "1"); err != ErrUnauthenticated {
		t.Errorf("RequireOrgMember(anonymous) error = %v, want ErrUnauthenticated", err)
	}
}

// TestRequireOrgMemberForbiddenForNonMember is I8's regression test for the "genuinely not a
// member" path: a real membership-check failure (organization.ErrMemberNotInOrganization) must
// still map to ErrForbidden, distinct from TestRequireOrgMemberInternalErrorOnDBFailure below
// (any other error, which must NOT map to ErrForbidden).
func TestRequireOrgMemberForbiddenForNonMember(t *testing.T) {
	ts := newTestService(t)

	ownerEmail := "org-owner-b@example.com"
	ts.signUpVerifiedAndSignIn(t, ownerEmail)
	triggerSessionResolution(t, ts)

	ownerID := lookupUserID(t, ts, ownerEmail)
	owner := &limen.User{ID: ownerID, Email: ownerEmail}
	page, err := ts.svc.orgs.ListOrganizations(context.Background(), owner, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	orgID := fmt.Sprint(page.Items[0].ID)

	// A second, unrelated user (their own personal org, not this one) tries to access the first
	// user's org — same client/jar is fine since RequireOrgMember is called directly here, not
	// through an HTTP round trip that would depend on which of the two is currently signed in.
	outsiderEmail := "org-outsider@example.com"
	ts.signUpVerifiedAndSignIn(t, outsiderEmail)
	triggerSessionResolution(t, ts)
	outsiderID := lookupUserID(t, ts, outsiderEmail)

	sess := &Session{UserID: fmt.Sprint(outsiderID)}
	ctx := context.WithValue(context.Background(), sessionCtxKey{}, sess)

	_, err = ts.svc.RequireOrgMember(ctx, orgID)
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("RequireOrgMember(non-member, someone else's org) error = %v, want ErrForbidden", err)
	}
	if errors.Is(err, ErrInternal) {
		t.Errorf("RequireOrgMember(non-member) error = %v, should not also be ErrInternal", err)
	}
}

// TestRequireOrgMemberInternalErrorOnDBFailure is I8's regression test for the other path: when
// the membership check itself fails for a reason that has nothing to do with membership (here, a
// closed database), RequireOrgMember must return ErrInternal, not ErrForbidden — the caller was
// never actually determined to be unauthorized, so a handler mapping ErrForbidden straight to 403
// would misreport a backend failure as an authorization decision.
func TestRequireOrgMemberInternalErrorOnDBFailure(t *testing.T) {
	ts := newTestService(t)
	email := "internal-error-check@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)
	userID := lookupUserID(t, ts, email)

	sess := &Session{UserID: fmt.Sprint(userID)}
	ctx := context.WithValue(context.Background(), sessionCtxKey{}, sess)

	if err := ts.svc.db.Close(); err != nil {
		t.Fatalf("closing db: %v", err)
	}

	_, err := ts.svc.RequireOrgMember(ctx, "1")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("RequireOrgMember with closed DB: error = %v, want ErrInternal", err)
	}
	if errors.Is(err, ErrForbidden) {
		t.Errorf("RequireOrgMember with closed DB: error = %v, should not also be ErrForbidden", err)
	}
}

func TestMakeStaffUnknownEmailErrors(t *testing.T) {
	ts := newTestService(t)
	if err := ts.svc.MakeStaff(context.Background(), "nobody@example.com"); err == nil {
		t.Error("MakeStaff(unknown email) = nil error, want an error")
	}
}

// TestOAuthAuthorizeTrustsViteDevOriginOnlyInDevelopment: under `bun dev` the SPA runs on Vite's
// :5173 and proxies /api to :3000, so GoogleButton sends redirect_uri=http://localhost:5173/...
// — which Limen's oauth plugin checks against IsTrustedOrigin (its base URL, i.e. APP_URL, plus
// WithHTTPTrustedOrigins). Development trusts the Vite origin; production trusts APP_URL alone.
func TestOAuthAuthorizeTrustsViteDevOriginOnlyInDevelopment(t *testing.T) {
	cfgFor := func(env string) *config.Config {
		return &config.Config{
			AppEnv:             env,
			AppURL:             "http://localhost:3000",
			LimenSecret:        make([]byte, 32),
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			Capabilities:       config.Capabilities{Google: true},
		}
	}
	const viteRedirect = "/api/v1/auth/oauth/google/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A5173%2Flogin"

	dev := newTestServiceWithConfig(t, cfgFor("development"))
	devResp := dev.get(t, viteRedirect)
	defer func() { _ = devResp.Body.Close() }()
	if devResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(devResp.Body)
		t.Fatalf("development: status %d, want 200 (Vite's origin must be a trusted redirect_uri): %s", devResp.StatusCode, body)
	}

	prod := newTestServiceWithConfig(t, cfgFor("production"))
	prodResp := prod.get(t, viteRedirect)
	defer func() { _ = prodResp.Body.Close() }()
	if prodResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(prodResp.Body)
		t.Fatalf("production: status %d, want 403 (only APP_URL's origin is trusted): %s", prodResp.StatusCode, body)
	}
}
