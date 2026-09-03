package admin_test

// Task 4's own HTTP-surface tests, per the task brief: a staff-gate table test (non-staff ->
// 403, anonymous -> 401, on EVERY route), each route's happy path, retry resurrecting a dead job
// plus its own audit row, and an explicit assertion that GET jobs/failed never leaks a payload.
//
// Unlike bookings/polls' own handlers_test.go (a fakeAuth standing in for *auth.Service), this
// file drives a REAL auth.Service behind a real httptest.Server — admin.Register's own signature
// takes a concrete *auth.Service (not an interface), and RequireStaff itself (a real session,
// actually resolved from a cookie, actually carrying a real staff_users row) is exactly the thing
// the staff-gate test needs to exercise end-to-end. newAdminHTTPHarness below reuses the
// *authHarness type users_test.go already established (same package) rather than inventing a
// second one, extending only its mux (mounting admin.Register alongside the Limen auth handler
// users_test.go's own newAuthHarness mounts).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/admin"
	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/testdb"
)

// newAdminHTTPHarness is newAuthHarness (users_test.go) plus admin.Register mounted on the same
// mux, before the server starts — Register needs the real *auth.Service newAuthHarness itself
// builds, so this can't just wrap that constructor; it has to duplicate its shape.
func newAdminHTTPHarness(t *testing.T, sqlDB *sql.DB) *authHarness {
	t.Helper()
	cfg := &config.Config{AppURL: "http://app.example", LimenSecret: make([]byte, 32)}
	svc, err := auth.New(cfg, sqlDB)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", svc.Handler())
	admin.Register(mux, svc, sqlDB)

	server := httptest.NewServer(svc.Middleware(mux))
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &authHarness{svc: svc, server: server, client: &http.Client{Jar: jar}}
}

// makeStaff flags email as platform staff via the real auth.Service — the next request that
// resolves a session for that user picks it up (session.go's own staff query runs fresh on every
// request; nothing here is cached).
func (h *authHarness) makeStaff(t *testing.T, email string) {
	t.Helper()
	if err := h.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff: %v", err)
	}
}

// staffClient signs up and signs in a fresh user on their own cookie jar, flags them staff, and
// returns the client to drive requests as that caller.
func staffClient(t *testing.T, h *authHarness, email string) *http.Client {
	t.Helper()
	client := h.newClient(t)
	h.postJSONWith(t, client, "/api/v1/auth/signup/credential", map[string]any{"email": email, "password": harnessPassword})
	h.markVerified(t, email)
	h.postJSONWith(t, client, "/api/v1/auth/signin/credential", map[string]any{"credential": email, "password": harnessPassword})
	h.makeStaff(t, email)
	return client
}

