package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
	mux.Handle("/api/v1/auth/", svc.Handler())

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

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

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

func TestMakeStaffUnknownEmailErrors(t *testing.T) {
	ts := newTestService(t)
	if err := ts.svc.MakeStaff(context.Background(), "nobody@example.com"); err == nil {
		t.Error("MakeStaff(unknown email) = nil error, want an error")
	}
}
