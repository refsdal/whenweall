package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// signUp creates a credential user through Limen's own route and returns their stringified id —
// the shape every seam method takes. No session is needed for the profile methods.
func signUp(t *testing.T, ts *testService, email string) string {
	t.Helper()
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	return fmt.Sprint(lookupUserID(t, ts, email))
}

func TestGetProfileDefaults(t *testing.T) {
	ts := newTestService(t)
	userID := signUp(t, ts, "ada.lovelace@example.com")

	p, err := ts.svc.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.UserID != userID {
		t.Errorf("UserID = %q, want %q", p.UserID, userID)
	}
	if p.Name != "ada.lovelace" {
		t.Errorf("Name = %q, want the email local part %q", p.Name, "ada.lovelace")
	}
	if p.Locale != "en" {
		t.Errorf("Locale = %q, want %q (no preferences row yet)", p.Locale, "en")
	}
	if p.EmailVerified {
		t.Errorf("EmailVerified = true for a fresh signup, want false")
	}
}

func TestSetProfileRoundTrip(t *testing.T) {
	ts := newTestService(t)
	userID := signUp(t, ts, "profile@example.com")
	ctx := context.Background()

	name := "  Ada   Lovelace "
	locale := "nb"
	if err := ts.svc.SetProfile(ctx, userID, &name, &locale); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	p, err := ts.svc.GetProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want %q (whitespace collapsed)", p.Name, "Ada Lovelace")
	}
	if p.Locale != "nb" {
		t.Errorf("Locale = %q, want nb", p.Locale)
	}
	if got := ts.svc.LocaleFor(ctx, userID); got != "nb" {
		t.Errorf("LocaleFor = %q, want nb", got)
	}

	// nil means unchanged: a locale-only update must not blank the name.
	en := "en"
	if err := ts.svc.SetProfile(ctx, userID, nil, &en); err != nil {
		t.Fatalf("SetProfile(locale only): %v", err)
	}
	p, _ = ts.svc.GetProfile(ctx, userID)
	if p.Name != "Ada Lovelace" || p.Locale != "en" {
		t.Errorf("after locale-only update: Name=%q Locale=%q, want Ada Lovelace/en", p.Name, p.Locale)
	}

	// The stored split is first_name/last_name, so admin's composeUserName sees the same name.
	var first, last string
	if err := ts.svc.db.QueryRowContext(ctx, "SELECT first_name, last_name FROM users WHERE id = $1", lookupUserID(t, ts, "profile@example.com")).Scan(&first, &last); err != nil {
		t.Fatalf("reading first/last name: %v", err)
	}
	if first != "Ada" || last != "Lovelace" {
		t.Errorf("first_name/last_name = %q/%q, want Ada/Lovelace", first, last)
	}
}

func TestSetProfileValidation(t *testing.T) {
	ts := newTestService(t)
	userID := signUp(t, ts, "validate@example.com")
	ctx := context.Background()

	cases := []struct {
		name      string
		nameArg   *string
		localeArg *string
		wantField string
	}{
		{"blank name", ptr("   "), nil, "name"},
		{"name over 80 runes", ptr(strings.Repeat("å", 81)), nil, "name"},
		{"unsupported locale", nil, ptr("de"), "locale"},
		{"empty locale", nil, ptr(""), "locale"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ts.svc.SetProfile(ctx, userID, tc.nameArg, tc.localeArg)
			var verr *ProfileValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("SetProfile error = %v, want *ProfileValidationError", err)
			}
			if verr.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", verr.Field, tc.wantField)
			}
		})
	}

	// Exactly 80 runes is fine.
	ok := strings.Repeat("å", 80)
	if err := ts.svc.SetProfile(ctx, userID, &ok, nil); err != nil {
		t.Errorf("SetProfile(80 runes) = %v, want nil", err)
	}
}

func TestProfileUnknownUser(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	if _, err := ts.svc.GetProfile(ctx, "999999"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("GetProfile(unknown) = %v, want ErrNoSuchUser", err)
	}
	if _, err := ts.svc.GetProfile(ctx, "not-a-number"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("GetProfile(garbage) = %v, want ErrNoSuchUser", err)
	}
	name := "Nobody"
	if err := ts.svc.SetProfile(ctx, "999999", &name, nil); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SetProfile(unknown, name) = %v, want ErrNoSuchUser", err)
	}
	nb := "nb"
	if err := ts.svc.SetProfile(ctx, "999999", nil, &nb); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SetProfile(unknown, locale) = %v, want ErrNoSuchUser", err)
	}
	if got := ts.svc.LocaleFor(ctx, "999999"); got != "en" {
		t.Errorf("LocaleFor(unknown) = %q, want en", got)
	}
	if err := ts.svc.MarkEmailVerified(ctx, "nobody@example.com"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("MarkEmailVerified(unknown) = %v, want ErrNoSuchUser", err)
	}
}

func TestMarkEmailVerified(t *testing.T) {
	ts := newTestService(t)
	email := "Verify.Me@Example.com"
	userID := signUp(t, ts, email)
	ctx := context.Background()

	// Accepts the un-normalized spelling the caller has, same as Limen's own lookups.
	if err := ts.svc.MarkEmailVerified(ctx, email); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	p, err := ts.svc.GetProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if !p.EmailVerified {
		t.Error("EmailVerified = false after MarkEmailVerified")
	}
	// Idempotent.
	if err := ts.svc.MarkEmailVerified(ctx, email); err != nil {
		t.Errorf("second MarkEmailVerified: %v", err)
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct{ first, last, email, want string }{
		{"Ada", "Lovelace", "ada@example.com", "Ada Lovelace"},
		{"Ada", "", "ada@example.com", "Ada"},
		{"", "Lovelace", "ada@example.com", "Lovelace"},
		{"", "", "ada.l@example.com", "ada.l"},
		{" ", " ", "weird", "weird"},
	}
	for _, tc := range cases {
		if got := DisplayName(tc.first, tc.last, tc.email); got != tc.want {
			t.Errorf("DisplayName(%q,%q,%q) = %q, want %q", tc.first, tc.last, tc.email, got, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
