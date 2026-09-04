package httpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// turnstileConfig is testConfig plus a configured Turnstile pair (so cfg.Capabilities.Turnstile
// is true — config.Load derives it from the pair).
func turnstileConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "http://localhost:3000", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
		"TURNSTILE_SITE_KEY": "site", "TURNSTILE_SECRET_KEY": "secret",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Capabilities.Turnstile {
		t.Fatal("test config did not enable the Turnstile capability")
	}
	return cfg
}

func postAuth(t *testing.T, ts *httptest.Server, path string, body map[string]any, captcha string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if captcha != "" {
		req.Header.Set("X-Captcha-Token", captcha)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func readCode(t *testing.T, resp *http.Response) (int, string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body.Error.Code
}

func TestAuthCaptcha_RequiredOnHotRoutesWhenConfigured(t *testing.T) {
	// siteverify accepts exactly the token "good".
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": r.PostFormValue("response") == "good"})
	}))
	withSiteverifyStub(t, stub)

	d := testdb.New(t)
	cfg := turnstileConfig(t)
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, path := range []string{
		"/api/v1/auth/signup/credential",
		"/api/v1/auth/signin/credential",
		"/api/v1/auth/passwords/request-reset",
	} {
		body := map[string]any{"email": "cap@example.com", "credential": "cap@example.com", "password": "Str0ngPassw0rd"}

		status, code := readCode(t, postAuth(t, ts, path, body, ""))
		if status != http.StatusForbidden || code != "captcha_failed" {
			t.Errorf("%s without token: %d %q, want 403 captcha_failed", path, status, code)
		}
		status, code = readCode(t, postAuth(t, ts, path, body, "bad"))
		if status != http.StatusForbidden || code != "captcha_failed" {
			t.Errorf("%s with rejected token: %d %q, want 403 captcha_failed", path, status, code)
		}
		status, code = readCode(t, postAuth(t, ts, path, body, "good"))
		if code == "captcha_failed" {
			t.Errorf("%s with accepted token: still %d captcha_failed — request never reached Limen", path, status)
		}
	}

	// Anything else under the mount is untouched: a bogus verify-email token gets Limen's own
	// 4xx, never captcha_failed.
	status, code := readCode(t, postAuth(t, ts, "/api/v1/auth/verify-email", map[string]any{"token": "x"}, ""))
	if code == "captcha_failed" || status/100 == 2 {
		t.Errorf("verify-email: %d %q, want Limen's own rejection", status, code)
	}
}

func TestAuthCaptcha_NoOpWhenTurnstileUnconfigured(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig() // no TURNSTILE_* → capability off
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	status, code := readCode(t, postAuth(t, ts, "/api/v1/auth/signup/credential",
		map[string]any{"email": "nocap@example.com", "password": "Str0ngPassw0rd"}, ""))
	if status/100 != 2 || code == "captcha_failed" {
		t.Fatalf("signup without captcha configured: %d %q, want 2xx", status, code)
	}
}
