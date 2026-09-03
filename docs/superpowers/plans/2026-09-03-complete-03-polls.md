# Complete 03 — Polls, Sign-up Sheets & Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every confirmed polls-area gap between the Go rewrite and the old TS backend: server-side input validation, the votes enum, returning-guest captcha, the finalize `sent` count, the digest re-send race, locale-aware poll mail and roster, roster/ics parity, subscription cleanup on delete, the 2N+1 poll list, the guest token leaking on the poll websocket, and the last un-ported test assertions.

**Architecture:** Everything lands inside `internal/polls` (handlers → service → sqlc queries) plus two narrow seams: a `MailSender` interface so send-path tests can record rendered messages instead of dialing SMTP, and a `LocaleSource` interface fed by Plan A's `auth.Service.LocaleFor`. The digest race is fixed by making the `poll.digest` handler take ownership of its batch under the same poll-scoped advisory lock `EnqueueDigestItem` already uses. On the web side the poll/booking live hooks stop sending both the guest token and the `?since=` cursor (their snapshot is already ground truth).

**Tech Stack:** Go 1.26 (stdlib `net/http`, `database/sql` + pgx stdlib, sqlc, goose), Postgres via `internal/testdb` (testcontainers), `coder/websocket`; web: React + TanStack Router, vitest + Testing Library + msw, bun.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§4 realtime, §5 jobs & mail — "Email localization keeps parity with today", §9 testing); scope brief: the "Plan C" section of the 2026-09-03 completion contract (findings titles listed per task below).

## Global Constraints

- Contract decisions (fixed): email verification gate restored (Plan A); Google Calendar sync disabled (Plan B/D); **email locale restored — per-user locale persisted, guest forms send `locale`, nb mail renders again, dates in mail are locale-aware**; everything lands as commits on `feat/go-rewrite`. Never reintroduce: passkeys, billing, magic links, TOTP 2FA, staff impersonation, SSR/OG tags, web push, booking-page follower notifications.
- Execution order A → B → **C (this plan)** → D → E → F. This plan **consumes** (never redefines) from Plan A, exactly as named there:
  - `func (s *auth.Service) LocaleFor(ctx context.Context, userID string) string` — "en" fallback, never empty.
  - `func (s *auth.Service) GetProfile(ctx context.Context, userID string) (auth.Profile, error)` (not called directly here; `LocaleFor` is the cheap helper this plan uses).
  - `var mailer.SupportedLocales = []string{"en", "nb"}`.
  - `func mailer.FormatDateTime(locale string, t time.Time, loc *time.Location) string` — en `"Tue 1 Sep, 18:30"`, nb `"tir. 1. sep., 18:30"` (24h clock for both).
  - `func mailer.FormatDate(locale string, t time.Time, loc *time.Location) string` — en `"Tue 1 Sep"`, nb `"tir. 1. sep."`.
  - `func mailer.FormatTimeRange(locale string, start, end time.Time, loc *time.Location) string` — `"18:30–19:30"` (en dash).
  - The web client already sends `locale: getLocale()` on vote/claim request bodies (Plan A); Go already stores it on `participants.locale`.
