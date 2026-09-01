package polls_test

// Tests internal/polls/handlers.go: httptest through the full middleware chain (session
// resolution -> route -> handler), using a fakeAuth standing in for *auth.Service via the
// polls.Auth seam (see handlers.go's own doc comment on why: a real signup/signin flow through
// Limen for every one of this file's cases would be prohibitively slow and mostly test Limen, not
// this package's handlers). One test per endpoint-table row (some folded together where they
// share setup, noted at each), plus a dedicated test per accumulated review requirement (a)-(e).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

// ---- test harness -------------------------------------------------------------------------

// fakeAuth implements polls.Auth without touching Limen: sessions are logged in by userID and
// selected per-request via the X-Test-Session header (the harness's stand-in for a real session
// cookie), and guest tokens are a trivially invertible "guest-token-for-<id>" pair — this package
// never re-verifies MintGuestToken/VerifyGuestToken's own cryptography (internal/auth/guest_test.go
// already does), so the fake only needs to preserve the *contract* those two methods have with
// each other and with handlers.go.
type fakeAuth struct {
	sessions map[string]*auth.Session
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{sessions: map[string]*auth.Session{}}
}

// login registers sess under its own UserID, returning that UserID for convenience — pass it (or
// call sessHeader(userID)) as the X-Test-Session header value on a request to act as that caller.
func (f *fakeAuth) login(sess *auth.Session) string {
	f.sessions[sess.UserID] = sess
	return sess.UserID
}

type fakeSessionKey struct{}

// Middleware is this fake's stand-in for auth.Service.Middleware: resolves X-Test-Session into
// context for EVERY request (mirroring the real middleware wrapping the whole mux, not just
// RequireSession-gated routes) — public handlers that call FromContext directly (GetView,
// AddParticipant, ...) need this to see a signed-in caller too.
func (f *fakeAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Test-Session"); id != "" {
			if sess, ok := f.sessions[id]; ok {
				r = r.WithContext(context.WithValue(r.Context(), fakeSessionKey{}, sess))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (f *fakeAuth) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := f.FromContext(r.Context()); !ok {
			httpserver.Err(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
			return
		}
		next(w, r)
	}
}

func (f *fakeAuth) FromContext(ctx context.Context) (*auth.Session, bool) {
	sess, ok := ctx.Value(fakeSessionKey{}).(*auth.Session)
	return sess, ok
}

func (f *fakeAuth) VerifyGuestToken(token string) (string, bool) {
	const prefix = "guest-token-for-"
	if !strings.HasPrefix(token, prefix) {
		return "", false
	}
	return strings.TrimPrefix(token, prefix), true
}

func (f *fakeAuth) MintGuestToken(participantID string) string {
	return "guest-token-for-" + participantID
}

// sessHeader is shorthand for the header map doRequest expects, keyed by a userID fakeAuth.login
// already returned/registered.
func sessHeader(userID string) map[string]string {
	return map[string]string{"X-Test-Session": userID}
}

// testConfig builds a minimal valid *config.Config — no optional capability on — the same shape
// internal/httpserver's own tests use.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "https://whenweall.example", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// testConfigWithTurnstile is testConfig plus a configured (fake) Turnstile key pair, so
// cfg.Capabilities.Turnstile is on — used by the captcha-gating test.
func testConfigWithTurnstile(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "https://whenweall.example", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
		"TURNSTILE_SITE_KEY": "site-key", "TURNSTILE_SECRET_KEY": "secret-key",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// newTestHandler builds a *polls.Service bound to d, registers its whole HTTP surface on a fresh
// mux via a fakeAuth, and wraps that mux in the fake's own Middleware — the same shape
// internal/httpserver.Server.Handler wraps authSvc.Middleware around its mux, just without the
// rest of that stack (SecurityHeaders/RequestLogger/Recover/CheckOrigin are internal/httpserver's
// own, already covered by that package's tests).
func newTestHandler(d *sql.DB, cfg *config.Config) (http.Handler, *fakeAuth, *polls.Service) {
	s := polls.NewService(d)
	a := newFakeAuth()
	mux := http.NewServeMux()
	s.Register(mux, a, cfg)
	return a.Middleware(mux), a, s
}

// doRequest builds and serves one request against h. body, if non-nil, is JSON-marshaled and
// sent with a Content-Type header; headers are applied after that (so a caller can still override
// Content-Type if it ever needs to).
func doRequest(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeBody JSON-decodes rec's body into T, failing the test on any decode error.
func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response body %s: %v", rec.Body.String(), err)
	}
	return out
}

// errCode extracts the {"error":{"code":...}} envelope's code from rec's body.
func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope %s: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

// digestEventsFor reads every accumulated "poll.digest" job item queued for pollID (across
// whichever single accumulator row EnqueueDigestItem has been upserting into) and returns the set
// of distinct event names present — this file's evidence for accumulated requirement (d).
func digestEventsFor(t *testing.T, d *sql.DB, pollID string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, j := range listJobs(t, d, "poll.digest") {
		if !j.RoomKey.Valid || j.RoomKey.String != "poll:"+pollID {
			continue
		}
		var payload struct {
			Items []struct {
				Event string `json:"event"`
			} `json:"items"`
		}
		if err := json.Unmarshal(j.Payload, &payload); err != nil {
			t.Fatalf("decode poll.digest payload %s: %v", j.Payload, err)
		}
		for _, item := range payload.Items {
			out[item.Event] = true
		}
	}
	return out
}

// withSiteverifyStubT points httpserver.TurnstileSiteverifyURL at ts for the duration of the
// current test — the polls_test package's own copy of turnstile_test.go's withSiteverifyStub
// (unexported there, so it can't be reused directly across packages).
func withSiteverifyStubT(t *testing.T, ts *httptest.Server) {
	t.Helper()
	orig := httpserver.TurnstileSiteverifyURL
	httpserver.TurnstileSiteverifyURL = ts.URL
	t.Cleanup(func() {
		httpserver.TurnstileSiteverifyURL = orig
		ts.Close()
	})
}

func turnstileStub(success bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": success})
	}))
}