// requestJSON drives one request against h's server: body (nil for none) is JSON-marshalled as
// the request body. The caller owns closing the returned response's Body.
func (h *authHarness) requestJSON(t *testing.T, client *http.Client, method, path string, body map[string]any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("building %s %s: %v", method, path, err)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// userIDByEmail looks up a signed-up user's own bigint id, for building a "target myself" URL in
// the self-guard tests below.
func userIDByEmail(t *testing.T, d *sql.DB, email string) string {
	t.Helper()
	var id int64
	if err := d.QueryRowContext(context.Background(), `SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatalf("looking up user id for %q: %v", email, err)
	}
	return fmt.Sprint(id)
}

// seedDeadJob schedules a one-attempt "mail:send" job carrying a recipient address and a token in
// its payload (exactly what GET jobs/failed must never echo back), claims it, and fails it once —
// dead-lettering it immediately (max_attempts: 1) — returning its id.
func seedDeadJob(t *testing.T, d *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind:        "mail:send",
		RunAt:       time.Now().Add(-time.Second),
		Payload:     map[string]string{"to": "secret-recipient@example.com", "token": "super-secret-token"},
		MaxAttempts: 1,
	}); err != nil {
		t.Fatalf("scheduling dead-letter job: %v", err)
	}
	claimed, err := jobs.ClaimDue(ctx, d, "handlers-test-worker", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	var id string
	for _, j := range claimed {
		if j.Kind == "mail:send" {
			id = j.ID
		}
	}
	if id == "" {
		t.Fatal("seeded job was not claimed")
	}
	if _, err := jobs.Fail(ctx, d, id, "smtp: connection refused"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	return id
}

// seedLiveClaimedJob schedules a "mail:send" job with room to fail more than once
// (max_attempts: 3) and claims it — attempts=1, still < max_attempts, i.e. live: neither
// dead-lettered nor untouched. Used by TestHandleRetryJob_LiveJobReturns409 to prove Retry is
// refused for anything jobs.Dead itself wouldn't surface.
func seedLiveClaimedJob(t *testing.T, d *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind:        "mail:send",
		RunAt:       time.Now().Add(-time.Second),
		Payload:     map[string]string{"to": "still-live@example.com"},
		MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("scheduling live job: %v", err)
	}
	claimed, err := jobs.ClaimDue(ctx, d, "handlers-test-worker-live", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	var id string
	for _, j := range claimed {
		if j.Kind == "mail:send" {
			id = j.ID
		}
	}
	if id == "" {
		t.Fatal("seeded live job was not claimed")
	}
	return id
}

// --- staff gate ---------------------------------------------------------------------------------

// adminRoutes is the full endpoint table this task's brief specifies — every one of them must be
// unreachable by anyone but staff. Bodies don't matter for the gate itself: RequireStaff rejects
// the request before any handler ever reads one.
var adminRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/v1/admin/stats"},
	{http.MethodGet, "/api/v1/admin/users"},
	{http.MethodGet, "/api/v1/admin/users/1"},
	{http.MethodPost, "/api/v1/admin/users/1/lock"},
	{http.MethodPost, "/api/v1/admin/users/1/unlock"},
	{http.MethodDelete, "/api/v1/admin/users/1"},
	{http.MethodGet, "/api/v1/admin/audit"},
	{http.MethodGet, "/api/v1/admin/jobs/failed"},
	{http.MethodPost, "/api/v1/admin/jobs/1/retry"},
}

func TestRequireStaff_RejectsNonStaffSessionOnEveryRoute(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	h.signUpAndSignIn(t, "plain-user@example.com")

	for _, rt := range adminRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			resp := h.requestJSON(t, h.client, rt.method, rt.path, nil)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
			}
		})
	}
}

func TestRequireStaff_RejectsAnonymousOnEveryRoute(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	anon := h.newClient(t)

	for _, rt := range adminRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			resp := h.requestJSON(t, anon, rt.method, rt.path, nil)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

// --- happy paths ---------------------------------------------------------------------------------

func TestHandleStats_ReturnsDashboardStats(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-stats@example.com")

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/stats", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var stats admin.DashboardStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Users.Total < 1 {
		t.Errorf("Users.Total = %d, want >= 1 (the staff caller itself)", stats.Users.Total)
	}
}

func TestHandleSearchUsers_FindsSeededUser(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-search@example.com")
	targetID, targetEmail := seedUser(t, d)

	q := url.Values{"query": {targetEmail}}
	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/users?"+q.Encode(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Users      []admin.AdminUserRow `json:"users"`
		NextCursor string               `json:"nextCursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsUserID(out.Users, targetID) {
		t.Errorf("users = %+v, want to include %s", out.Users, targetID)
	}
}

func TestHandleUserDetail_ReturnsDetailForKnownUser(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-detail@example.com")
	targetID, _ := seedUser(t, d)

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/users/"+targetID, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var detail admin.AdminUserDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.ID != targetID {
		t.Errorf("ID = %q, want %q", detail.ID, targetID)
	}
}

func TestHandleUserDetail_UnknownIDReturns404(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-detail-404@example.com")

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/users/does-not-exist", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleLockUser_LocksAndAudits(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-lock@example.com")
	targetID, _ := seedUser(t, d)

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+targetID+"/lock", map[string]any{"reason": "abuse report"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var locked bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)`, mustParseUserID(t, targetID),
	).Scan(&locked); err != nil {
		t.Fatalf("checking locked_users: %v", err)
	}
	if !locked {
		t.Error("user is not locked after POST .../lock")
	}
	if !auditRowExists(t, d, "lock-user", targetID) {
		t.Error("POST .../lock left no admin_audit_log row")
	}
}

func TestHandleLockUser_RequiresReason(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-lock-reason@example.com")
	targetID, _ := seedUser(t, d)

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+targetID+"/lock", map[string]any{"reason": "   "})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleLockUser_RejectsSelfTarget(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	email := "staff-self-lock@example.com"
	client := staffClient(t, h, email)
	selfID := userIDByEmail(t, d, email)

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+selfID+"/lock", map[string]any{"reason": "oops"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (self-lock must be rejected)", resp.StatusCode)
	}

	var locked bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)`, mustParseUserID(t, selfID),
	).Scan(&locked); err != nil {
		t.Fatalf("checking locked_users: %v", err)
	}
	if locked {
		t.Error("self-lock request must not actually lock the caller")
	}
}

