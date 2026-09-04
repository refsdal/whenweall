package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestInlineScriptHashes_OnlySrclessScripts pins the extraction rule: every <script> WITHOUT a
// src attribute is hashed over its exact text content (whitespace included — that is what the
// browser hashes too), case-insensitively on the tag, and scripts with src= are skipped. The
// expected values were computed independently with
// `printf '<body>' | openssl dgst -sha256 -binary | base64`.
func TestInlineScriptHashes_OnlySrclessScripts(t *testing.T) {
	html := []byte("<html><head>\n" +
		"<script>console.log(1)</script>\n" +
		"<script type=\"module\" src=\"/assets/index-abc.js\"></script>\n" +
		"<SCRIPT type=\"text/javascript\">\n  alert(2)\n</SCRIPT>\n" +
		"</head><body></body></html>")

	got := httpserver.InlineScriptHashes(html)
	want := []string{
		"'sha256-CihokcEcBW4atb/CW/XWsvWwbTjqwQlE9nj9ii5ww5M='", // console.log(1)
		"'sha256-wgjd6XWUJfa5+rUf4ibLSBtQ2O4Mj1Bb0Fp7DU7zxRk='", // "\n  alert(2)\n"
	}
	if len(got) != len(want) {
		t.Fatalf("InlineScriptHashes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hash[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestWebIndexInlineScriptsAreAllHashed is the guard the audit asked for: if web/index.html grows
// an inline <script> the extraction regex does not catch, this fails — the browser would block
// that script under the served CSP. The independent count below deliberately does NOT reuse the
// production regex.
func TestWebIndexInlineScriptsAreAllHashed(t *testing.T) {
	html, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatalf("reading web/index.html: %v", err)
	}

	inline := 0
	for _, after := range strings.Split(strings.ToLower(string(html)), "<script")[1:] {
		openTag := after[:strings.Index(after, ">")]
		if !strings.Contains(openTag, "src=") {
			inline++
		}
	}
	if inline < 2 {
		t.Fatalf("counted %d inline scripts in web/index.html, expected at least the theme and locale bootstraps", inline)
	}

	hashes := httpserver.InlineScriptHashes(html)
	if len(hashes) != inline {
		t.Fatalf("InlineScriptHashes found %d scripts, the independent count found %d — an inline script is not being hashed", len(hashes), inline)
	}

	policy := httpserver.BuildSecurityPolicy("https://whenweall.example", html)
	scriptSrc := directive(t, policy.CSP, "script-src")
	for _, h := range hashes {
		if !strings.Contains(scriptSrc, h) {
			t.Errorf("script-src %q is missing %s", scriptSrc, h)
		}
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Errorf("script-src must rely on hashes, not 'unsafe-inline': %q", scriptSrc)
	}
	if !strings.Contains(scriptSrc, "https://challenges.cloudflare.com") {
		t.Errorf("script-src must allow Turnstile: %q", scriptSrc)
	}
}

func TestBuildSecurityPolicy_ConnectFrameAndHSTS(t *testing.T) {
	https := httpserver.BuildSecurityPolicy("https://whenweall.example", nil)
	if !https.HSTS {
		t.Error("HSTS should be on for an https APP_URL")
	}
	if got := directive(t, https.CSP, "connect-src"); got != "connect-src 'self' https://challenges.cloudflare.com wss://whenweall.example" {
		t.Errorf("connect-src = %q", got)
	}
	if got := directive(t, https.CSP, "frame-src"); got != "frame-src https://challenges.cloudflare.com" {
		t.Errorf("frame-src = %q", got)
	}
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'self'", "form-action 'self'", "object-src 'none'", "style-src 'self' 'unsafe-inline'"} {
		if !strings.Contains(https.CSP, want) {
			t.Errorf("CSP missing %q: %s", want, https.CSP)
		}
	}

	plain := httpserver.BuildSecurityPolicy("http://localhost:3000", nil)
	if plain.HSTS {
		t.Error("HSTS must be off for an http APP_URL — a browser would otherwise refuse the plain-http dev origin for a year")
	}
	if got := directive(t, plain.CSP, "connect-src"); got != "connect-src 'self' https://challenges.cloudflare.com ws://localhost:3000" {
		t.Errorf("connect-src = %q", got)
	}
}

// TestSecurityHeadersOnEveryResponse goes through the real Server.Handler(): the SPA shell, the
// health check and an API 404 all carry the full header set, and the served CSP covers whatever
// inline scripts the embedded dist/index.html actually has (the committed placeholder has none;
// a binary built with the real SPA has two — same test, real coverage in e2e).
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig() // APP_URL http://localhost:3000
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	h := srv.Handler()

	embeddedHashes := httpserver.InlineScriptHashes(httpserver.EmbeddedIndexHTML())

	for _, path := range []string{"/", "/healthz", "/api/v1/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s: no Content-Security-Policy header", path)
		}
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP missing %q: %s", path, want, csp)
			}
		}
		for _, hash := range embeddedHashes {
			if !strings.Contains(csp, hash) {
				t.Errorf("%s: served CSP does not cover embedded inline script %s", path, hash)
			}
		}
		if got := rec.Header().Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
			t.Errorf("%s: Permissions-Policy = %q", path, got)
		}
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("%s: HSTS %q emitted for an http APP_URL", path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: nosniff = %q", path, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
	}
}

func TestSecurityHeadersHSTSWhenPolicySaysSo(t *testing.T) {
	h := httpserver.SecurityHeaders(httpserver.SecurityPolicy{CSP: "default-src 'self'", HSTS: true})(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("HSTS = %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// directive returns the one CSP directive named name (e.g. "script-src 'self' ...") from csp.
func directive(t *testing.T, csp, name string) string {
	t.Helper()
	for _, d := range strings.Split(csp, "; ") {
		if strings.HasPrefix(d, name+" ") {
			return d
		}
	}
	t.Fatalf("CSP %q has no %s directive", csp, name)
	return ""
}
