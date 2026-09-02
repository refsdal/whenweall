package admin_test

// Ports the behavioral cases from src/server/admin/__tests__/users.workers.test.ts (search by
// email/name fragment, UserDetail's orgs+counts, UserDetail(unknown id) => nil) and from
// src/server/auth/__tests__/user-delete.workers.test.ts (sole-owner org cascades with its content;
// an org with another owner survives untouched; the oldest remaining member is promoted to owner
// rather than the org being deleted) — adapted to this port's interfaces, which differ from the TS
// originals in ways the task brief calls out explicitly:
//
//   - SearchUsers is cursor-paginated (matching admin.List's own keyset convention), not
//     offset+total — so "reports a total independent of page size" has no analogue; in its place,
//     TestSearchUsers_CursorWalksFullSetNoDupesNoGaps exercises paging the way audit_test.go's own
//     cursor-walk test does.
//   - "never returns password or token material" has no analogue either: AdminUserRow simply has
//     no such field, a property the compiler already guarantees (same reasoning audit_test.go
//     gives for skipping the TS "no mutator" reflection test).
//   - Lock/unlock replace TS's Better-Auth-plugin ban/unban (against `user.banned`) — there is no
//     such column on Limen's schema (see migrations/00007_admin_locks.sql) — and delete's cascade
//     is ported by hand against Limen's organization/organization_members/organization_member_roles
//     tables rather than Better-Auth's `member.role` column.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/admin"
	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/testdb"
)

// --- seed helpers -----------------------------------------------------------------------------

// seedUserWithName is seedUser (audit_test.go) plus first/last name, for the search-by-name case.
func seedUserWithName(t *testing.T, d *sql.DB, firstName, lastName string) (userID, email string) {
	t.Helper()
	n := seedSeq.Add(1)
	email = fmt.Sprintf("named-%d@example.com", n)
	var uid int64
	if err := d.QueryRowContext(context.Background(), `
		INSERT INTO users (email, first_name, last_name, updated_at) VALUES ($1, $2, $3, now()) RETURNING id
	`, email, firstName, lastName).Scan(&uid); err != nil {
		t.Fatalf("seeding named user: %v", err)
	}
	return fmt.Sprint(uid), email
}

func seedUserAt(t *testing.T, d *sql.DB, createdAt time.Time) string {
	t.Helper()
	n := seedSeq.Add(1)
	var uid int64
	if err := d.QueryRowContext(context.Background(), `
		INSERT INTO users (email, created_at, updated_at) VALUES ($1, $2, now()) RETURNING id
	`, fmt.Sprintf("paged-%d@example.com", n), createdAt).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return fmt.Sprint(uid)
}

func seedOrg(t *testing.T, d *sql.DB, name string) int64 {
	t.Helper()
	n := seedSeq.Add(1)
	var oid int64
	if err := d.QueryRowContext(context.Background(), `
		INSERT INTO organizations (name, slug, updated_at) VALUES ($1, $2, now()) RETURNING id
	`, name, fmt.Sprintf("org-%d", n)).Scan(&oid); err != nil {
		t.Fatalf("seeding organization: %v", err)
	}
	return oid
}

// seedMember adds userID to orgID as a member, at createdAt (membership order matters for
// cascadeOrphanedOwnerOrganizations' "oldest remaining member" rule), with the given role names
// (may be empty — a plain member with no organization_member_roles row at all).
func seedMember(t *testing.T, d *sql.DB, orgID, userID int64, createdAt time.Time, roles ...string) int64 {
	t.Helper()
	var memberID int64
	if err := d.QueryRowContext(context.Background(), `
		INSERT INTO organization_members (organization_id, user_id, created_at, updated_at) VALUES ($1, $2, $3, now()) RETURNING id
	`, orgID, userID, createdAt).Scan(&memberID); err != nil {
		t.Fatalf("seeding organization member: %v", err)
	}
	for _, role := range roles {
		if _, err := d.ExecContext(context.Background(), `
			INSERT INTO organization_member_roles (organization_id, member_id, role) VALUES ($1, $2, $3)
		`, orgID, memberID, role); err != nil {
			t.Fatalf("seeding organization member role %q: %v", role, err)
		}
	}
	return memberID
}

