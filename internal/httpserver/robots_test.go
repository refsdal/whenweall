package httpserver_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// A poll page renders participant names, their votes and their comments; the privacy policy
// promises those are "visible to anyone who has the link", which is a materially different
// promise from "visible to anyone searching your name". Nothing stops a crawler that finds a
// link — in a public Slack export, a forum post, a mailing list archive — from indexing the
// page, so every path that can render someone else's personal data (or carry a token in its
// URL) answers with X-Robots-Tag: noindex.
func TestNoindexHeaderOnPrivatePaths(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	for _, path := range []string{
		"/p/abc123456789",
		"/p/abc123456789/edit",
		"/book/anders/standup",
		"/booking/bk_123",
		"/bookings",
		"/dashboard",
		"/settings",
		"/admin",
		"/admin/users/u_1",
		"/new",
		"/accept-invitation/inv_123",
		"/reset-password",
		"/verify-email",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("GET %s: X-Robots-Tag = %q, want noindex", path, got)
		}
	}
}

// The marketing surface must stay indexable — the whole point of scoping the rule to prefixes
// rather than blanketing the SPA.
func TestNoindexHeaderNotOnPublicPaths(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	for _, path := range []string{"/", "/privacy", "/terms", "/login", "/signup", "/robots.txt"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if got := rec.Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("GET %s: X-Robots-Tag = %q, want no header", path, got)
		}
	}
}

// A prefix match must be on a path SEGMENT: /pricing is not under /p/, and /settingsomething is
// not /settings. Getting this wrong would quietly deindex a marketing page.
func TestNoindexPrefixesMatchWholeSegments(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	for _, path := range []string{"/pricing", "/newsletter", "/settingsomething", "/bookkeeping"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if got := rec.Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("GET %s: X-Robots-Tag = %q, want no header", path, got)
		}
	}
}

// robots.txt is served by the Go process, not shipped as a static file in the SPA build, so the
// disallow list and the X-Robots-Tag list are one list — they cannot drift apart.
func TestRobotsTxt(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	// Every Disallow line must belong to a wildcard group, so the group has to be declared
	// before the first of them (comment lines above it are fine).
	body := rec.Body.String()
	group := strings.Index(body, "User-agent: *\n")
	if group < 0 {
		t.Fatalf("no wildcard user-agent group:\n%s", body)
	}
	if first := strings.Index(body, "Disallow:"); first < group {
		t.Errorf("a Disallow line precedes the wildcard group:\n%s", body)
	}
	for _, want := range []string{
		"Disallow: /booking/", "Disallow: /bookings", "Disallow: /dashboard",
		"Disallow: /settings", "Disallow: /admin", "Disallow: /new",
		"Disallow: /accept-invitation/", "Disallow: /reset-password",
		"Disallow: /verify-email", "Disallow: /api/",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("robots.txt is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Disallow: /\n") {
		t.Error("robots.txt disallows the whole site; the landing page must stay crawlable")
	}
}

// The shared links — a poll and a public booking page — must stay CRAWLABLE even though they
// are noindex. Two reasons, and they point the same way:
//
//   - Slackbot, Twitterbot and Facebook's scraper all honour robots.txt. Disallowing /p/ would
//     turn every poll link pasted into a group chat back into a bare URL, which is the product's
//     entire distribution model.
//   - A page a crawler is forbidden to FETCH is a page whose X-Robots-Tag it never reads. Google
//     documents this exact trap: a disallowed URL can still be indexed URL-only from an inbound
//     link, and the noindex that would have removed it is never seen. Allow the fetch, deny the
//     index.
func TestRobotsTxtKeepsSharedLinksCrawlable(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/robots.txt", nil))
	body := rec.Body.String()

	for _, mustNot := range []string{"Disallow: /p/", "Disallow: /p\n", "Disallow: /book/", "Disallow: /book\n"} {
		if strings.Contains(body, mustNot) {
			t.Errorf("robots.txt contains %q — link previews for shared pages would stop working:\n%s", mustNot, body)
		}
	}
}