- From Plan B: `internal/testdb` fails (not skips) when `CI` is set; `http.MaxBytesHandler(1<<20)` already wraps `/api/` (so this plan adds **no** body-size cap of its own).
- Migration numbering: this plan adds exactly one migration, `migrations/00011_votes_answer_check.sql`. After any change under `migrations/` or `internal/polls/queries/polls.sql`, run `sqlc generate` (config `sqlc.yaml` at repo root) and commit the regenerated `internal/*/queries/*.go`.
- Error envelope `{"error":{"code","message","fields?"}}`; codes snake_case. Validation failures use the envelope this package already emits for `*ValidationError`: **422 `invalid` with `fields`** (the SPA maps `invalid`; the brief's shorthand "400 validation_failed" is superseded by this existing, frontend-known shape).
- Every new user-facing string goes into `web/messages/en.json` AND `nb.json` (this plan adds none — existing keys suffice).
- Commit messages: conventional (`fix(polls): …`), each ending with the two trailer lines:
  ```
  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
  ```
- TDD per task: failing test first, run it, implement, run, commit. Go tests use `internal/testdb` (live Postgres) and each package's existing httptest helpers (`newTestHandler`, `doRequest`, `decodeBody`, `errCode`, `seedOrgAndUser`, `addOrgMember`, `createTestPoll`, `createSignupPoll`, `seedParticipant`, `listJobs`, `forceDue`, `filterByEvent`, `decodeMailPollJobs`, `findParticipant`, `fieldsOf`, `strPtr`, `intPtr`, `textOption`, `datetimeOption`, `withCapacity` — all in `internal/polls/*_test.go`, package `polls_test`).
- Gates before declaring this plan done: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`; `bunx playwright test`.
- Explicit no-ops from the brief: "Push column removed from NotificationGrid; Go still stores a push flag" — leave the Go `ChannelPrefs.Push` field alone (it is what the stored jsonb carries).

---

### Task 1: Server-side validation for participant, comment, claim and notification-prefs requests

Finding: "Server no longer validates participant name/email, comment author/body, or claimant email" (body-size cap half is Plan B's `MaxBytesHandler`).

**Files:**
- Modify: `internal/polls/schemas.go` (limits block at top; new helpers at the end)
- Modify: `internal/polls/handlers.go` (DTOs at lines 306–337; handlers `handleAddParticipant`, `handleUpdateParticipant`, `handleAddComment`, `handleClaim`, `handleUpdateNotificationPrefs`)
- Test: `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `newValidationError`, `*ValidationError`, `NotificationGrid`, `systemDefaults` (all `internal/polls`).
- Produces: `polls.LimitName = 80`, `polls.LimitComment = 2000`, `polls.LimitEmail = 254`; unexported `validateNameField`, `validateEmailField`, `validationErrorFrom` (schemas.go); DTO methods `addParticipantRequest.validate()`, `updateParticipantRequest.validate()`, `addCommentRequest.validate(anonymous bool)`, `claimRequest.validate()` (Task 3 reuses), `notificationPrefsRequest.toGrid()`; test helper `errFields(t, rec) map[string]string` (Task 2 reuses).

- [ ] **Step 1: Write the failing handler test**

Append to `internal/polls/handlers_test.go`:

```go
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
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/polls/ -run TestHandlerValidatesPublicInput -v`
Expected: FAIL — compile error `undefined: polls.LimitName` (and, once defined, the reject cases get 201/200 instead of 422).

- [ ] **Step 3: Add the limits and validation helpers to `schemas.go`**

In the `const (` block at the top of `internal/polls/schemas.go`, after `LimitParticipants = 500`, add:

```go
	// LimitName is LIMITS.name — a participant's name, a comment's authorName, a claimant's name.
	LimitName = 80
	// LimitComment is LIMITS.comment — a comment body.
	LimitComment = 2000
	// LimitEmail mirrors addParticipantSchema/claimSchema's z.email().max(254).
	LimitEmail = 254
```

Add `"net/mail"` and `"unicode/utf8"` to the import block, then append at the end of the file:

```go
// validateNameField ports `z.string().trim().min(1).max(LIMITS.name)` (schemas.ts): records a
// message under field when the trimmed name is empty or longer than LimitName (counted in runes,
// like zod's .max over a JS string), and returns the trimmed value the caller should store.
func validateNameField(field, name string, fields map[string]string) string {
	trimmed := strings.TrimSpace(name)
	switch {
	case trimmed == "":
		fields[field] = field + " is required"
	case utf8.RuneCountInString(trimmed) > LimitName:
		fields[field] = fmt.Sprintf("%s must be at most %d characters", field, LimitName)
	}
	return trimmed
}

// validateEmailField ports `z.union([z.literal(''), z.email().max(254)]).optional()`: nil and ""
// both mean "no address" and pass; anything else must be a bare address (no display name, no
// angle brackets — mail.ParseAddress would happily accept "Ada <ada@example.com>", so the parsed
// address has to round-trip to the input) with a dotted domain, at most LimitEmail runes. Returns
// the trimmed value (a pointer to "" for an explicit empty string, so callers keep the
// "provided but empty" vs "absent" distinction the TS schema also preserved).
func validateEmailField(field string, email *string, fields map[string]string) *string {
	if email == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*email)
	if trimmed == "" {
		return &trimmed
	}
	if utf8.RuneCountInString(trimmed) > LimitEmail {
		fields[field] = fmt.Sprintf("%s must be at most %d characters", field, LimitEmail)
		return &trimmed
	}
	if !isBareEmail(trimmed) {
		fields[field] = field + " must be a valid email address"
	}
	return &trimmed
}

// isBareEmail is validateEmailField's address check — see its doc comment.
func isBareEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	return at > 0 && at < len(s)-1 && strings.Contains(s[at+1:], ".")
}

// validationErrorFrom turns an accumulated fields map into a *ValidationError, or nil when it is
// empty — the tail every request-DTO validate() method ends with.
func validationErrorFrom(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}
```

- [ ] **Step 4: Add the DTO validate methods and wire them into the handlers**

In `internal/polls/handlers.go`, replace the five DTO declarations (`addParticipantRequest` … `notificationPrefsRequest`) with:

```go
type addParticipantRequest struct {
	Name    string            `json:"name"`
	Email   *string           `json:"email"`
	Answers map[string]string `json:"answers"`
	Locale  *string           `json:"locale"`
}

// validate ports addParticipantSchema (schemas.ts): name trimmed 1..LimitName, email ''|valid
// address ≤ LimitEmail. Answer VALUES are checked by the service (validateAnswersTx), which also
// needs the poll's own allowIfNeedBe setting. Returns the request with name/email trimmed.
func (req addParticipantRequest) validate() (addParticipantRequest, error) {
	fields := map[string]string{}
	req.Name = validateNameField("name", req.Name, fields)
	req.Email = validateEmailField("email", req.Email, fields)
	return req, validationErrorFrom(fields)
}

type updateParticipantRequest struct {
	Name    *string           `json:"name"`
	Answers map[string]string `json:"answers"`
}

// validate ports updateParticipantSchema: name is optional, but when present must be a trimmed
// 1..LimitName string.
func (req updateParticipantRequest) validate() (updateParticipantRequest, error) {
	fields := map[string]string{}
	if req.Name != nil {
		name := validateNameField("name", *req.Name, fields)
		req.Name = &name
	}
	return req, validationErrorFrom(fields)
}

type addCommentRequest struct {
	AuthorName string `json:"authorName"`
	Body       string `json:"body"`
}

// validate ports addCommentSchema: body trimmed 1..LimitComment; authorName trimmed 1..LimitName
// — required only for an anonymous author, since handleAddComment replaces a signed-in author's
// name with their account name regardless of what the client sent (resolveAuthorName).
func (req addCommentRequest) validate(anonymous bool) (addCommentRequest, error) {
	fields := map[string]string{}
	if anonymous {
		req.AuthorName = validateNameField("authorName", req.AuthorName, fields)
	} else {
		req.AuthorName = strings.TrimSpace(req.AuthorName)
		if utf8.RuneCountInString(req.AuthorName) > LimitName {
			fields["authorName"] = fmt.Sprintf("authorName must be at most %d characters", LimitName)
		}
	}
	req.Body = strings.TrimSpace(req.Body)
	switch {
	case req.Body == "":
		fields["body"] = "body is required"
	case utf8.RuneCountInString(req.Body) > LimitComment:
		fields["body"] = fmt.Sprintf("body must be at most %d characters", LimitComment)
	}
	return req, validationErrorFrom(fields)
}

type claimRequest struct {
	OptionID      string  `json:"optionId"`
	ParticipantID string  `json:"participantId"`
	Name          string  `json:"name"`
	Email         *string `json:"email"`
	Locale        *string `json:"locale"`
}

// validate ports claimSchema: name optional (the service requires it only when it actually
// creates a participant — prepareNewParticipant) but capped at LimitName when given; email
// ''|valid address ≤ LimitEmail.
func (req claimRequest) validate() (claimRequest, error) {
	fields := map[string]string{}
	req.Name = strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(req.Name) > LimitName {
		fields["name"] = fmt.Sprintf("name must be at most %d characters", LimitName)
	}
	req.Email = validateEmailField("email", req.Email, fields)
	return req, validationErrorFrom(fields)
}

type notificationPrefsRequest struct {
	Channels json.RawMessage `json:"channels"`
}

// toGrid ports notificationPrefsSchema (schemas.ts) + gridSchema (src/lib/notifications.ts):
// absent/null channels clears the per-poll override (nil grid); otherwise an object whose values
// are {email, push} with BOTH booleans present. Unknown event keys are stripped rather than
// rejected — a stored grid may outlive a renamed event, and a user's whole preference row must
// not become unwritable because of it.
func (req notificationPrefsRequest) toGrid() (NotificationGrid, error) {
	if len(req.Channels) == 0 || string(req.Channels) == "null" {
		return nil, nil
	}
	var raw map[string]struct {
		Email *bool `json:"email"`
		Push  *bool `json:"push"`
	}
	if err := json.Unmarshal(req.Channels, &raw); err != nil {
		return nil, newValidationError("channels", "channels must be an object of {email, push} booleans")
	}
	grid := NotificationGrid{}
	for key, v := range raw {
		event := NotificationEvent(key)
		if _, known := systemDefaults[event]; !known {
			continue
		}
		if v.Email == nil || v.Push == nil {
			return nil, newValidationError("channels."+key, "email and push must both be booleans")
		}
		grid[event] = ChannelPrefs{Email: *v.Email, Push: *v.Push}
	}
	return grid, nil
}
```

Add `"fmt"`, `"strings"` and `"unicode/utf8"` to handlers.go's import block. Then wire the calls:

In `handleAddParticipant`, replace the block from `var req addParticipantRequest` through the `s.AddParticipant(...)` call with:

```go
		var req addParticipantRequest
		if !httpserver.DecodeJSON(w, r, &req) {
			return
		}
		req, err := req.validate()
		if err != nil {
			writeServiceError(w, err)
			return
		}
		viewer := viewerFromRequest(a, r)
		result, err := s.AddParticipant(ctx, pollID, ParticipantInput{
			Name: req.Name, Email: req.Email, Answers: req.Answers, Locale: req.Locale,
		}, viewer)
```

In `handleUpdateParticipant`, after `DecodeJSON` add:

```go
		req, err := req.validate()
		if err != nil {
			writeServiceError(w, err)
			return
		}
```

(and change the later `if err := s.UpdateParticipant(...); err != nil {` to `if err = s.UpdateParticipant(...); err != nil {` so `err` is reused, not redeclared).

In `handleAddComment`, replace from `var req addCommentRequest` through `viewer := viewerFromRequest(a, r)` with:

```go
		var req addCommentRequest
		if !httpserver.DecodeJSON(w, r, &req) {
			return
		}
		viewer := viewerFromRequest(a, r)
		req, err := req.validate(viewer.UserID == "")
		if err != nil {
			writeServiceError(w, err)
			return
		}
```

and change the later `comment, err := s.AddComment(` to `comment, err = s.AddComment(`.

In `handleClaim`, after `DecodeJSON` add:

```go
		req, err := req.validate()
		if err != nil {
			writeServiceError(w, err)
			return
		}
```

and change `result, err := s.Claim(` to `result, err = s.Claim(`.

In `handleUpdateNotificationPrefs`, replace the `s.UpdateNotificationPrefs(...)` call with:

```go
	grid, err := req.toGrid()
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if err := s.UpdateNotificationPrefs(r.Context(), pollID, sess.ActiveOrgID, sess.UserID, grid); err != nil {
		writeServiceError(w, err)
		return
	}
```

- [ ] **Step 5: Run the test and the whole package**

Run: `go test ./internal/polls/ -run 'TestHandlerValidatesPublicInput|TestHandlerUpdateNotificationPrefs|TestHandlerParticipantAndCommentDigestWiring' -v`
Expected: PASS (all subtests).

Run: `go build ./... && go vet ./internal/polls/ && go test ./internal/polls/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/polls/schemas.go internal/polls/handlers.go internal/polls/handlers_test.go
git commit -m "fix(polls): validate participant, comment, claim and prefs input server-side

Ports addParticipantSchema/updateParticipantSchema/addCommentSchema/claimSchema
(name 1..80 trimmed, email ''|valid <=254) and gridSchema (both booleans
required, unknown events stripped, null clears) to the HTTP layer as 422
invalid with field messages.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 2: Vote answers must be yes / ifneedbe / no (service check + CHECK constraint)

Finding: "Vote answer values are not validated against yes/ifneedbe/no".

**Files:**
- Modify: `internal/polls/participants.go:114-135` (`validateAnswersTx`)
- Create: `migrations/00011_votes_answer_check.sql`
- Regenerate: `internal/polls/queries/*.go`, `internal/bookings/queries/*.go` (`sqlc generate`; expect no diff, commit if any)
- Test: `internal/polls/participants_test.go`, `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `fieldsOf(t, err)` (schemas_test.go), `errFields` (Task 1), `seedParticipant`.
- Produces: DB constraint `votes_answer_check`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/polls/participants_test.go` (add `"strings"` to its imports if absent):

```go
// TestVoteAnswerMustBeYesIfneedbeNo ports answerSchema (main:src/server/polls/schemas.ts:17,
// z.enum(['yes','ifneedbe','no'])): any other value is a validation failure on "answers" at the
// service layer and — since votes.answer is read back verbatim by every viewer and by scoring —
// rejected by the database too (migration 00011's CHECK constraint).
func TestVoteAnswerMustBeYesIfneedbeNo(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	opt := created.Options[0].ID

	t.Run("AddParticipant rejects an unknown answer", func(t *testing.T) {
		_, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Ada", Answers: map[string]string{opt: "maybe"},
		}, polls.Viewer{})
		if !errors.Is(err, polls.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		if fieldsOf(t, err)["answers"] == "" {
			t.Errorf("fields = %v, want an answers message", fieldsOf(t, err))
		}
	})

	t.Run("UpdateParticipant rejects an unknown answer and keeps the old vote", func(t *testing.T) {
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Bob", Answers: map[string]string{opt: "yes"},
		}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		err = s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt: "YES"},
		}, polls.Viewer{GuestParticipantID: result.ParticipantID})
		if !errors.Is(err, polls.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		view, err := s.GetView(ctx, created.ID, polls.Viewer{})
		if err != nil {
			t.Fatalf("GetView: %v", err)
		}
		if p := findParticipant(view, result.ParticipantID); p == nil || p.Votes[opt] != "yes" {
			t.Errorf("participant = %+v, want the original yes vote untouched", p)
		}
	})

	t.Run("each of yes, ifneedbe, no is accepted", func(t *testing.T) {
		for _, answer := range []string{"yes", "ifneedbe", "no"} {
			if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
				Name: "Voter " + answer, Answers: map[string]string{opt: answer},
			}, polls.Viewer{}); err != nil {
				t.Errorf("answer %q: %v", answer, err)
			}
		}
	})

	t.Run("votes.answer CHECK constraint rejects a direct write", func(t *testing.T) {
		pid := seedParticipant(t, d, created.ID, "Direct", nil, "")
		_, err := d.ExecContext(ctx,
			`INSERT INTO votes (participant_id, option_id, answer) VALUES ($1, $2, 'maybe')`, pid, opt)
		if err == nil || !strings.Contains(err.Error(), "votes_answer_check") {
			t.Fatalf("insert err = %v, want a votes_answer_check violation", err)
		}
	})
}
```

Append to `internal/polls/handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/polls/ -run 'TestVoteAnswerMustBeYesIfneedbeNo|TestHandlerRejectsUnknownAnswer' -v`
Expected: FAIL — "err = <nil>, want ErrValidation" for the unknown answers and "insert err = <nil>" for the CHECK case.

- [ ] **Step 3: Add the enum check to `validateAnswersTx`**

In `internal/polls/participants.go`, replace the `for optionID, answer := range answers {` loop body with:

```go
	for optionID, answer := range answers {
		if !validIDs[optionID] {
			return newValidationError("answers", fmt.Sprintf("option %q is not on this poll", optionID))
		}
		// answerSchema (schemas.ts): the only three values a vote may hold. Checked here, not just by
		// the CHECK constraint (migration 00011), so a bad value is a 422 with a field message rather
		// than a 500 from a constraint violation.
		switch answer {
		case "yes", "ifneedbe", "no":
		default:
			return newValidationError("answers", fmt.Sprintf("answer %q for option %q must be one of yes, ifneedbe, no", answer, optionID))
		}
		if answer == "ifneedbe" && !allowIfNeedBe {
			return newValidationError("answers", "ifneedbe is not allowed on this poll")
		}
	}
```

- [ ] **Step 4: Write the migration and regenerate sqlc**

Create `migrations/00011_votes_answer_check.sql`:

```sql
-- +goose Up
-- Ports answerSchema (main:src/server/polls/schemas.ts:17, z.enum(['yes','ifneedbe','no'])) into
-- the schema itself. validateAnswersTx (internal/polls/participants.go) rejects other values at
-- the service layer; this constraint guarantees no other writer (Claim's UpsertVote, a future code
-- path, a manual fix-up) can ever store one either — votes.answer is echoed verbatim to every
-- viewer (participants[].votes) and read by scoring.
ALTER TABLE votes ADD CONSTRAINT votes_answer_check CHECK (answer IN ('yes', 'ifneedbe', 'no'));

-- +goose Down
ALTER TABLE votes DROP CONSTRAINT votes_answer_check;
```

Run: `sqlc generate && git status --short internal/`
Expected: sqlc exits 0; no generated-file changes (a CHECK constraint changes no column types). If anything did change, it is committed in Step 6.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/polls/ -run 'TestVoteAnswerMustBeYesIfneedbeNo|TestHandlerRejectsUnknownAnswer|TestAddParticipant|TestUpdateParticipant|TestClaim' -v`
Expected: PASS.

Run: `go test ./internal/db/ ./internal/polls/`
Expected: `ok` for both (internal/db's migration test applies 00011 against the template database).

- [ ] **Step 6: Commit**

```bash
git add migrations/00011_votes_answer_check.sql internal/polls/participants.go internal/polls/participants_test.go internal/polls/handlers_test.go internal/polls/queries internal/bookings/queries
git commit -m "fix(polls): reject vote answers outside yes/ifneedbe/no

validateAnswersTx now enforces the old answerSchema enum (422 invalid,
fields.answers) and migration 00011 adds a matching CHECK constraint on
votes.answer so no writer can store another value.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 3: Returning guests claiming another slot are not captcha-gated

Finding: "Returning guest claiming a second slot is blocked by captcha when Turnstile is enabled".

**Files:**
- Modify: `internal/polls/handlers.go` (`handleClaim`)
- Test: `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `claimRequest.validate()` (Task 1), `httpserver.RequireCaptchaIfAnon`, test helpers `testConfigWithTurnstile`, `withSiteverifyStubT`, `turnstileStub`.
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Append to `internal/polls/handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/polls/ -run TestHandlerClaimReturningGuestSkipsCaptcha -v`
Expected: FAIL — "second claim …: status = 403, want 200" with code `captcha_failed`.

- [ ] **Step 3: Decode first, then gate only the new-claimant branch**

In `internal/polls/handlers.go`, replace the beginning of `handleClaim`'s closure — from `if err := httpserver.RequireCaptchaIfAnon(cfg, a, r); err != nil {` through the `optionID` resolution — with:

```go
		var req claimRequest
		if !httpserver.DecodeJSON(w, r, &req) {
			return
		}
		req, err := req.validate()
		if err != nil {
			writeServiceError(w, err)
			return
		}
		// Turnstile only for a brand-new anonymous claimant — ported from claimSlot's branch
		// structure (participants.functions.ts): the participantId branch calls
		// requireParticipantAuth and never requireTurnstile. A returning guest identifies via
		// participantId + X-Guest-Token; Claim itself rejects an unauthorized participantId
		// (ErrForbidden/ErrNotFound) before any write, so skipping the captcha here can never be
		// used to act on someone else's participant.
		if req.ParticipantID == "" {
			if err := httpserver.RequireCaptchaIfAnon(cfg, a, r); err != nil {
				httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
				return
			}
		}
		optionID := req.OptionID
		if optionID == "" {
			optionID = r.URL.Query().Get("optionId")
		}
```

(The `validate()` call added in Task 1 now sits in this block; make sure it appears exactly once.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/polls/ -run 'TestHandlerClaim|TestHandlerCaptchaGatesAnonymousParticipant' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/polls/handlers.go internal/polls/handlers_test.go
git commit -m "fix(polls): do not captcha-gate a returning guest's further claims

handleClaim decodes the body first and only demands Turnstile when no
participantId is given, matching the old claimSlot branch; a participantId
is still authorized by Claim (guest token / session / manager).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 4: Finalize responds with `{"sent": n}`

Findings: "Finalize response no longer carries `sent`" / "finalizePoll response shape mismatch".

**Files:**
- Modify: `internal/polls/service.go:559-676` (`Finalize` → `FinalizeWithCount` + thin wrapper)
- Modify: `internal/polls/handlers.go` (`handleFinalize`)
- Create: `web/src/api/__tests__/polls.test.ts`
- Test: `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `enqueueMailPoll`, `resolveRecipients`, `requireOrgPoll`, `jobs.Cancel`, `rooms.Emit`.
- Produces: `func (s *Service) FinalizeWithCount(ctx context.Context, pollID, orgID, optionID, actorUserID string) (sent int, err error)` — `sent` = number of distinct recipients a "finalized" mail:poll job was enqueued for. `Finalize(...) error` keeps its signature (22 existing call sites) and delegates.
- Web contract (unchanged type, now honoured by the server): `finalizePoll(pollId, optionId): Promise<{ sent: number }>`; `AdminBar` already passes `onFinalized={onChanged}` (router invalidate), so no SPA wiring change is needed.

- [ ] **Step 1: Write the failing Go handler test**

Append to `internal/polls/handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/polls/ -run TestHandlerFinalizeReturnsSentCount -v`
Expected: FAIL — "sent = <nil>, want 3" (the body is a PollView).

- [ ] **Step 3: Split `Finalize` into `FinalizeWithCount` + wrapper**

In `internal/polls/service.go`, rename the existing `func (s *Service) Finalize(ctx context.Context, pollID, orgID, optionID, actorUserID string) error {` to:

```go
// FinalizeWithCount is Finalize's implementation, additionally reporting how many distinct
// recipients a "finalized" mail:poll job was enqueued for — the `{ sent }` count finalizePoll
// (polls.functions.ts) returned and the SPA's success toast prints. Finalize below keeps the
// error-only signature every other caller uses.
func (s *Service) FinalizeWithCount(ctx context.Context, pollID, orgID, optionID, actorUserID string) (int, error) {
```

The complete function (the pre-enqueue half is the existing body with `return X` → `return 0, X`):

```go
func (s *Service) FinalizeWithCount(ctx context.Context, pollID, orgID, optionID, actorUserID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := requireOrgPoll(ctx, q, pollID, orgID)
	if err != nil {
		return 0, err
	}
	if poll.Type == string(PollTypeSignup) {
		return 0, newValidationError("type", "signup polls cannot be finalized")
	}
	if poll.Status == pollFinalizedStatus {
		// Plain ErrConflict, deliberately not ErrPollFinalized: finalizePoll's own "already
		// finalized" guard (service.ts) throws the plain CONFLICT code, not POLL_FINALIZED — see
		// errors.go's package doc comment for why the two near-identical English messages carry
		// different TS codes.
		return 0, fmt.Errorf("%w: poll is already finalized", ErrConflict)
	}

	options, err := q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return 0, err
	}
	found := false
	for _, o := range options {
		if o.ID == optionID {
			found = true
			break
		}
	}
	if !found {
		return 0, ErrNotFound
	}

	if err := q.FinalizePoll(ctx, queries.FinalizePollParams{
		ID:                pollID,
		FinalizedOptionID: sql.NullString{String: optionID, Valid: true},
		Status:            pollFinalizedStatus,
		UpdatedAt:         time.Now().UTC(),
	}); err != nil {
		return 0, err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "poll"}); err != nil {
		return 0, err
	}

	// Port of finalizePoll's own syncDeadline(id, null) call (polls.functions.ts): a finalized
	// poll no longer needs its deadline job OR its 24h "closes soon" reminder (I11).
	if err := jobs.Cancel(ctx, tx, jobKindDeadline, "poll:"+pollID); err != nil {
		return 0, err
	}
	if err := jobs.Cancel(ctx, tx, jobKindReminder, "poll:"+pollID); err != nil {
		return 0, err
	}

	// Port of finalizePoll's recipient computation (service.ts) + sendFinalizedEmails: every
	// participant with an email, plus the poll's creator if one exists — deduped by email (lower-
	// cased), owner added only if not already present. One "mail:poll"/"finalized" job per unique
	// recipient, ids-only (participantId or userId — never an address). sent counts them.
	sent := 0
	seenEmail := make(map[string]bool)
	seenUserID := make(map[string]bool)
	participantRows, err := q.ListParticipantsByPoll(ctx, pollID)
	if err != nil {
		return 0, err
	}
	for _, p := range participantRows {
		if !p.Email.Valid {
			continue
		}
		key := strings.ToLower(p.Email.String)
		if seenEmail[key] {
			continue
		}
		seenEmail[key] = true
		if err := enqueueMailPoll(ctx, tx, mailPollPayload{PollID: pollID, Event: "finalized", ParticipantID: p.ID}); err != nil {
			return 0, err
		}
		sent++
	}
	if poll.CreatedBy.Valid {
		owner, oerr := q.GetUser(ctx, poll.CreatedBy.Int64)
		switch {
		case errors.Is(oerr, sql.ErrNoRows):
			// Account gone — same graceful skip as the TS source (finalizePoll's own comment).
		case oerr != nil:
			return 0, oerr
		default:
			key := strings.ToLower(owner.Email)
			ownerIDStr := strconv.FormatInt(owner.ID, 10)
			if !seenEmail[key] {
				if err := enqueueMailPoll(ctx, tx, mailPollPayload{
					PollID: pollID, Event: "finalized", UserID: ownerIDStr,
				}); err != nil {
					return 0, err
				}
				sent++
			}
			seenUserID[ownerIDStr] = true
		}
	}

	// The separate subscriber notification (emitPollEvent's own poll.finalized call in
	// polls.functions.ts's finalizePoll route) — every org member subscribed to poll.finalized's
	// email channel, minus the actor (resolveRecipients' own actorUserID parameter) and minus
	// anyone the direct-mail loop above already enqueued for.
	recipients, err := s.resolveRecipients(ctx, q, poll.OrganizationID, pollID, EventPollFinalized, actorUserID)
	if err != nil {
		return 0, err
	}
	for _, r := range recipients {
		if seenUserID[r.UserID] {
			continue
		}
		seenUserID[r.UserID] = true
		if err := enqueueMailPoll(ctx, tx, mailPollPayload{PollID: pollID, Event: "finalized", UserID: r.UserID}); err != nil {
			return 0, err
		}
		sent++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	// finalizePoll's own recordPollFinalized call (polls.functions.ts) — reached only on a genuine
	// not-decided -> decided transition. Post-commit, best-effort — see recordStats.
	s.recordStats(ctx, map[string]int64{rooms.StatsPollsFinalized: 1})

	return sent, nil
}

// Finalize ports finalizePoll — see FinalizeWithCount for the body; this is the error-only
// signature the brief pinned and every non-HTTP caller uses.
func (s *Service) Finalize(ctx context.Context, pollID, orgID, optionID, actorUserID string) error {
	_, err := s.FinalizeWithCount(ctx, pollID, orgID, optionID, actorUserID)
	return err
}
```

Keep the existing doc comment block that precedes today's `Finalize` (it documents the recipient rules) directly above `FinalizeWithCount`'s own comment.

In `internal/polls/handlers.go`, replace the tail of `handleFinalize` (from `if err := s.Finalize(` to the end of the function) with:

```go
	sent, err := s.FinalizeWithCount(r.Context(), pollID, sess.ActiveOrgID, req.OptionID, sess.UserID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// finalizePoll (polls.functions.ts) returned `{ sent }`, and the SPA's FinalizeDialog toast
	// prints exactly that count; the fresh PollView the client needs arrives through its own
	// router.invalidate() (onFinalized) — see web/src/components/poll/FinalizeDialog.tsx.
	httpserver.JSON(w, http.StatusOK, map[string]int{"sent": sent})
```

- [ ] **Step 4: Run the Go tests**

Run: `go build ./... && go test ./internal/polls/ -run 'TestHandlerFinalize|TestFinalize' -v`
Expected: PASS (both new and existing finalize tests).

- [ ] **Step 5: Write the web contract test (msw, same pattern as `client.test.ts`)**

Create `web/src/api/__tests__/polls.test.ts`:

```ts
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { finalizePoll } from '#/api/polls'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('finalizePoll', () => {
  it('posts the option and returns the server-reported { sent } count', async () => {
    let seenBody: unknown = null
    server.use(
      http.post('/api/v1/polls/abc/finalize', async ({ request }) => {
        seenBody = await request.json()
        return HttpResponse.json({ sent: 3 })
      }),
    )

    const result = await finalizePoll('abc', 'opt-1')

    expect(seenBody).toEqual({ optionId: 'opt-1' })
    expect(result).toEqual({ sent: 3 })
  })
})
```

Run: `cd web && bunx vitest run src/api/__tests__/polls.test.ts`
Expected: 1 passed.

- [ ] **Step 6: Commit**

```bash
git add internal/polls/service.go internal/polls/handlers.go internal/polls/handlers_test.go web/src/api/__tests__/polls.test.ts
git commit -m "fix(polls): finalize responds with the {sent} recipient count again

FinalizeWithCount counts the distinct recipients it enqueues finalized mail
for; handleFinalize returns {sent} like the old finalizePoll so the SPA's
toast stops printing undefined. Finalize keeps its error-only signature.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 5: Deleting a poll removes its notification_subscriptions rows

Finding: "Deleting a poll no longer removes its notification_subscriptions rows".

**Files:**
- Modify: `internal/polls/queries/polls.sql` (new query), regenerate `internal/polls/queries/*.go`
- Modify: `internal/polls/service.go:698-730` (`Delete`)
- Test: `internal/polls/service_test.go` (`TestDelete`)

**Interfaces:**
- Produces: `queries.DeleteSubscriptionsByScope(ctx, DeleteSubscriptionsByScopeParams{ScopeType, ScopeID string}) error`.

- [ ] **Step 1: Write the failing test**

Add a third subtest inside `TestDelete` in `internal/polls/service_test.go` (after "deleting twice returns ErrNotFound"):

```go
	t.Run("removes the poll's notification_subscriptions rows (the manual cascade for the polymorphic scope)", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID) // Create subscribes the creator
		mateID := seedUser(t, d)
		if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
			t.Fatalf("SetFollowing: %v", err)
		}
		countSubs := func() int {
			var n int
			if err := d.QueryRowContext(ctx,
				`SELECT count(*) FROM notification_subscriptions WHERE scope_type = 'poll' AND scope_id = $1`, created.ID,
			).Scan(&n); err != nil {
				t.Fatalf("count subscriptions: %v", err)
			}
			return n
		}
		if got := countSubs(); got != 2 {
			t.Fatalf("subscriptions before delete = %d, want 2 (creator + follower)", got)
		}

		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got := countSubs(); got != 0 {
			t.Errorf("subscriptions after delete = %d, want 0", got)
		}
	})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/polls/ -run 'TestDelete' -v`
Expected: FAIL — "subscriptions after delete = 2, want 0".

- [ ] **Step 3: Add the query, regenerate, call it inside Delete's transaction**

Append to `internal/polls/queries/polls.sql`:

```sql
-- name: DeleteSubscriptionsByScope :exec
-- The manual cascade for the polymorphic (scope_type, scope_id) pair — no FK is possible there,
-- so deletePoll called deleteScopeSubscriptions (subscriptions.ts) explicitly; Delete (service.go)
-- does the same inside its own transaction.
DELETE FROM notification_subscriptions WHERE scope_type = $1 AND scope_id = $2;
```

Run: `sqlc generate`
Expected: `internal/polls/queries/polls.sql.go` and `querier.go` gain `DeleteSubscriptionsByScope`.

In `internal/polls/service.go` `Delete`, right after the `q.SoftDeletePoll(...)` block, add:

```go
	// deletePoll's deleteScopeSubscriptions (service.ts:436-448): subscriptions are keyed by a
	// polymorphic scope, so no FK cascades them — drop them here, in the same transaction.
	if err := q.DeleteSubscriptionsByScope(ctx, queries.DeleteSubscriptionsByScopeParams{
		ScopeType: "poll", ScopeID: pollID,
	}); err != nil {
		return err
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/polls/ -run 'TestDelete|TestHandlerAuthzRetrofitAcrossManagingEndpoints' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/polls/queries internal/polls/service.go internal/polls/service_test.go
git commit -m "fix(polls): cascade notification_subscriptions when a poll is deleted

Delete now runs DeleteSubscriptionsByScope('poll', id) in its transaction,
restoring deletePoll's manual cascade for the polymorphic scope.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 6: `ListMine` in one aggregate query

Finding: "Confirmed: poll list is 2N+1 queries" (polls half; bookings half is Plan D).

**Files:**
- Modify: `internal/polls/queries/polls.sql` (new `ListPollSummariesByOrg`), regenerate
- Modify: `internal/polls/service.go:829-870` (`ListMine`)
- Test: `internal/polls/service_test.go` (`TestListMine`)

**Interfaces:**
- Produces: `queries.ListPollSummariesByOrg(ctx, organizationID int64) ([]queries.ListPollSummariesByOrgRow, error)` with fields `ID, Title, Type, Status string; DeadlineAt sql.NullTime; CreatedAt, UpdatedAt time.Time; ParticipantCount, ClaimCount int64`.

- [ ] **Step 1: Write the failing test**

Add a third subtest to `TestListMine` in `internal/polls/service_test.go`:

```go
	t.Run("aggregates per poll: counts never bleed across polls or multiply across joins", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		a := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
		b := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 1)
		empty := createTestPoll(t, ctx, s, orgID, ownerID)

		// Poll A: two participants, three yes-votes. Poll B: one participant, one yes-vote and one
		// no-vote (a no-vote is not a claim). Poll "empty": nothing.
		seedParticipant(t, d, a.ID, "Alice", map[string]string{a.Options[0].ID: "yes", a.Options[1].ID: "yes"}, "")
		seedParticipant(t, d, a.ID, "Bob", map[string]string{a.Options[0].ID: "yes"}, "")
		seedParticipant(t, d, b.ID, "Cleo", map[string]string{b.Options[0].ID: "yes"}, "")
		seedParticipant(t, d, b.ID, "Dan", map[string]string{b.Options[0].ID: "no"}, "")

		list, err := s.ListMine(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMine: %v", err)
		}
		want := map[string][2]int{a.ID: {2, 3}, b.ID: {2, 1}, empty.ID: {0, 0}}
		for id, counts := range want {
			got := findSummary(list, id)
			if got == nil {
				t.Fatalf("summary for %s missing from %+v", id, list)
			}
			if got.ParticipantCount != counts[0] || got.ClaimCount != counts[1] {
				t.Errorf("%s: (participants, claims) = (%d, %d), want (%d, %d)", got.Title, got.ParticipantCount, got.ClaimCount, counts[0], counts[1])
			}
		}
	})
```

- [ ] **Step 2: Run it — it passes against the loop implementation (a regression guard for the rewrite)**

Run: `go test ./internal/polls/ -run TestListMine -v`
Expected: PASS. (The rewrite in Step 3 must keep it green; the deliverable here is one round trip instead of 2N+1, verified by reading the new `ListMine` body — it calls exactly one query method.)

- [ ] **Step 3: Add the aggregate query and rewrite `ListMine`**

Append to `internal/polls/queries/polls.sql`:

```sql
-- name: ListPollSummariesByOrg :many
-- The dashboard list in ONE round trip (listMyPolls's relational query in service.ts:247-268):
-- per-poll participant count and yes-vote ("claim") count as correlated aggregates, instead of
-- ListPollsByOrg + ListParticipantsByPoll + ListVotesByPoll per poll (2N+1).
SELECT
  p.id, p.title, p.type, p.status, p.deadline_at, p.created_at, p.updated_at,
  (SELECT count(*) FROM participants pa WHERE pa.poll_id = p.id) AS participant_count,
  (SELECT count(*) FROM votes v JOIN participants pa ON pa.id = v.participant_id
     WHERE pa.poll_id = p.id AND v.answer = 'yes') AS claim_count
FROM polls p
WHERE p.organization_id = $1 AND p.deleted_at IS NULL
ORDER BY p.created_at DESC;
```

Run: `sqlc generate`
Expected: `ListPollSummariesByOrg` + `ListPollSummariesByOrgRow` generated.

Replace `ListMine` in `internal/polls/service.go` with:

```go
// ListMine ports listMyPolls — one aggregate query (ListPollSummariesByOrg), never a per-poll
// loop: the dashboard loader calls this on every visit, so its cost must stay O(1) round trips.
func (s *Service) ListMine(ctx context.Context, orgID string) ([]PollSummary, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrForbidden
	}

	rows, err := s.q.ListPollSummariesByOrg(ctx, orgIDInt)
	if err != nil {
		return nil, err
	}

	out := make([]PollSummary, 0, len(rows))
	for _, p := range rows {
		out = append(out, PollSummary{
			ID:               p.ID,
			Title:            p.Title,
			Type:             p.Type,
			Status:           p.Status,
			DeadlineAt:       nullTimeToISO(p.DeadlineAt),
			ParticipantCount: int(p.ParticipantCount),
			ClaimCount:       int(p.ClaimCount),
			CreatedAt:        formatISO(p.CreatedAt),
			UpdatedAt:        formatISO(p.UpdatedAt),
		})
	}
	return out, nil
}
```

`ListPollsByOrg` has no remaining Go caller after this; delete its `-- name: ListPollsByOrg :many` block from `polls.sql` and run `sqlc generate` again so the generated method disappears too.

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go test ./internal/polls/ -run 'TestListMine|TestDelete|TestHandlerListMine' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/polls/queries internal/polls/service.go internal/polls/service_test.go
git commit -m "perf(polls): list polls with one aggregate query instead of 2N+1

ListPollSummariesByOrg computes participant and yes-vote counts as
correlated aggregates; ListMine no longer loads every participant and vote
row of every poll to count them.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 7: `MailSender` seam + per-user locale for account-holder poll mail

Findings: "Account-holder emails always render in English" (poll part) and "Poll mails and roster are effectively English-only" (recipient-locale part).

**Files:**
- Create: `internal/polls/locale.go`
- Modify: `internal/polls/service.go:30-42` (`Service` struct gains `locales LocaleSource`)
- Modify: `internal/polls/timers.go` (`MailSender` interface; `RegisterJobs`, `handleMailPollJob`, `sendFinalizedMail`, `sendClosedMail`, `sendReminderMail`, `sendDigestMail` take `MailSender`; user-recipient locale)
- Modify: `internal/polls/notifications.go:189` (`resolveRecipients` → real locale) and its package doc comment (lines 14–17)
- Modify: `cmd/whenweall/main.go:130-155` (construct `authSvc` before the worker starts; `pollsSvc.SetLocaleSource(authSvc)`)
- Test: `internal/polls/notifications_test.go`

**Interfaces:**
- Consumes: `auth.Service.LocaleFor(ctx, userID) string` (Plan A) — satisfies `LocaleSource` with no adapter.
- Produces:
  ```go
  // internal/polls/timers.go
  type MailSender interface {
  	AppURL() string
  	Send(ctx context.Context, msg mailer.Message) error
  }
  func (s *Service) RegisterJobs(w *jobs.Worker, m MailSender)   // *mailer.Mailer still satisfies it
  // internal/polls/locale.go
  type LocaleSource interface { LocaleFor(ctx context.Context, userID string) string }
  func (s *Service) SetLocaleSource(src LocaleSource)
  func (s *Service) userLocale(ctx context.Context, userID string) string // "en" when unset/empty
  ```
  Test helpers (package `polls_test`, `notifications_test.go`): `recordingMailer` (`sent []mailer.Message`, `byTemplate(name)`), `fakeLocales map[string]string`, `drainJobs(t, ctx, w)`. Tasks 8, 10 and 11 reuse them.

- [ ] **Step 1: Write the failing test (and the helpers it needs)**

Append to `internal/polls/notifications_test.go`:

```go
// recordingMailer is a polls.MailSender that records every Send instead of dialing SMTP — the
// seam send-path tests use to assert on the rendered Message (template, Data, attachments)
// directly, without Mailpit.
type recordingMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (r *recordingMailer) AppURL() string { return "https://whenweall.example" }

func (r *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return nil
}

func (r *recordingMailer) byTemplate(template string) []mailer.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []mailer.Message
	for _, m := range r.sent {
		if m.Template == template {
			out = append(out, m)
		}
	}
	return out
}

// fakeLocales is a polls.LocaleSource keyed by userID — the test stand-in for
// auth.Service.LocaleFor (Plan A), which this package never calls directly in tests.
type fakeLocales map[string]string

func (f fakeLocales) LocaleFor(_ context.Context, userID string) string {
	if l, ok := f[userID]; ok {
		return l
	}
	return "en"
}

// drainJobs runs w until a RunOnce claims nothing — every due job, and every job those jobs
// scheduled in turn (poll.digest -> mail:poll), has been processed.
func drainJobs(t *testing.T, ctx context.Context, w *jobs.Worker) {
	t.Helper()
	for i := 0; i < 50; i++ {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("drainJobs: jobs still pending after 50 rounds")
}

// TestUserRecipientMailUsesLocaleSource restores the user.locale half of the old recipients
// (main:src/server/notifications/recipients.ts:78, finalize-emails.ts:51): a user-identified
// recipient renders in the locale the LocaleSource (auth.Service.LocaleFor in production)
// reports, not a hard-coded "en".
func TestUserRecipientMailUsesLocaleSource(t *testing.T) {
	ctx := context.Background()

	t.Run("digest and finalized owner mail render in the user's locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		s.SetLocaleSource(fakeLocales{ownerID: "nb"})
		created := createTestPoll(t, ctx, s, orgID, ownerID) // Create subscribes the creator

		if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventResponseCreated, Name: "Ada"}); err != nil {
			t.Fatalf("EnqueueDigestItem: %v", err)
		}
		forceDue(t, d, "poll.digest")
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ""); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		digests := rec.byTemplate("digest")
		if len(digests) != 1 || digests[0].Data["Locale"] != "nb" {
			t.Errorf("digest mails = %+v, want exactly one with Locale nb", digests)
		}
		finalized := rec.byTemplate("finalized")
		if len(finalized) != 1 || finalized[0].Data["Locale"] != "nb" {
			t.Errorf("finalized mails = %+v, want exactly one (the owner) with Locale nb", finalized)
		}
	})

	t.Run("falls back to en when no LocaleSource is wired", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventResponseCreated, Name: "Ada"}); err != nil {
			t.Fatalf("EnqueueDigestItem: %v", err)
		}
		forceDue(t, d, "poll.digest")

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		if digests := rec.byTemplate("digest"); len(digests) != 1 || digests[0].Data["Locale"] != "en" {
			t.Errorf("digest mails = %+v, want exactly one with Locale en", digests)
		}
	})
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/polls/ -run TestUserRecipientMailUsesLocaleSource -v`
Expected: FAIL to compile — `s.RegisterJobs(w, rec)`: `*recordingMailer does not implement *mailer.Mailer`; `s.SetLocaleSource undefined`.

- [ ] **Step 3: Introduce the two seams**

Create `internal/polls/locale.go`:

```go
package polls

import "context"

// LocaleSource resolves a signed-in user's preferred mail locale. In production this is
// auth.Service (its LocaleFor reads user_preferences.locale, "en" fallback — Plan A); tests pass
// a map. nil (SetLocaleSource never called) means every user-identified recipient renders in "en",
// which is what this package did before user locales existed at all.
type LocaleSource interface {
	LocaleFor(ctx context.Context, userID string) string
}

// SetLocaleSource wires the user-locale lookup — a post-construction setter, like SetStats, so
// every existing NewService call site (tests included) stays unchanged. main.go calls it before
// the job worker starts, so no mail:poll job can run against a half-wired Service.
func (s *Service) SetLocaleSource(src LocaleSource) {
	s.locales = src
}

// userLocale is every user-recipient send path's locale lookup (resolveRecipients, and the
// userId branches of sendFinalizedMail/sendClosedMail/sendReminderMail/sendDigestMail): the
// LocaleSource's answer, or "en" when there is no source, no user, or an empty answer.
// Participants (guests) never come through here — they carry their own participants.locale
// column, read via orDefaultLocale.
func (s *Service) userLocale(ctx context.Context, userID string) string {
	if s.locales == nil || userID == "" {
		return "en"
	}
	if l := s.locales.LocaleFor(ctx, userID); l != "" {
		return l
	}
	return "en"
}
```

In `internal/polls/service.go`, add to the `Service` struct after `stats *rooms.StatsService`:

```go
	// locales resolves user recipients' mail locale — nil until SetLocaleSource (locale.go).
	locales LocaleSource
```

In `internal/polls/timers.go`:

1. Add after the `mailPollPayload` type:

```go
// MailSender is the narrow seam this package's send paths need from *mailer.Mailer — the
// canonical app origin for links, and Send. An interface (rather than *mailer.Mailer in every
// signature) so a test can record the rendered Message instead of running an SMTP server.
type MailSender interface {
	AppURL() string
	Send(ctx context.Context, msg mailer.Message) error
}

var _ MailSender = (*mailer.Mailer)(nil)
```

2. Change these signatures (bodies otherwise unchanged): `RegisterJobs(w *jobs.Worker, m MailSender)`, `handleMailPollJob(ctx context.Context, m MailSender, job jobs.Job)`, and `m MailSender` in `sendFinalizedMail`, `sendClosedMail`, `sendReminderMail`, `sendDigestMail`, `sendClaimConfirmationMail`. Update RegisterJobs' doc comment: "m is the real mailer (any MailSender) used only by mail:poll".

3. Locale of user recipients:
   - `sendFinalizedMail`, `case payload.UserID != "":` branch: `name, email, locale = displayName(u), u.Email, s.userLocale(ctx, payload.UserID)`.
   - `sendClosedMail`: `Data: map[string]any{"PollTitle": poll.Title, "PollURL": pollURL, "Locale": s.userLocale(ctx, payload.UserID)}`.
   - `sendReminderMail` and `sendDigestMail`: replace `"Locale": "en",` with `"Locale": s.userLocale(ctx, payload.UserID),`.

In `internal/polls/notifications.go`:
   - line 189: `out = append(out, Recipient{UserID: uid, Email: u.Email, Name: displayName(u), Locale: s.userLocale(ctx, uid)})`
   - Replace the package doc paragraph "User locale is NOT carried either: …" (lines 14–17) with:
     ```go
     // User locale comes from the LocaleSource wired via SetLocaleSource (locale.go) — auth.Service's
     // LocaleFor over user_preferences in production; participants keep their own locale column.
     ```

In `cmd/whenweall/main.go`, move the `authSvc, err := auth.New(cfg, sqlDB)` block (with its error check) up to directly after `bookingsSvc.RegisterJobs(worker, m)` — i.e. BEFORE `jobs.EnsureScheduled` and `go worker.Run(ctx)` — and add right after it:

```go
	// Plan C: user recipients' mail locale (user_preferences via auth.Service.LocaleFor). Wired
	// before worker.Run so the first mail:poll job the worker claims already sees it.
	pollsSvc.SetLocaleSource(authSvc)
```

- [ ] **Step 4: Run the tests and the build**

Run: `go build ./... && go vet ./... && go test ./internal/polls/ -run 'TestUserRecipientMailUsesLocaleSource|TestResolveRecipientsViaDigest|TestMailPollDeliversRealMail' -v`
Expected: build OK; PASS (`TestMailPollDeliversRealMail` skips without Docker for Mailpit — that is its existing behaviour).

- [ ] **Step 5: Commit**

```bash
git add internal/polls/locale.go internal/polls/service.go internal/polls/timers.go internal/polls/notifications.go internal/polls/notifications_test.go cmd/whenweall/main.go
git commit -m "feat(polls): render account-holder poll mail in the user's locale

Adds a LocaleSource seam fed by auth.Service.LocaleFor and a MailSender seam
so send paths are testable without SMTP; digest/closed/reminder/finalized
mail to users now use the stored locale instead of hard-coded en.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 8: Locale-aware option labels in mail and roster; request locale for the roster

Findings: "Dates/times in emails are English 12-hour strings" (poll part) and "Poll mails and roster are effectively English-only" (label + roster part).

**Files:**
- Create: `internal/httpserver/locale.go`, `internal/httpserver/locale_test.go`
- Modify: `internal/polls/timers.go:663-700` (`optionLabelText` gains a locale), `sendFinalizedMail`, `sendClaimConfirmationMail`
- Modify: `internal/polls/roster.go` (`BuildRosterCSV` gains `locale`; drop the "Deviation" paragraph in its file comment)
- Modify: `internal/polls/handlers.go` (`handleRosterCSV`)
- Test: `internal/polls/notifications_test.go`, `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `mailer.FormatDate`, `mailer.FormatDateTime`, `mailer.FormatTimeRange`, `mailer.SupportedLocales` (Plan A); `recordingMailer`, `drainJobs` (Task 7).
- Produces:
  ```go
  // internal/httpserver/locale.go
  const LocaleCookieName = "whenweall_locale"
  func RequestLocale(r *http.Request, supported []string) string
  // internal/polls
  func optionLabelText(o queries.PollOption, locale, timezone string) string   // unexported
  func (s *Service) BuildRosterCSV(ctx context.Context, pollID, locale string) (string, error)
  ```
  Label shape (formatOptionLabel's primary/secondary/tertiary joined): text → label; date → `FormatDate(locale, start, UTC)`; datetime without end → `FormatDateTime(locale, start, tz)`; with end → `FormatDate(locale, start, tz) + ", " + FormatTimeRange(locale, start, end, tz)`. Fixed fixture used by every test below: option 2026-09-01T16:30:00Z–17:30:00Z in Europe/Oslo → en `"Tue 1 Sep, 18:30–19:30"`, nb `"tir. 1. sep., 18:30–19:30"`.

- [ ] **Step 1: Write the failing tests**

Create `internal/httpserver/locale_test.go`:

```go
package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
)

func TestRequestLocale(t *testing.T) {
	supported := []string{"en", "nb"}
	cases := []struct {
		name, cookie, acceptLanguage, want string
	}{
		{"nothing → first supported", "", "", "en"},
		{"cookie wins", "nb", "en", "nb"},
		{"unsupported cookie ignored, Accept-Language used", "de", "nb-NO,nb;q=0.9,en;q=0.8", "nb"},
		{"Accept-Language base-language match", "", "nb-NO", "nb"},
		{"Accept-Language q-ordering", "", "en;q=0.5, nb;q=0.9", "nb"},
		{"Accept-Language skips unsupported and wildcard", "", "de, *;q=0.1, en;q=0.2", "en"},
		{"Accept-Language q=0 excluded", "", "nb;q=0, en", "en"},
		{"garbage header → default", "", ";;,,q=", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: httpserver.LocaleCookieName, Value: tc.cookie})
			}
			if tc.acceptLanguage != "" {
				r.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if got := httpserver.RequestLocale(r, supported); got != tc.want {
				t.Errorf("RequestLocale = %q, want %q", got, tc.want)
			}
		})
	}
}
```

Append to `internal/polls/notifications_test.go`:

```go
// fixtureStart/fixtureEnd is the one dated slot every locale-label test uses: Tue 1 Sep 2026,
// 18:30–19:30 Europe/Oslo (16:30–17:30 UTC).
const (
	fixtureStart = "2026-09-01T16:30:00.000Z"
	fixtureEnd   = "2026-09-01T17:30:00.000Z"
	wantLabelEN  = "Tue 1 Sep, 18:30–19:30"
	wantLabelNB  = "tir. 1. sep., 18:30–19:30"
)

// createDatedSignupPoll creates a sign-up sheet (Europe/Oslo) whose single slot is the fixture
// datetime above, with unlimited capacity and maxClaims 2.
func createDatedSignupPoll(t *testing.T, ctx context.Context, s *polls.Service, orgID, userID string) *polls.PollView {
	t.Helper()
	view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
		Type: polls.PollTypeSignup, Title: "Shifts", Timezone: "Europe/Oslo",
		Options:         []polls.OptionInput{withCapacity(datetimeOption(fixtureStart, fixtureEnd), nil)},
		SignupMaxClaims: intPtr(2),
	})
	if err != nil {
		t.Fatalf("Create (dated signup): %v", err)
	}
	return view
}

// TestOptionLabelsFollowRecipientLocale ports formatOptionLabel's per-locale output
// (main:src/lib/__tests__/time.test.ts:18-49: en "Tue 1 Sep" / "18:30" / "– 19:30", nb weekday
// "tir…") as rendered into claim-confirmation Slots and the finalized mail's OptionLabel, via
// Plan A's mailer.Format* helpers.
func TestOptionLabelsFollowRecipientLocale(t *testing.T) {
	ctx := context.Background()

	t.Run("claim confirmation slots use the participant's locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createDatedSignupPoll(t, ctx, s, orgID, ownerID)
		slot := created.Options[0].ID

		if _, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Kari", Email: strPtr("kari@example.com"), Locale: strPtr("nb")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim (nb): %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Ada", Email: strPtr("ada@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim (en): %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		want := map[string][2]string{"kari@example.com": {"nb", wantLabelNB}, "ada@example.com": {"en", wantLabelEN}}
		mails := rec.byTemplate("claim_confirmation")
		if len(mails) != 2 {
			t.Fatalf("claim_confirmation mails = %d, want 2", len(mails))
		}
		for _, m := range mails {
			exp := want[m.To]
			slots, _ := m.Data["Slots"].([]string)
			if m.Data["Locale"] != exp[0] || len(slots) != 1 || slots[0] != exp[1] {
				t.Errorf("mail to %s: Locale=%v Slots=%v, want Locale=%s Slots=[%s]", m.To, m.Data["Locale"], slots, exp[0], exp[1])
			}
		}
	})

	t.Run("finalized mail's OptionLabel uses the recipient's locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		s.SetLocaleSource(fakeLocales{ownerID: "nb"})
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Kickoff", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{datetimeOption(fixtureStart, fixtureEnd)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Ada", Email: strPtr("ada@example.com"), Answers: map[string]string{created.Options[0].ID: "yes"},
		}, polls.Viewer{}); err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ""); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		var gotEN, gotNB bool
		for _, m := range rec.byTemplate("finalized") {
			switch {
			case m.To == "ada@example.com" && m.Data["Locale"] == "en" && m.Data["OptionLabel"] == wantLabelEN:
				gotEN = true
			case m.To != "ada@example.com" && m.Data["Locale"] == "nb" && m.Data["OptionLabel"] == wantLabelNB:
				gotNB = true
			default:
				t.Errorf("unexpected finalized mail: To=%s Locale=%v OptionLabel=%v", m.To, m.Data["Locale"], m.Data["OptionLabel"])
			}
		}
		if !gotEN || !gotNB {
			t.Errorf("finalized mails: en participant seen=%v, nb owner seen=%v; want both", gotEN, gotNB)
		}
	})
}
```

Append to `internal/polls/handlers_test.go`:

```go
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/httpserver/ -run TestRequestLocale -v && go test ./internal/polls/ -run 'TestOptionLabelsFollowRecipientLocale|TestHandlerRosterCSVUsesRequestLocale' -v`
Expected: FAIL to compile — `httpserver.RequestLocale`/`LocaleCookieName` undefined; then (after Step 3's first half) polls labels come out as "Tuesday, September 1, 2026 6:30 PM – 7:30 PM".

- [ ] **Step 3: Implement `RequestLocale`**

Create `internal/httpserver/locale.go`:

```go
package httpserver

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// LocaleCookieName is the cookie paraglide persists the SPA's locale switch in
// (web/vite.config.ts's `cookieName`). Server-rendered text for a browser request (the roster
// CSV's slot labels) reads it first, so what the user sees in the app and what they download
// agree.
const LocaleCookieName = "whenweall_locale"

// RequestLocale resolves the locale a request wants server-rendered text in: the SPA's locale
// cookie when it names a supported locale, else the best Accept-Language match (RFC 9110
// q-ordered, base-language match so "nb-NO" → "nb"), else supported[0]. Only members of
// supported are ever returned — pass mailer.SupportedLocales.
func RequestLocale(r *http.Request, supported []string) string {
	if c, err := r.Cookie(LocaleCookieName); err == nil {
		if l, ok := matchLocale(c.Value, supported); ok {
			return l
		}
	}
	for _, tag := range parseAcceptLanguage(r.Header.Get("Accept-Language")) {
		if l, ok := matchLocale(tag, supported); ok {
			return l
		}
	}
	if len(supported) > 0 {
		return supported[0]
	}
	return "en"
}

// matchLocale maps a language tag onto supported: exact (case-insensitive) or by base language.
func matchLocale(tag string, supported []string) (string, bool) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return "", false
	}
	base := tag
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		base = tag[:i]
	}
	for _, s := range supported {
		if s == tag || s == base {
			return s, true
		}
	}
	return "", false
}

// parseAcceptLanguage returns the header's language tags ordered by descending q (header order
// for ties); "*" and q=0 entries are dropped.
func parseAcceptLanguage(header string) []string {
	type entry struct {
		tag string
		q   float64
	}
	var entries []entry
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				if v, err := strconv.ParseFloat(p[2:], 64); err == nil {
					q = v
				}
			}
		}
		if q <= 0 {
			continue
		}
		entries = append(entries, entry{tag: tag, q: q})
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].q > entries[b].q })
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.tag
	}
	return out
}
```

- [ ] **Step 4: Make `optionLabelText` locale-aware and thread the locale through**

Replace `optionLabelText` in `internal/polls/timers.go` with:

```go
// optionLabelText renders one poll option as a plain-text label in the recipient's locale —
// formatOptionLabel (src/lib/time.ts) with primary/secondary/tertiary joined into one string,
// built on internal/mailer's locale-aware formatters: a text option is its label; a date option
// is a calendar date (FormatDate in UTC — never shifted by the poll's timezone); a datetime option
// is FormatDateTime in the poll's timezone, or "<date>, <start>–<end>" when the option has an
// end. Used by transactional mail (finalized/claim_confirmation) and the roster CSV.
func optionLabelText(o queries.PollOption, locale, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}

	switch OptionKind(o.Kind) {
	case OptionKindText:
		if o.Label.Valid {
			return o.Label.String
		}
		return ""
	case OptionKindDate:
		if !o.StartAt.Valid {
			return ""
		}
		return mailer.FormatDate(locale, o.StartAt.Time, time.UTC)
	case OptionKindDatetime:
		if !o.StartAt.Valid {
			return ""
		}
		if o.EndAt.Valid {
			return mailer.FormatDate(locale, o.StartAt.Time, loc) + ", " +
				mailer.FormatTimeRange(locale, o.StartAt.Time, o.EndAt.Time, loc)
		}
		return mailer.FormatDateTime(locale, o.StartAt.Time, loc)
	default:
		return ""
	}
}
```

In `sendFinalizedMail`, the `"OptionLabel"` entry becomes `optionLabelText(*option, locale, poll.Timezone)` (the `locale` variable is already resolved just above the `m.Send` call). In `sendClaimConfirmationMail`, hoist `locale := orDefaultLocale(p.Locale)` to right after the `p.PollID != poll.ID || !p.Email.Valid` check, use `optionLabelText(o, locale, poll.Timezone)` in the slots loop, and pass `"Locale": locale` in Data.

In `internal/polls/roster.go`: change the signature to `func (s *Service) BuildRosterCSV(ctx context.Context, pollID, locale string) (string, error)`, use `optionLabelText(option, locale, poll.Timezone)`, and replace the file comment's "Deviation: …" paragraph with:

```go
// Labels render through optionLabelText (timers.go) in the caller's locale — the roster route's
// getLocale() in the TS source becomes httpserver.RequestLocale at the handler.
```

In `internal/polls/handlers.go` `handleRosterCSV`: `csv, err := s.BuildRosterCSV(r.Context(), pollID, httpserver.RequestLocale(r, mailer.SupportedLocales))` and add `"github.com/refsdal/whenweall/internal/mailer"` to the imports.

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./internal/httpserver/ -run TestRequestLocale -v && go test ./internal/polls/ -run 'TestOptionLabelsFollowRecipientLocale|TestHandlerRosterCSV|TestMailPollDeliversRealMail|TestUserRecipientMailUsesLocaleSource' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/httpserver/locale.go internal/httpserver/locale_test.go internal/polls/timers.go internal/polls/roster.go internal/polls/handlers.go internal/polls/notifications_test.go internal/polls/handlers_test.go
git commit -m "feat(polls): locale-aware option labels in mail and roster

optionLabelText renders through mailer.FormatDate/FormatDateTime/
FormatTimeRange in the recipient's locale (24h clock, nb day/month names);
the roster CSV uses the request locale via the new httpserver.RequestLocale
(whenweall_locale cookie, then Accept-Language).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 9: Roster rejects non-signup polls; id-bearing download filenames; roster CSV rules pinned

Findings: "Roster endpoint no longer rejects non-signup polls and changed its filename"; "Specific old assertions not re-expressed in Go" item (1) — roster CSV content rules.

**Files:**
- Modify: `internal/polls/errors.go` (new `ErrNotSignup`), `internal/polls/handlers.go` (`mapServiceError`, `handleRosterCSV`, `handleCalendarICS`), `internal/polls/roster.go` (`BuildRosterCSV` type gate)
- Create: `internal/polls/roster_test.go`
- Test: `internal/polls/handlers_test.go` (`TestHandlerRosterCSV`, `TestHandlerCalendarICS`)

**Interfaces:**
- Consumes: `BuildRosterCSV(ctx, pollID, locale)` (Task 8).
- Produces: `polls.ErrNotSignup` → HTTP 400 `not_signup`; `Content-Disposition: attachment; filename="whenweall-{id}-roster.csv"` and `…filename="whenweall-{id}.ics"`.

- [ ] **Step 1: Write the failing tests**

Create `internal/polls/roster_test.go`:

```go
package polls_test

// Ports main:src/server/polls/__tests__/roster.workers.test.ts case-for-case against
// BuildRosterCSV: header + one row per claim + a zero-claim row per untaken slot, the UTF-8 BOM,
// RFC 4180 quoting, formula-injection defusing, empty capacity for unlimited slots, NOT_FOUND —
// plus the route-level "not a sign-up sheet" rule the old roster route enforced.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

// rosterLines strips the BOM and splits the CSV into its non-empty CRLF-terminated lines.
func rosterLines(t *testing.T, csv string) []string {
	t.Helper()
	if !strings.HasPrefix(csv, "\uFEFF") {
		t.Fatalf("csv does not start with a UTF-8 BOM: %q", csv[:min(len(csv), 12)])
	}
	var out []string
	for _, l := range strings.Split(strings.TrimPrefix(csv, "\uFEFF"), "\r\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestBuildRosterCSV(t *testing.T) {
	ctx := context.Background()

	t.Run("emits a header row, one row per claim, and a zero-claim row for an unclaimed slot", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(2), nil}, 0)
		if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Alice", Email: strPtr("alice@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim: %v", err)
		}

		csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
		if err != nil {
			t.Fatalf("BuildRosterCSV: %v", err)
		}
		lines := rosterLines(t, csv)
		if lines[0] != "slot,capacity,claimed,participant,email" {
			t.Errorf("header = %q", lines[0])
		}
		if !strings.Contains(csv, "Slot 0,2,1,Alice,alice@example.com") {
			t.Errorf("missing Alice's claim row: %q", csv)
		}
		// Slot 1 (unlimited, unclaimed): one row, empty capacity, zero claimed, empty participant/email.
		found := false
		for _, l := range lines {
			if l == "Slot 1,,0,," {
				found = true
			}
		}
		if !found {
			t.Errorf("missing zero-claim row %q in %q", "Slot 1,,0,,", lines)
		}
	})

	t.Run("quotes fields containing commas, quotes, or newlines per RFC 4180", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeSignup, Title: "Sheet", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{withCapacity(textOption(`Bring "snacks", please`), nil)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Bob, Jr.", Email: strPtr("bob@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim: %v", err)
		}

		csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
		if err != nil {
			t.Fatalf("BuildRosterCSV: %v", err)
		}
		for _, want := range []string{`"Bring ""snacks"", please"`, `"Bob, Jr."`} {
			if !strings.Contains(csv, want) {
				t.Errorf("csv missing %s: %q", want, csv)
			}
		}
	})

	for _, name := range []string{"=1+1", "+1+1", "-1+1", "@SUM(A1)"} {
		t.Run("prefixes a formula-looking name ("+name+") with a single quote", func(t *testing.T) {
			d := testdb.New(t)
			s := polls.NewService(d)
			orgID, ownerID := seedOrgAndUser(t, d)
			created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
			if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: name}, polls.Viewer{}); err != nil {
				t.Fatalf("Claim: %v", err)
			}

			csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
			if err != nil {
				t.Fatalf("BuildRosterCSV: %v", err)
			}
			if !strings.Contains(csv, ",'"+name) {
				t.Errorf("csv did not defuse %q: %q", name, csv)
			}
		})
	}

	t.Run("leaves capacity empty for unlimited slots and prints the number for capped slots", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(3), nil}, 0)

		csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
		if err != nil {
			t.Fatalf("BuildRosterCSV: %v", err)
		}
		lines := rosterLines(t, csv)
		var capped, unlimited bool
		for _, l := range lines {
			capped = capped || strings.HasPrefix(l, "Slot 0,3,")
			unlimited = unlimited || strings.HasPrefix(l, "Slot 1,,")
		}
		if !capped || !unlimited {
			t.Errorf("capacity columns wrong (capped=%v unlimited=%v): %q", capped, unlimited, lines)
		}
	})

	t.Run("ErrNotFound for a missing poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		if _, err := s.BuildRosterCSV(ctx, "missing12345", "en"); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrNotSignup for a scheduling poll (the old route's 400 'Not a sign-up sheet')", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if _, err := s.BuildRosterCSV(ctx, created.ID, "en"); !errors.Is(err, polls.ErrNotSignup) {
			t.Errorf("err = %v, want ErrNotSignup", err)
		}
	})
}
```

In `internal/polls/handlers_test.go`, add two subtests at the end of `TestHandlerRosterCSV` and one assertion to `TestHandlerCalendarICS`:

```go
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
```

and, in `TestHandlerCalendarICS` right after the `Content-Type` check:

```go
	if want := `attachment; filename="whenweall-` + created.ID + `.ics"`; rec.Header().Get("Content-Disposition") != want {
		t.Errorf("Content-Disposition = %q, want %q", rec.Header().Get("Content-Disposition"), want)
	}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/polls/ -run 'TestBuildRosterCSV|TestHandlerRosterCSV|TestHandlerCalendarICS' -v`
Expected: FAIL — `polls.ErrNotSignup` undefined; after adding it: the 400 subtest gets 200, filenames are `roster.csv` / `calendar.ics`.

- [ ] **Step 3: Implement**

`internal/polls/errors.go` — add to the `var (` block:

```go
	// ErrNotSignup is returned by BuildRosterCSV for a poll that is not a sign-up sheet: only
	// sheets have a roster. The old roster route answered 400 "Not a sign-up sheet" — owner-only
	// past the auth check, so there's nothing to leak by being explicit (its own comment).
	ErrNotSignup = errors.New("polls: not a sign-up sheet")
```

`internal/polls/handlers.go` `mapServiceError` — add before the `errors.Is(err, ErrNotFound)` case:

```go
	case errors.Is(err, ErrNotSignup):
		return http.StatusBadRequest, "not_signup", "not a sign-up sheet", nil, true
```

`internal/polls/roster.go` `BuildRosterCSV` — right after the `GetPoll` error handling:

```go
	if poll.Type != string(PollTypeSignup) {
		return "", ErrNotSignup
	}
```

`internal/polls/handlers.go` — the two download headers carry the poll id again (the old routes'
`whenweall-{id}-roster.csv` / `whenweall-{id}.ics`). In `handleRosterCSV`:

```go
	w.Header().Set("Content-Disposition", `attachment; filename="whenweall-`+pollID+`-roster.csv"`)
```

In `handleCalendarICS`, ignore the filename `BuildPollICS` returns (it stays `"calendar.ics"` — that is
still the finalized mail's attachment name, asserted by `TestMailPollDeliversRealMail`) and set the
download name yourself:

```go
	_, ics, err := BuildPollICS(r.Context(), s.q, pollID, pollURL)
	// ...existing error / nil handling unchanged...
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="whenweall-`+pollID+`.ics"`)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/polls/ -run 'TestBuildRosterCSV|TestHandlerRosterCSV|TestHandlerCalendarICS|TestBuildPollICS' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/polls/errors.go internal/polls/handlers.go internal/polls/roster.go internal/polls/roster_test.go internal/polls/handlers_test.go
git commit -m "fix(polls): roster rejects non-signup polls; id-bearing download names

BuildRosterCSV returns ErrNotSignup (400 not_signup) for scheduling polls,
downloads are whenweall-{id}-roster.csv / whenweall-{id}.ics again, and the
old roster unit rules (BOM, RFC 4180, formula defusing, empty capacity) are
pinned in roster_test.go.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 10: Claim confirmation mail attaches a multi-VEVENT `.ics`

Findings: "Claim confirmation email no longer attaches the multi-VEVENT .ics" / "…lost its multi-event .ics attachment".

**Files:**
- Modify: `internal/polls/ics.go` (new `BuildClaimICS`)
- Modify: `internal/polls/timers.go` (`sendClaimConfirmationMail`; delete its "Still without an .ics attachment" paragraph)
- Test: `internal/polls/ics_test.go`, `internal/polls/notifications_test.go`

**Interfaces:**
- Consumes: `icsStartFromOption`, `buildVeventLines`, `ics.BuildCalendar`; `recordingMailer`, `drainJobs`, `createDatedSignupPoll` (Tasks 7–8).
- Produces: `func BuildClaimICS(poll queries.Poll, claimed []queries.PollOption, pollURL string, now time.Time) []byte` — one VCALENDAR, one VEVENT per claimed option with calendar meaning, uid `<pollID>-<optionID>@whenweall`; nil when none has.

- [ ] **Step 1: Write the failing tests**

Append to `internal/polls/ics_test.go` (add `"database/sql"` and `"time"` to its imports):

```go
// TestBuildClaimICS ports buildIcsMulti as used by sendClaimConfirmation
// (main:src/server/notifications/claim-emails.ts:63-92 and its test's "ics attachment for
// date/datetime slots only" case): one VEVENT per claimed dated slot, uid {pollId}-{optionId}@whenweall,
// none at all for text-only slots. A pure function — no database.
func TestBuildClaimICS(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	poll := queries.Poll{
		ID: "poll1234abcd", Title: "Shifts", Timezone: "Europe/Oslo",
		Description: sql.NullString{String: "Bring gloves", Valid: true},
		Location:    sql.NullString{String: "Depot", Valid: true},
	}
	dateOpt := queries.PollOption{ID: "optDate", Kind: "date", StartAt: sql.NullTime{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Valid: true}}
	timedOpt := queries.PollOption{
		ID: "optTimed", Kind: "datetime",
		StartAt: sql.NullTime{Time: time.Date(2026, 9, 1, 16, 30, 0, 0, time.UTC), Valid: true},
		EndAt:   sql.NullTime{Time: time.Date(2026, 9, 1, 17, 30, 0, 0, time.UTC), Valid: true},
	}
	textOpt := queries.PollOption{ID: "optText", Kind: "text", Label: sql.NullString{String: "Bake a cake", Valid: true}}
	pollURL := "https://whenweall.example/p/" + poll.ID

	t.Run("one VEVENT per dated slot, text slots skipped", func(t *testing.T) {
		body := string(polls.BuildClaimICS(poll, []queries.PollOption{dateOpt, textOpt, timedOpt}, pollURL, now))
		if got := strings.Count(body, "BEGIN:VEVENT\r\n"); got != 2 {
			t.Fatalf("BEGIN:VEVENT count = %d, want 2: %q", got, body)
		}
		if strings.Count(body, "BEGIN:VCALENDAR\r\n") != 1 || !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
			t.Errorf("not a single VCALENDAR: %q", body)
		}
		for _, want := range []string{
			"UID:poll1234abcd-optDate@whenweall\r\n",
			"UID:poll1234abcd-optTimed@whenweall\r\n",
			"DTSTART;VALUE=DATE:20260901\r\n",
			"DTEND;VALUE=DATE:20260902\r\n",
			"DTSTART:20260901T163000Z\r\n",
			"DTEND:20260901T173000Z\r\n",
			"DTSTAMP:20260903T120000Z\r\n",
			"SUMMARY:Shifts\r\n",
			"DESCRIPTION:Bring gloves\r\n",
			"LOCATION:Depot\r\n",
			"URL:" + pollURL + "\r\n",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q: %q", want, body)
			}
		}
		if strings.Contains(body, "Bake a cake") {
			t.Errorf("text slot leaked into the calendar: %q", body)
		}
	})

	t.Run("nil when no claimed slot has calendar meaning", func(t *testing.T) {
		if got := polls.BuildClaimICS(poll, []queries.PollOption{textOpt}, pollURL, now); got != nil {
			t.Errorf("BuildClaimICS = %q, want nil", got)
		}
		if got := polls.BuildClaimICS(poll, nil, pollURL, now); got != nil {
			t.Errorf("BuildClaimICS(no options) = %q, want nil", got)
		}
	})
}
```

Append to `internal/polls/notifications_test.go`:

```go
// TestClaimConfirmationAttachesMultiEventICS: the sent claim_confirmation mail carries
// calendar.ics with one VEVENT per claimed dated slot (claim-emails.ts), and no attachment at all
// for a text-only sheet.
func TestClaimConfirmationAttachesMultiEventICS(t *testing.T) {
	ctx := context.Background()

	t.Run("dated slots: one attachment, one VEVENT per claim", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeSignup, Title: "Shifts", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{
				withCapacity(datetimeOption(fixtureStart, fixtureEnd), nil),
				withCapacity(datetimeOption("2026-09-02T16:30:00.000Z"), nil),
			},
			SignupMaxClaims: intPtr(2),
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		first, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Ada", Email: strPtr("ada@example.com")}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim 1: %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, created.Options[1].ID, polls.ClaimInput{ParticipantID: first.ParticipantID}, polls.Viewer{GuestParticipantID: first.ParticipantID}); err != nil {
			t.Fatalf("Claim 2: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		mails := rec.byTemplate("claim_confirmation")
		if len(mails) == 0 {
			t.Fatal("no claim_confirmation mail sent")
		}
		last := mails[len(mails)-1] // re-reads current claims at send time: both slots by now
		if len(last.Attachments) != 1 {
			t.Fatalf("attachments = %d, want 1", len(last.Attachments))
		}
		att := last.Attachments[0]
		if att.Filename != "calendar.ics" || att.ContentType != "text/calendar" {
			t.Errorf("attachment = %q/%q, want calendar.ics/text/calendar", att.Filename, att.ContentType)
		}
		body := string(att.Content)
		if got := strings.Count(body, "BEGIN:VEVENT\r\n"); got != 2 {
			t.Errorf("BEGIN:VEVENT count = %d, want 2: %q", got, body)
		}
		for _, o := range created.Options {
			if !strings.Contains(body, "UID:"+created.ID+"-"+o.ID+"@whenweall\r\n") {
				t.Errorf("body missing UID for option %s: %q", o.ID, body)
			}
		}
	})

	t.Run("text-only slots: no attachment", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Bob", Email: strPtr("bob@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		mails := rec.byTemplate("claim_confirmation")
		if len(mails) != 1 || len(mails[0].Attachments) != 0 {
			t.Errorf("mails = %+v, want exactly one with no attachments", mails)
		}
	})
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/polls/ -run 'TestBuildClaimICS|TestClaimConfirmationAttachesMultiEventICS' -v`
Expected: FAIL to compile — `polls.BuildClaimICS` undefined.

- [ ] **Step 3: Implement `BuildClaimICS` and attach it**

Append to `internal/polls/ics.go`:

```go
// BuildClaimICS ports buildIcsMulti as sendClaimConfirmation used it (claim-emails.ts): one
// VCALENDAR holding one VEVENT per claimed option that has calendar meaning (icsStartFromOption —
// text slots contribute nothing), so a participant holding several shifts adds all of them from
// one attachment. UID is "<pollID>-<optionID>@whenweall" (distinct from BuildPollICS's
// "<pollID>@whenweall" for the finalized option, so the two never collide in a calendar). Returns
// nil when no claimed option has calendar meaning — the caller then attaches nothing. claimed
// should already be the participant's own claimed options in position order.
func BuildClaimICS(poll queries.Poll, claimed []queries.PollOption, pollURL string, now time.Time) []byte {
	var lines []string
	for _, o := range claimed {
		start, ok := icsStartFromOption(o)
		if !ok {
			continue
		}
		event := icsEvent{
			uid:   poll.ID + "-" + o.ID + "@whenweall",
			title: poll.Title,
			url:   pollURL,
			start: start,
		}
		if poll.Description.Valid {
			event.description = poll.Description.String
		}
		if poll.Location.Valid {
			event.location = poll.Location.String
		}
		lines = append(lines, buildVeventLines(event, now)...)
	}
	if len(lines) == 0 {
		return nil
	}
	return ics.BuildCalendar(lines)
}
```

In `internal/polls/timers.go` `sendClaimConfirmationMail`: delete the doc-comment paragraph beginning "Still without an .ics attachment" and replace it with "Attaches BuildClaimICS's multi-VEVENT calendar.ics whenever any claimed slot has calendar meaning (claim-emails.ts's buildIcsMulti)." Then replace the slots loop and the `m.Send` call with:

```go
	slots := make([]string, 0, len(claimed))
	claimedOptions := make([]queries.PollOption, 0, len(claimed))
	for _, o := range options {
		if claimed[o.ID] {
			slots = append(slots, optionLabelText(o, locale, poll.Timezone))
			claimedOptions = append(claimedOptions, o)
		}
	}

	var attachments []mailer.Attachment
	if cal := BuildClaimICS(poll, claimedOptions, pollURL, time.Now()); cal != nil {
		attachments = []mailer.Attachment{{Filename: "calendar.ics", ContentType: "text/calendar", Content: cal}}
	}

	return m.Send(ctx, mailer.Message{
		To:       p.Email.String,
		Template: "claim_confirmation",
		Data: map[string]any{
			"Name":      p.Name,
			"PollTitle": poll.Title,
			"PollURL":   pollURL,
			"Slots":     slots,
			"Locale":    locale,
		},
		Attachments: attachments,
	})
```

(`locale` is the variable Task 8 hoisted; `options` is `ListOptionsByPoll`'s position-ordered result, so `claimedOptions` is already in position order.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/polls/ -run 'TestBuildClaimICS|TestClaimConfirmationAttachesMultiEventICS|TestBuildPollICS|TestOptionLabelsFollowRecipientLocale' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/polls/ics.go internal/polls/timers.go internal/polls/ics_test.go internal/polls/notifications_test.go
git commit -m "feat(polls): attach a multi-VEVENT calendar.ics to claim confirmations

BuildClaimICS builds one VEVENT per claimed dated slot (uid
{pollId}-{optionId}@whenweall) and sendClaimConfirmationMail attaches it,
restoring claim-emails.ts's buildIcsMulti behaviour.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 11: A digest item enqueued mid-run never re-sends already-delivered items

Findings: "Digest item enqueued while the digest job is running re-sends already-delivered items" / "…causes the whole digest to be re-sent".

**Files:**
- Modify: `internal/jobs/worker.go` (export `ProcessClaimed` test seam)
- Modify: `internal/polls/timers.go` (`EnqueueDigestItem` fresh-window rule; new `takeDigestItems`; `RegisterJobs`'s `poll.digest` closure)
- Test: `internal/polls/notifications_test.go`

**Interfaces:**
- Consumes: `jobs.ClaimDue(ctx, tx db.DBTX, replicaID string, limit int) ([]jobs.Job, error)`, `jobs.Schedule` upsert semantics (ON CONFLICT swaps in `EXCLUDED.id`), `recordingMailer`, `drainJobs`.
- Produces: `func (w *jobs.Worker) ProcessClaimed(ctx context.Context, job jobs.Job)`; unexported `func (s *Service) takeDigestItems(ctx, jobID, pollID string) (owned bool, err error)`.

Mechanics of the fix (both halves are needed):
1. **Handler takes ownership first.** Before fanning out, the `poll.digest` handler — under the same `pg_advisory_xact_lock(hashtext('poll.digest:' || pollID))` `EnqueueDigestItem` takes — empties the item list of the row **by the exact id it was claimed under**. `RowsAffected == 0` means an `EnqueueDigestItem` already merged this batch into a replacement row (jobs.Schedule swapped the id) which the next poll will process in full, so the handler sends nothing. `== 1` means the batch is now exclusively the handler's: any later enqueue sees an empty accumulator.
2. **Enqueue never joins an emptied batch's timer.** When the existing row has zero items, `EnqueueDigestItem` arms a fresh `digestDelay` window instead of inheriting the taken batch's (past) `run_at`.

- [ ] **Step 1: Write the failing test**

Append to `internal/polls/notifications_test.go` (imports already include `encoding/json`, `time`, `jobs`):

```go
// TestDigestItemEnqueuedMidRunIsNotResent reproduces the race deterministically by driving the
// worker's two steps by hand: ClaimDue (the row is now "mid-run") -> EnqueueDigestItem lands
// -> the claimed job's handler runs (ProcessClaimed) -> the next poll runs. Before the fix, the
// handler fanned out its stale batch AND the merged replacement row was processed again, so the
// owner received Ada twice. PollRoom.ts's #clearDigest-after-send made this impossible by
// construction; takeDigestItems is that guarantee's Postgres form.
func TestDigestItemEnqueuedMidRunIsNotResent(t *testing.T) {
	ctx := context.Background()

	enqueue := func(t *testing.T, s *polls.Service, pollID, name string) {
		t.Helper()
		if err := s.EnqueueDigestItem(ctx, pollID, polls.DigestItem{Event: polls.EventResponseCreated, Name: name}); err != nil {
			t.Fatalf("EnqueueDigestItem(%s): %v", name, err)
		}
	}
	claimOne := func(t *testing.T, d *sql.DB) jobs.Job {
		t.Helper()
		claimed, err := jobs.ClaimDue(ctx, d, "test-replica", 20)
		if err != nil {
			t.Fatalf("ClaimDue: %v", err)
		}
		if len(claimed) != 1 || claimed[0].Kind != "poll.digest" {
			t.Fatalf("claimed = %+v, want exactly one poll.digest job", claimed)
		}
		return claimed[0]
	}
	digestRow := func(t *testing.T, d *sql.DB, pollID string) (runAt time.Time, names []string) {
		t.Helper()
		var raw []byte
		err := d.QueryRowContext(ctx,
			`SELECT run_at, payload FROM scheduled_jobs WHERE kind = 'poll.digest' AND room_key = $1`, "poll:"+pollID,
		).Scan(&runAt, &raw)
		if err != nil {
			t.Fatalf("digest row: %v", err)
		}
		var p struct {
			Items []polls.DigestItem `json:"items"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("decode digest payload %s: %v", raw, err)
		}
		for _, it := range p.Items {
			names = append(names, it.Name)
		}
		return runAt, names
	}

	t.Run("item landing between claim and handler: one digest, each item exactly once", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)

		enqueue(t, s, created.ID, "Ada")
		forceDue(t, d, "poll.digest")
		stale := claimOne(t, d) // the worker holds [Ada] "mid-run"
		enqueue(t, s, created.ID, "Bob")
		w.ProcessClaimed(ctx, stale) // the held job's handler finally runs
		if n := len(rec.byTemplate("digest")) + countJobs(t, d, "mail:poll"); n != 0 {
			t.Fatalf("the superseded job sent/queued %d digest mails, want 0 (its batch now lives in the replacement row)", n)
		}

		forceDue(t, d, "poll.digest")
		drainJobs(t, ctx, w)

		digests := rec.byTemplate("digest")
		if len(digests) != 1 {
			t.Fatalf("digest mails = %d, want exactly 1: %+v", len(digests), digests)
		}
		lines, _ := digests[0].Data["Lines"].([]mailer.DigestLine)
		if len(lines) != 1 || lines[0].Event != "response.created" || lines[0].Count != 2 ||
			len(lines[0].Names) != 2 || lines[0].Names[0] != "Ada" || lines[0].Names[1] != "Bob" {
			t.Errorf("digest lines = %+v, want one response.created line, count 2, names [Ada Bob]", lines)
		}
		if n := countJobs(t, d, "poll.digest"); n != 0 {
			t.Errorf("poll.digest rows left = %d, want 0", n)
		}
	})

	t.Run("item landing after the handler took the batch starts a fresh window", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)

		enqueue(t, s, created.ID, "Ada")
		forceDue(t, d, "poll.digest")
		held := claimOne(t, d)
		// Exactly what takeDigestItems does at the top of the handler (the handler's fan-out is
		// simulated as already done): the accumulator is emptied in place, id unchanged.
		if _, err := d.ExecContext(ctx,
			`UPDATE scheduled_jobs SET payload = jsonb_build_object('pollId', $1::text, 'items', '[]'::jsonb) WHERE id = $2`,
			created.ID, held.ID,
		); err != nil {
			t.Fatalf("simulate takeDigestItems: %v", err)
		}

		before := time.Now()
		enqueue(t, s, created.ID, "Bob")

		runAt, names := digestRow(t, d, created.ID)
		if len(names) != 1 || names[0] != "Bob" {
			t.Errorf("items = %v, want [Bob] only", names)
		}
		if runAt.Before(before.Add(9 * time.Minute)) {
			t.Errorf("run_at = %s, want a fresh ~10 minute window (not the taken batch's past run_at)", runAt)
		}

		// The held job finishing now must send nothing — its id is gone.
		w.ProcessClaimed(ctx, held)
		if n := len(rec.byTemplate("digest")) + countJobs(t, d, "mail:poll"); n != 0 {
			t.Errorf("superseded job produced %d digest mails, want 0", n)
		}
		if _, names := digestRow(t, d, created.ID); len(names) != 1 || names[0] != "Bob" {
			t.Errorf("Bob's fresh window was disturbed: items = %v", names)
		}
	})
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/polls/ -run TestDigestItemEnqueuedMidRunIsNotResent -v`
Expected: FAIL to compile — `w.ProcessClaimed undefined`; once exported (Step 3 first bullet), subtest 1 fails with "the superseded job sent/queued 1 digest mails, want 0" and/or "digest mails = 2, want exactly 1", subtest 2 with "run_at … want a fresh ~10 minute window".

- [ ] **Step 3: Implement**

`internal/jobs/worker.go` — add after `RunOnce`:

```go
// ProcessClaimed runs one job ClaimDue already handed out through its handler and reconciles the
// outcome (Complete on success, fail otherwise) — RunOnce's per-job body. Exported purely as a
// test seam: a test can hold a claimed job "mid-run" (claim it, let something else happen, then
// process it) deterministically. Production code only ever reaches process via RunOnce.
func (w *Worker) ProcessClaimed(ctx context.Context, job Job) {
	w.process(ctx, job)
}
```

`internal/polls/timers.go`:

1. In `EnqueueDigestItem`, replace the two lines `payload.PollID = pollID` / `payload.Items = append(payload.Items, item)` with:

```go
	// A row whose items were just taken by the running handler (takeDigestItems empties them in
	// place; the row itself survives until Complete) is not a batch to join: start a fresh
	// debounce window rather than inheriting the taken batch's run_at, which is already in the
	// past and would send this lone item immediately.
	if scanErr == nil && len(payload.Items) == 0 {
		runAt = time.Now().Add(digestDelay)
	}
	payload.PollID = pollID
	payload.Items = append(payload.Items, item)
```

and append to the function's doc comment:

```go
//
// Interplay with the running handler (the mid-run race): jobs.Schedule's upsert swaps in a NEW id
// on conflict, so an enqueue that lands while a worker holds this row makes the worker's
// Complete(oldID) a no-op and the replacement row due again — before takeDigestItems existed that
// re-sent the whole old batch. Now the handler first empties the row's items under this same
// advisory lock (keyed by the id it claimed); an enqueue that wins the lock first merges the old
// batch into a replacement row (and the handler, finding its id gone, sends nothing — the
// replacement carries everything); an enqueue that comes second finds an empty accumulator and
// starts a fresh window (above). Either way every item is sent exactly once.
```

2. Add after `EnqueueDigestItem`:

```go
// takeDigestItems is the "poll.digest" handler's ownership step — see EnqueueDigestItem's doc
// comment for the race it closes. Under the same poll-scoped advisory lock, empty the item list
// of the row the worker claimed, addressed by that exact job id. Returns false when no row has
// that id anymore: EnqueueDigestItem has already merged this batch into a replacement row (the
// upsert swaps ids), which the next poll processes in full — so the caller must send nothing.
// After true, the batch the caller was handed is exclusively its own; any later enqueue sees an
// empty accumulator and starts a fresh window instead of re-queuing these items.
func (s *Service) takeDigestItems(ctx context.Context, jobID, pollID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('poll.digest:' || $1))`, pollID); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE scheduled_jobs SET payload = jsonb_build_object('pollId', $2::text, 'items', '[]'::jsonb) WHERE id = $1 AND kind = $3`,
		jobID, pollID, jobKindDigest,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}
```

3. In `RegisterJobs`, replace the `jobKindDigest` closure body's final `return s.handleDigestJob(ctx, p)` with:

```go
		owned, err := s.takeDigestItems(ctx, job.ID, p.PollID)
		if err != nil {
			return err
		}
		if !owned {
			return nil // merged into a replacement row by a concurrent enqueue; that row sends it
		}
		return s.handleDigestJob(ctx, p)
```

- [ ] **Step 4: Run the digest tests**

Run: `go test ./internal/polls/ -run 'TestDigestItemEnqueuedMidRunIsNotResent|TestResolveRecipientsViaDigest|TestEnqueueDigestItemConcurrentRace|TestUserRecipientMailUsesLocaleSource' -v && go test ./internal/jobs/`
Expected: PASS; `ok` for internal/jobs.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs/worker.go internal/polls/timers.go internal/polls/notifications_test.go
git commit -m "fix(polls): digest handler takes ownership of its batch before fan-out

Under the poll's advisory lock the poll.digest handler empties the claimed
row's items by job id and sends nothing if the id was already replaced; an
item enqueued into an emptied accumulator starts a fresh window. Items are
delivered exactly once even when one lands mid-run. jobs.Worker gains a
ProcessClaimed test seam.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 12: Poll websocket snapshot is anonymous; live hooks send neither token nor `?since=`

Findings: "Guest edit token is sent in the poll websocket URL"; "Reconnect backfill triggers one full loader refetch per replayed event" (chosen fix: omit `?since=` from the two snapshot-as-ground-truth consumers).

**Files:**
- Modify: `internal/rooms/endpoints.go` (`Register`, `pollWSHandler`), `internal/rooms/PROTOCOL.md`
- Test: `internal/rooms/endpoints_test.go`
- Modify: `web/src/lib/room-socket.ts`, `web/src/lib/use-live-poll.ts`, `web/src/lib/use-live-page.ts`, `web/src/routes/p/$id/index.tsx`
- Test: `web/src/lib/__tests__/room-socket.test.ts`, `web/src/lib/__tests__/use-live-poll.test.ts`, create `web/src/lib/__tests__/use-live-page.test.ts`

**Interfaces:**
- Produces (Go): `pollWSHandler(h *Hub, svc PollService) http.HandlerFunc` — no auth seam; `PollSnapshot` is always called with `PollViewer{}`. `rooms.PollService` is unchanged.
- Produces (web): `ConnectRoomOptions` loses `guestToken` and gains `backfill?: boolean` (default `true`; `false` = never send `?since=`); `useLivePoll(pollId, onEvent)` (two parameters).

- [ ] **Step 1: Write the failing Go test**

In `internal/rooms/endpoints_test.go`, extend `fakePollService` to record the viewer it was asked for:

```go
type fakePollService struct {
	byID       map[string]any
	onExists   func(pollID string)
	lastViewer rooms.PollViewer // what PollSnapshot was last asked to scope to
}

func (f *fakePollService) PollSnapshot(_ context.Context, pollID string, viewer rooms.PollViewer) (any, error) {
	f.lastViewer = viewer
	return f.byID[pollID], nil
}
```

and append:

```go
// TestPollWS_SnapshotIsAnonymousRegardlessOfCredentials: the poll route's snapshot is always the
// anonymous PollView. The SPA never read the viewer-scoped snapshot (useLivePoll refetches over
// REST on it), so the only effect of resolving identity here was to require the guest edit token
// on the WS URL — where reverse proxies log it. Nothing on this route reads a token or a session
// for the snapshot anymore.
func TestPollWS_SnapshotIsAnonymousRegardlessOfCredentials(t *testing.T) {
	server, a, polls, _ := newTestMux(t)
	polls.byID["p1"] = map[string]any{"id": "p1"}
	userID := a.login(&auth.Session{UserID: "u1", ActiveOrgID: "org-1"})

	conn := dialWSExpectSuccess(t, server, "/api/v1/polls/p1/ws?token=guest-token-for-participant-1",
		http.Header{"X-Test-Session": {userID}, "X-Guest-Token": {"guest-token-for-participant-1"}})
	defer func() { _ = conn.CloseNow() }()
	if frame := readWSFrame(t, conn, 5*time.Second); frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v, want snapshot", frame["type"])
	}

	if polls.lastViewer != (rooms.PollViewer{}) {
		t.Errorf("PollSnapshot viewer = %+v, want the zero (anonymous) viewer", polls.lastViewer)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/rooms/ -run TestPollWS_SnapshotIsAnonymousRegardlessOfCredentials -v`
Expected: FAIL — "PollSnapshot viewer = {UserID:u1 GuestParticipantID:participant-1}".

- [ ] **Step 3: Make the poll snapshot anonymous**

In `internal/rooms/endpoints.go`, change the mount line to `mux.Handle("GET /api/v1/polls/{id}/ws", connectLimit(pollWSHandler(h, polls)))` and replace `pollWSHandler` with:

```go
// pollWSHandler builds one poll WS connection's handler per request (the Authorize/Snapshot
// closures need the path's poll id). The snapshot is ALWAYS the anonymous PollView: this route
// resolves no session and no guest token. The SPA's useLivePoll never reads the snapshot's data
// (it answers every snapshot with a REST refetch that carries identity in a header), so scoping
// it bought nothing and cost a guest edit token on the URL query string — which reverse proxies
// log by default. Anything that wants a viewer-scoped view fetches GET /api/v1/polls/{id}.
//
// Authorize and Snapshot deliberately do NOT share a memoized result — see PollService's own doc
// comment for why Snapshot must run its own fresh query after Subscribe.
func pollWSHandler(h *Hub, svc PollService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")

		h.ServeWS(WSOptions{
			Authorize: func(rq *http.Request) (string, error) {
				exists, err := svc.PollExists(rq.Context(), pollID)
				if err != nil {
					return "", err
				}
				if !exists {
					return "", ErrNotFound
				}
				return "poll:" + pollID, nil
			},
			Snapshot: func(ctx context.Context, _ string) (any, error) {
				return svc.PollSnapshot(ctx, pollID, PollViewer{})
			},
			Presence: true,
		})(w, r)
	}
}
```

Update `Register`'s doc comment bullet for polls to: "public (a session, a guest token, or a fully anonymous caller may all connect — none of them changes the snapshot, which is always the anonymous view)".

In `internal/rooms/PROTOCOL.md`:
- Table row: `| GET /api/v1/polls/{id}/ws | public (snapshot always anonymous) | poll:{id} | on |`.
- Replace the whole `?token=<guest-edit-token>` bullet under "Query parameters" with:

```markdown
- There is **no identity parameter**. The poll route's snapshot is always the anonymous `PollView`
  (what `GET /api/v1/polls/{id}` returns to a caller with no session and no token); a client that
  needs its own viewer-scoped view fetches that REST endpoint, where the guest token travels in
  the `X-Guest-Token` header. A client MUST NOT put a guest edit token on the WS URL — query
  strings land in reverse-proxy access logs, and nothing server-side reads it there anymore.
```

- Under "Recovery: snapshot is primary, `?since=` is belt-and-braces", append a final paragraph:

```markdown
Corollary for a consumer that answers every snapshot by refetching its full state over REST (the
SPA's `useLivePoll` and `useLivePage` both do): omit `?since=` entirely. Each backfilled frame
would only trigger one more redundant refetch — N loader round trips after a brief outage — for
no correctness gain, since the snapshot that precedes the backfill is already complete.
```

Run: `go build ./... && go test ./internal/rooms/ -run 'TestPollWS' -v`
Expected: PASS (including the existing `TestPollWS_SignedInAndGuestTokenAlsoConnect` — connecting with credentials still works, they just don't scope the snapshot).

- [ ] **Step 4: Write the failing web tests**

In `web/src/lib/__tests__/room-socket.test.ts`, replace the test `'appends the guest edit token as ?token= (the only way a browser WebSocket can send it)'` with:

```ts
  it('never puts anything but the path on the URL on a first connect', () => {
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })

    expect(FakeSocket.last.url).toBe(`ws://${window.location.host}${PATH}`)
  })

  it('omits ?since= on reconnect when backfill is false (snapshot-as-ground-truth consumers)', () => {
    vi.useFakeTimers()
    connectRoom({ path: PATH, backfill: false, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })
    FakeSocket.last.open()
    FakeSocket.last.send({ type: 'snapshot', seq: 5, data: null })
    FakeSocket.last.send({ type: 'poll.changed', seq: 12, entity: 'vote' })
    FakeSocket.last.onclose?.()

    vi.advanceTimersByTime(1000)

    expect(FakeSocket.instances).toHaveLength(2)
    expect(FakeSocket.last.url).not.toContain('since=')
  })
```

In `web/src/lib/__tests__/use-live-poll.test.ts`, replace the test `'appends the guest edit token as ?token='` with:

```ts
  it('never puts a token on the URL and reconnects without ?since= (the snapshot is ground truth)', () => {
    vi.useFakeTimers()
    renderHook(() => useLivePoll(POLL_ID, vi.fn()))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 7, data: null })
      FakeSocket.last.send({ type: 'poll.changed', seq: 9, entity: 'vote' })
      FakeSocket.last.onclose?.()
    })
    act(() => vi.advanceTimersByTime(1000))

    expect(FakeSocket.instances).toHaveLength(2)
    expect(FakeSocket.last.url).toBe(`ws://${window.location.host}/api/v1/polls/${POLL_ID}/ws`)
  })
```

and update the file's header comment to drop "the guest token query param" (it now checks "the absence of any token/since query params").

Create `web/src/lib/__tests__/use-live-page.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useLivePage } from '#/lib/use-live-page'

const PAGE_ID = 'page-abcdef'

/** Mock WS server standing in for `internal/rooms`'s hub — the same shape `use-live-poll.test.ts`
 * uses; this file only checks `useLivePage`'s own wiring (synthetic `page.changed` on snapshot
 * and resync, no `?since=` on reconnect). */
class FakeSocket {
  static instances: FakeSocket[] = []

  url: string
  readyState = 0
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn(() => {
    this.readyState = 3
    this.onclose?.()
  })

  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }

  open() {
    this.readyState = 1
    this.onopen?.()
  }

  send(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }

  static get last(): FakeSocket {
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('no socket was opened')
    return socket
  }
}

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('useLivePage', () => {
  it('opens a websocket to the booking-page room', () => {
    renderHook(() => useLivePage(PAGE_ID, vi.fn()))

    expect(FakeSocket.last.url).toBe(
      `ws://${window.location.host}/api/v1/booking-pages/${PAGE_ID}/ws`,
    )
  })

  it('forwards a synthetic page.changed for the snapshot and for resync', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePage(PAGE_ID, onEvent))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 1, data: null })
    })
    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onEvent).toHaveBeenCalledWith({ type: 'page.changed' })

    act(() => FakeSocket.last.send({ type: 'resync' }))
    expect(onEvent).toHaveBeenCalledTimes(2)
  })

  it('reconnects without ?since= (the snapshot is ground truth)', () => {
    vi.useFakeTimers()
    renderHook(() => useLivePage(PAGE_ID, vi.fn()))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 4, data: null })
      FakeSocket.last.send({ type: 'page.changed', seq: 6 })
      FakeSocket.last.onclose?.()
    })
    act(() => vi.advanceTimersByTime(1000))

    expect(FakeSocket.instances).toHaveLength(2)
    expect(FakeSocket.last.url).not.toContain('since=')
  })
})
```

Run: `cd web && bunx vitest run src/lib/__tests__/room-socket.test.ts src/lib/__tests__/use-live-poll.test.ts src/lib/__tests__/use-live-page.test.ts`
Expected: FAIL — `backfill` is not a known option (typecheck) / URLs contain `since=`.

- [ ] **Step 5: Implement the web side**

`web/src/lib/room-socket.ts`:
- In `ConnectRoomOptions`, delete the `guestToken?: string` field and its doc comment; add:

```ts
  /** Whether a reconnect requests `?since=<max seq observed>` backfill (PROTOCOL.md "Recovery").
   * Default `true`. A consumer that already treats every snapshot as ground truth and refetches
   * its full state on it (`useLivePoll`, `useLivePage`) passes `false`: for it, each replayed
   * frame would only trigger one more redundant refetch — N loader round trips after a brief
   * outage — for no correctness gain. */
  backfill?: boolean
