package mailer

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// LocaleCookieName is the cookie paraglide persists the SPA's locale switch in
// (web/vite.config.ts's `cookieName`). Every Go call site that needs to read a request's
// browser-side locale choice — server-rendered text for the roster CSV, the signup mail-locale
// hint — reads this SAME cookie, so what the user sees in the app, what they download, and what
// their transactional mail renders in all agree. There used to be two independent copies of this
// constant (internal/httpserver and internal/auth), each paired with its own slightly different
// resolution logic; see RequestLocale's doc comment for why that was a bug, not just duplication.
const LocaleCookieName = "whenweall_locale"

// RequestLocale resolves the locale a request wants server-rendered text in from its browser
// signals: the SPA's locale cookie when it names a supported locale, else the best
// Accept-Language match (RFC 9110 q-ordered, matched case-insensitively, base-language so
// "nb-NO" -> "nb"), else SupportedLocales[0]. Only ever returns a member of SupportedLocales.
//
// This is the ONE implementation of that resolution — internal/httpserver's roster-CSV route and
// internal/auth's signup hook (which additionally consults the signup request's own JSON body
// locale first — see auth.requestLocale) both call this rather than keeping their own copies.
// They used to: httpserver's honoured Accept-Language's q-values and matched case-insensitively,
// auth's took header order and matched case-sensitively, so
// "Accept-Language: en;q=0.5, nb;q=0.9" resolved to "nb" on one path and "en" on the other for the
// exact same request. This implementation keeps the more correct (q-ordering, case-insensitive)
// semantics from the httpserver side.
func RequestLocale(r *http.Request) string {
	if r != nil {
		if c, err := r.Cookie(LocaleCookieName); err == nil {
			if l, ok := matchLocale(c.Value); ok {
				return l
			}
		}
		for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
			if l, ok := matchLocale(tag); ok {
				return l
			}
		}
	}
	if len(SupportedLocales) > 0 {
		return SupportedLocales[0]
	}
	return "en"
}

// matchLocale maps a language tag onto SupportedLocales: exact (case-insensitive) or by base
// language ("nb-NO" -> "nb").
func matchLocale(tag string) (string, bool) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return "", false
	}
	base := tag
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		base = tag[:i]
	}
	for _, s := range SupportedLocales {
		if s == tag || s == base {
			return s, true
		}
	}
	return "", false
}

// parseAcceptLanguage returns the header's language tags ordered by descending q (header order
// for ties); "*" and q=0 entries are dropped.
func parseAcceptLanguage(header string) []string {
	type entry struct {
		tag string
		q   float64
	}
	var entries []entry
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				if v, err := strconv.ParseFloat(p[2:], 64); err == nil {
					q = v
				}
			}
		}
		if q <= 0 {
			continue
		}
		entries = append(entries, entry{tag: tag, q: q})
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].q > entries[b].q })
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.tag
	}
	return out
}
