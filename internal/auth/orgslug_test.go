package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"
)

func TestValidateOrgSlug(t *testing.T) {
	for _, ok := range []string{"abc", "team-ada", "a1-b2-c3", strings.Repeat("a", 30)} {
		if err := ValidateOrgSlug(ok); err != nil {
			t.Errorf("ValidateOrgSlug(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "ab", "Team", "team ada", "-team", "team-", "team_ada", "Foo Bar!!", strings.Repeat("a", 31)} {
		if err := ValidateOrgSlug(bad); !errors.Is(err, ErrInvalidOrgSlug) {
			t.Errorf("ValidateOrgSlug(%q) = %v, want ErrInvalidOrgSlug", bad, err)
		}
	}
}

func TestPersonalOrgSlugAlwaysSatisfiesTheHandleRule(t *testing.T) {
	cases := []struct{ email, userID string }{
		{"ada@example.com", "1"},
		{"a.very.long.email.local.part.that.goes.on@example.com", "1234567"},
		{"---@example.com", "42"},
		{"Ünïcödé.Náme@example.com", "9"},
		{"x@example.com", "99999999999"},
	}
	for _, tc := range cases {
		slug := personalOrgSlug(tc.email, tc.userID)
		if err := ValidateOrgSlug(slug); err != nil {
			t.Errorf("personalOrgSlug(%q, %q) = %q: %v", tc.email, tc.userID, slug, err)
		}
		if !strings.HasSuffix(slug, "-"+tc.userID) {
			t.Errorf("personalOrgSlug(%q, %q) = %q, want the -<userID> suffix", tc.email, tc.userID, slug)
		}
	}
}

func TestOrgRoutesRejectInvalidSlugs(t *testing.T) {
	ts := newTestService(t)
	email := "slug-owner@example.com"
	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)

	resp := ts.postJSON(t, "/api/v1/auth/organizations/", map[string]any{"name": "Bad", "slug": "Foo Bar!!"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 422 {
		t.Fatalf("create with invalid slug: status %d, want 422", resp.StatusCode)
	}
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/", map[string]any{"name": "Good", "slug": "good-slug"}), "create with valid slug")

	// The update hook, through the plugin API (the HTTP PATCH route takes an id the list route
	// never returns — see routes.txt).
	user := &limen.User{ID: lookupUserID(t, ts, email), Email: email}
	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	var good *organization.Organization
	for _, o := range page.Items {
		if o.Slug == "good-slug" {
			good = o
		}
	}
	if good == nil {
		t.Fatalf("good-slug not found among %d orgs", len(page.Items))
	}
	bad := "Nope!!"
	if _, err := ts.svc.orgs.UpdateOrganization(context.Background(), user, good.ID, &organization.UpdateOrganizationRequest{Slug: &bad}); err == nil {
		t.Error("UpdateOrganization with invalid slug succeeded, want an error")
	}
	fine := "renamed-slug"
	if _, err := ts.svc.orgs.UpdateOrganization(context.Background(), user, good.ID, &organization.UpdateOrganizationRequest{Slug: &fine}); err != nil {
		t.Errorf("UpdateOrganization with valid slug: %v", err)
	}
}
