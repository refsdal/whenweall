package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// accountHarness runs the real Server.Handler() (every middleware, the auth mount, the account
// routes) behind httptest, with one cookie-jar client.
type accountHarness struct {
	t       *testing.T
	authSvc *auth.Service
	server  *httptest.Server
	client  *http.Client
}

func newAccountHarness(t *testing.T) *accountHarness {
	t.Helper()
	d := testdb.New(t)
	cfg := testConfig()
	authSvc := testAuthService(t, cfg, d)
	srv := httpserver.New(cfg, d, authSvc)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &accountHarness{t: t, authSvc: authSvc, server: ts, client: &http.Client{Jar: jar}}
}

func (h *accountHarness) do(method, path string, body any) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (h *accountHarness) mustStatus(resp *http.Response, want int, what string) map[string]any {
	h.t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		h.t.Fatalf("%s: status %d, want %d: %s", what, resp.StatusCode, want, raw)
	}
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// must2xx is mustStatus for routes whose exact success code is Limen's business (200 vs 201).
func (h *accountHarness) must2xx(resp *http.Response, what string) {
	h.t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("%s: status %d, want 2xx: %s", what, resp.StatusCode, raw)
	}
}

func (h *accountHarness) errorCode(body map[string]any) string {
	errObj, _ := body["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

const accountPassword = "Str0ngPassw0rd"

func (h *accountHarness) signUp(email string) {
	h.t.Helper()
	h.must2xx(h.do(http.MethodPost, "/api/v1/auth/signup/credential", map[string]any{"email": email, "password": accountPassword}), "signup")
}

func (h *accountHarness) signIn(email string) {
	h.t.Helper()
	h.must2xx(h.do(http.MethodPost, "/api/v1/auth/signin/credential", map[string]any{"credential": email, "password": accountPassword}), "signin")
}

func (h *accountHarness) verify(email string) {
	h.t.Helper()
	if err := h.authSvc.MarkEmailVerified(context.Background(), email); err != nil {
		h.t.Fatalf("MarkEmailVerified: %v", err)
	}
}

func (h *accountHarness) me() map[string]any {
	h.t.Helper()
	body := h.mustStatus(h.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusOK, "GET /me")
	user, _ := body["user"].(map[string]any)
	return user
}

func TestPatchMe_UpdatesNameAndLocaleEvenBeforeVerification(t *testing.T) {
	h := newAccountHarness(t)
	email := "patch-me@example.com"
	h.signUp(email)
	h.signIn(email) // unverified on purpose

	h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "Ada Lovelace", "locale": "nb"}), http.StatusNoContent, "PATCH /me")

	user := h.me()
	if got, _ := user["name"].(string); got != "Ada Lovelace" {
		t.Errorf("name = %q, want Ada Lovelace", got)
	}
	if got, _ := user["locale"].(string); got != "nb" {
		t.Errorf("locale = %q, want nb", got)
	}

	body := h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{"locale": "de"}), http.StatusUnprocessableEntity, "PATCH bad locale")
	if h.errorCode(body) != "invalid" {
		t.Errorf("error.code = %q, want invalid", h.errorCode(body))
	}
	fields, _ := body["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["locale"]; !ok {
		t.Errorf("fields = %+v, want a locale entry", fields)
	}

	body = h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{}), http.StatusBadRequest, "PATCH empty")
	if h.errorCode(body) != "invalid" {
		t.Errorf("error.code = %q, want invalid", h.errorCode(body))
	}
}

func TestPatchMe_RequiresASession(t *testing.T) {
	h := newAccountHarness(t)
	body := h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "x"}), http.StatusUnauthorized, "anonymous PATCH")
	if h.errorCode(body) != "unauthenticated" {
		t.Errorf("error.code = %q, want unauthenticated", h.errorCode(body))
	}
}

func TestDeleteMe_RequiresCurrentPasswordForCredentialAccounts(t *testing.T) {
	h := newAccountHarness(t)
	email := "delete-me-http@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)

	body := h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", nil), http.StatusBadRequest, "DELETE without body")
	if h.errorCode(body) != "password_required" {
		t.Errorf("error.code = %q, want password_required", h.errorCode(body))
	}
	body = h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", map[string]any{"password": "wrong"}), http.StatusForbidden, "DELETE wrong password")
	if h.errorCode(body) != "invalid_password" {
		t.Errorf("error.code = %q, want invalid_password", h.errorCode(body))
	}
	h.me() // still alive

	h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", map[string]any{"password": accountPassword}), http.StatusNoContent, "DELETE /me")
	h.mustStatus(h.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized, "GET /me after delete")
}

