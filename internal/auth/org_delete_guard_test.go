package auth

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/refsdal/whenweall/internal/db"
)

// TestAuthMountRefusesOrganizationDelete (#64): Limen's DELETE /organizations/:id has no
// last-owner guard, no confirmation and no audit row, and cascades every poll and booking page
// the organization owns. The SPA never calls it — organizations are only ever deleted by
// DeleteOwnAccount's cascade (cascade.go), which goes straight to SQL — so AuthMountGuard refuses
// it outright. The organization and its content must survive the attempt.
func TestAuthMountRefusesOrganizationDelete(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := "would-delete-org@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	orgID := probeActiveOrgID(t, ts)
	if orgID == "" {
		t.Fatal("no active org after sign-in")
	}
	pollID := db.NewID()
	if _, err := ts.svc.db.ExecContext(ctx, `
		INSERT INTO polls (id, organization_id, type, title, timezone, created_at, updated_at)
		VALUES ($1, $2, 'dates', 'Survives', 'UTC', now(), now())
	`, pollID, orgID); err != nil {
		t.Fatalf("seed poll: %v", err)
	}

	for _, path := range []string{
		"/api/v1/auth/organizations/" + orgID,
		"/api/v1/auth/organizations/" + orgID + "/",
		"/api/v1/auth/organizations//" + orgID,
	} {
		resp := ts.delete(t, path)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("DELETE %s: status %d, want 403: %s", path, resp.StatusCode, body)
		}
	}

	var orgs, polls int
	if err := ts.svc.db.QueryRowContext(ctx, `SELECT count(*) FROM organizations WHERE id = $1`, orgID).Scan(&orgs); err != nil {
		t.Fatal(err)
	}
	if err := ts.svc.db.QueryRowContext(ctx, `SELECT count(*) FROM polls WHERE id = $1`, pollID).Scan(&polls); err != nil {
		t.Fatal(err)
	}
	if orgs != 1 || polls != 1 {
		t.Errorf("after refused delete: %d orgs, %d polls; want 1 and 1", orgs, polls)
	}
}

func TestIsOrganizationDeleteRoute(t *testing.T) {
	for route, want := range map[string]bool{
		"DELETE /api/v1/auth/organizations/42":         true,
		"DELETE /api/v1/auth/organizations/members/42": false, // removing a member keeps its own last-owner guard
		"DELETE /api/v1/auth/organizations":            false,
		"PATCH /api/v1/auth/organizations/42":          false,
		"GET /api/v1/auth/organizations/42":            false,
	} {
		if got := isOrganizationDeleteRoute(route); got != want {
			t.Errorf("isOrganizationDeleteRoute(%q) = %v, want %v", route, got, want)
		}
	}
}
