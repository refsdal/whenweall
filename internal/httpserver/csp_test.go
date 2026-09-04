package httpserver_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"

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
// an inline <script> the extraction regex does not catch — or, more subtly, extracts the WRONG
// text for one it does catch (e.g. a script body that happens to contain the literal substring
// "</script" inside a string or comment, which a naive non-greedy match could end on early or
// late relative to what a browser's HTML5 tokenizer would treat as the element's end) — this
// fails, because the browser would then reject that script's hash under the served CSP. The
// independent extraction below deliberately does NOT reuse the production regex: it drives
// golang.org/x/net/html's spec-compliant HTML5 tokenizer (the same "script data state" raw-text
// handling a real browser implements) over the file and hashes each script's raw text content
// exactly as the tokenizer delivers it, then requires the two hash LISTS to be byte-for-byte
// identical and in the same order — not merely equal in count, which a truncated or over-captured
// body could satisfy while still shipping a hash the browser computes differently. Verified by
// hand against a synthetic file with a "</script >" (space before '>') close tag: the production
// regex requires the literal "</script>" and so merges that script with the next one in the file
// (mismatched hash count, already the old guard's job) while golang.org/x/net/html correctly
// treats the whitespace-tolerant close as ending the element, exactly like a real browser — see
// the fix report for the two extractions' outputs. One residual gap this guard shares with the
// production regex: neither implements the WHATWG "script data escaped state" (a literal `<!--`
// inside the script body suppresses a following `</script>` from closing it until a matching
// `-->`); closing this would need a full tokenizer state machine rather than any regex or the
// simplified golang.org/x/net/html tokenizer used here. Left as-is: triggering it requires the
// developer to hand-write `<!--` into their own inline script, not something an attacker without
// write access to web/index.html could exploit — and if they had that access, hashing wouldn't be
// the control holding them back.
func TestWebIndexInlineScriptsAreAllHashed(t *testing.T) {
	htmlBytes, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatalf("reading web/index.html: %v", err)
	}

	independent := independentInlineScriptHashes(t, htmlBytes)
	if len(independent) < 2 {
		t.Fatalf("independent tokenizer found %d inline scripts in web/index.html, expected at least the theme and locale bootstraps", len(independent))
	}

	hashes := httpserver.InlineScriptHashes(htmlBytes)
	if len(hashes) != len(independent) {
		t.Fatalf("InlineScriptHashes found %d scripts, the independent HTML5-tokenizer extraction found %d — an inline script is not being hashed (or is being mis-split)", len(hashes), len(independent))
	}
	for i := range hashes {
		if hashes[i] != independent[i] {
			t.Fatalf("hash[%d] = %s, independent extraction computed %s for the same script — the production regex captured different bytes than a real HTML5 parser would treat as this script's content", i, hashes[i], independent[i])
		}
	}

	policy := httpserver.BuildSecurityPolicy("https://whenweall.example", htmlBytes)
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

// independentInlineScriptHashes extracts one 'sha256-<base64>' entry per src-less <script>
// element in htmlSrc, in document order, using golang.org/x/net/html's HTML5 tokenizer — a
// completely different code path from httpserver.InlineScriptHashes's regex, so the two can only
// agree if both are actually looking at the same bytes for each script.
func independentInlineScriptHashes(t *testing.T, htmlSrc []byte) []string {
	t.Helper()
	z := html.NewTokenizer(bytes.NewReader(htmlSrc))
	var hashes []string
	inNoSrcScript := false
	var body bytes.Buffer

	for {
		switch z.Next() {
		case html.ErrorToken:
			return hashes
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" {
				continue
			}
			hasSrc := false
			for hasAttr {
				var key, val []byte
				key, val, hasAttr = z.TagAttr()
				_ = val
				if string(key) == "src" {
					hasSrc = true
				}
			}
			if !hasSrc {
				inNoSrcScript = true
				body.Reset()
			}
		case html.TextToken:
			if inNoSrcScript {
				body.Write(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "script" && inNoSrcScript {
				sum := sha256.Sum256(body.Bytes())
				hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
				inNoSrcScript = false
			}
		}
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
