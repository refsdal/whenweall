package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/refsdal/whenweall/internal/config"
)

// postSignup is postJSON with extra request decoration (cookies / headers) — the signup hook reads
// the locale off the request, not only off the body.
func postSignup(t *testing.T, ts *testService, body map[string]any, decorate func(*http.Request)) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.url("/api/v1/auth/signup/credential"), strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if decorate != nil {
		decorate(req)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatalf("POST signup: %v", err)
	}
	return resp
}

func TestSignupStoresNameAndLocaleFromBody(t *testing.T) {
	ts := newTestService(t)
	email := "named@example.com"

	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": "Ada Lovelace", "locale": "nb",
	}, nil), "signup")

	p, err := ts.svc.GetProfile(context.Background(), lookupUserIDString(t, ts, email))
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want Ada Lovelace", p.Name)
	}
	if p.Locale != "nb" {
		t.Errorf("Locale = %q, want nb", p.Locale)
	}

	// The verification mail enqueued by that same signup greets by name, in the user's locale.
	msg, ok := ts.mail.find("verify_email")
	if !ok {
		t.Fatal("no verify_email mail captured")
	}
	if got, _ := msg.Data["Name"].(string); got != "Ada Lovelace" {
		t.Errorf("verify_email Data.Name = %q, want Ada Lovelace", got)
	}
	if got, _ := msg.Data["Locale"].(string); got != "nb" {
		t.Errorf("verify_email Data.Locale = %q, want nb", got)
	}
}

func TestSignupLocaleFallsBackToCookieThenAcceptLanguage(t *testing.T) {
	ts := newTestService(t)

	cases := []struct {
		name     string
		email    string
		decorate func(*http.Request)
		want     string
	}{
		{"cookie", "cookie-nb@example.com", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "whenweall_locale", Value: "nb"})
		}, "nb"},
		{"accept-language", "header-nb@example.com", func(r *http.Request) {
			r.Header.Set("Accept-Language", "nb-NO,nb;q=0.9,en;q=0.8")
		}, "nb"},
		{"unsupported everywhere", "de@example.com", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "whenweall_locale", Value: "de"})
			r.Header.Set("Accept-Language", "de-DE,fr;q=0.5")
		}, "en"},
		{"nothing", "plain@example.com", nil, "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireStatus2xx(t, postSignup(t, ts, map[string]any{"email": tc.email, "password": signupPassword}, tc.decorate), "signup")
			if got := ts.svc.LocaleFor(context.Background(), lookupUserIDString(t, ts, tc.email)); got != tc.want {
				t.Errorf("LocaleFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSignupIgnoresInvalidNameButKeepsLocale(t *testing.T) {
	ts := newTestService(t)
	email := "longname@example.com"

	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": strings.Repeat("x", 200), "locale": "nb",
	}, nil), "signup")

	p, err := ts.svc.GetProfile(context.Background(), lookupUserIDString(t, ts, email))
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Name != "longname" {
		t.Errorf("Name = %q, want the email local part (invalid name dropped)", p.Name)
	}
	if p.Locale != "nb" {
		t.Errorf("Locale = %q, want nb (a bad name must not cost the locale)", p.Locale)
	}
}

func TestSessionResponseCarriesProfileFields(t *testing.T) {
	ts := newTestService(t)
	email := "session-fields@example.com"

	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": "Grace Hopper", "locale": "nb",
	}, nil), "signup")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")

	me := decodeJSON(t, ts.get(t, "/api/v1/auth/me"))
	user, _ := me["user"].(map[string]any)
	if got, _ := user["name"].(string); got != "Grace Hopper" {
		t.Errorf("user.name = %q, want Grace Hopper", got)
	}
	if got, _ := user["locale"].(string); got != "nb" {
		t.Errorf("user.locale = %q, want nb", got)
	}
	if verified, ok := user["emailVerified"].(bool); !ok || verified {
		t.Errorf("user.emailVerified = %#v, want false", user["emailVerified"])
	}
	if hasPassword, _ := user["hasPassword"].(bool); !hasPassword {
		t.Errorf("user.hasPassword = %#v, want true for a credential signup", user["hasPassword"])
	}
	if _, leaked := user["password"]; leaked {
		t.Error("user.password must never be in the payload")
	}
}