func seedPollForUser(t *testing.T, d *sql.DB, orgID, createdBy int64) string {
	t.Helper()
	id := db.NewID()
	now := time.Now()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO polls (id, organization_id, created_by, type, title, timezone, created_at, updated_at)
		VALUES ($1, $2, $3, 'options', 'Lunch spot', 'Europe/Oslo', $4, $4)
	`, id, orgID, createdBy, now); err != nil {
		t.Fatalf("seeding poll: %v", err)
	}
	return id
}

func seedBookingPageForUser(t *testing.T, d *sql.DB, orgID, createdBy int64) string {
	t.Helper()
	id := db.NewID()
	now := time.Now()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO booking_pages (
			id, organization_id, created_by, slug, title, timezone,
			slot_duration_min, buffer_before_min, buffer_after_min, min_notice_min, max_days_ahead,
			availability, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'Intro call', 'Europe/Oslo', 30, 0, 0, 0, 60, '{}'::jsonb, $5, $5)
	`, id, orgID, createdBy, "intro-call-"+id, now); err != nil {
		t.Fatalf("seeding booking page: %v", err)
	}
	return id
}

func pollExists(t *testing.T, d *sql.DB, pollID string) bool {
	t.Helper()
	var exists bool
	if err := d.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM polls WHERE id = $1)`, pollID).Scan(&exists); err != nil {
		t.Fatalf("checking poll existence: %v", err)
	}
	return exists
}

func bookingPageCreatedBy(t *testing.T, d *sql.DB, pageID string) sql.NullInt64 {
	t.Helper()
	var createdBy sql.NullInt64
	if err := d.QueryRowContext(context.Background(), `SELECT created_by FROM booking_pages WHERE id = $1`, pageID).Scan(&createdBy); err != nil {
		t.Fatalf("reading booking page created_by: %v", err)
	}
	return createdBy
}

func orgExists(t *testing.T, d *sql.DB, orgID int64) bool {
	t.Helper()
	var exists bool
	if err := d.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM organizations WHERE id = $1)`, orgID).Scan(&exists); err != nil {
		t.Fatalf("checking organization existence: %v", err)
	}
	return exists
}

func userExists(t *testing.T, d *sql.DB, userID string) bool {
	t.Helper()
	var exists bool
	if err := d.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		t.Fatalf("checking user existence: %v", err)
	}
	return exists
}

