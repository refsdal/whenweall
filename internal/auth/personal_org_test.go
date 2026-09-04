package auth

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"
)

// lookupUserID looks up email's user id directly (same approach as MakeStaff — there is no
// public find-by-email API on Limen), so the test can build a *limen.User to drive the
// organization API the same way Limen itself would after authenticating that user.
func lookupUserID(t *testing.T, ts *testService, email string) int64 {
	t.Helper()
	var id int64
	if err := ts.svc.db.QueryRowContext(context.Background(),
		"SELECT id FROM users WHERE email = $1", limen.NormalizeEmail(email),
	).Scan(&id); err != nil {
		t.Fatalf("lookupUserID(%q): %v", email, err)
	}
	return id
}

// countOrganizations returns how many organizations user belongs to.
func countOrganizations(t *testing.T, ts *testService, user *limen.User) int64 {
	t.Helper()
	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	return page.Total
}

// triggerSessionResolution hits an authenticated route so Middleware/resolveSession actually runs
// with the session cookie present — the invariant is enforced lazily in resolveSession
// (ensurePersonalOrgOnce), not at signup/signin response time itself: the signup/signin request's
// own pass through Middleware happens before that response's Set-Cookie has reached the client, so
// it takes one more round trip (here, /me) for resolveSession to ever see the cookie.
func triggerSessionResolution(t *testing.T, ts *testService) {
	t.Helper()
	resp := ts.get(t, "/api/v1/auth/me")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /me: status %d: %s", resp.StatusCode, body)
	}
}

// TestPersonalOrgCreatedOnSignup covers Task 3's brief step 1: after a credential signup and the
// first authenticated request, the new user has exactly one organization (the silent personal
// org); signing out and back in again does not create a second one.
func TestPersonalOrgCreatedOnSignup(t *testing.T) {
	ts := newTestService(t)
	email := "org-owner@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after signup = %d, want 1", got)
	}

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signout", map[string]any{}), "signout")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   signupPassword,
	}), "signin")
	triggerSessionResolution(t, ts)

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after sign out/in again = %d, want still 1", got)
	}
}

// TestPersonalOrgEnsureIsIdempotentAtTheDBLevel directly exercises the guard
// ensurePersonalOrganizationForUser relies on (ListOrganizations count re-checked, under an
// advisory lock, before CreateOrganization) — independent of the in-process once-cache
// (personalOrgEnsured), which TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne below covers
// separately. This is what makes a redirect-mode OAuth re-sign-in of an existing user (which this
// suite can't drive end-to-end without a live OAuth provider) a no-op: it hits exactly this
// function on every request, cache or no cache.
func TestPersonalOrgEnsureIsIdempotentAtTheDBLevel(t *testing.T) {
	ts := newTestService(t)
	email := "repeat-signer@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after signup = %d, want 1", got)
	}

	// Call the core check-then-create directly a second time, bypassing the once-cache
	// entirely, to prove the DB-level guard itself (not just the cache) is idempotent.
	if err := ts.svc.ensurePersonalOrganizationForUser(context.Background(), user); err != nil {
		t.Fatalf("ensurePersonalOrganizationForUser (second call): %v", err)
	}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after a second ensure call = %d, want still 1 (not idempotent)", got)
	}
}