func TestPasswordResetMailUsesProfileNameAndLocale(t *testing.T) {
	ts := newTestService(t)
	email := "reset-nb@example.com"
	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": "Kari Nordmann", "locale": "nb",
	}, nil), "signup")

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/passwords/request-reset", map[string]any{"email": email}), "request-reset")
	msg, ok := ts.mail.find("reset_password")
	if !ok {
		t.Fatal("no reset_password mail captured")
	}
	if got, _ := msg.Data["Name"].(string); got != "Kari Nordmann" {
		t.Errorf("Data.Name = %q, want Kari Nordmann", got)
	}
	if got, _ := msg.Data["Locale"].(string); got != "nb" {
		t.Errorf("Data.Locale = %q, want nb", got)
	}
}

func TestRequestLocaleParsing(t *testing.T) {
	mk := func(cookie, accept string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "http://x/signup", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "whenweall_locale", Value: cookie})
		}
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		return r
	}
	cases := []struct {
		body   any
		cookie string
		accept string
		want   string
	}{
		{"nb", "", "", "nb"},
		{"NB", "", "", "en"}, // exact match only
		{nil, "nb", "en", "nb"},
		{nil, "", "en-GB,en;q=0.9,nb;q=0.8", "en"},
		{nil, "", "nb-NO", "nb"},
		{nil, "", " fr , nb ;q=0.2", "nb"},
		{nil, "", "*", "en"},
		{42, "", "", "en"},
	}
	for _, tc := range cases {
		if got := requestLocale(mk(tc.cookie, tc.accept), tc.body); got != tc.want {
			t.Errorf("requestLocale(body=%v cookie=%q accept=%q) = %q, want %q", tc.body, tc.cookie, tc.accept, got, tc.want)
		}
	}
}

// lookupUserIDString is lookupUserID (personal_org_test.go) in the seam's string-id shape.
func lookupUserIDString(t *testing.T, ts *testService, email string) string {
	t.Helper()
	return fmt.Sprint(lookupUserID(t, ts, email))
}

