package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
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