// TestHandleLockUser_RejectsSelfTargetWithPaddedID is M4's own regression test: the self-target
// guard used to compare the path {id} against actor.UserID byte-for-byte, so a staff member could
// self-target by padding their own id with a leading zero (a string ServeMux never normalizes,
// and strconv.ParseInt happily accepts) — same underlying user, different string, guard skipped.
func TestHandleLockUser_RejectsSelfTargetWithPaddedID(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	email := "staff-self-lock-padded@example.com"
	client := staffClient(t, h, email)
	selfID := userIDByEmail(t, d, email)
	paddedID := "0" + selfID // same underlying id once parsed; never equal to selfID as a string

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+paddedID+"/lock", map[string]any{"reason": "oops"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (a padded self-id must still be recognized as self)", resp.StatusCode)
	}

	var locked bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)`, mustParseUserID(t, selfID),
	).Scan(&locked); err != nil {
		t.Fatalf("checking locked_users: %v", err)
	}
	if locked {
		t.Error("padded self-lock request must not actually lock the caller")
	}
}

// TestHandleLockUser_SelfTargetWinsOverEmptyReason is M11's own regression test: a self-lock
// request with a blank reason must report the self-target error, not "reason is required" — the
// self-target condition is checked first specifically so its message wins.
func TestHandleLockUser_SelfTargetWinsOverEmptyReason(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	email := "staff-self-lock-empty-reason@example.com"
	client := staffClient(t, h, email)
	selfID := userIDByEmail(t, d, email)

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+selfID+"/lock", map[string]any{"reason": ""})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Fields["id"] != "self" {
		t.Errorf(`error.fields = %v, want {"id":"self"} — the self-target message must win over the empty-reason one`, body.Error.Fields)
	}
}

// TestHandleDeleteUser_RejectsSelfTargetWithPaddedID mirrors the lock-side padded-id test above
// for DeleteUser's own guard.
func TestHandleDeleteUser_RejectsSelfTargetWithPaddedID(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	email := "staff-self-delete-padded@example.com"
	client := staffClient(t, h, email)
	selfID := userIDByEmail(t, d, email)
	paddedID := "0" + selfID

	resp := h.requestJSON(t, client, http.MethodDelete, "/api/v1/admin/users/"+paddedID, map[string]any{"reason": "oops"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (a padded self-id must still be recognized as self)", resp.StatusCode)
	}

	var stillExists bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, mustParseUserID(t, selfID),
	).Scan(&stillExists); err != nil {
		t.Fatalf("checking users: %v", err)
	}
	if !stillExists {
		t.Error("padded self-delete request must not actually delete the caller")
	}
}

func TestHandleUnlockUser_UnlocksAndAudits(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-unlock@example.com")
	targetID, _ := seedUser(t, d)

	actor := actorSession(t, d)
	if err := admin.LockUser(context.Background(), d, nil, actor, targetID, "initial lock"); err != nil {
		t.Fatalf("seeding lock: %v", err)
	}

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+targetID+"/unlock", map[string]any{"reason": "resolved"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var locked bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)`, mustParseUserID(t, targetID),
	).Scan(&locked); err != nil {
		t.Fatalf("checking locked_users: %v", err)
	}
	if locked {
		t.Error("user is still locked after POST .../unlock")
	}
	if !auditRowExists(t, d, "unlock-user", targetID) {
		t.Error("POST .../unlock left no admin_audit_log row")
	}
}