// TestConcurrentSameEmailSignupsPersistOnlyWinnersProfile guards the exact race a code review
// found in the first version of this mechanism: pendingSignupProfiles was keyed only by e-mail
// with last-write-wins semantics, so two concurrent signup attempts for the same address could
// interleave as - A's Before hook stores A's name/locale, B's Before hook overwrites it with B's,
// A's handler wins the race and creates the account, A's After hook reads (B's now-overwritten)
// entry and persists it onto A's account. The eventual fix must make the persisted profile depend
// only on whichever attempt actually created the account, never on a same-address attempt that
// raced with it and lost. Run as many independent trials (each its own e-mail address) as it
// takes to make a surviving race very likely to show up in one run without -race (unavailable in
// this environment): assertions are on the persisted rows, never on timing.
//
// EnableTestRoutes: true disables Limen's own credential-password rate limiter (5 requests/10s
// per IP by default — see httpConfigOptions' own doc comment), which 40 rapid requests from one
// test would otherwise blow through long before the race itself gets a chance to matter.
func TestConcurrentSameEmailSignupsPersistOnlyWinnersProfile(t *testing.T) {
	ts := newTestServiceWithConfig(t, &config.Config{
		AppURL:           "http://app.example",
		LimenSecret:      make([]byte, 32),
		EnableTestRoutes: true,
	})

	const trials = 20
	for i := 0; i < trials; i++ {
		email := fmt.Sprintf("race-%d@example.com", i)
		submissions := [2]struct{ name, locale string }{
			{fmt.Sprintf("Attempt A %d", i), "en"},
			{fmt.Sprintf("Attempt B %d", i), "nb"},
		}
		type result struct {
			name, locale string
			status       int
			err          error
		}
		var results [2]result

		var wg sync.WaitGroup
		for j, sub := range submissions {
			wg.Add(1)
			go func(j int, sub struct{ name, locale string }) {
				defer wg.Done()
				body, _ := json.Marshal(map[string]any{
					"email": email, "password": signupPassword, "name": sub.name, "locale": sub.locale,
				})
				resp, err := ts.client.Post(ts.url("/api/v1/auth/signup/credential"), "application/json", bytes.NewReader(body))
				results[j] = result{name: sub.name, locale: sub.locale, err: err}
				if err == nil {
					results[j].status = resp.StatusCode
					_ = resp.Body.Close()
				}
			}(j, sub)
		}
		wg.Wait()

		var winner *result
		successCount := 0
		for j := range results {
			r := &results[j]
			if r.err != nil {
				t.Fatalf("trial %d: signup request %d: %v", i, j, r.err)
			}
			if r.status/100 == 2 {
				successCount++
				winner = r
			}
		}
		if successCount != 1 {
			t.Fatalf("trial %d: want exactly one successful concurrent signup for %s, got %d (statuses %d, %d)",
				i, email, successCount, results[0].status, results[1].status)
		}

		userID := lookupUserIDString(t, ts, email)
		p, err := ts.svc.GetProfile(context.Background(), userID)
		if err != nil {
			t.Fatalf("trial %d: GetProfile: %v", i, err)
		}
		if p.Name != winner.name || p.Locale != winner.locale {
			t.Errorf("trial %d: persisted profile = %q/%q, want the winning attempt's own %q/%q (never the other attempt's data)",
				i, p.Name, p.Locale, winner.name, winner.locale)
		}
	}
}

// TestPendingSignupCacheClearedAfterRejectedSignup covers the mechanism's own lifecycle: whatever
// beforeSignup stashes for a given address must not outlive the request, whether that request
// went on to create an account or was rejected outright (duplicate e-mail; a password too weak to
// pass credential-password's own validation, which never even reaches user creation). A leak here
// would mean a later, unrelated signup attempt for the same address could pick up stale data.
func TestPendingSignupCacheClearedAfterRejectedSignup(t *testing.T) {
	ts := newTestService(t)

	assertNoPendingEntry := func(t *testing.T, email string) {
		t.Helper()
		if _, ok := ts.svc.pendingSignupProfiles.Load(email); ok {
			t.Errorf("pendingSignupProfiles still holds an entry for %q", email)
		}
	}

	// A successful signup: the winning request's own entry must be gone once it completes.
	okEmail := "cache-ok@example.com"
	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": okEmail, "password": signupPassword, "name": "Cache Ok",
	}, nil), "signup")
	assertNoPendingEntry(t, okEmail)

	// Rejected: duplicate e-mail (the account from above already exists).
	dupResp := postSignup(t, ts, map[string]any{
		"email": okEmail, "password": signupPassword, "name": "Duplicate Attempt",
	}, nil)
	_ = dupResp.Body.Close()
	if dupResp.StatusCode/100 == 2 {
		t.Fatalf("duplicate-email signup unexpectedly succeeded")
	}
	assertNoPendingEntry(t, okEmail)

	// Rejected: password fails credential-password's own strength validation, so no user is ever
	// created — the Before hook still ran (it only needs the body, not a successful signup).
	weakEmail := "cache-weak@example.com"
	weakResp := postSignup(t, ts, map[string]any{
		"email": weakEmail, "password": "short", "name": "Weak Password",
	}, nil)
	_ = weakResp.Body.Close()
	if weakResp.StatusCode/100 == 2 {
		t.Fatalf("weak-password signup unexpectedly succeeded")
	}
	assertNoPendingEntry(t, weakEmail)
}