// TestPersonalOrgRecreatedAfterCacheClearAndOrgDeleted proves the lazy path actually re-derives
// the invariant from the database rather than only ever trusting the cache: after the personal org
// is created and the user is marked done in personalOrgEnsured, deleting the org out from under
// them and clearing that one cache entry (simulating this same process having forgotten, or a
// fresh replica that never saw this user before) makes the very next authenticated request recreate
// exactly one organization.
func TestPersonalOrgRecreatedAfterCacheClearAndOrgDeleted(t *testing.T) {
	ts := newTestService(t)
	email := "cache-cleared@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	orgID := page.Items[0].ID

	if _, err := ts.svc.db.ExecContext(context.Background(),
		"DELETE FROM organization_members WHERE organization_id = $1", orgID,
	); err != nil {
		t.Fatalf("delete organization_members: %v", err)
	}
	if _, err := ts.svc.db.ExecContext(context.Background(),
		"DELETE FROM organizations WHERE id = $1", orgID,
	); err != nil {
		t.Fatalf("delete organizations: %v", err)
	}
	if got := countOrganizations(t, ts, user); got != 0 {
		t.Fatalf("organizations after manual deletion = %d, want 0", got)
	}

	// Clear this user's once-cache entry directly (white-box: same package) rather than adding a
	// dedicated exported test hook — this stands in for "a fresh replica that never marked this
	// user done".
	ts.svc.personalOrgEnsured.Delete(fmt.Sprint(userID))

	triggerSessionResolution(t, ts)

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after re-triggering with the cache cleared = %d, want 1", got)
	}
}

// TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne exercises the advisory lock directly: N
// goroutines all make a fresh org-less user's first authenticated request at the same time (none
// of them has this user in personalOrgEnsured yet), and exactly one organization must result.
func TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne(t *testing.T) {
	ts := newTestService(t)
	email := "concurrent-first@example.com"

	ts.signUpVerifiedAndSignIn(t, email)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp, err := ts.client.Get(ts.url("/api/v1/auth/me"))
			if err != nil {
				errs <- fmt.Errorf("GET /me: %w", err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				errs <- fmt.Errorf("GET /me: status %d: %s", resp.StatusCode, body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent /me request failed: %v", err)
	}

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}
	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after %d concurrent first requests = %d, want 1", n, got)
	}
}

// TestPersonalOrgSlugCollisionAcrossDomainsBothSucceed is C2's regression test: two users who
// share an email local part but differ in domain (ada@foo.com, ada@bar.com) both derive the same
// organization *name* ("ada"), which used to also mean the same Limen-generated *slug* (slugs are
// globally unique) — so the second signup's CreateOrganization would fail with
// ErrOrganizationSlugAlreadyExists on literally every request from then on, permanently leaving
// that user without a personal org. Passing an explicitly per-user slug
// (createPersonalOrgIfMissing / personalOrgSlug) fixes this: each user gets exactly one
// organization regardless of local-part collisions.
func TestPersonalOrgSlugCollisionAcrossDomainsBothSucceed(t *testing.T) {
	ts := newTestService(t)

	emails := []string{"ada@foo.example", "ada@bar.example"}
	for _, email := range emails {
		ts.signUpVerifiedAndSignIn(t, email)
	}

	for _, email := range emails {
		// Sign in as this user specifically (the shared jar client's session is currently
		// whichever of the two signed up last) so triggerSessionResolution/countOrganizations
		// checks the right user's invariant.
		requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
			"credential": email,
			"password":   signupPassword,
		}), "signin "+email)
		triggerSessionResolution(t, ts)

		userID := lookupUserID(t, ts, email)
		user := &limen.User{ID: userID, Email: email}
		if got := countOrganizations(t, ts, user); got != 1 {
			t.Errorf("organizations for %s = %d, want exactly 1", email, got)
		}
	}
}

// TestActiveOrgIDDefaultsToPersonalOrgOnFirstProbe is I5's regression test: a freshly signed-up
// user's very first authenticated request must see a non-empty Session.ActiveOrgID — equal to
// their personal org's id — rather than "" until they happen to call organizations/switch, which
// nothing in a fresh signup flow ever does on their behalf.
func TestActiveOrgIDDefaultsToPersonalOrgOnFirstProbe(t *testing.T) {
	ts := newTestService(t)
	email := "fresh-signup@example.com"

	ts.signUpVerifiedAndSignIn(t, email)

	probeResp := ts.get(t, "/probe")
	probeBody := decodeJSON(t, probeResp)
	if anon, _ := probeBody["anonymous"].(bool); anon {
		t.Fatalf("probe reported anonymous after signup: %+v", probeBody)
	}
	activeOrgID, _ := probeBody["ActiveOrgID"].(string)
	if activeOrgID == "" {
		t.Fatal("probe Session.ActiveOrgID is empty on the very first authenticated request, want the personal org's id")
	}

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}
	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	wantOrgID := fmt.Sprint(page.Items[0].ID)
	if activeOrgID != wantOrgID {
		t.Errorf("probe Session.ActiveOrgID = %q, want the personal org's id %q", activeOrgID, wantOrgID)
	}
}