func memberRoles(t *testing.T, d *sql.DB, orgID int64) map[string][]string {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), `
		SELECT m.user_id, coalesce(string_agg(mr.role, ',' ORDER BY mr.role), '')
		FROM organization_members m
		LEFT JOIN organization_member_roles mr ON mr.member_id = m.id
		WHERE m.organization_id = $1
		GROUP BY m.user_id
	`, orgID)
	if err != nil {
		t.Fatalf("listing members: %v", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	for rows.Next() {
		var userID int64
		var rolesCSV string
		if err := rows.Scan(&userID, &rolesCSV); err != nil {
			t.Fatalf("scanning member: %v", err)
		}
		var roles []string
		if rolesCSV != "" {
			roles = strings.Split(rolesCSV, ",")
		}
		out[fmt.Sprint(userID)] = roles
	}
	return out
}

func auditRowExists(t *testing.T, d *sql.DB, action, targetID string) bool {
	t.Helper()
	var exists bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM admin_audit_log WHERE action = $1 AND target_id = $2)`, action, targetID,
	).Scan(&exists); err != nil {
		t.Fatalf("checking audit log: %v", err)
	}
	return exists
}

// mustParseUserID parses a seedUser-style stringified id back to the bigint SQL helpers below
// need for parameter binding.
func mustParseUserID(t *testing.T, s string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("parsing user id %q: %v", s, err)
	}
	return id
}

// actorSession seeds a fresh user (admin_audit_log.actor_user_id has a real FK to users(id), so
// the actor must exist) and returns a Session for it, suitable as LockUser/UnlockUser/DeleteUser's
// actor argument.
func actorSession(t *testing.T, d *sql.DB) *auth.Session {
	t.Helper()
	userID, email := seedUser(t, d)
	return &auth.Session{UserID: userID, Email: email, Staff: true}
}

// --- SearchUsers --------------------------------------------------------------------------------

func TestSearchUsers_FindsByEmailFragmentCaseInsensitively(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userID, email := seedUser(t, d)

	found, _, err := admin.SearchUsers(ctx, d, admin.UserFilter{Query: strings.ToUpper(email[:12]), Limit: 50})
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if !containsUserID(found, userID) {
		t.Errorf("SearchUsers(%q) = %v, want it to include user %s", email, found, userID)
	}
}

func TestSearchUsers_FindsByName(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userID, _ := seedUserWithName(t, d, "Ada", "Lovelace")

	found, _, err := admin.SearchUsers(ctx, d, admin.UserFilter{Query: "Ada Lovelace", Limit: 50})
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if !containsUserID(found, userID) {
		t.Errorf("SearchUsers(name) = %v, want it to include user %s", found, userID)
	}
	for _, u := range found {
		if u.ID == userID && u.Name != "Ada Lovelace" {
			t.Errorf("Name = %q, want %q", u.Name, "Ada Lovelace")
		}
	}
}

func containsUserID(rows []admin.AdminUserRow, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestSearchUsers_CursorWalksFullSetNoDupesNoGaps mirrors audit_test.go's own cursor-walk test:
// SearchUsers is cursor-paginated (not offset+total, unlike listAdminUsers in the TS source — see
// this file's own package doc comment), so paging correctness is what's exercised here in place of
// the ported "reports a total independent of page size" case.
func TestSearchUsers_CursorWalksFullSetNoDupesNoGaps(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()

	const total = 17
	wantIDs := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		// Every third row shares an instant with its neighbours, exercising the id tie-break.
		ts := base.Add(time.Duration(i/3) * time.Second)
		id := seedUserAt(t, d, ts)
		wantIDs[id] = true
	}

	seen := make(map[string]bool, total)
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatalf("SearchUsers did not terminate after %d pages", pages)
		}
		rows, next, err := admin.SearchUsers(ctx, d, admin.UserFilter{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("SearchUsers: %v", err)
		}
		for _, r := range rows {
			if !wantIDs[r.ID] {
				continue // some other test's leftover users aren't possible (fresh testdb), but be defensive
			}
			if seen[r.ID] {
				t.Fatalf("SearchUsers revisited %s — cursor produced a duplicate", r.ID)
			}
			seen[r.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}

	if len(seen) != total {
		t.Fatalf("walked %d of %d seeded users — cursor left a gap", len(seen), total)
	}
}

// --- UserDetail -----------------------------------------------------------------------------

func TestUserDetail_UnknownIDReturnsNilNotError(t *testing.T) {
	d := testdb.New(t)
	detail, err := admin.UserDetail(context.Background(), d, "nope-does-not-exist")
	if err != nil {
		t.Fatalf("UserDetail: %v", err)
	}
	if detail != nil {
		t.Errorf("UserDetail(unknown) = %+v, want nil", detail)
	}
}

func TestUserDetail_IncludesOrgsAndCounts(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userIDStr, _ := seedUser(t, d)
	userID := mustParseUserID(t, userIDStr)

	orgID := seedOrg(t, d, "Detail Org")
	seedMember(t, d, orgID, userID, time.Now(), "owner")
	seedPollForUser(t, d, orgID, userID)
	seedBookingPageForUser(t, d, orgID, userID)

	detail, err := admin.UserDetail(ctx, d, userIDStr)
	if err != nil {
		t.Fatalf("UserDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("UserDetail = nil, want a detail")
	}
	if len(detail.Orgs) != 1 || detail.Orgs[0].ID != fmt.Sprint(orgID) {
		t.Errorf("Orgs = %+v, want exactly org %d", detail.Orgs, orgID)
	}
	if len(detail.Orgs) == 1 && (len(detail.Orgs[0].Roles) != 1 || detail.Orgs[0].Roles[0] != "owner") {
		t.Errorf("Orgs[0].Roles = %v, want [owner]", detail.Orgs[0].Roles)
	}
	if detail.Counts.Polls != 1 {
		t.Errorf("Counts.Polls = %d, want 1", detail.Counts.Polls)
	}
	if detail.Counts.BookingPages != 1 {
		t.Errorf("Counts.BookingPages = %d, want 1", detail.Counts.BookingPages)
	}
}

// --- LockUser / UnlockUser --------------------------------------------------------------------

// authHarness wires a real auth.Service (real Postgres, real Limen HTTP handler) behind an
// httptest.Server, mirroring internal/auth/auth_test.go's own testService — but built only from
// internal/auth's exported surface, since this package cannot reach that file's unexported
// helpers.
type authHarness struct {
	svc    *auth.Service
	server *httptest.Server
	client *http.Client
}

func newAuthHarness(t *testing.T, sqlDB *sql.DB) *authHarness {
	t.Helper()
	cfg := &config.Config{AppURL: "http://app.example", LimenSecret: make([]byte, 32)}
	svc, err := auth.New(cfg, sqlDB)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", svc.Handler())
	mux.HandleFunc("/probe/session", svc.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(svc.Middleware(mux))
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &authHarness{svc: svc, server: server, client: &http.Client{Jar: jar}}
}

const harnessPassword = "Str0ngPassw0rd"

func (h *authHarness) signUpAndSignIn(t *testing.T, email string) {
	t.Helper()
	h.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{"email": email, "password": harnessPassword})
	h.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{"credential": email, "password": harnessPassword})
}

func (h *authHarness) postJSON(t *testing.T, path string, body map[string]any) {
	t.Helper()
	h.postJSONWith(t, h.client, path, body)
}

// postJSONWith is postJSON against an explicit client rather than the harness's own default one —
// TestLockUser_RejectsAFreshSignInAfterLock needs a second, independent cookie jar to prove the
// resolveSession locked_users check itself (not just RevokeUserSessions revoking the original
// session) is what stops a locked user.
func (h *authHarness) postJSONWith(t *testing.T, client *http.Client, path string, body map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	resp, err := client.Post(h.server.URL+path, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("POST %s: status %d", path, resp.StatusCode)
	}
}

func (h *authHarness) probeSessionStatus(t *testing.T) int {
	t.Helper()
	return h.probeSessionStatusWith(t, h.client)
}

func (h *authHarness) probeSessionStatusWith(t *testing.T, client *http.Client) int {
	t.Helper()
	resp, err := client.Get(h.server.URL + "/probe/session")
	if err != nil {
		t.Fatalf("GET /probe/session: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// newClient returns a fresh cookie-jar client against the same harness server — a brand new
// "browser" with no session of its own yet.
func (h *authHarness) newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// TestLockUser_RevokesSessionsAndSessionStopsValidating ports users.workers.test.ts/
// user-delete.workers.test.ts's "lock revokes sessions" case, adapted per the task brief: asserted
// via the auth seam itself (a real Limen session, resolved through Middleware/RequireSession)
// rather than by inspecting Better-Auth's session table directly.
func TestLockUser_RevokesSessionsAndSessionStopsValidating(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	h := newAuthHarness(t, d)
	email := "locked-user@example.com"
	h.signUpAndSignIn(t, email)

	if status := h.probeSessionStatus(t); status != http.StatusOK {
		t.Fatalf("probe/session before lock: status %d, want 200", status)
	}

	var userID int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatalf("looking up user id: %v", err)
	}
	userIDStr := fmt.Sprint(userID)
	actor := actorSession(t, d)

	if err := admin.LockUser(ctx, d, h.svc, actor, userIDStr, "policy violation"); err != nil {
		t.Fatalf("LockUser: %v", err)
	}

	if status := h.probeSessionStatus(t); status != http.StatusUnauthorized {
		t.Fatalf("probe/session after lock: status %d, want 401 (session should stop validating)", status)
	}

	var remainingSessions int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&remainingSessions); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if remainingSessions != 0 {
		t.Errorf("sessions remaining for locked user = %d, want 0 (RevokeUserSessions should have cleared them)", remainingSessions)
	}

	if !auditRowExists(t, d, "lock-user", userIDStr) {
		t.Error("LockUser left no admin_audit_log row")
	}
}

// TestLockUser_RejectsAFreshSignInAfterLock is the review finding this file was missing: the
// previous test's 401 is fully explained by RevokeUserSessions clearing the original session row,
// which would still pass even if resolveSession's own locked_users EXISTS check (session.go) were
// deleted entirely. This test signs back in *after* the lock — on a brand new cookie jar, so it's
// a genuinely fresh session Limen itself has no reason to refuse (its credential-password plugin
// has no concept of a lock) — and asserts the seam still treats it as anonymous. Delete the
// locked_users check and this is the test that fails.
func TestLockUser_RejectsAFreshSignInAfterLock(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	h := newAuthHarness(t, d)
	email := "locked-user-resigns-in@example.com"
	h.signUpAndSignIn(t, email)

	var userID int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatalf("looking up user id: %v", err)
	}
	userIDStr := fmt.Sprint(userID)
	actor := actorSession(t, d)

	if err := admin.LockUser(ctx, d, h.svc, actor, userIDStr, "policy violation"); err != nil {
		t.Fatalf("LockUser: %v", err)
	}

	fresh := h.newClient(t)
	h.postJSONWith(t, fresh, "/api/v1/auth/signin/credential", map[string]any{"credential": email, "password": harnessPassword})

	if status := h.probeSessionStatusWith(t, fresh); status != http.StatusUnauthorized {
		t.Fatalf("probe/session on a brand new sign-in after lock: status %d, want 401 (locked_users check should reject it)", status)
	}
}

func TestLockUser_NilAuthSvcStillLocksAndAudits(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userID, _ := seedUser(t, d)
	actor := actorSession(t, d)

	if err := admin.LockUser(ctx, d, nil, actor, userID, "no auth service in this test"); err != nil {
		t.Fatalf("LockUser: %v", err)
	}

	var locked bool
	if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)`, userID).Scan(&locked); err != nil {
		t.Fatalf("checking locked_users: %v", err)
	}
	if !locked {
		t.Error("user is not locked after LockUser")
	}
	if !auditRowExists(t, d, "lock-user", userID) {
		t.Error("LockUser left no admin_audit_log row")
	}
}