```

- In `buildUrl`, delete the `if (opts.guestToken) …` line and change the `since` line to:

```ts
    if (opts.backfill !== false && cursor !== undefined) url.searchParams.set('since', String(cursor))
```

- In the file header, drop the parenthetical about `?token=` if present.

`web/src/lib/use-live-poll.ts`: remove the `guestToken?: string` parameter, pass `backfill: false` to `connectRoom` (delete the `guestToken,` line), change the effect deps to `[pollId]`, and update the doc comment: replace the sentence starting "A fresh snapshot (first connect, …" so it also says: "The connection carries no identity and asks for no `?since=` backfill: the snapshot is ground truth and the route refetches over REST (with the guest token in a header) on every snapshot, so a token on the URL — which proxies log — and per-frame backfill refetches would both be pure cost (PROTOCOL.md)."

`web/src/lib/use-live-page.ts`: add `backfill: false,` to the `connectRoom` call and append the same one-sentence rationale to its doc comment.

`web/src/routes/p/$id/index.tsx`: delete `import { useEditToken } from '#/lib/edit-tokens'` and `const editToken = useEditToken(poll.id)`, and call `useLivePoll(poll.id, onEvent)`.

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: typecheck/lint clean; all vitest files pass (including the three above).

- [ ] **Step 6: Commit**

```bash
git add internal/rooms/endpoints.go internal/rooms/endpoints_test.go internal/rooms/PROTOCOL.md web/src/lib/room-socket.ts web/src/lib/use-live-poll.ts web/src/lib/use-live-page.ts web/src/routes/p/\$id/index.tsx web/src/lib/__tests__/room-socket.test.ts web/src/lib/__tests__/use-live-poll.test.ts web/src/lib/__tests__/use-live-page.test.ts
git commit -m "fix(rooms+web): anonymous poll WS snapshot; live hooks send no token or ?since=

