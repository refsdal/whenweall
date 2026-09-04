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
	"strconv"
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
	addOrgMember(t, d, orgID, userID, "owner")
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
	addOrgMember(t, d, orgID, ownerID, "owner")
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	// EmailVerified: true — this session stands in for the poll's own creator managing it
	// (UpdateParticipant/DeleteComment/RemoveParticipant below all resolve "is this caller a
	// manager" from viewer.UserID via viewerFromRequest, which now only carries UserID through for
	// a verified session; see the reviewer's finding 4).
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID, EmailVerified: true})

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
	if want := `attachment; filename="whenweall-` + created.ID + `.ics"`; rec.Header().Get("Content-Disposition") != want {
		t.Errorf("Content-Disposition = %q, want %q", rec.Header().Get("Content-Disposition"), want)
	}
	if !strings.Contains(rec.Body.String(), "BEGIN:VCALENDAR") {
		t.Errorf("body missing VCALENDAR: %s", rec.Body.String())
	}

	// The VEVENT's URL property must be built from cfg.AppURL, never the incoming request's
	// Host/X-Forwarded-Proto — those are caller-controlled and this test's own request carries
	// neither a Host override nor an X-Forwarded-Proto header (httptest's default Host is
	// "example.com"), so a body containing testConfig's AppURL (not "example.com") is exactly the
	// proof this handler ignores them.
	wantURL := "URL:" + cfg.AppURL + "/p/" + created.ID
	if !strings.Contains(rec.Body.String(), wantURL) {
		t.Errorf("body missing %q (built from cfg.AppURL): %s", wantURL, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "example.com/p/") {
		t.Errorf("body derived its link from the request's Host header, not cfg.AppURL: %s", rec.Body.String())
	}
}

func TestHandlerRosterCSV(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
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

	t.Run("filename carries the poll id", func(t *testing.T) {
		rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID+"/roster.csv", nil, sessHeader(ownerID))
		want := `attachment; filename="whenweall-` + created.ID + `-roster.csv"`
		if got := rec.Header().Get("Content-Disposition"); got != want {
			t.Errorf("Content-Disposition = %q, want %q", got, want)
		}
	})

	t.Run("400 not_signup for a scheduling poll", func(t *testing.T) {
		scheduling := createTestPoll(t, ctx, s, orgID, ownerID)
		rec := doRequest(t, h, "GET", "/api/v1/polls/"+scheduling.ID+"/roster.csv", nil, sessHeader(ownerID))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "not_signup" {
			t.Errorf("code = %q, want not_signup", errCode(t, rec))
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
		"channels": map[string]any{"response.created": map[string]bool{"email": false, "push": false}},
	}
	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/notification-prefs", body, sessHeader(ownerID))
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
	addOrgMember(t, d, orgID, ownerID, "owner")
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
	addOrgMember(t, d, orgID, ownerID, "owner")
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	slot := created.Options[0].ID

	result, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Ada"}, polls.Viewer{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, memberID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, memberID, "member")
	// EmailVerified: true on both — the "forbidden" case must fail because a plain member isn't a
	// manager, not merely because an unverified session is now treated as anonymous (see finding
	// 4), and the "succeeds" case needs the creator's UserID to actually reach canManagePoll via
	// viewerFromRequest.
	a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID, EmailVerified: true})
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID, EmailVerified: true})

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