func TestHandleUnlockUser_RequiresReason(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-unlock-reason@example.com")
	targetID, _ := seedUser(t, d)

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+targetID+"/unlock", map[string]any{"reason": ""})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleDeleteUser_DeletesAndAudits(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-delete@example.com")
	targetID, _ := seedUser(t, d)

	resp := h.requestJSON(t, client, http.MethodDelete, "/api/v1/admin/users/"+targetID, map[string]any{"reason": "gdpr request"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var stillExists bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, mustParseUserID(t, targetID),
	).Scan(&stillExists); err != nil {
		t.Fatalf("checking users: %v", err)
	}
	if stillExists {
		t.Error("user still exists after DELETE")
	}
	if !auditRowExists(t, d, "delete-user", targetID) {
		t.Error("DELETE left no admin_audit_log row")
	}
}

func TestHandleDeleteUser_RequiresReason(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-delete-reason@example.com")
	targetID, _ := seedUser(t, d)

	resp := h.requestJSON(t, client, http.MethodDelete, "/api/v1/admin/users/"+targetID, map[string]any{"reason": " "})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleDeleteUser_RejectsSelfTarget(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	email := "staff-self-delete@example.com"
	client := staffClient(t, h, email)
	selfID := userIDByEmail(t, d, email)

	resp := h.requestJSON(t, client, http.MethodDelete, "/api/v1/admin/users/"+selfID, map[string]any{"reason": "oops"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (self-delete must be rejected)", resp.StatusCode)
	}

	var stillExists bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, mustParseUserID(t, selfID),
	).Scan(&stillExists); err != nil {
		t.Fatalf("checking users: %v", err)
	}
	if !stillExists {
		t.Error("self-delete request must not actually delete the caller")
	}
}

func TestHandleAuditList_ReturnsRecordedActions(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-audit@example.com")
	targetID, _ := seedUser(t, d)

	lockResp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+targetID+"/lock", map[string]any{"reason": "abuse report"})
	_ = lockResp.Body.Close()
	if lockResp.StatusCode != http.StatusOK {
		t.Fatalf("seeding lock via HTTP: status %d", lockResp.StatusCode)
	}

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/audit?action=lock-user", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Entries    []admin.AuditEntry `json:"entries"`
		NextCursor string             `json:"nextCursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, e := range out.Entries {
		if e.Action == "lock-user" && e.TargetID != nil && *e.TargetID == targetID {
			found = true
		}
	}
	if !found {
		t.Errorf("entries = %+v, want a lock-user row targeting %s", out.Entries, targetID)
	}
}

// TestHandleAuditList_FiltersByTargetTypeAndTargetIDAndReportsTotal is I3's own regression test:
// handleAuditList must read targetType/targetId off the query string (List already supported both
// as AuditFilter fields; only the handler was missing them) and the response must include a
// "total" independent of the page a limit/cursor happens to return.
func TestHandleAuditList_FiltersByTargetTypeAndTargetIDAndReportsTotal(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-audit-filters@example.com")
	targetA, _ := seedUser(t, d)
	targetB, _ := seedUser(t, d)

	for _, id := range []string{targetA, targetA, targetB} {
		resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+id+"/lock", map[string]any{"reason": "r"})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("seeding lock for %s: status %d", id, resp.StatusCode)
		}
		unlockResp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/users/"+id+"/unlock", map[string]any{"reason": "r"})
		_ = unlockResp.Body.Close()
	}

	q := url.Values{"targetType": {"user"}, "targetId": {targetA}, "action": {"lock-user"}, "limit": {"1"}}
	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/audit?"+q.Encode(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Entries    []admin.AuditEntry `json:"entries"`
		NextCursor string             `json:"nextCursor"`
		Total      int64              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries = %+v, want exactly 1 (limit=1)", out.Entries)
	}
	if out.Entries[0].TargetID == nil || *out.Entries[0].TargetID != targetA {
		t.Errorf("entries[0].TargetID = %v, want %s", out.Entries[0].TargetID, targetA)
	}
	// Exactly one lock-user row targets targetA (the two-lock/unlock sequence above locked it
	// once each iteration, but only the FIRST of the two matching iterations for targetA counts —
	// targetA appears twice in the seed loop, so two lock-user rows target it).
	if out.Total != 2 {
		t.Errorf("total = %d, want 2 (independent of limit=1)", out.Total)
	}
	if out.NextCursor == "" {
		t.Error("nextCursor is empty, want a next-page cursor (total=2 > limit=1)")
	}
}

func TestHandleSearchUsers_ReportsTotalIndependentOfLimit(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-search-total@example.com")
	seedUserWithName(t, d, "Marie", "Curie-A")
	seedUserWithName(t, d, "Marie", "Curie-B")

	q := url.Values{"query": {"Marie"}, "limit": {"1"}}
	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/users?"+q.Encode(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Users      []admin.AdminUserRow `json:"users"`
		NextCursor string               `json:"nextCursor"`
		Total      int64                `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Users) != 1 {
		t.Fatalf("users = %+v, want exactly 1 (limit=1)", out.Users)
	}
	if out.Total != 2 {
		t.Errorf("total = %d, want 2 (independent of limit=1)", out.Total)
	}
}

