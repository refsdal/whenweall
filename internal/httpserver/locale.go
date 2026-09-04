package httpserver

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// LocaleCookieName is the cookie paraglide persists the SPA's locale switch in
// (web/vite.config.ts's `cookieName`). Server-rendered text for a browser request (the roster
// CSV's slot labels) reads it first, so what the user sees in the app and what they download
// agree.
const LocaleCookieName = "whenweall_locale"

// RequestLocale resolves the locale a request wants server-rendered text in: the SPA's locale
// cookie when it names a supported locale, else the best Accept-Language match (RFC 9110
// q-ordered, base-language match so "nb-NO" → "nb"), else supported[0]. Only members of
// supported are ever returned — pass mailer.SupportedLocales.
func RequestLocale(r *http.Request, supported []string) string {
	if c, err := r.Cookie(LocaleCookieName); err == nil {
		if l, ok := matchLocale(c.Value, supported); ok {
			return l
		}
	}
	for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
		if l, ok := matchLocale(tag, supported); ok {
			return l
		}
	}
	if len(supported) > 0 {
		return supported[0]
	}
	return "en"
}

// matchLocale maps a language tag onto supported: exact (case-insensitive) or by base language.
func matchLocale(tag string, supported []string) (string, bool) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return "", false
	}
	base := tag
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		base = tag[:i]
	}
	for _, s := range supported {
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