func TestUnlockUser_ClearsLockAndAudits(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userID, _ := seedUser(t, d)
	actor := actorSession(t, d)

	if err := admin.LockUser(ctx, d, nil, actor, userID, "first"); err != nil {
		t.Fatalf("LockUser: %v", err)
	}
	if err := admin.UnlockUser(ctx, d, actor, userID, "resolved"); err != nil {
		t.Fatalf("UnlockUser: %v", err)
	}

	var locked bool
	if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)`, userID).Scan(&locked); err != nil {
		t.Fatalf("checking locked_users: %v", err)
	}
	if locked {
		t.Error("user is still locked after UnlockUser")
	}
	if !auditRowExists(t, d, "unlock-user", userID) {
		t.Error("UnlockUser left no admin_audit_log row")
	}
}

// --- DeleteUser -------------------------------------------------------------------------------

// TestDeleteUser_CascadesSolePersonalOrgWithItsContent ports user-delete.workers.test.ts's
// "deletes the user's sole-owned personal org, and its polls/booking pages with it".
func TestDeleteUser_CascadesSolePersonalOrgWithItsContent(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	ownerIDStr, _ := seedUser(t, d)
	ownerID := mustParseUserID(t, ownerIDStr)

	orgID := seedOrg(t, d, "Solo Owner's Org")
	seedMember(t, d, orgID, ownerID, time.Now(), "owner")
	pollID := seedPollForUser(t, d, orgID, ownerID)
	pageID := seedBookingPageForUser(t, d, orgID, ownerID)
	actor := actorSession(t, d)

	if err := admin.DeleteUser(ctx, d, nil, actor, ownerIDStr, "gdpr request"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if userExists(t, d, ownerIDStr) {
		t.Error("user still exists after DeleteUser")
	}
	if orgExists(t, d, orgID) {
		t.Error("sole-owned org still exists after DeleteUser, want it deleted with its owner")
	}
	if pollExists(t, d, pollID) {
		t.Error("poll still exists after its org was deleted")
	}
	if pageExists := func() bool {
		var exists bool
		if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM booking_pages WHERE id = $1)`, pageID).Scan(&exists); err != nil {
			t.Fatalf("checking booking page existence: %v", err)
		}
		return exists
	}(); pageExists {
		t.Error("booking page still exists after its org was deleted")
	}
	if !auditRowExists(t, d, "delete-user", ownerIDStr) {
		t.Error("DeleteUser left no admin_audit_log row")
	}
}