The poll websocket never needed identity (the SPA refetches over REST on
every snapshot), so the guest edit token no longer travels on the URL where
proxies log it. useLivePoll/useLivePage also opt out of ?since= backfill,
which only produced one redundant loader refetch per replayed frame.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 13: Pin the last un-ported assertions — WS 426 / malformed id, polls 429

Finding: "Specific old assertions not re-expressed in Go" items (5) and (8).

**Files:**
- Test: `internal/rooms/endpoints_test.go`
- Test: `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `newTestMux`, `dialWSExpectStatus` (rooms tests); `newTestHandler`, `doRequest`, `errCode` (polls tests); `coder/websocket`'s `Accept`, which answers a non-upgrade request with 426 before anything else (`verifyClientRequest`, accept.go).
- Produces: nothing — tests only. If any assertion below fails, that is a real regression to fix, not a test to adjust.

- [ ] **Step 1: Add the rooms tests**

Append to `internal/rooms/endpoints_test.go`:

```go
// TestPollWS_NonUpgradeRequestIs426 ports ws.workers.test.ts's "returns 426 for a non-websocket
// request": Authorize passes for an existing poll, then coder/websocket's Accept rejects a plain
// GET (no Connection: Upgrade) with 426 Upgrade Required and writes the response itself.
func TestPollWS_NonUpgradeRequestIs426(t *testing.T) {
	server, _, polls, _ := newTestMux(t)
	polls.byID["p1"] = map[string]any{"id": "p1"}

	resp, err := http.Get(server.URL + "/api/v1/polls/p1/ws")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want 426", resp.StatusCode)
	}
}