// TestRequireOrgMemberSucceedsForPersonalOrg is the happy-path RequireOrgMember test deferred from
// the previous plan's review: after signup and the first authenticated request creates the
// personal org, RequireOrgMember succeeds for that user against that org's id.
func TestRequireOrgMemberSucceedsForPersonalOrg(t *testing.T) {
	ts := newTestService(t)
	email := "member-check@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	orgID := fmt.Sprint(page.Items[0].ID)

	sess := &Session{UserID: fmt.Sprint(userID)}
	ctx := context.WithValue(context.Background(), sessionCtxKey{}, sess)

	gotSess, err := ts.svc.RequireOrgMember(ctx, orgID)
	if err != nil {
		t.Fatalf("RequireOrgMember(member, their personal org) error = %v, want nil", err)
	}
	if gotSess != sess {
		t.Errorf("RequireOrgMember returned session = %+v, want the same *Session passed in via context", gotSess)
	}
}

// createOrgForUser creates an extra organization owned by user through Limen's own API (the same
// call createPersonalOrgIfMissing makes), so the membership row is exactly the shape production
// writes. Returns the new organization's bigint id.
func createOrgForUser(t *testing.T, ts *testService, user *limen.User, name, slug string) int64 {
	t.Helper()
	org, err := ts.svc.orgs.CreateOrganization(context.Background(), user, &organization.CreateOrganizationRequest{Name: name, Slug: slug})
	if err != nil {
		t.Fatalf("CreateOrganization(%s): %v", name, err)
	}
	id, err := strconv.ParseInt(fmt.Sprint(org.ID), 10, 64)
	if err != nil {
		t.Fatalf("organization id %v is not an int64: %v", org.ID, err)
	}
	return id
}

// setMembershipCreatedAt pins one membership row's created_at so the "oldest membership" rule is
// exercised on an explicit, clock-independent ordering (the same trick the TS original used).
func setMembershipCreatedAt(t *testing.T, ts *testService, orgID, userID int64, createdAt string) {
	t.Helper()
	if _, err := ts.svc.db.ExecContext(context.Background(),
		`UPDATE organization_members SET created_at = $3::timestamptz WHERE organization_id = $1 AND user_id = $2`,
		orgID, userID, createdAt,
	); err != nil {
		t.Fatalf("pinning membership created_at for org %d: %v", orgID, err)
	}
}

// probeActiveOrgID reads Session.ActiveOrgID off the /probe route for the harness's current cookie.
func probeActiveOrgID(t *testing.T, ts *testService) string {
	t.Helper()
	body := decodeJSON(t, ts.get(t, "/probe"))
	if anon, _ := body["anonymous"].(bool); anon {
		t.Fatalf("probe reported anonymous: %+v", body)
	}
	id, _ := body["ActiveOrgID"].(string)
	return id
}

