package httpserver

// Crawler policy for the SPA's routes, in one place: the X-Robots-Tag header a private path
// answers with, and the robots.txt that lists which of them may not even be fetched. Both are
// generated from noindexRoutes below, so the two can never drift apart.
//
// Why a response header rather than a `<meta name="robots">` in the page: this is a pure SPA.
// Every one of these paths is served the SAME static index.html (spa.go), and the per-route meta
// tags in web/src/routes/**/*.tsx are injected by React AFTER load — a crawler that does not run
// JavaScript never sees them, and one that does sees them only if it renders far enough. The
// header is on the document response itself, so it applies either way, and it needs no
// cooperation from the frontend build. (`/admin` and `/booking/$id` still carry their own meta
// tag; it agrees with the header, so there is nothing to reconcile.)
//
// robots.txt and the header do different jobs and both are needed. robots.txt asks a well-behaved
// crawler not to FETCH the page — but a URL it never fetched can still be indexed URL-only from
// an inbound link, and a disallowed page's X-Robots-Tag is never read, because the crawler never
// requested it. The header is what actually removes a page that was fetched from the index.

import (
	"net/http"
	"strings"
)

// privateRoute is one SPA path that must not be indexed.
type privateRoute struct {
	// path is the route, with no trailing slash.
	path string
	// subtree is true when there is no page at path itself and only its children are real
	// routes (/p/<id>, never a bare /p). It decides both how a request is matched and how the
	// robots.txt line is written.
	subtree bool
	// crawlable is true for the pages people actually share — a poll, a public booking page.
	// They are noindex like everything else here, but robots.txt must NOT disallow them:
	//
	//   - Slackbot, Twitterbot and Facebook's scraper all honour robots.txt, so a Disallow turns
	//     every poll link pasted into a group chat back into a bare URL. That link is the
	//     product's entire distribution model.
	//   - A crawler forbidden to FETCH a page never reads its X-Robots-Tag. Google documents
	//     this trap directly: a disallowed URL can still be indexed URL-only from an inbound
	//     link, and the noindex that would have removed it is never seen. Allowing the fetch is
	//     what makes the noindex effective.
	//
	// False for everything whose URL is a credential or that only its owner should ever load:
	// there is no preview to preserve, and no reason for a bot to fetch it at all.
	crawlable bool
}

// matches reports whether urlPath is this route or, where applicable, below it. The comparison is
// per SEGMENT, never a bare string prefix: "/settings" must not match a future "/settings-guide"
// marketing page, and "/new" must not match "/newsletter".
func (r privateRoute) matches(urlPath string) bool {
	if !r.subtree && urlPath == r.path {
		return true
	}
	return strings.HasPrefix(urlPath, r.path+"/")
}

// disallow is this route's robots.txt line value. A subtree route is written with its trailing
// slash ("/p/") because there is no page at "/p" to speak of; a page route is written bare so it
// covers the page and anything under it.
func (r privateRoute) disallow() string {
	if r.subtree {
		return r.path + "/"
	}
	return r.path
}

// noindexRoutes is every SPA path that renders someone's personal data or carries a secret in its
// URL. Two kinds of thing are on this list:
//
//   - Pages showing data that is scoped to "whoever has the link": a poll or sign-up sheet and
//     its participants' names, votes and comments; a booking page; a booking's own manage view;
//     and the signed-in surfaces (dashboard, settings, admin, the creator wizard).
//   - Pages whose URL IS a credential: an invitation link, a password-reset link, an
//     email-verification link. An indexed one of those is a handed-over account.
//
// Deliberately NOT here: /, /privacy, /terms, /login and /signup. Those are the marketing and
// entry surface and are meant to be found.
var noindexRoutes = []privateRoute{
	// Shared links: noindex, but crawlable so their previews still unfurl.
	{path: "/p", subtree: true, crawlable: true},    // polls and sign-up sheets, incl. /p/<id>/edit
	{path: "/book", subtree: true, crawlable: true}, // public booking pages, /book/<handle>/<slug>
	{path: "/booking", subtree: true},               // a single booking's manage view
	{path: "/bookings"},                             // the organiser's own booking pages list
	{path: "/dashboard"},                            //
	{path: "/settings"},                             //
	{path: "/admin"},                                // also covered by its own route meta tag
	{path: "/new"},                                  // the creator wizard
	{path: "/accept-invitation", subtree: true},     // URL carries the invitation token
	{path: "/reset-password"},                       // URL carries the reset token
	{path: "/verify-email"},                         // URL carries the verification token
}

// robotsOnlyDisallow are paths listed in robots.txt but not header-tagged: /api/ answers JSON to
// programs, never HTML to a reader, so there is nothing to deindex — but there is also no reason
// for a crawler to spend requests on it. Tagging every API response instead would put a header
// nobody reads on the hottest path in the process.
var robotsOnlyDisallow = []string{"/api/"}

// robotsTxt is the served body, built once at package init from the lists above.
var robotsTxt = buildRobotsTxt()

func buildRobotsTxt() string {
	var b strings.Builder
	b.WriteString("# Every path below renders data scoped to whoever holds the link, or carries a\n")
	b.WriteString("# token in its URL. See internal/httpserver/robots.go.\n")
	b.WriteString("User-agent: *\n")
	for _, r := range noindexRoutes {
		if r.crawlable {
			continue
		}
		b.WriteString("Disallow: " + r.disallow() + "\n")
	}
	for _, p := range robotsOnlyDisallow {
		b.WriteString("Disallow: " + p + "\n")
	}
	return b.String()
}

func handleRobotsTxt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// Long-lived by design: this file changes only when a route is added, and a crawler
	// re-reading it a day late costs nothing.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(robotsTxt))
}

// Noindex sets X-Robots-Tag: noindex on every request for a path in noindexRoutes. Applied
// globally in Handler so it covers the SPA fallback, which is what actually serves these paths.
func Noindex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, route := range noindexRoutes {
			if route.matches(r.URL.Path) {
				w.Header().Set("X-Robots-Tag", "noindex")
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}