// TestHandlerUnclaimSelfParticipantIDFootgun covers I6: a caller who passes
// ?participantId=<their own participant> must NOT be routed through UnclaimFor (manage-required)
// just because target != "" — that would 403 an ordinary participant unclaiming their own slot
// who simply happened to pass their own id explicitly. It must succeed as plain self-service,
// exactly as if participantId had been omitted.
func TestHandlerUnclaimSelfParticipantIDFootgun(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	slot := created.Options[0].ID

	claimantID := seedUser(t, d)
	addOrgMember(t, d, orgID, claimantID, "member")
	result, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Mallory"}, polls.Viewer{UserID: claimantID})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// EmailVerified: true — the self-unclaim resolution below (s.selfParticipantID) needs
	// viewer.UserID to find the claimant's own participant row via viewerFromRequest.
	a.login(&auth.Session{UserID: claimantID, ActiveOrgID: orgID, EmailVerified: true})

	rec := doRequest(t, h, "DELETE",
		"/api/v1/polls/"+created.ID+"/claims/"+slot+"?participantId="+result.ParticipantID,
		nil, sessHeader(claimantID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (self-service, not a manage-required force-unclaim); body=%s", rec.Code, rec.Body)
	}

	view, err := s.GetView(ctx, created.ID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if p := findParticipant(view, result.ParticipantID); p != nil && len(p.Votes) != 0 {
		t.Errorf("expected the unclaim to remove the vote, got %+v", p.Votes)
	}
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
// caller is rejected when Turnstile is on and no token verifies, accepted with one that does, a
// verified signed-in caller skips the check entirely (the siteverify stub errors unconditionally
// in that subtest — if it were ever called, that subtest would fail), and — the reviewer's finding
// 4 — an UNVERIFIED signed-in caller does NOT get that same free pass: they're treated exactly
// like an anonymous caller and still need a valid token.
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

	t.Run("verified signed-in caller skips captcha entirely", func(t *testing.T) {
		withSiteverifyStubT(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})))
		_, memberID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, memberID, "member")
		a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID, EmailVerified: true})

		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body, sessHeader(memberID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (captcha must not be checked for a verified signed-in caller); body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("unverified signed-in caller is NOT exempt from captcha", func(t *testing.T) {
		// No siteverify stub override here: the default from this test's setup rejects any token
		// (turnstileStub isn't installed for this subtest), so this only passes if the request is
		// rejected for lacking a valid token — proving the unverified session bought no exemption.
		_, unverifiedID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, unverifiedID, "member")
		a.login(&auth.Session{UserID: unverifiedID, ActiveOrgID: orgID, EmailVerified: false})

		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body, sessHeader(unverifiedID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (an unverified session must not skip captcha); body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "captcha_failed" {
			t.Errorf("code = %q, want captcha_failed", errCode(t, rec))
		}
	})
}

// TestHandlerAddCommentAuthorName is requirement C3(a)'s evidence — resolveAuthorName
// (participants.functions.ts): a signed-in commenter's display name always comes from their own
// account (GetUser + displayName), never the client-supplied authorName, so nobody signed in can
// impersonate another name in their own comments; a guest (no session) keeps whatever name they
// typed.
func TestHandlerAddCommentAuthorName(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	commenterID := seedUser(t, d)
	addOrgMember(t, d, orgID, commenterID, "member")
	commenterIDInt, perr := strconv.ParseInt(commenterID, 10, 64)
	if perr != nil {
		t.Fatalf("parse commenterID: %v", perr)
	}
	if _, err := d.ExecContext(ctx,
		`UPDATE users SET first_name = $2, last_name = $3 WHERE id = $1`,
		commenterIDInt, "Real", "Name",
	); err != nil {
		t.Fatalf("seed user name: %v", err)
	}
	// EmailVerified: true — authorName resolution below only trusts the session's own UserID
	// (over the client-supplied value) for a verified session; see viewerFromRequest's own doc
	// comment (finding 4) and this test's own unverified-caller subtest further down.
	a.login(&auth.Session{UserID: commenterID, ActiveOrgID: orgID, EmailVerified: true})

	t.Run("a signed-in caller's spoofed client authorName is ignored in favor of their session name", func(t *testing.T) {
		body := map[string]any{"authorName": "Totally Someone Else", "body": "hi"}
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/comments", body, sessHeader(commenterID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		comment := decodeBody[map[string]any](t, rec)
		if comment["authorName"] != "Real Name" {
			t.Errorf("authorName = %v, want %q (session name, not the spoofed client value)", comment["authorName"], "Real Name")
		}
	})

	t.Run("a guest's client-supplied authorName is kept as-is", func(t *testing.T) {
		body := map[string]any{"authorName": "Casual Guest", "body": "hi"}
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/comments", body, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		comment := decodeBody[map[string]any](t, rec)
		if comment["authorName"] != "Casual Guest" {
			t.Errorf("authorName = %v, want %q (guest-supplied name kept)", comment["authorName"], "Casual Guest")
		}
	})

	// Finding 4: an unverified session must be treated exactly like no session at all — its
	// client-supplied authorName is kept as-is, never overridden from its (unusable) account,
	// exactly like the guest case just above.
	t.Run("an unverified signed-in caller's client-supplied authorName is kept as-is, same as a guest", func(t *testing.T) {
		_, unverifiedID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, unverifiedID, "member")
		a.login(&auth.Session{UserID: unverifiedID, ActiveOrgID: orgID, EmailVerified: false})

		body := map[string]any{"authorName": "Not Yet Verified", "body": "hi"}
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/comments", body, sessHeader(unverifiedID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		comment := decodeBody[map[string]any](t, rec)
		if comment["authorName"] != "Not Yet Verified" {
			t.Errorf("authorName = %v, want %q (an unverified session must not have its identity attributed)", comment["authorName"], "Not Yet Verified")
		}
	})
}