// TestPollWS_MalformedIDIs404 ports the old route's malformed-id rejection. The Go route has no
// separate id-shape check: any id that is not a live poll — malformed or merely unknown — is
// rejected during the handshake as 404 not_found by PollExists, before any upgrade or Subscribe.
func TestPollWS_MalformedIDIs404(t *testing.T) {
	server, _, _, _ := newTestMux(t)
	dialWSExpectStatus(t, server, "/api/v1/polls/not-a-valid-id!/ws", nil, http.StatusNotFound)
}
```

Run: `go test ./internal/rooms/ -run 'TestPollWS_NonUpgradeRequestIs426|TestPollWS_MalformedIDIs404' -v`
Expected: PASS (both pin current behaviour).

- [ ] **Step 2: Add the polls rate-limit test**

Append to `internal/polls/handlers_test.go`:

```go
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
```

Run: `go test ./internal/polls/ -run TestHandlerPublicRateLimits -v`
Expected: PASS.

- [ ] **Step 3: Full Go gate for the plan so far**

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add internal/rooms/endpoints_test.go internal/polls/handlers_test.go
git commit -m "test(rooms+polls): pin WS 426/404 handshake rules and public 429 limits

Re-expresses the last un-ported ws.workers/server-functions assertions: a
non-upgrade GET on the poll room is 426, an unknown/malformed id is 404, and
the vote/comment limiters answer 429 rate_limited past their budget.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 14: Delete the orphaned web libs (`ics.ts`, `ids.ts`, `tokens.ts`) and the `nanoid` dependency

Finding: "ID format changed … old id/token/ics libs linger unused in web/" (dead-code half of item 13). Verified before writing this plan: no import of `#/lib/ics`, `#/lib/ids` or `#/lib/tokens` (or a relative form) exists outside their own tests; `nanoid` is imported only by `ids.ts`.