// ---- row 1: POST /api/v1/polls (auth+org -> Create) ----------------------------------------

func TestHandlerCreate(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, _ := newTestHandler(d, cfg)
	orgID, userID := seedOrgAndUser(t, d)
	a.login(&auth.Session{UserID: userID, ActiveOrgID: orgID})

	body := map[string]any{
		"type": "datetime", "title": "Team sync", "timezone": "UTC",
		"options": []map[string]any{{"kind": "datetime", "startAt": tomorrowAt("10:00")}},
	}
	rec := doRequest(t, h, "POST", "/api/v1/polls", body, sessHeader(userID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[polls.PollView](t, rec)
	if view.Title != "Team sync" || !view.IsOwner {
		t.Errorf("view = %+v, want Title=Team sync IsOwner=true", view)
	}

	t.Run("401 without a session", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/polls", body, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("422 invalid with field errors for a missing title", func(t *testing.T) {
		bad := map[string]any{"type": "datetime", "timezone": "UTC", "options": body["options"]}
		rec := doRequest(t, h, "POST", "/api/v1/polls", bad, sessHeader(userID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid" {
			t.Errorf("code = %q, want invalid", errCode(t, rec))
		}
	})
}

// ---- row 2: GET /api/v1/polls/{id} (public -> GetView) -------------------------------------

func TestHandlerGetView(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[polls.PollView](t, rec)
	if view.ID != created.ID {
		t.Errorf("ID = %q, want %q", view.ID, created.ID)
	}

	t.Run("404 JSON for an unknown id", func(t *testing.T) {
		rec := doRequest(t, h, "GET", "/api/v1/polls/does-not-exist", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		if errCode(t, rec) != "not_found" {
			t.Errorf("code = %q, want not_found", errCode(t, rec))
		}
	})
}

// ---- row 8: GET /api/v1/polls (auth+org -> ListMine) ----------------------------------------

func TestHandlerListMine(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	createTestPoll(t, ctx, s, orgID, ownerID)
	createTestPoll(t, ctx, s, orgID, ownerID)
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	rec := doRequest(t, h, "GET", "/api/v1/polls", nil, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	summaries := decodeBody[[]polls.PollSummary](t, rec)
	if len(summaries) != 2 {
		t.Errorf("len(summaries) = %d, want 2", len(summaries))
	}
}

// ---- rows 9-13, 4(d): participants/comments + digest wiring --------------------------------

// TestHandlerAddParticipant covers row 9, including the anonymous-caller guestToken.
func TestHandlerAddParticipant(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	body := map[string]any{"name": "Ada", "answers": map[string]string{created.Options[0].ID: "yes"}}
	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	resp := decodeBody[map[string]any](t, rec)
	if resp["participantId"] == "" || resp["participantId"] == nil {
		t.Errorf("participantId missing: %+v", resp)
	}
	if resp["guestToken"] == "" || resp["guestToken"] == nil {
		t.Errorf("guestToken missing for an anonymous participant: %+v", resp)
	}
}

// TestHandlerParticipantAndCommentDigestWiring covers rows 10-13 (UpdateParticipant,
// RemoveParticipant, AddComment, DeleteComment) content-wise, AND is this file's evidence for
// accumulated requirement (d) for every non-claim event: response.created/updated/withdrawn and
// comment.created all land in the poll's digest accumulator.
func TestHandlerParticipantAndCommentDigestWiring(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	addBody := map[string]any{"name": "Ada", "answers": map[string]string{created.Options[0].ID: "yes"}}
	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", addBody, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("AddParticipant: status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	addResp := decodeBody[map[string]any](t, rec)
	participantID, _ := addResp["participantId"].(string)

	updBody := map[string]any{"name": "Ada B", "answers": map[string]string{created.Options[0].ID: "no"}}
	rec = doRequest(t, h, "PATCH", "/api/v1/polls/"+created.ID+"/participants/"+participantID, updBody, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateParticipant: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view, err := s.GetView(ctx, created.ID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if p := findParticipant(view, participantID); p == nil || p.Name != "Ada B" {
		t.Errorf("participant after update = %+v, want Name=Ada B", p)
	}

	commentBody := map[string]any{"authorName": "Ada", "body": "hello"}
	rec = doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/comments", commentBody, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("AddComment: status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	comment := decodeBody[map[string]any](t, rec)
	commentID, _ := comment["id"].(string)
	if comment["body"] != "hello" {
		t.Errorf("comment body = %v, want hello", comment["body"])
	}

	rec = doRequest(t, h, "DELETE", "/api/v1/polls/"+created.ID+"/comments/"+commentID, nil, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("DeleteComment: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view, err = s.GetView(ctx, created.ID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if findComment(view, commentID) != nil {
		t.Error("comment still present after DeleteComment")
	}

	rec = doRequest(t, h, "DELETE", "/api/v1/polls/"+created.ID+"/participants/"+participantID, nil, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("RemoveParticipant: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view, err = s.GetView(ctx, created.ID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if findParticipant(view, participantID) != nil {
		t.Error("participant still present after RemoveParticipant")
	}

	events := digestEventsFor(t, d, created.ID)
	for _, want := range []string{"response.created", "response.updated", "comment.created", "response.withdrawn"} {
		if !events[want] {
			t.Errorf("requirement (d): missing digest item for event %q; got %v", want, events)
		}
	}
}

// ---- rows 14-15, (d)/(b): claims -------------------------------------------------------------

// TestHandlerClaim covers row 14, including the 409 capacity_full case the brief calls out
// explicitly, and (as part of requirement (d)) the response.created + signup.full digest items a
// claim that fills the sheet's only slot raises.
func TestHandlerClaim(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(1)}, 0)
	slot := created.Options[0].ID

	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
		map[string]any{"optionId": slot, "name": "Ada"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first claim: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	resp := decodeBody[map[string]any](t, rec)
	if resp["participantId"] == "" || resp["guestToken"] == "" || resp["guestToken"] == nil {
		t.Errorf("expected participantId+guestToken for a new anonymous claimant: %+v", resp)
	}

	t.Run("409 capacity_full for a second distinct claimant", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
			map[string]any{"optionId": slot, "name": "Bob"}, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "capacity_full" {
			t.Errorf("code = %q, want capacity_full", errCode(t, rec))
		}
	})

	events := digestEventsFor(t, d, created.ID)
	if !events["response.created"] {
		t.Errorf("requirement (d): missing response.created digest item; got %v", events)
	}
	if !events["signup.full"] {
		t.Errorf("requirement (d): missing signup.full digest item after filling the sheet's only slot; got %v", events)
	}
}

// TestHandlerUnclaim covers row 15's self-service path, plus requirement (d)'s
// response.withdrawn digest item for it.
func TestHandlerUnclaim(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	slot := created.Options[0].ID

	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
		map[string]any{"optionId": slot, "name": "Ada"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Claim: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	resp := decodeBody[map[string]any](t, rec)
	participantID, _ := resp["participantId"].(string)

	token := a.MintGuestToken(participantID)
	rec = doRequest(t, h, "DELETE", "/api/v1/polls/"+created.ID+"/claims/"+slot, nil,
		map[string]string{"X-Guest-Token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("Unclaim: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	view, err := s.GetView(ctx, created.ID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if p := findParticipant(view, participantID); p != nil && len(p.Votes) != 0 {
		t.Errorf("expected the claim to be removed, votes = %+v", p.Votes)
	}

	if !digestEventsFor(t, d, created.ID)["response.withdrawn"] {
		t.Error("requirement (d): missing response.withdrawn digest item after self-service unclaim")
	}
}

// ---- rows 16-17: calendar/roster --------------------------------------------------------------

func TestHandlerCalendarICS(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	t.Run("404 before the poll is finalized", func(t *testing.T) {
		rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID+"/calendar.ics", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})

	if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID+"/calendar.ics", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("content-type = %q, want text/calendar", ct)
	}
	if !strings.Contains(rec.Body.String(), "BEGIN:VCALENDAR") {
		t.Errorf("body missing VCALENDAR: %s", rec.Body.String())
	}
}

func TestHandlerRosterCSV(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(1)}, 0)
	if _, err := s.Claim(ctx, created.ID, created.Options[0].ID,
		polls.ClaimInput{Name: "Ada", Email: strPtr("ada@example.com")}, polls.Viewer{}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, memberID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, memberID, "member")
	a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID})
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	t.Run("403 for a plain org member (roster export is owner-only)", func(t *testing.T) {
		rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID+"/roster.csv", nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("200 csv for the creator", func(t *testing.T) {
		rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID+"/roster.csv", nil, sessHeader(ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("content-type = %q, want text/csv", ct)
		}
		if !strings.Contains(rec.Body.String(), "Ada") {
			t.Errorf("csv missing claimant: %s", rec.Body.String())
		}
	})
}

// ---- rows 18-19: notification prefs/following -------------------------------------------------

func TestHandlerUpdateNotificationPrefs(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	body := map[string]any{
		"pollId":   created.ID,
		"channels": map[string]any{"response.created": map[string]bool{"email": false, "push": false}},
	}
	rec := doRequest(t, h, "POST", "/api/v1/me/notification-prefs", body, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

func TestHandlerSetFollowing(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/following",
		map[string]any{"following": true}, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// ---- row 20: GET /api/v1/config ---------------------------------------------------------------

func TestHandlerConfig(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, _ := newTestHandler(d, cfg)

	rec := doRequest(t, h, "GET", "/api/v1/config", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	body := decodeBody[map[string]any](t, rec)
	if _, has := body["turnstileSiteKey"]; has {
		t.Errorf("turnstileSiteKey should be omitted when Turnstile is not configured: %+v", body)
	}
	if googleEnabled, _ := body["googleEnabled"].(bool); googleEnabled {
		t.Errorf("googleEnabled = true, want false")
	}
}

// ================================================================================================
// Accumulated review requirements (a)-(e)
// ================================================================================================

// TestHandlerAuthzRetrofitAcrossManagingEndpoints is requirement (a)'s evidence: a plain org
// member (no managing role, not the poll's creator) gets 403 on PATCH/status/finalize/delete/
// duplicate of another member's poll; the creator and an org admin (neither is the other) both
// succeed. Also covers rows 3-7's status codes.
func TestHandlerAuthzRetrofitAcrossManagingEndpoints(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	_, memberID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, memberID, "member")
	_, adminID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, adminID, "admin")
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})
	a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID})
	a.login(&auth.Session{UserID: adminID, ActiveOrgID: orgID})

	cases := []struct {
		name         string
		method, path string
		body         func(pollID, optionID string) map[string]any
	}{
		{"update", "PATCH", "", func(string, string) map[string]any { return map[string]any{"title": "Renamed"} }},
		{"status", "POST", "/status", func(string, string) map[string]any { return map[string]any{"status": "closed"} }},
		{"finalize", "POST", "/finalize", func(_, optionID string) map[string]any { return map[string]any{"optionId": optionID} }},
		{"delete", "DELETE", "", func(string, string) map[string]any { return nil }},
		{"duplicate", "POST", "/duplicate", func(string, string) map[string]any { return nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/forbidden for a plain org member", func(t *testing.T) {
			created := createTestPoll(t, ctx, s, orgID, ownerID)
			rec := doRequest(t, h, tc.method, "/api/v1/polls/"+created.ID+tc.path,
				tc.body(created.ID, created.Options[0].ID), sessHeader(memberID))
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body)
			}
		})
		t.Run(tc.name+"/allowed for the creator", func(t *testing.T) {
			created := createTestPoll(t, ctx, s, orgID, ownerID)
			rec := doRequest(t, h, tc.method, "/api/v1/polls/"+created.ID+tc.path,
				tc.body(created.ID, created.Options[0].ID), sessHeader(ownerID))
			if rec.Code/100 != 2 {
				t.Errorf("status = %d, want 2xx; body=%s", rec.Code, rec.Body)
			}
		})
		t.Run(tc.name+"/allowed for an org admin who did not create the poll", func(t *testing.T) {
			created := createTestPoll(t, ctx, s, orgID, ownerID)
			rec := doRequest(t, h, tc.method, "/api/v1/polls/"+created.ID+tc.path,
				tc.body(created.ID, created.Options[0].ID), sessHeader(adminID))
			if rec.Code/100 != 2 {
				t.Errorf("status = %d, want 2xx; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// TestHandlerManagerForceUnclaim is requirement (b)'s evidence: DELETE .../claims/{oid}?
// participantId=<other> is forbidden for a plain org member and succeeds for a manager
// (the poll's own creator), freeing a slot claimed by someone else entirely.
func TestHandlerManagerForceUnclaim(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	slot := created.Options[0].ID

	result, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Ada"}, polls.Viewer{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, memberID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, memberID, "member")
	a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID})
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	target := "/api/v1/polls/" + created.ID + "/claims/" + slot + "?participantId=" + result.ParticipantID

	t.Run("forbidden for a plain org member", func(t *testing.T) {
		rec := doRequest(t, h, "DELETE", target, nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("succeeds for the poll's creator (a manager)", func(t *testing.T) {
		rec := doRequest(t, h, "DELETE", target, nil, sessHeader(ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		view, err := s.GetView(ctx, created.ID, polls.Viewer{})
		if err != nil {
			t.Fatalf("GetView: %v", err)
		}
		if p := findParticipant(view, result.ParticipantID); p != nil && len(p.Votes) != 0 {
			t.Errorf("expected the forced unclaim to remove the vote, got %+v", p.Votes)
		}
	})
}

// TestHandlerWrongOrgIs404 is requirement (c)'s evidence: a poll that exists, but in a different
// org than the caller's active one, reports 404 not_found — never 403 — so its existence isn't
// leaked outside its own org.
func TestHandlerWrongOrgIs404(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	otherOrgID, otherUserID := seedOrgAndUser(t, d)
	a.login(&auth.Session{UserID: otherUserID, ActiveOrgID: otherOrgID})

	rec := doRequest(t, h, "PATCH", "/api/v1/polls/"+created.ID,
		map[string]any{"title": "x"}, sessHeader(otherUserID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if errCode(t, rec) != "not_found" {
		t.Errorf("code = %q, want not_found", errCode(t, rec))
	}
}

// TestHandlerFinalizeExcludesActorFromSubscriberNotification is requirement (e)'s evidence: the
// caller who finalizes a poll (the "actor") is excluded from the poll.finalized subscriber
// notification, while another subscribed org member still receives it.
func TestHandlerFinalizeExcludesActorFromSubscriberNotification(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	_, adminID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, adminID, "admin")
	_, subscriberID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, subscriberID, "member")

	if err := s.SetFollowing(ctx, created.ID, orgID, adminID, true); err != nil {
		t.Fatalf("SetFollowing(admin): %v", err)
	}
	if err := s.SetFollowing(ctx, created.ID, orgID, subscriberID, true); err != nil {
		t.Fatalf("SetFollowing(subscriber): %v", err)
	}
	a.login(&auth.Session{UserID: adminID, ActiveOrgID: orgID})

	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/finalize",
		map[string]any{"optionId": created.Options[0].ID}, sessHeader(adminID))
	if rec.Code != http.StatusOK {
		t.Fatalf("Finalize: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	finalized := filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "finalized")
	var gotSubscriber, gotActor bool
	for _, p := range finalized {
		if p.UserID == subscriberID {
			gotSubscriber = true
		}
		if p.UserID == adminID {
			gotActor = true
		}
	}
	if !gotSubscriber {
		t.Errorf("expected a finalized mail job for the subscribed non-actor member; got %+v", finalized)
	}
	if gotActor {
		t.Errorf("the acting user should be excluded from the subscriber notification; got %+v", finalized)
	}
}

// TestHandlerCaptchaGatesAnonymousParticipant covers the "public+captcha" column for AddParticipant
// (representative of AddComment/Claim, which share requireCaptchaIfAnon verbatim): an anonymous
// caller is rejected when Turnstile is on and no token verifies, accepted with one that does, and
// a signed-in caller skips the check entirely (the siteverify stub errors unconditionally in that
// last subtest — if it were ever called, that subtest would fail).
func TestHandlerCaptchaGatesAnonymousParticipant(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfigWithTurnstile(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	body := map[string]any{"name": "Guest", "answers": map[string]string{created.Options[0].ID: "yes"}}

	t.Run("anonymous caller with no captcha token is rejected", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "captcha_failed" {
			t.Errorf("code = %q, want captcha_failed", errCode(t, rec))
		}
	})

	t.Run("anonymous caller with a valid captcha token succeeds", func(t *testing.T) {
		withSiteverifyStubT(t, turnstileStub(true))
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body,
			map[string]string{"X-Captcha-Token": "tok"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("signed-in caller skips captcha entirely", func(t *testing.T) {
		withSiteverifyStubT(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})))
		_, memberID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, memberID, "member")
		a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID})

		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body, sessHeader(memberID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (captcha must not be checked for a signed-in caller); body=%s", rec.Code, rec.Body)
		}
	})
}