// TestHandlerCreateRateLimited pins the 'create' budget back onto POST /api/v1/polls and its
// duplicate sibling: 20/min per IP, one shared bucket, applied OUTSIDE the session gate — which is
// what lets this test exhaust it with unauthenticated requests (each a 401) rather than creating
// twenty real polls.
func TestHandlerCreateRateLimited(t *testing.T) {
	d := testdb.New(t)
	h, _, _ := newTestHandler(d, testConfig(t))

	for i := 0; i < 20; i++ {
		rec := doRequest(t, h, "POST", "/api/v1/polls", map[string]any{}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401 (under budget, no session)", i+1, rec.Code)
		}
	}

	over := doRequest(t, h, "POST", "/api/v1/polls", map[string]any{}, nil)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("21st create: status = %d, want 429; body=%s", over.Code, over.Body)
	}
	if errCode(t, over) != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", errCode(t, over))
	}

	// Duplicate shares the same bucket, exactly as the old rateLimitMiddleware('create') did.
	dup := doRequest(t, h, "POST", "/api/v1/polls/some-id/duplicate", map[string]any{}, nil)
	if dup.Code != http.StatusTooManyRequests {
		t.Fatalf("duplicate after the create budget is spent: status = %d, want 429; body=%s", dup.Code, dup.Body)
	}
}

// errFields extracts the {"error":{"fields":{...}}} map from a 422 "invalid" envelope.
func errFields(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope %s: %v", rec.Body.String(), err)
	}
	return body.Error.Fields
}

