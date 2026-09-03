package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"
)

func countRows(t *testing.T, ts *testService, query string, args ...any) int {
	t.Helper()
	var n int
	if err := ts.svc.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestCheckOwnPassword(t *testing.T) {
	ts := newTestService(t)
	email := "pw-check@example.com"
	userID := signUp(t, ts, email)
	ctx := context.Background()

	if err := ts.svc.CheckOwnPassword(ctx, userID, signupPassword); err != nil {
		t.Errorf("correct password: %v, want nil", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, userID, "definitely-not-it"); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("wrong password: %v, want ErrPasswordMismatch", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, userID, ""); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("empty password: %v, want ErrPasswordRequired", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, "999999", signupPassword); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("unknown user: %v, want ErrNoSuchUser", err)
	}

	// An OAuth-only account has no credential to re-check; deletion must not demand one.
	if _, err := ts.svc.db.ExecContext(ctx, "UPDATE users SET password = NULL WHERE id = $1", lookupUserID(t, ts, email)); err != nil {
		t.Fatalf("nulling password: %v", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, userID, ""); err != nil {
		t.Errorf("oauth-only account, empty password: %v, want nil", err)
	}
}

func TestDeleteOwnAccountCascades(t *testing.T) {
	ts := newTestService(t)
	email := "delete-me@example.com"
	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts) // creates the personal org
	userID := fmt.Sprint(lookupUserID(t, ts, email))
	ctx := context.Background()

	if got := countRows(t, ts, "SELECT count(*) FROM organization_members WHERE user_id = $1", lookupUserID(t, ts, email)); got != 1 {
		t.Fatalf("memberships before delete = %d, want 1", got)
	}
	nb := "nb"
	if err := ts.svc.SetProfile(ctx, userID, nil, &nb); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}

	if err := ts.svc.DeleteOwnAccount(ctx, userID); err != nil {
		t.Fatalf("DeleteOwnAccount: %v", err)
	}

	if got := countRows(t, ts, "SELECT count(*) FROM users WHERE email = $1", email); got != 0 {
		t.Errorf("users rows after delete = %d, want 0", got)
	}
	if got := countRows(t, ts, "SELECT count(*) FROM organizations WHERE slug LIKE 'delete-me-%'"); got != 0 {
		t.Errorf("sole-owned personal org survived deletion (%d rows)", got)
	}
	if got := countRows(t, ts, "SELECT count(*) FROM user_preferences"); got != 0 {
		t.Errorf("user_preferences rows after delete = %d, want 0 (FK cascade)", got)
	}
	// The cookie the client still holds is dead.
	probe := decodeJSON(t, ts.get(t, "/probe"))
	if anon, _ := probe["anonymous"].(bool); !anon {
		t.Errorf("probe still sees a session after account deletion: %+v", probe)
	}
	if err := ts.svc.DeleteOwnAccount(ctx, userID); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("second DeleteOwnAccount = %v, want ErrNoSuchUser", err)
	}
}

func TestListAndSwitchOrganizations(t *testing.T) {
	ts := newTestService(t)
	email := "switcher@example.com"
	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)
	ctx := context.Background()

	// A second org through Limen's own route (the SPA has no other way to create one).
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/", map[string]any{
		"name": "Team Ada", "slug": "team-ada",
	}), "create org")

	sess := &Session{UserID: fmt.Sprint(lookupUserID(t, ts, email))}
	probe := decodeJSON(t, ts.get(t, "/probe"))
	sess.ActiveOrgID, _ = probe["ActiveOrgID"].(string)

	orgs, err := ts.svc.ListUserOrganizations(ctx, sess)
	if err != nil {
		t.Fatalf("ListUserOrganizations: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2: %+v", len(orgs), orgs)
	}
	var team, personal *OrgSummary
	for i := range orgs {
		switch orgs[i].Slug {
		case "team-ada":
			team = &orgs[i]
		default:
			personal = &orgs[i]
		}
	}
	if team == nil || personal == nil {
		t.Fatalf("expected a personal org and team-ada, got %+v", orgs)
	}
	// Limen's own POST /organizations/ (organization@v0.1.1's CreateOrganization handler)
	// unconditionally calls SetActiveOrganization on the org it just created — confirmed against
	// the pinned source (handlers.go's CreateOrganization) — so team-ada, not the personal org, is
	// already the session's active organization at this point, with no switch call made yet.
	if personal.Active || !team.Active {
		t.Errorf("Active flags right after create: personal=%v team=%v, want false/true", personal.Active, team.Active)
	}
	if team.ID == "" || team.Name != "Team Ada" {
		t.Errorf("team summary = %+v", *team)
	}

	// Switch back to the personal org through the probe route (SwitchOrganization needs the
	// request's Limen session), then to team-ada again — exercising a real transition each way.
	requireStatus2xx(t, ts.get(t, "/probe/switch?org="+personal.ID), "switch to personal")
	probe = decodeJSON(t, ts.get(t, "/probe"))
	if got, _ := probe["ActiveOrgID"].(string); got != personal.ID {
		t.Errorf("ActiveOrgID after switch to personal = %q, want %q", got, personal.ID)
	}

	requireStatus2xx(t, ts.get(t, "/probe/switch?org="+team.ID), "switch to team")
	probe = decodeJSON(t, ts.get(t, "/probe"))
	if got, _ := probe["ActiveOrgID"].(string); got != team.ID {
		t.Errorf("ActiveOrgID after switch to team = %q, want %q", got, team.ID)
	}

	// Someone else's org: forbidden, and the active org does not move.
	outsider := "outsider-switch@example.com"
	fresh := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, _ := cookiejar.New(nil)
	fresh.client = &http.Client{Jar: jar}
	fresh.signUpVerifiedAndSignIn(t, outsider)
	resp := fresh.get(t, "/probe/switch?org="+team.ID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("outsider switch status = %d, want 403", resp.StatusCode)
	}
	resp2 := fresh.get(t, "/probe/switch?org=not-a-number")
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("garbage org id status = %d, want 403", resp2.StatusCode)
	}
}