// TestSessionFallsBackToOldestRemainingMembershipWhenActiveOrgMembershipIsDangling re-expresses
// session.functions.workers.test.ts's "falls back to the oldest remaining membership when the
// active org id is dangling": the session still names an organization the user has no membership
// row in (the org itself survives), and the very next request must (a) report the OLDEST remaining
// membership by organization_members.created_at — not by id, hence the pinned timestamps below,
// which make the org created LAST the one that must win — and (b) persist that choice on the
// session so it isn't re-derived on every request.
func TestSessionFallsBackToOldestRemainingMembershipWhenActiveOrgMembershipIsDangling(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := "dangling-membership@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	// Signup alone establishes no session — credential-password is configured with
	// WithAutoSignInOnSignUp(false) (auth.go) — so an explicit signin is required before
	// triggerSessionResolution's GET /me has any cookie to resolve (see TestSignupSigninMeFlow,
	// which does the same two calls for the same reason).
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   signupPassword,
	}), "signin")
	triggerSessionResolution(t, ts) // creates the personal org and makes it active

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}
	personalOrgID, err := strconv.ParseInt(probeActiveOrgID(t, ts), 10, 64)
	if err != nil {
		t.Fatalf("personal org id from probe: %v", err)
	}

	oldestOrgID := createOrgForUser(t, ts, user, "Oldest Org", fmt.Sprintf("oldest-org-%d", userID))
	staleOrgID := createOrgForUser(t, ts, user, "Stale Org", fmt.Sprintf("stale-org-%d", userID))

	// By id the personal org is oldest; by membership created_at "Oldest Org" is. The fallback
	// must follow created_at.
	setMembershipCreatedAt(t, ts, personalOrgID, userID, "2020-06-01")
	setMembershipCreatedAt(t, ts, oldestOrgID, userID, "2020-01-01")
	setMembershipCreatedAt(t, ts, staleOrgID, userID, "2020-09-01")

	// Point the session at the stale org (the very column Limen's SetActiveOrganization writes),
	// then delete only the membership — the organization row itself survives, so this is a
	// dangling membership, not a deleted org (TestPersonalOrgRecreatedAfterCacheClearAndOrgDeleted
	// already covers that one).
	if _, err := ts.svc.db.ExecContext(ctx,
		`UPDATE sessions SET active_organization_id = $1 WHERE user_id = $2`, staleOrgID, userID,
	); err != nil {
		t.Fatalf("pointing session at stale org: %v", err)
	}
	if _, err := ts.svc.db.ExecContext(ctx,
		`DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2`, staleOrgID, userID,
	); err != nil {
		t.Fatalf("deleting stale membership: %v", err)
	}

	if got, want := probeActiveOrgID(t, ts), fmt.Sprint(oldestOrgID); got != want {
		t.Errorf("Session.ActiveOrgID = %q, want the oldest remaining membership %q (stale org %d, personal org %d)", got, want, staleOrgID, personalOrgID)
	}

	var stored sql.NullInt64
	if err := ts.svc.db.QueryRowContext(ctx,
		`SELECT active_organization_id FROM sessions WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, userID,
	).Scan(&stored); err != nil {
		t.Fatalf("reading session active_organization_id: %v", err)
	}
	if !stored.Valid || stored.Int64 != oldestOrgID {
		t.Errorf("sessions.active_organization_id = %v, want %d persisted by the fallback", stored, oldestOrgID)
	}
}

// TestActiveOrgIDStaysOnItsCurrentOrgWhenMembershipIsStillValid is the "ordinary session"
// counterpart to the dangling-membership test above: it proves the non-dangling arm
// (activeOrgID != nil && !membershipDangling(...)) is what keeps a session's active organization
// put, rather than every request re-deriving the oldest membership. Without that arm (e.g. if the
// switch collapsed back to only checking activeOrgID != nil, or the membership check were
// inverted), this test would fail because the fallback would silently move the user from their
// current, still-valid org to a DIFFERENT, older one purely because it re-ran every time.
func TestActiveOrgIDStaysOnItsCurrentOrgWhenMembershipIsStillValid(t *testing.T) {
	ts := newTestService(t)
	email := "stable-session@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts) // creates the personal org and makes it active
	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	currentOrgID, err := strconv.ParseInt(probeActiveOrgID(t, ts), 10, 64)
	if err != nil {
		t.Fatalf("active org id from probe: %v", err)
	}

	// An org created AFTER the personal org, but whose membership is pinned EARLIER than it: if
	// the fallback ran again on the next request (as it would for a dangling or absent active
	// org), it would prefer this one. It must not, because the current active org's own
	// membership is still valid.
	olderByTimestampOrgID := createOrgForUser(t, ts, user, "Older By Timestamp", fmt.Sprintf("older-by-timestamp-%d", userID))
	setMembershipCreatedAt(t, ts, currentOrgID, userID, "2020-06-01")
	setMembershipCreatedAt(t, ts, olderByTimestampOrgID, userID, "2020-01-01")

	if got, want := probeActiveOrgID(t, ts), fmt.Sprint(currentOrgID); got != want {
		t.Errorf("Session.ActiveOrgID = %q after a second, unrelated request, want unchanged %q (not the older membership %d)", got, want, olderByTimestampOrgID)
	}

	var stored sql.NullInt64
	if err := ts.svc.db.QueryRowContext(context.Background(),
		`SELECT active_organization_id FROM sessions WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, userID,
	).Scan(&stored); err != nil {
		t.Fatalf("reading session active_organization_id: %v", err)
	}
	if !stored.Valid || stored.Int64 != currentOrgID {
		t.Errorf("sessions.active_organization_id = %v, want unchanged %d", stored, currentOrgID)
	}
}