// TestHandlerValidatesPublicInput ports addParticipantSchema/updateParticipantSchema/
// addCommentSchema/claimSchema/notificationPrefsSchema (main:src/server/polls/schemas.ts:170-224,
// gridSchema in main:src/lib/notifications.ts:82-97) at the HTTP layer: every rule rejects with the
// standard 422 "invalid" envelope naming the offending field, and the accept-side rules (empty
// email string, trimming, unknown grid keys stripped, null grid clears) hold too.
func TestHandlerValidatesPublicInput(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	// EmailVerified: true — the "comment: signed-in caller may omit authorName" subtest below
	// exercises resolveAuthorName's session-name override (handleAddComment), which
	// viewerFromRequest only grants a verified session (see its own doc comment).
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID, EmailVerified: true})
	poll := createTestPoll(t, ctx, s, orgID, ownerID)
	signup := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	answers := map[string]string{poll.Options[0].ID: "yes"}
	longName := strings.Repeat("x", polls.LimitName+1)
	longBody := strings.Repeat("y", polls.LimitComment+1)
	longEmail := strings.Repeat("a", polls.LimitEmail) + "@example.com"

	participantsPath := "/api/v1/polls/" + poll.ID + "/participants"
	commentsPath := "/api/v1/polls/" + poll.ID + "/comments"
	claimsPath := "/api/v1/polls/" + signup.ID + "/claims"
	prefsPath := "/api/v1/polls/" + poll.ID + "/notification-prefs"

	// One participant the PATCH cases can target.
	rec := doRequest(t, h, "POST", participantsPath, map[string]any{"name": "Ada", "answers": answers}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed participant: status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	seeded := decodeBody[map[string]any](t, rec)
	participantID, _ := seeded["participantId"].(string)
	guestToken, _ := seeded["guestToken"].(string)
	guest := map[string]string{"X-Guest-Token": guestToken}

	rejects := []struct {
		name, method, path string
		body               map[string]any
		headers            map[string]string
		field              string
	}{
		{"participant: blank name", "POST", participantsPath, map[string]any{"name": "   ", "answers": answers}, nil, "name"},
		{"participant: name over LimitName", "POST", participantsPath, map[string]any{"name": longName, "answers": answers}, nil, "name"},
		{"participant: malformed email", "POST", participantsPath, map[string]any{"name": "Ada", "email": "not-an-email", "answers": answers}, nil, "email"},
		{"participant: email with display name", "POST", participantsPath, map[string]any{"name": "Ada", "email": "Ada <ada@example.com>", "answers": answers}, nil, "email"},
		{"participant: email over LimitEmail", "POST", participantsPath, map[string]any{"name": "Ada", "email": longEmail, "answers": answers}, nil, "email"},
		{"update participant: blank name", "PATCH", participantsPath + "/" + participantID, map[string]any{"name": " ", "answers": answers}, guest, "name"},
		{"update participant: name over LimitName", "PATCH", participantsPath + "/" + participantID, map[string]any{"name": longName, "answers": answers}, guest, "name"},
		{"comment: blank body", "POST", commentsPath, map[string]any{"authorName": "Ada", "body": "  \n "}, nil, "body"},
		{"comment: body over LimitComment", "POST", commentsPath, map[string]any{"authorName": "Ada", "body": longBody}, nil, "body"},
		{"comment: anonymous blank authorName", "POST", commentsPath, map[string]any{"authorName": "", "body": "hello"}, nil, "authorName"},
		{"comment: authorName over LimitName", "POST", commentsPath, map[string]any{"authorName": longName, "body": "hello"}, nil, "authorName"},
		{"claim: name over LimitName", "POST", claimsPath, map[string]any{"optionId": signup.Options[0].ID, "name": longName}, nil, "name"},
		{"claim: malformed email", "POST", claimsPath, map[string]any{"optionId": signup.Options[0].ID, "name": "Ada", "email": "nope"}, nil, "email"},
		{"prefs: non-boolean channel value", "POST", prefsPath, map[string]any{"channels": map[string]any{"response.created": map[string]any{"email": "yes", "push": true}}}, sessHeader(ownerID), "channels"},
		{"prefs: missing push flag", "POST", prefsPath, map[string]any{"channels": map[string]any{"response.created": map[string]any{"email": true}}}, sessHeader(ownerID), "channels.response.created"},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, h, tc.method, tc.path, tc.body, tc.headers)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
			}
			if errCode(t, rec) != "invalid" {
				t.Errorf("code = %q, want invalid", errCode(t, rec))
			}
			if fields := errFields(t, rec); fields[tc.field] == "" {
				t.Errorf("fields = %v, want a message under %q", fields, tc.field)
			}
		})
	}

	t.Run("participant: empty-string email is accepted and stored as no address", func(t *testing.T) {
		rec := doRequest(t, h, "POST", participantsPath, map[string]any{"name": "Bob", "email": "", "answers": answers}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		id, _ := decodeBody[map[string]any](t, rec)["participantId"].(string)
		view, err := s.GetView(ctx, poll.ID, polls.Viewer{})
		if err != nil {
			t.Fatalf("GetView: %v", err)
		}
		if p := findParticipant(view, id); p == nil || p.HasEmail {
			t.Errorf("participant = %+v, want HasEmail=false", p)
		}
	})

	t.Run("participant: name and email are stored trimmed", func(t *testing.T) {
		rec := doRequest(t, h, "POST", participantsPath, map[string]any{"name": "  Cleo  ", "email": " cleo@example.com ", "answers": answers}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		id, _ := decodeBody[map[string]any](t, rec)["participantId"].(string)
		view, err := s.GetView(ctx, poll.ID, polls.Viewer{})
		if err != nil {
			t.Fatalf("GetView: %v", err)
		}
		if p := findParticipant(view, id); p == nil || p.Name != "Cleo" || !p.HasEmail {
			t.Errorf("participant = %+v, want Name=Cleo HasEmail=true", p)
		}
	})

	t.Run("comment: signed-in caller may omit authorName (account name wins)", func(t *testing.T) {
		rec := doRequest(t, h, "POST", commentsPath, map[string]any{"authorName": "", "body": "hello"}, sessHeader(ownerID))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("prefs: unknown event keys are stripped, not rejected", func(t *testing.T) {
		body := map[string]any{"channels": map[string]any{
			"response.created": map[string]bool{"email": false, "push": false},
			"bogus.event":      map[string]bool{"email": true, "push": true},
		}}
		rec := doRequest(t, h, "POST", prefsPath, body, sessHeader(ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		view, err := s.GetView(ctx, poll.ID, polls.Viewer{UserID: ownerID})
		if err != nil || view == nil || view.Notifications == nil {
			t.Fatalf("GetView = %+v, %v; want a view with Notifications", view, err)
		}
		if _, ok := view.Notifications.Channels["bogus.event"]; ok {
			t.Errorf("unknown key survived into the stored grid: %v", view.Notifications.Channels)
		}
		if _, ok := view.Notifications.Channels["response.created"]; !ok {
			t.Errorf("known key missing from the stored grid: %v", view.Notifications.Channels)
		}
	})

	t.Run("prefs: null channels clears the override", func(t *testing.T) {
		rec := doRequest(t, h, "POST", prefsPath, map[string]any{"channels": nil}, sessHeader(ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		view, err := s.GetView(ctx, poll.ID, polls.Viewer{UserID: ownerID})
		if err != nil || view == nil || view.Notifications == nil {
			t.Fatalf("GetView = %+v, %v; want a view with Notifications", view, err)
		}
		if len(view.Notifications.Channels) != 0 {
			t.Errorf("Channels = %v, want empty after clearing", view.Notifications.Channels)
		}
	})
}

// TestHandlerRejectsUnknownAnswer is TestVoteAnswerMustBeYesIfneedbeNo's HTTP-layer twin: the
// service's *ValidationError surfaces as 422 invalid with fields.answers.
func TestHandlerRejectsUnknownAnswer(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	body := map[string]any{"name": "Ada", "answers": map[string]string{created.Options[0].ID: "maybe"}}
	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", body, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
	}
	if errCode(t, rec) != "invalid" || errFields(t, rec)["answers"] == "" {
		t.Errorf("envelope = %s, want code invalid with fields.answers", rec.Body)
	}
}

// TestHandlerClaimReturningGuestSkipsCaptcha ports claimSlot's branch structure
// (main:src/server/polls/participants.functions.ts:232-243): Turnstile is demanded only of a
// brand-new anonymous claimant (no participantId); a returning guest re-identified by
// participantId + X-Guest-Token is authorized by that token instead. The siteverify stub returns
// 500 for every call after the first claim, so any captcha check on the later requests fails them.
func TestHandlerClaimReturningGuestSkipsCaptcha(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfigWithTurnstile(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
	slotA, slotB := created.Options[0].ID, created.Options[1].ID

	withSiteverifyStubT(t, turnstileStub(true))
	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
		map[string]any{"optionId": slotA, "name": "Ada"}, map[string]string{"X-Captcha-Token": "tok"})
	if rec.Code != http.StatusOK {
		t.Fatalf("first claim: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	first := decodeBody[map[string]any](t, rec)
	participantID, _ := first["participantId"].(string)
	guestToken := a.MintGuestToken(participantID)

	// From here on, siteverify must never be consulted.
	withSiteverifyStubT(t, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})))

	t.Run("second claim with participantId + guest token needs no captcha", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
			map[string]any{"optionId": slotB, "participantId": participantID},
			map[string]string{"X-Guest-Token": guestToken})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		ids, _ := decodeBody[map[string]any](t, rec)["claimedOptionIds"].([]any)
		if len(ids) != 2 {
			t.Errorf("claimedOptionIds = %v, want both slots", ids)
		}
	})

	t.Run("participantId without an authorizing token is 403 forbidden, not captcha_failed", func(t *testing.T) {
		rec := doRequest(t, h, "DELETE", "/api/v1/polls/"+created.ID+"/claims/"+slotB, nil,
			map[string]string{"X-Guest-Token": guestToken})
		if rec.Code != http.StatusOK {
			t.Fatalf("unclaim to free slotB: status = %d; body=%s", rec.Code, rec.Body)
		}
		rec = doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
			map[string]any{"optionId": slotB, "participantId": participantID}, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "forbidden" {
			t.Errorf("code = %q, want forbidden", errCode(t, rec))
		}
	})

	t.Run("a brand-new anonymous claimant is still captcha-gated", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/claims",
			map[string]any{"optionId": slotB, "name": "Bob"}, nil)
		if rec.Code != http.StatusForbidden || errCode(t, rec) != "captcha_failed" {
			t.Fatalf("status/code = %d/%q, want 403/captcha_failed; body=%s", rec.Code, errCode(t, rec), rec.Body)
		}
	})
}

// TestHandlerFinalizeReturnsSentCount ports finalizePoll's `{ sent }` response
// (main:src/server/polls/polls.functions.ts:116-126): the count of distinct recipients a
// "finalized" mail was queued for — emailed participants deduped by lower-cased address, plus the
// creator if their address isn't already among them, plus subscribed non-actor members.
func TestHandlerFinalizeReturnsSentCount(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	opt := created.Options[0].ID

	seedParticipant(t, d, created.ID, "Ada", map[string]string{opt: "yes"}, "ada@example.com")
	seedParticipant(t, d, created.ID, "Ada again", map[string]string{opt: "no"}, "ADA@example.com") // same address, different case
	seedParticipant(t, d, created.ID, "Bob", map[string]string{opt: "yes"}, "bob@example.com")
	seedParticipant(t, d, created.ID, "No mail", map[string]string{opt: "yes"}, "")

	rec := doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/finalize",
		map[string]any{"optionId": opt}, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	resp := decodeBody[map[string]any](t, rec)
	// ada + bob (participants, deduped) + the creator's own distinct address = 3.
	if sent, _ := resp["sent"].(float64); sent != 3 {
		t.Errorf("sent = %v, want 3; body=%s", resp["sent"], rec.Body)
	}
	if n := len(filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "finalized")); n != 3 {
		t.Errorf("finalized mail:poll jobs = %d, want 3 (sent must equal what was actually queued)", n)
	}
}

