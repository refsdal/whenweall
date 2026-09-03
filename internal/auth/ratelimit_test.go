package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestLimenRateLimiterIgnoresSpoofedForwardedFor: with TrustProxy off (the test config's zero
// value), six failed sign-ins from one RemoteAddr must trip credential-password's 5-per-10s rule
// even though every request claims a different X-Forwarded-For. Before this task Limen keyed on
// the raw header, so the six requests would have landed in six separate buckets.
func TestLimenRateLimiterIgnoresSpoofedForwardedFor(t *testing.T) {
	ts := newTestService(t)
	email := "spoof@example.com"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	}), "signup")

	var sawTooMany bool
	for i := 0; i < 6; i++ {
		body, _ := json.Marshal(map[string]any{"credential": email, "password": "wrong-password"})
		req, err := http.NewRequest(http.MethodPost, ts.url("/api/v1/auth/signin/credential"), strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		resp, err := ts.client.Do(req)
		if err != nil {
			t.Fatalf("signin %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			sawTooMany = true
		}
	}
	if !sawTooMany {
		t.Fatal("six failed sign-ins with distinct X-Forwarded-For never hit 429 — Limen's limiter is keying on the spoofable header")
	}
}

// TestMeIsNotRateLimitedByLimen: the SPA reads /me on every navigation; Limen's default 100/min
// global rule used to cover it. 105 reads in a row must all succeed.
func TestMeIsNotRateLimitedByLimen(t *testing.T) {
	ts := newTestService(t)
	ts.signUpVerifiedAndSignIn(t, "busy@example.com")
	for i := 0; i < 105; i++ {
		resp := ts.get(t, "/api/v1/auth/me")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /me #%d: status %d, want 200", i+1, resp.StatusCode)
		}
	}
	for i := 0; i < 105; i++ {
		resp := ts.get(t, "/api/v1/auth/organizations/active")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /organizations/active #%d: status %d, want 200", i+1, resp.StatusCode)
		}
	}
}