// TestDeleteUser_KeepsOrgWhenAnotherOwnerRemains ports "keeps an org and its content when another
// owner remains, removing only the departing member row".
func TestDeleteUser_KeepsOrgWhenAnotherOwnerRemains(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	ownerAIDStr, _ := seedUser(t, d)
	ownerAID := mustParseUserID(t, ownerAIDStr)
	ownerBIDStr, _ := seedUser(t, d)
	ownerBID := mustParseUserID(t, ownerBIDStr)

	orgID := seedOrg(t, d, "Two Owners")
	seedMember(t, d, orgID, ownerAID, time.Now(), "owner")
	seedMember(t, d, orgID, ownerBID, time.Now(), "owner")
	pollID := seedPollForUser(t, d, orgID, ownerAID)
	pageID := seedBookingPageForUser(t, d, orgID, ownerAID)
	actor := actorSession(t, d)

	if err := admin.DeleteUser(ctx, d, nil, actor, ownerAIDStr, "gdpr request"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if userExists(t, d, ownerAIDStr) {
		t.Error("user still exists after DeleteUser")
	}
	if !orgExists(t, d, orgID) {
		t.Fatal("org was deleted even though another owner remained")
	}
	if !pollExists(t, d, pollID) {
		t.Error("poll was deleted even though its org survived")
	}
	createdBy := bookingPageCreatedBy(t, d, pageID)
	if createdBy.Valid {
		t.Errorf("booking page created_by = %v, want NULL (set null via its own FK, not deleted)", createdBy)
	}

	roles := memberRoles(t, d, orgID)
	if _, stillMember := roles[ownerAIDStr]; stillMember {
		t.Error("departed owner's own member row is still present")
	}
	if got, ok := roles[ownerBIDStr]; !ok || len(got) != 1 || got[0] != "owner" {
		t.Errorf("remaining owner's roles = %v, want [owner] unchanged", got)
	}
}

// TestDeleteUser_PromotesOldestRemainingMemberWhenSoleOwnerLeaves ports "promotes the oldest
// remaining member to owner (rather than deleting the org) when the sole owner leaves but a plain
// member is still around".
func TestDeleteUser_PromotesOldestRemainingMemberWhenSoleOwnerLeaves(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	ownerIDStr, _ := seedUser(t, d)
	ownerID := mustParseUserID(t, ownerIDStr)
	memberIDStr, _ := seedUser(t, d)
	memberID := mustParseUserID(t, memberIDStr)

	orgID := seedOrg(t, d, "Owner Plus Member")
	base := time.Now().Add(-time.Hour)
	seedMember(t, d, orgID, ownerID, base, "owner")
	seedMember(t, d, orgID, memberID, base.Add(time.Minute)) // plain member, no role row
	pollID := seedPollForUser(t, d, orgID, ownerID)
	actor := actorSession(t, d)

	if err := admin.DeleteUser(ctx, d, nil, actor, ownerIDStr, "gdpr request"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if !orgExists(t, d, orgID) {
		t.Fatal("org was deleted even though a plain member remained to promote")
	}
	if !pollExists(t, d, pollID) {
		t.Error("poll was deleted even though its org survived")
	}

	roles := memberRoles(t, d, orgID)
	if _, stillOwner := roles[ownerIDStr]; stillOwner {
		t.Error("departed owner's own member row is still present")
	}
	if got, ok := roles[memberIDStr]; !ok || len(got) != 1 || got[0] != "owner" {
		t.Errorf("promoted member's roles = %v, want [owner]", got)
	}
}