// TestHandlerRosterCSVUsesRequestLocale ports the roster route's getLocale() (main:src/routes/
// p/$id/roster[.]csv.ts:54): slot labels render in the caller's locale — the SPA's locale cookie
// first, Accept-Language otherwise.
func TestHandlerRosterCSVUsesRequestLocale(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})
	created := createDatedSignupPoll(t, ctx, s, orgID, ownerID)
	path := "/api/v1/polls/" + created.ID + "/roster.csv"

	t.Run("Accept-Language: nb", func(t *testing.T) {
		rec := doRequest(t, h, "GET", path, nil, map[string]string{"X-Test-Session": ownerID, "Accept-Language": "nb-NO,nb;q=0.9"})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), wantLabelNB) {
			t.Errorf("status = %d, body = %q; want 200 containing %q", rec.Code, rec.Body.String(), wantLabelNB)
		}
	})

	t.Run("locale cookie wins over Accept-Language", func(t *testing.T) {
		rec := doRequest(t, h, "GET", path, nil, map[string]string{
			"X-Test-Session": ownerID, "Accept-Language": "nb", "Cookie": httpserver.LocaleCookieName + "=en",
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), wantLabelEN) {
			t.Errorf("status = %d, body = %q; want 200 containing %q", rec.Code, rec.Body.String(), wantLabelEN)
		}
	})
}