**Files:**
- Delete: `web/src/lib/ics.ts`, `web/src/lib/ids.ts`, `web/src/lib/tokens.ts`, `web/src/lib/__tests__/ics.test.ts`, `web/src/lib/__tests__/ids.test.ts`, `web/src/lib/__tests__/tokens.test.ts`
- Modify: `web/package.json`, `web/bun.lock` (via `bun remove nanoid`)

**Interfaces:** none.

- [ ] **Step 1: Re-verify nothing imports them (guards against drift since this plan was written)**

Run: `cd web && grep -rn "lib/ics'\|lib/ids'\|lib/tokens'\|from 'nanoid'" src e2e --include=*.ts --include=*.tsx | grep -v "__tests__/\(ics\|ids\|tokens\).test.ts" | grep -v "^src/lib/ids.ts"`
Expected: no output. (Any hit means that file still needs the lib — stop and report instead of deleting.)

- [ ] **Step 2: Delete the files and the dependency**

```bash
cd web
git rm src/lib/ics.ts src/lib/ids.ts src/lib/tokens.ts src/lib/__tests__/ics.test.ts src/lib/__tests__/ids.test.ts src/lib/__tests__/tokens.test.ts
bun remove nanoid
```

- [ ] **Step 3: Web gate**

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: clean; vitest reports the three deleted files gone and everything else passing.