// TestSessionSelfHealsAfterUserDeletesTheirOnlyOrganization covers the gap the fix round 1 review
// found: Limen's DeleteOrganization (organizations.go in the plugin) has no last-owner guard —
// unlike LeaveOrganization/RemoveMember, which call validateOwnerRoleCanBeRemoved — so a personal
// org's owner (every user, by construction) can delete their own only organization through the
// real route this test drives, DELETE /api/v1/auth/organizations/:id. That clears
// sessions.active_organization_id and removes the organization_members row, leaving the user with
// ZERO memberships — but ensurePersonalOrgOnce already cached them as "done" on an earlier
// request, and that cache is never re-checked otherwise, so without resolveSession's self-heal
// (session.go: clear the cache entry and re-run ensurePersonalOrgOnce when oldestMembership finds
// nothing) this user would be stuck with no active organization for the rest of the process's
// life. Asserts the very next request recreates a personal org and makes it active, and that
// exactly one organization exists afterwards (no duplicate).
func TestSessionSelfHealsAfterUserDeletesTheirOnlyOrganization(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()
	email := "deletes-only-org@example.com"

	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts) // creates the personal org, makes it active, warms personalOrgEnsured
	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations before delete = %d, want 1", got)
	}
	originalOrgID := probeActiveOrgID(t, ts)
	if originalOrgID == "" {
		t.Fatal("active org id is empty before delete")
	}

	// The real route, driven as an authenticated HTTP request — not a direct DB delete — so this
	// proves the production path (owner deletes their own org) actually reaches the zero-
	// membership state the fix handles.
	requireStatus2xx(t, ts.delete(t, "/api/v1/auth/organizations/"+originalOrgID), "delete own organization")

	if got := countOrganizations(t, ts, user); got != 0 {
		t.Fatalf("organizations right after delete = %d, want 0", got)
	}

	// The next authenticated request must self-heal: recreate exactly one organization and make
	// it active, rather than leaving ActiveOrgID empty forever (personalOrgEnsured still marks
	// this user "done" from the earlier triggerSessionResolution call above).
	healedOrgID := probeActiveOrgID(t, ts)
	if healedOrgID == "" {
		t.Fatal("Session.ActiveOrgID is still empty after the self-heal request, want a recreated personal org")
	}
	if healedOrgID == originalOrgID {
		t.Fatalf("healed org id %q equals the deleted org's id; want a freshly created organization", healedOrgID)
	}
	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after self-heal = %d, want exactly 1 (no duplicates)", got)
	}

	var stored sql.NullInt64
	if err := ts.svc.db.QueryRowContext(ctx,
		`SELECT active_organization_id FROM sessions WHERE user_id = $1 ORDER BY id DESC LIMIT 1`, userID,
	).Scan(&stored); err != nil {
		t.Fatalf("reading session active_organization_id: %v", err)
	}
	if !stored.Valid || fmt.Sprint(stored.Int64) != healedOrgID {
		t.Errorf("sessions.active_organization_id = %v, want the healed org id %s persisted", stored, healedOrgID)
	}
}