// TestDeleteMe_IsRateLimited is the reviewer's finding 2: DELETE /api/v1/me runs Argon2id
// (auth.ComparePassword) with no rate limit at all, unlike every other password-verifying entry
// point (POST /signin/credential gets both authRateLimitMiddleware's 10/min and
// credential-password's own 5-per-10s rule). Five wrong-password attempts per minute is the
// budget this account.delete limiter is meant to enforce (account.go's fix); the sixth must be
// refused with 429 rate_limited before ComparePassword ever runs again, regardless of whether the
// password supplied would have been right or wrong.
func TestDeleteMe_IsRateLimited(t *testing.T) {
	h := newAccountHarness(t)
	email := "delete-me-ratelimit@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)

	for i := 1; i <= 5; i++ {
		resp := h.do(http.MethodDelete, "/api/v1/me", map[string]any{"password": "wrong"})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("attempt %d: status = %d, want 403 invalid_password while under the rate-limit budget", i, resp.StatusCode)
		}
	}

	body := h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", map[string]any{"password": "wrong"}), http.StatusTooManyRequests, "DELETE /me over the rate-limit budget")
	if h.errorCode(body) != "rate_limited" {
		t.Errorf("error.code = %q, want rate_limited", h.errorCode(body))
	}

	// The account must still exist: even the correct password must not get through once the
	// budget is exhausted.
	h.me()
}

func TestMyOrganizations_ListAndSwitch(t *testing.T) {
	h := newAccountHarness(t)
	email := "orgs-http@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)
	h.me() // resolves the session once so the personal org exists and is active

	h.must2xx(h.do(http.MethodPost, "/api/v1/auth/organizations/", map[string]any{"name": "Team HTTP", "slug": "team-http"}), "create org")

	resp := h.do(http.MethodGet, "/api/v1/me/organizations", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me/organizations: %d", resp.StatusCode)
	}
	var orgs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2: %+v", len(orgs), orgs)
	}
	// Limen's own POST /organizations/ (organization@v0.1.1's CreateOrganization handler)
	// unconditionally activates the org it just created — confirmed against the pinned source
	// (handlers.go's CreateOrganization calls SetActiveOrganization unconditionally) — so
	// team-http, not the personal org, is already active here, with no switch call made yet.
	var teamID, personalID string
	for _, o := range orgs {
		id, _ := o["id"].(string)
		active, _ := o["active"].(bool)
		switch o["slug"] {
		case "team-http":
			teamID = id
			if !active {
				t.Error("team-http reported inactive right after being created")
			}
		default:
			personalID = id
			if active {
				t.Error("personal org reported active right after team-http was created")
			}
		}
	}
	if teamID == "" || personalID == "" {
		t.Fatalf("expected team-http and a personal org, got %+v", orgs)
	}

	// Switch back to personal, then to team-http — a real transition each way. The intermediate
	// GET below is load-bearing: without it, a handleSwitchOrganization that never called
	// s.authSvc.SwitchOrganization at all (a no-op 204) would leave team-http active throughout,
	// and the final assertions (checking only the net state after BOTH switches) would still
	// pass — see the mid-switch check for the state a no-op would fail to produce.
	h.mustStatus(h.do(http.MethodPost, "/api/v1/me/active-organization", map[string]any{"orgId": personalID}), http.StatusNoContent, "switch to personal")

	mid := h.do(http.MethodGet, "/api/v1/me/organizations", nil)
	defer func() { _ = mid.Body.Close() }()
	if mid.StatusCode != http.StatusOK {
		t.Fatalf("GET /me/organizations after switch to personal: %d", mid.StatusCode)
	}
	var midOrgs []map[string]any
	if err := json.NewDecoder(mid.Body).Decode(&midOrgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, o := range midOrgs {
		active, _ := o["active"].(bool)
		if (o["id"] == personalID) != active {
			t.Errorf("after switch to personal: %v (id=%v) active=%v", o["slug"], o["id"], active)
		}
	}

	h.mustStatus(h.do(http.MethodPost, "/api/v1/me/active-organization", map[string]any{"orgId": teamID}), http.StatusNoContent, "switch to team")

	resp2 := h.do(http.MethodGet, "/api/v1/me/organizations", nil)
	defer func() { _ = resp2.Body.Close() }()
	var after []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, o := range after {
		active, _ := o["active"].(bool)
		if (o["slug"] == "team-http") != active {
			t.Errorf("after switch: %v active=%v", o["slug"], active)
		}
	}

	body := h.mustStatus(h.do(http.MethodPost, "/api/v1/me/active-organization", map[string]any{"orgId": "999999"}), http.StatusForbidden, "switch to unknown org")
	if h.errorCode(body) != "forbidden" {
		t.Errorf("error.code = %q, want forbidden", h.errorCode(body))
	}
}

func TestMyOrganizations_GatedOnVerification(t *testing.T) {
	h := newAccountHarness(t)
	email := "orgs-unverified@example.com"
	h.signUp(email)
	h.signIn(email)
	body := h.mustStatus(h.do(http.MethodGet, "/api/v1/me/organizations", nil), http.StatusForbidden, "unverified list")
	if h.errorCode(body) != "email_unverified" {
		t.Errorf("error.code = %q, want email_unverified", h.errorCode(body))
	}
}