- [ ] **Step 4: Commit**

```bash
git add web/package.json web/bun.lock web/src/lib
git commit -m "chore(web): remove unused ics/ids/tokens libs and the nanoid dependency

Ids, guest tokens and .ics files are all minted server-side now; these
client copies had no importers left.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

## Final gate (after Task 14)

Run, from the repo root, and confirm every line is green before declaring Plan C done:

```bash
go build ./... && go vet ./... && golangci-lint run ./... && go test ./...
cd web && bun run typecheck && bun run lint && bunx vitest run && cd ..
bunx playwright test
```

## Scope coverage (self-review against the Plan C brief)

| Brief item | Task |
| --- | --- |
| 1 validation of participant/comment/claim/prefs input | 1 |
| 2 vote answer enum + migration 00011 | 2 |
| 3 returning-guest claim captcha | 3 |
| 4 claim confirmation multi-VEVENT .ics | 10 |
| 5 finalize `{sent}` | 4 |
| 6 digest mid-run re-send race (deterministic repro) | 11 |
| 7 poll mail + roster locale: user recipients, participant locale, locale-aware labels, roster request locale | 7, 8 |
| 8 roster 400 not_signup, id-bearing filenames, roster unit rules | 9 |
| 9 delete cascades notification_subscriptions | 5 |
| 10 poll list 2N+1 → one aggregate query | 6 |
| 11 guest token off the poll WS URL; no `?since=` from useLivePoll/useLivePage; PROTOCOL.md | 12 |
| 12 WS 426 + malformed id; polls 429 at handler level | 13 |
| 13 dead web libs deleted; push flag left alone (explicit no-op) | 14 / Global Constraints |

Type-consistency notes checked while writing: `BuildRosterCSV(ctx, pollID, locale)` is introduced in Task 8 and used with three arguments in Tasks 8–9; `optionLabelText(o, locale, timezone)` is introduced in Task 8 and its Task 10 call site passes the hoisted `locale`; `recordingMailer`/`drainJobs`/`fakeLocales` are defined once in Task 7 and reused in 8, 10, 11; `createDatedSignupPoll`, `fixtureStart/End`, `wantLabelEN/NB` are defined once in Task 8 and reused in 8 (handler test) and 10; `FinalizeWithCount` (Task 4) is the only new Service signature — `Finalize` keeps its shape for every other caller; `errFields` (Task 1) is reused in Task 2.