func TestHandleFailedJobs_OmitsPayloadIncludesOtherFields(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-jobs@example.com")
	jobID := seedDeadJob(t, d)

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/jobs/failed", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	if strings.Contains(string(body), "secret-recipient@example.com") || strings.Contains(string(body), "super-secret-token") {
		t.Errorf("response leaked the dead job's payload contents: %s", body)
	}

	var raw struct {
		Jobs []map[string]any `json:"jobs"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode (raw): %v", err)
	}
	for _, j := range raw.Jobs {
		if _, ok := j["payload"]; ok {
			t.Errorf("job %v: response included a \"payload\" field, want it omitted entirely", j)
		}
	}

	var out struct {
		Jobs []admin.FailedJobView `json:"jobs"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found *admin.FailedJobView
	for i, j := range out.Jobs {
		if j.ID == jobID {
			found = &out.Jobs[i]
		}
	}
	if found == nil {
		t.Fatalf("jobs = %+v, want to include dead-lettered job %s", out.Jobs, jobID)
	}
	if found.Kind != "mail:send" {
		t.Errorf("Kind = %q, want mail:send", found.Kind)
	}
	if found.Attempts < 1 {
		t.Errorf("Attempts = %d, want >= 1", found.Attempts)
	}
	if found.LastError == nil || !strings.Contains(*found.LastError, "smtp") {
		t.Errorf("LastError = %v, want to contain the failure text", found.LastError)
	}
	if found.RunAt == "" {
		t.Error("RunAt = \"\", want a formatted timestamp")
	}
}

func TestHandleRetryJob_ResurrectsDeadJobAndAudits(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-retry@example.com")
	jobID := seedDeadJob(t, d)

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/jobs/"+jobID+"/retry", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ctx := context.Background()
	claimed, err := jobs.ClaimDue(ctx, d, "post-retry-worker", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	found := false
	for _, j := range claimed {
		if j.ID == jobID {
			found = true
		}
	}
	if !found {
		t.Error("retried job was not claimable — Retry should have reset attempts/run_at")
	}
	if !auditRowExists(t, d, "job.retry", jobID) {
		t.Error("POST .../retry left no admin_audit_log row")
	}
}

// TestHandleRetryJob_LiveJobReturns409 is I2's own regression test: retrying a job that exists
// but isn't dead-lettered (attempts < max_attempts — here, one that's already claimed and being
// worked) must be refused with 409 "conflict", not silently reset out from under whatever already
// has it claimed.
func TestHandleRetryJob_LiveJobReturns409(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-retry-live@example.com")
	jobID := seedLiveClaimedJob(t, d)

	var attemptsBefore, lockedByBefore sql.NullString
	if err := d.QueryRowContext(context.Background(),
		`SELECT attempts::text, locked_by FROM scheduled_jobs WHERE id = $1`, jobID,
	).Scan(&attemptsBefore, &lockedByBefore); err != nil {
		t.Fatalf("reading job state before retry: %v", err)
	}

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/jobs/"+jobID+"/retry", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "conflict" {
		t.Errorf("error.code = %q, want conflict", body.Error.Code)
	}

	var attemptsAfter, lockedByAfter sql.NullString
	if err := d.QueryRowContext(context.Background(),
		`SELECT attempts::text, locked_by FROM scheduled_jobs WHERE id = $1`, jobID,
	).Scan(&attemptsAfter, &lockedByAfter); err != nil {
		t.Fatalf("reading job state after retry: %v", err)
	}
	if attemptsAfter != attemptsBefore || lockedByAfter != lockedByBefore {
		t.Errorf("job state changed after a rejected retry: before (attempts=%v, locked_by=%v), after (attempts=%v, locked_by=%v)",
			attemptsBefore, lockedByBefore, attemptsAfter, lockedByAfter)
	}
	if auditRowExists(t, d, "job.retry", jobID) {
		t.Error("rejected retry left a job.retry audit row")
	}
}

func TestHandleRetryJob_UnknownIDReturns404(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-retry-404@example.com")

	resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/jobs/does-not-exist/retry", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
