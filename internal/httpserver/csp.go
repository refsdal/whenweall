package httpserver

// Content-Security-Policy for the embedded SPA, computed ONCE per process (New) from the exact
// index.html bytes spaHandler serves. The old TanStack Start server (main:src/start.ts) shipped a
// CSP with script-src 'unsafe-inline'; this one hashes the inline scripts instead. There are
// exactly two (web/index.html: the theme bootstrap and the <html lang> cookie bootstrap), both
// copied verbatim by `vite build`, so a sha256 per script is cheaper and stricter than a nonce:
// no per-request rewrite of index.html, and an XSS payload injected as an inline <script> is
// blocked because its hash is not on the list. csp_test.go's TestWebIndexInlineScriptsAreAllHashed
// fails the build the day web/index.html grows a script this file's extraction does not see.

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
)

// turnstileOrigin is Cloudflare Turnstile's one origin: the widget script
// (challenges.cloudflare.com/turnstile/v0/api.js), its iframe, and its own fetch/XHR traffic all
// come from here (web/src/components/auth/TurnstileField.tsx via @marsidev/react-turnstile), so it
// is allowed in script-src, frame-src and connect-src. Google OAuth is a top-level redirect and
// needs nothing; Google Calendar calls are server-side only.
const turnstileOrigin = "https://challenges.cloudflare.com"

// SecurityPolicy is SecurityHeaders' computed-once input: the full Content-Security-Policy header
// value and whether Strict-Transport-Security applies (only when APP_URL is https — HSTS on a
// plain-http dev origin would make the browser refuse http://localhost for a year).
type SecurityPolicy struct {
	CSP  string
	HSTS bool
}

var (
	// inlineScriptRE matches every <script ...>...</script> element (case-insensitive, dot
	// matches newlines) and captures the attribute text and the body separately.
	inlineScriptRE = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	// srcAttrRE recognizes an external script by its src attribute — those are covered by
	// script-src 'self', not by a hash.
	srcAttrRE = regexp.MustCompile(`(?i)\bsrc\s*=`)
)

// InlineScriptHashes returns one CSP source expression — 'sha256-<base64>' — per src-less <script>
// element in html, in document order. The hash covers the element's exact text content, leading
// and trailing whitespace included, because that is precisely what a browser hashes when it
// evaluates the policy.
func InlineScriptHashes(html []byte) []string {
	matches := inlineScriptRE.FindAllSubmatch(html, -1)
	hashes := make([]string, 0, len(matches))
	for _, m := range matches {
		if srcAttrRE.Match(m[1]) {
			continue
		}
		sum := sha256.Sum256(m[2])
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return hashes
}

// EmbeddedIndexHTML returns the SPA shell exactly as embedded into this binary (dist/index.html) —
// the same bytes spaHandler serves, so hashes computed from it match what the browser receives.
// nil if the embed is somehow missing (spaHandler panics on that case anyway).
func EmbeddedIndexHTML() []byte {
	b, err := distFS.ReadFile("dist/index.html")
	if err != nil {
		return nil
	}
	return b
}

// BuildSecurityPolicy derives the policy for appURL and the given index.html bytes:
//
//   - script-src: 'self', one sha256 per inline script, and Turnstile's origin.
//   - style-src 'unsafe-inline': React style={{}} props and motion/react's animated inline styles
//     need it; scripts do not get the same latitude.
//   - connect-src: 'self' (which covers same-origin ws(s):// in every current browser), Turnstile,
//     plus the app's own ws:// or wss:// origin spelled out for older WebKit builds that did not
//     treat 'self' as covering the WebSocket scheme.
//   - frame-ancestors 'none' (the header-level form of the X-Frame-Options: DENY we already send),
//     base-uri/form-action 'self', object-src 'none'.
func BuildSecurityPolicy(appURL string, indexHTML []byte) SecurityPolicy {
	scriptSrc := append([]string{"'self'"}, InlineScriptHashes(indexHTML)...)
	scriptSrc = append(scriptSrc, turnstileOrigin)

	connectSrc := []string{"'self'", turnstileOrigin}
	hsts := false
	if u, err := url.Parse(appURL); err == nil && u.Host != "" {
		wsScheme := "ws"
		if u.Scheme == "https" {
			wsScheme = "wss"
			hsts = true
		}
		connectSrc = append(connectSrc, wsScheme+"://"+u.Host)
	}

	directives := []string{
		"default-src 'self'",
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connectSrc, " "),
		"frame-src " + turnstileOrigin,
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}
	return SecurityPolicy{CSP: strings.Join(directives, "; "), HSTS: hsts}
}