// TestHandlerPublicRateLimits ports test/server-functions.workers.test.ts:96-106 (vote/comment
// limiters return 429) at the handler level: the "vote" bucket (30/min, shared by participants +
// claims) and the "comment" bucket (20/min) each 429 rate_limited past their limit for one IP.
// httptest requests all share RemoteAddr 192.0.2.1, so every request here counts against the same
// key. Bodies are deliberately invalid: the limiter runs before the handler, so a rejected
// request still consumes budget, and nothing needs to be written to the poll.
func TestHandlerPublicRateLimits(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, _, s := newTestHandler(d, cfg)
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	t.Run("comments: 21st request in a minute is 429 rate_limited", func(t *testing.T) {
		var last *httptest.ResponseRecorder
		for i := 0; i < 21; i++ {
			last = doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/comments", map[string]any{"authorName": "", "body": ""}, nil)
		}
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("21st request: status = %d, want 429; body=%s", last.Code, last.Body)
		}
		if errCode(t, last) != "rate_limited" {
			t.Errorf("code = %q, want rate_limited", errCode(t, last))
		}
		if last.Header().Get("Retry-After") == "" {
			t.Error("missing Retry-After header")
		}
	})

	t.Run("votes: 31st request in a minute is 429 rate_limited", func(t *testing.T) {
		var last *httptest.ResponseRecorder
		for i := 0; i < 31; i++ {
			last = doRequest(t, h, "POST", "/api/v1/polls/"+created.ID+"/participants", map[string]any{"name": "", "answers": map[string]string{}}, nil)
		}
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("31st request: status = %d, want 429; body=%s", last.Code, last.Body)
		}
		if errCode(t, last) != "rate_limited" {
			t.Errorf("code = %q, want rate_limited", errCode(t, last))
		}
	})
}
