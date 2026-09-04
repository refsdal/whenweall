# Completion Plan E — Admin Console UI, Jobs & Mail Queue

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the staff console actually do what `docs/admin-console.md` and the README already claim — lock/unlock/delete from the user page and a dead-letter screen with Retry — and close the mail-queue gaps behind it: the dashboard counts every mail kind, dead-lettered auth mail stops keeping live tokens forever, and two old TS assertions (read-only admin endpoints are unaudited; a dangling active-org membership falls back to the oldest remaining one) are re-expressed in Go.

**Architecture:** The Go side already exposes the full staff REST surface (`internal/admin/handlers.go`: lock/unlock/delete with a required reason, `GET jobs/failed`, `POST jobs/{id}/retry`) and the TS client already wraps it (`web/src/api/admin.ts`) — the SPA just never calls the mutating half. Two small presentational components (`UserActions`, `FailedJobsTable`) get unit-tested with msw against the real `api()` client, and the thin file routes wire them in with `router.invalidate()` for refetch. Server-side, a fourth self-rescheduling housekeeping chain (`deadletter:sweep`, `internal/jobs/housekeeping.go`) nulls the payload of dead `mail:*` rows after 24h and deletes dead rows after 30 days; the retry endpoint answers `409 payload_expired` for a purged row and `FailedJobView` flags it so the UI hides Retry. The session fallback is a behaviour fix in `internal/auth/session.go` (Limen's `GetActiveOrganizationID` is a plain session-data read and never checks membership), not only a test. This plan runs after Plan A: `admin.DeleteUser`'s cascade already lives in `internal/auth.CascadeDeleteUser`; nothing here touches it — the Delete button only calls the existing endpoint.

**Tech Stack:** Go 1.26 stdlib + `internal/testdb` (live Postgres) for every Go test; React 19 + TanStack Router (file routes, `tsr generate`) + paraglide messages; vitest + Testing Library + msw (`msw/node`) for web tests.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§5 "parks as failed and **surfaces in the admin console**", §2 envelope, §9 "old `*.workers.test.ts` assertions are re-expressed in the Go ports") · scope brief: findings "Lock, unlock and delete have API endpoints but no controls in the admin UI", "Failed-jobs dead-letter screen and retry are not in the admin UI", "Dashboard 'mail queue depth' counts only mail:send", "Dead-lettered auth mail keeps live verification/reset tokens", "Specific old assertions not re-expressed" items 4 and 7 · runbook `docs/admin-console.md`.

## Global Constraints

- Repo `/home/anders/projects/refsdal/whenweall`, branch `feat/go-rewrite`; everything lands as commits on that branch. Go toolchain: `~/.local/share/mise/installs/go/1.27.0/bin/go` if `go` is not on PATH; golangci-lint in `~/go/bin`.
- Go line is 1.26 (`go.mod`, Dockerfile, CI — set by Plan B). Do not change it.
- **No migrations in this plan.** `scheduled_jobs` stays `id, kind, room_key, run_at, payload jsonb, attempts, max_attempts, locked_by, locked_at, last_error, created_at` (migrations/00001_infra.sql).
- Error envelope `{"error":{"code","message","fields"?}}`; codes snake_case (`not_found`, `conflict`, `payload_expired`, …); the SPA reads `ApiError.code`/`.message`.
- Every new user-facing string goes into **both** `web/messages/en.json` and `web/messages/nb.json` (the `messages.test.ts` key-parity test enforces this). After editing messages run `cd web && bunx paraglide-js compile --project ./project.inlang --outdir ./src/paraglide` so `m.*` exists for `tsc`/vitest (the vite plugin only compiles during `vite dev/build`; `src/paraglide` is untracked).
- Old TS code exists only on `main` (`git show main:<path>`).
- TDD: failing test first, run it, implement, run, commit. Go tests use `internal/testdb` (Docker must be running); admin handler tests use the existing `newAdminHTTPHarness`/`staffClient` helpers in `internal/admin/handlers_test.go`.
- Commit messages are conventional (`fix(admin): …`) and END with exactly these two trailer lines:
  ```
  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
  ```
- Gates before this plan is declared done: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`. (Playwright is Plan F's job; the e2e admin spec keeps passing because nothing it asserts on changes.)
- Ordering below leaves the tree green after every task: Go-only tasks first (1–5), then web (6–8), then docs (9). Task 5 adds a TS type field nothing reads yet; Task 7 is the first consumer.

---

### Task 1: Dashboard mail queue depth counts every `mail:*` kind

**Files:**
- Modify: `internal/admin/stats.go:17-34` (DashboardStats doc), `internal/admin/stats.go:72-80` (queue-depth query)
- Test: `internal/admin/stats_test.go:157-170` (`TestStats_SeededExactNumbers` job seed + `want`)

**Interfaces:**
- Consumes: `admin.Stats(ctx, tx) (*DashboardStats, error)`, test helper `insertJob(t, d, kind, attempts, maxAttempts)` (both existing).
- Produces: `DashboardStats.MailQueueDepth` now = `count(*) FROM scheduled_jobs WHERE kind LIKE 'mail:%' AND attempts < max_attempts`. Wire shape unchanged (`mailQueueDepth`).

- [ ] **Step 1: Change the seed and expectation in the existing test**

In `internal/admin/stats_test.go`, replace the block that starts with `// Mail queue depth: only "mail:send" jobs` and ends with `insertJob(t, d, "poll.digest", 5, 5)` with:

```go
	// Mail queue depth: every "mail:*" kind that hasn't exhausted its attempts. Auth mail is
	// "mail:send" (internal/mailer); entity mail is "mail:poll" (internal/polls/timers.go) and
	// "mail:booking" (internal/bookings/emails.go), and those handlers deliver via Send directly —
	// they never re-enqueue as "mail:send" — so a backlog of digests or booking confirmations is
	// invisible unless all three count. A timer kind never does.
	insertJob(t, d, "mail:send", 0, 5)     // pending
	insertJob(t, d, "mail:send", 2, 5)     // pending
	insertJob(t, d, "mail:send", 5, 5)     // dead — excluded from queue depth, counted in FailedJobs
	insertJob(t, d, "mail:poll", 0, 5)     // pending entity mail — counts
	insertJob(t, d, "mail:booking", 1, 5)  // pending entity mail — counts
	insertJob(t, d, "poll.deadline", 0, 5) // a timer, not mail — never counted as queue depth

	// Failed jobs: every kind's dead-letter rows, mirroring jobs.Dead's own predicate.
	insertJob(t, d, "poll.digest", 5, 5)
```

and in the `want := &admin.DashboardStats{ … }` literal change `MailQueueDepth: 2,` to `MailQueueDepth: 4,` (FailedJobs stays `2`).

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/admin/ -run 'TestStats_SeededExactNumbers' -v`
Expected: FAIL with `Stats = {… MailQueueDepth:2 FailedJobs:2}, want {… MailQueueDepth:4 FailedJobs:2}`

- [ ] **Step 3: Widen the predicate**

In `internal/admin/stats.go` replace the comment + query for `MailQueueDepth` (the block from `// "mail:send" is internal/mailer's job kind` through `Scan(&s.MailQueueDepth)`'s error return) with:

```go
	// Every mail kind counts, not just internal/mailer's "mail:send" (auth/org mail): entity
	// mail runs as "mail:poll" (internal/polls/timers.go) and "mail:booking"
	// (internal/bookings/emails.go), and those handlers deliver via Send directly rather than
	// re-enqueueing as "mail:send", so a LIKE 'mail:%' predicate is the only one that shows an
	// operator a backlog of digests or booking confirmations. This counts every row that hasn't
	// yet either succeeded (Complete deletes the row — see jobs.go) or exhausted its attempts
	// (excluded here, counted separately below as FailedJobs — a queue's backlog vs its DLQ).
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM scheduled_jobs WHERE kind LIKE 'mail:%' AND attempts < max_attempts
	`).Scan(&s.MailQueueDepth); err != nil {
		return nil, fmt.Errorf("admin: counting mail queue depth: %w", err)
	}
```

and in the `DashboardStats` doc comment replace `(see internal/mailer's "mail:send" kind)` with `(every "mail:*" kind — mailer's "mail:send", polls' "mail:poll", bookings' "mail:booking")`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/admin/ -run 'TestStats_' -v`
Expected: PASS (both `TestStats_EmptyDatabaseIsAllZeros` and `TestStats_SeededExactNumbers`)

- [ ] **Step 5: Commit**

```bash
git add internal/admin/stats.go internal/admin/stats_test.go
git commit -m "$(cat <<'EOF'
fix(admin): mail queue depth counts every mail:* kind

MailQueueDepth filtered on kind = 'mail:send', so a backlog of poll digests
(mail:poll) or booking confirmations (mail:booking) showed as zero on the
dashboard while failedJobs could be non-zero. Predicate is now kind LIKE
'mail:%'; timers still never count.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 2: Read-only admin endpoints leave no audit rows (parity test)

**Files:**
- Test: `internal/admin/handlers_test.go` (append at end of file)

**Interfaces:**
- Consumes: existing helpers in the same file/package — `newAdminHTTPHarness(t, d)`, `staffClient(t, h, email)`, `(*authHarness).requestJSON(t, client, method, path, body)`, `seedUser(t, d)` (audit_test.go), `seedDeadJob(t, d)`.
- Produces: `countAuditRows(t, d) int64` test helper (used again by Task 5's tests).

Re-expresses `main:src/server/admin/__tests__/audit.workers.test.ts:103` ("does not audit read-only admin endpoints"). This pins EXISTING behaviour, so the test passes immediately — that is the point of a parity test; if it ever fails, a read handler has started writing `admin_audit_log` rows, which is the bug the runbook's "Read-only endpoints (search, detail, stats) are not audited, on purpose" line forbids.

- [ ] **Step 1: Append the helper and the test**

```go
// countAuditRows is the whole admin_audit_log row count — the before/after probe the read-only
// parity test below (and Task 5's payload_expired tests) compare.
func countAuditRows(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := d.QueryRowContext(context.Background(), `SELECT count(*) FROM admin_audit_log`).Scan(&n); err != nil {
		t.Fatalf("counting admin_audit_log: %v", err)
	}
	return n
}

// TestReadOnlyEndpoints_AreNotAudited re-expresses audit.workers.test.ts's "does not audit
// read-only admin endpoints": every GET the console makes (stats, search, detail, audit log, the
// dead-letter list) must leave admin_audit_log exactly as it found it. Auditing list views would
// bury the lock/unlock/delete/retry entries that matter (docs/admin-console.md).
func TestReadOnlyEndpoints_AreNotAudited(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-readonly@example.com")
	targetID, targetEmail := seedUser(t, d)
	seedDeadJob(t, d) // so GET jobs/failed has a row to render, not an empty list

	before := countAuditRows(t, d)

	for _, path := range []string{
		"/api/v1/admin/stats",
		"/api/v1/admin/users?" + url.Values{"query": {targetEmail}}.Encode(),
		"/api/v1/admin/users/" + targetID,
		"/api/v1/admin/audit",
		"/api/v1/admin/jobs/failed",
	} {
		resp := h.requestJSON(t, client, http.MethodGet, path, nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
		}
	}

	if after := countAuditRows(t, d); after != before {
		t.Errorf("admin_audit_log rows went from %d to %d across read-only GETs; reads must never be audited", before, after)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/admin/ -run 'TestReadOnlyEndpoints_AreNotAudited' -v`
Expected: PASS (`--- PASS: TestReadOnlyEndpoints_AreNotAudited`). If it fails, do NOT change the test — a GET handler is writing audit rows and that handler is the bug.

- [ ] **Step 3: Commit**

```bash
git add internal/admin/handlers_test.go
git commit -m "$(cat <<'EOF'
test(admin): read-only endpoints leave no audit rows

Re-expresses the old audit.workers.test.ts assertion: stats, user search,
user detail, the audit log and the dead-letter list are never audited.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 3: Session falls back to the oldest remaining membership when the active org's membership is dangling

**Files:**
- Modify: `internal/auth/session.go:126-158` (the `if validated.Session != nil { … }` block in `resolveSession`)
- Modify: `internal/auth/auth.go:458-468` (`firstOrganization` → `oldestMembership`)
- Test: `internal/auth/personal_org_test.go` (append; extend imports)

**Interfaces:**
- Consumes: `organization.API` methods already on `s.orgs` — `GetActiveOrganizationID(ctx, *limen.Session) (any, error)`, `CheckMemberExistsInOrganization(ctx, organizationID, userID any) error` (returns `organization.ErrMemberNotInOrganization`), `GetOrganization(ctx, organizationID any) (*organization.Organization, error)`, `SetActiveOrganization(ctx, *limen.Session, *organization.Organization) (*limen.SessionResult, error)`, `CreateOrganization(ctx, *limen.User, *organization.CreateOrganizationRequest) (*organization.Organization, error)`. Test helpers in package `auth`: `newTestService`, `triggerSessionResolution`, `lookupUserID`, `requireStatus2xx`, `decodeJSON`, `signupPassword`, the `/probe` route (JSON-encodes `Session`, so the field is `"ActiveOrgID"`).
- Produces: `func (s *Service) oldestMembership(ctx context.Context, user *limen.User) (*organization.Organization, bool)` and `func (s *Service) membershipDangling(ctx context.Context, orgID, userID any) bool` (both unexported; nothing outside `internal/auth` depends on them). Behaviour: `Session.ActiveOrgID` is never an organization the user has no `organization_members` row in.

Why a fix and not just a test: `main:src/server/auth/__tests__/session.functions.workers.test.ts:98` asserted that a stale `activeOrganizationId` falls back to the oldest remaining membership. Limen's `GetActiveOrganizationID` (`plugins/organization/authorization.go:63`) is a plain `GetSessionData` read — it never checks membership — and `resolveSession` uses its result verbatim, so today a user removed from their active org keeps it as `ActiveOrgID` until they switch manually.

- [ ] **Step 1: Write the failing test**

Add `"database/sql"` and `"strconv"` to the import block of `internal/auth/personal_org_test.go` (it already imports `context`, `fmt`, `io`, `net/http`, `sync`, `testing`, `limen`, `organization`). Then append:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/auth/ -run 'TestSessionFallsBackToOldestRemainingMembership' -v`
Expected: FAIL with `Session.ActiveOrgID = "<staleOrgID>", want the oldest remaining membership "<oldestOrgID>"` and `sessions.active_organization_id = {<staleOrgID> true}, want <oldestOrgID>`.

- [ ] **Step 3: Replace `firstOrganization` with `oldestMembership` in auth.go**

In `internal/auth/auth.go` replace the whole `firstOrganization` function (its doc comment through its closing brace) with:

```go
// oldestMembership returns the organization behind user's OLDEST membership row
// (organization_members.created_at, id as a tiebreak), used by resolveSession (session.go) to
// pick an active organization for a session that has none — or whose stored one is dangling
// (see membershipDangling). "Oldest membership" is the rule buildClientSession applied
// (main:src/server/auth/session.functions.ts); deliberately not Limen's ListOrganizations, whose
// default ordering is unspecified. ensurePersonalOrgOnce (called just before this in
// resolveSession) guarantees at least one membership exists by the time this runs.
func (s *Service) oldestMembership(ctx context.Context, user *limen.User) (*organization.Organization, bool) {
	var orgID int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT organization_id FROM organization_members WHERE user_id = $1 ORDER BY created_at, id LIMIT 1`,
		user.ID,
	).Scan(&orgID); err != nil {
		return nil, false
	}
	org, err := s.orgs.GetOrganization(ctx, orgID)
	if err != nil || org == nil {
		return nil, false
	}
	return org, true
}
```

Run `grep -rn firstOrganization internal/` — the only remaining hit must be the call in `session.go`, which the next step rewrites.

- [ ] **Step 4: Add the membership check and the fallback in session.go**

In `internal/auth/session.go`, inside `resolveSession`, replace the whole `if validated.Session != nil { … }` block (from that line to its closing brace, immediately before `return sess`) with:

```go
	if validated.Session != nil {
		activeOrgID, err := s.orgs.GetActiveOrganizationID(r.Context(), validated.Session)
		switch {
		case err != nil:
			// Leave ActiveOrgID "" rather than guess: handlers that need an org resolve it
			// themselves (RequireOrgMember) and 403 cleanly on an empty one.
			s.logger.Error("auth: read active organization failed", "user_id", sess.UserID, "error", err)
		case activeOrgID != nil && !s.membershipDangling(r.Context(), activeOrgID, validated.User.ID):
			sess.ActiveOrgID = fmt.Sprint(activeOrgID)
		default:
			// Either no active org yet — true for every fresh signup, since nothing ever calls
			// organizations/switch on their behalf — or a DANGLING one: the session still names an
			// organization this user has no membership row in any more (removed by an owner, left
			// from another device, or the org deleted out from under them). Limen's
			// GetActiveOrganizationID is a plain session-data read and never checks membership, so
			// without the check above a stale pointer would reach every handler as the active org.
			// Both cases default to the user's OLDEST remaining membership — the rule
			// buildClientSession applied (main:src/server/auth/session.functions.ts) — which
			// ensurePersonalOrgOnce just guaranteed exists, and persist that choice on the session
			// so the next request doesn't re-derive it.
			if org, ok := s.oldestMembership(r.Context(), validated.User); ok {
				result, err := s.orgs.SetActiveOrganization(r.Context(), validated.Session, org)
				if err != nil {
					s.logger.Error("auth: set default active organization failed",
						"user_id", sess.UserID, "error", err)
				} else {
					sess.ActiveOrgID = fmt.Sprint(org.ID)
					// The opaque session manager's UpdateSession (what SetActiveOrganization
					// calls underneath) always returns a nil *SessionResult — no new token, no
					// cookie to re-issue, confirmed against the pinned Limen source. This handles
					// a non-nil result anyway so a future session-manager plugin (e.g. JWT
					// sessions, which do mint a new token here) doesn't silently drop its cookie.
					if result != nil && result.Cookie != nil {
						http.SetCookie(w, result.Cookie)
					}
				}
			}
		}
	}
```

Then add this method after `resolveSession` (before `isUserLocked`):

```go
// membershipDangling reports whether the session's stored active organization no longer has an
// organization_members row for this user. Only ErrMemberNotInOrganization counts as dangling: any
// other error (a closed database, say) returns false, so an infrastructure fault leaves the
// stored choice alone instead of silently re-pointing someone's active organization on a flaky
// request — RequireOrgMember downstream still fails closed on its own check.
func (s *Service) membershipDangling(ctx context.Context, orgID, userID any) bool {
	err := s.orgs.CheckMemberExistsInOrganization(ctx, orgID, userID)
	switch {
	case err == nil:
		return false
	case errors.Is(err, organization.ErrMemberNotInOrganization):
		return true
	default:
		s.logger.Error("auth: active organization membership check failed; keeping stored active organization",
			"user_id", fmt.Sprint(userID), "error", err)
		return false
	}
}
```

`session.go` already imports `errors`, `fmt`, `net/http` and `organization`; no import changes.

- [ ] **Step 5: Run the auth package tests**

Run: `go build ./... && go test ./internal/auth/ -v`
Expected: PASS, including `TestSessionFallsBackToOldestRemainingMembershipWhenActiveOrgMembershipIsDangling`, `TestActiveOrgIDDefaultsToPersonalOrgOnFirstProbe` and `TestPersonalOrgRecreatedAfterCacheClearAndOrgDeleted` (that one now also goes through the dangling branch: the deleted org's id is still on the session, has no membership, and falls back to the recreated personal org).

- [ ] **Step 6: Commit**

```bash
git add internal/auth/session.go internal/auth/auth.go internal/auth/personal_org_test.go
git commit -m "$(cat <<'EOF'
fix(auth): fall back to the oldest membership when the active org membership is dangling

Limen's GetActiveOrganizationID is a plain session-data read and never checks
membership, so a user removed from their active org (or whose org was deleted)
kept it as Session.ActiveOrgID. resolveSession now verifies membership and,
when it is gone, picks the oldest remaining membership (organization_members
created_at, the rule the TS buildClientSession used) and persists it on the
session. Re-expresses session.functions.workers.test.ts:98.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 4: `deadletter:sweep` housekeeping job — purge dead mail payloads after 24h, delete dead rows after 30 days

**Files:**
- Modify: `internal/jobs/housekeeping.go` (constants, `RegisterHousekeeping`, `EnsureScheduled`, new `sweepDeadLetters`)
- Modify: `internal/jobs/jobs.go` (new `PayloadExpired`, add `"strings"` import)
- Test: `internal/jobs/housekeeping_test.go` (two new tests + rewrite of `TestEnsureScheduledSeedsAllThreeSingletons`), `internal/jobs/jobs_test.go` (append `TestPayloadExpired`)

**Interfaces:**
- Consumes: `jobs.NewWorker`, `jobs.RegisterHousekeeping(w, sqlDB, rooms.BroadcastPresenceTotal)`, `jobs.Schedule`, `(*Worker).RunOnce`, `rescheduleHousekeeping`/`seedHousekeeping` (existing, unexported).
- Produces:
  - job kind `"deadletter:sweep"` (room key `"global"`, interval 1h, `housekeepingMaxAttempts`), seeded by `EnsureScheduled` on boot like the other three — `cmd/whenweall/main.go` needs no change.
  - `func PayloadExpired(kind string, hasPayload bool) bool` in package `jobs`: true iff `kind` has prefix `"mail:"` and `hasPayload` is false. Task 5 uses it for the 409 and for `FailedJobView.PayloadExpired`.

Design notes (put in the code as comments, they are load-bearing): age is measured on `run_at`. `fail` (jobs.go) leaves `run_at` untouched once the budget is spent (`ELSE run_at`), so on a dead row `run_at` is the moment its final attempt became due — the closest thing to a "died at" timestamp the table has without a migration. Only `mail:*` payloads are nulled (they carry recipient addresses and, for verify/reset mail, a raw token — `internal/auth/auth.go`'s `enqueueTokenMail`); `kind`, `attempts`, `last_error` are kept so the console still shows what failed and why. The 30-day delete applies to dead rows of every kind. Housekeeping chains have `max_attempts = 1_000_000` and are never dead, so the sweep can never eat its own chain.

- [ ] **Step 1: Write the failing housekeeping tests**

Add `"errors"` to the import block of `internal/jobs/housekeeping_test.go`. Then append:

```go
// seedDeadRow inserts a dead-lettered scheduled_jobs row (attempts == max_attempts, so jobs.Dead
// lists it and ClaimDue never will) directly, with run_at pushed `age` (a Postgres interval
// literal, e.g. "2 days") into the past — the sweep measures a dead row's age on run_at (see
// sweepDeadLetters). A nil payload inserts SQL NULL.
func seedDeadRow(t *testing.T, d *sql.DB, kind string, payload *string, age string) string {
	t.Helper()
	id := db.NewID()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO scheduled_jobs (id, kind, run_at, payload, attempts, max_attempts, last_error)
		VALUES ($1, $2, now() - $3::interval, $4::jsonb, 3, 3, 'smtp: connection refused')
	`, id, kind, age, payload); err != nil {
		t.Fatalf("seeding dead %s row: %v", kind, err)
	}
	return id
}

func strPtr(s string) *string { return &s }

// deadRowState is what the sweep must and must not touch on a row; found == false once deleted.
type deadRowState struct {
	found      bool
	hasPayload bool
	attempts   int
	lastError  sql.NullString
}

func readDeadRow(t *testing.T, d *sql.DB, id string) deadRowState {
	t.Helper()
	var s deadRowState
	err := d.QueryRowContext(context.Background(),
		`SELECT payload IS NOT NULL, attempts, last_error FROM scheduled_jobs WHERE id = $1`, id,
	).Scan(&s.hasPayload, &s.attempts, &s.lastError)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return deadRowState{}
	case err != nil:
		t.Fatalf("reading job %s: %v", id, err)
	}
	s.found = true
	return s
}

// runDeadletterSweep schedules deadletter:sweep due now and runs one worker pass, the same way the
// other housekeeping tests in this file drive their kinds. Exactly one job must be processed —
// the sweep itself; the seeded dead rows are never claimable.
func runDeadletterSweep(t *testing.T, d *sql.DB) {
	t.Helper()
	ctx := context.Background()
	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)
	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "deadletter:sweep", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (only the sweep itself is claimable)", processed)
	}
}

// TestDeadletterSweepPurgesOldMailPayloadsOnly: a dead mail:* row older than 24h loses its payload
// (the recipient address and any verify/reset token) but keeps kind/attempts/last_error, so the
// console still shows WHAT failed and WHY; a dead mail row younger than 24h keeps its payload (a
// staff member may still Retry it); a dead non-mail row keeps its payload regardless (ids only,
// nothing sensitive, and Retry must keep working for it).
func TestDeadletterSweepPurgesOldMailPayloadsOnly(t *testing.T) {
	d := testdb.New(t)
	mailPayload := strPtr(`{"to":"secret-recipient@example.com","data":{"URL":"http://app.example/verify-email?token=super-secret"}}`)
	oldMail := seedDeadRow(t, d, "mail:send", mailPayload, "2 days")
	freshMail := seedDeadRow(t, d, "mail:send", mailPayload, "1 hour")
	oldOther := seedDeadRow(t, d, "poll.digest", strPtr(`{"pollId":"p1"}`), "2 days")

	runDeadletterSweep(t, d)

	if got := readDeadRow(t, d, oldMail); !got.found || got.hasPayload || got.attempts != 3 ||
		!got.lastError.Valid || got.lastError.String != "smtp: connection refused" {
		t.Errorf("old mail row after sweep = %+v, want present with payload NULL and attempts/last_error intact", got)
	}
	if got := readDeadRow(t, d, freshMail); !got.found || !got.hasPayload {
		t.Errorf("fresh mail row after sweep = %+v, want untouched (younger than 24h — staff may still retry it)", got)
	}
	if got := readDeadRow(t, d, oldOther); !got.found || !got.hasPayload {
		t.Errorf("old non-mail row after sweep = %+v, want payload kept (only mail:* payloads carry addresses/tokens)", got)
	}

	var runAt time.Time
	if err := d.QueryRowContext(context.Background(),
		"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "deadletter:sweep", "global",
	).Scan(&runAt); err != nil {
		t.Fatalf("select rescheduled job: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want in the future (rescheduled)", runAt)
	}
}

// TestDeadletterSweepDeletesDeadRowsOlderThan30Days: the dead-letter screen is a to-do list, not
// an archive — a dead row of ANY kind older than 30 days is deleted outright; a 29-day-old one
// survives (payload purged, as above); a LIVE row is never touched however old its run_at is.
func TestDeadletterSweepDeletesDeadRowsOlderThan30Days(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	ancientMail := seedDeadRow(t, d, "mail:send", nil, "31 days")
	ancientOther := seedDeadRow(t, d, "booking.reminder", strPtr(`{"bookingId":"b1"}`), "31 days")
	recentMail := seedDeadRow(t, d, "mail:send", strPtr(`{"to":"x@example.com"}`), "29 days")

	// A live row (attempts < max_attempts) with an ancient run_at is merely overdue, not dead. It
	// is locked by another replica a moment ago so THIS worker's RunOnce skips it (ClaimDue's
	// lock check) and the sweep is the only job processed.
	liveID := db.NewID()
	if _, err := d.ExecContext(ctx, `
		INSERT INTO scheduled_jobs (id, kind, run_at, payload, attempts, max_attempts, locked_by, locked_at)
		VALUES ($1, 'mail:send', now() - interval '40 days', '{"to":"live@example.com"}'::jsonb, 1, 5, 'other-replica', now())
	`, liveID); err != nil {
		t.Fatalf("seeding live row: %v", err)
	}

	runDeadletterSweep(t, d)

	if got := readDeadRow(t, d, ancientMail); got.found {
		t.Errorf("31-day-old dead mail row still present (%+v), want deleted", got)
	}
	if got := readDeadRow(t, d, ancientOther); got.found {
		t.Errorf("31-day-old dead booking.reminder row still present (%+v), want deleted — the 30-day delete is for every kind", got)
	}
	if got := readDeadRow(t, d, recentMail); !got.found || got.hasPayload {
		t.Errorf("29-day-old dead mail row = %+v, want present with payload purged", got)
	}
	if got := readDeadRow(t, d, liveID); !got.found || !got.hasPayload {
		t.Errorf("live row = %+v, want untouched — the sweep only ever touches attempts >= max_attempts", got)
	}
}
```

Then replace the whole existing `TestEnsureScheduledSeedsAllThreeSingletons` function (its doc-less header through its closing brace) with:

```go
func TestEnsureScheduledSeedsAllFourSingletons(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatalf("EnsureScheduled: %v", err)
	}

	kinds := []string{"rooms:prune", "presence:sweep", "ratelimit:sweep", "deadletter:sweep"}
	firstRunAt := make(map[string]time.Time, len(kinds))
	for _, kind := range kinds {
		var count, maxAttempts int
		var runAt time.Time
		if err := d.QueryRowContext(ctx,
			"SELECT count(*), max(run_at), max(max_attempts) FROM scheduled_jobs WHERE kind = $1 AND room_key = 'global' GROUP BY kind",
			kind,
		).Scan(&count, &runAt, &maxAttempts); err != nil {
			t.Fatalf("count %s: %v", kind, err)
		}
		if count != 1 {
			t.Errorf("kind %s: count = %d, want 1", kind, count)
		}
		if maxAttempts != 1_000_000 {
			t.Errorf("kind %s: max_attempts = %d, want 1000000 (a housekeeping chain must never die from a transient blip)", kind, maxAttempts)
		}
		firstRunAt[kind] = runAt
	}

	// Calling it again must not create duplicates (ScheduleIfAbsent's DO NOTHING makes boot
	// idempotent across replicas)...
	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatalf("EnsureScheduled (2nd): %v", err)
	}
	var total int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs").Scan(&total); err != nil {
		t.Fatalf("total count: %v", err)
	}
	if total != len(kinds) {
		t.Errorf("total = %d, want %d (no duplicates)", total, len(kinds))
	}

	// ...and, IMPORTANT 4 (reboot starvation), it must not move run_at either. A restart-happy
	// deploy calling EnsureScheduled again on every boot must never pull an already-scheduled job
	// back to "shortly after boot" — that would starve the chain of ever reaching a real run_at.
	for kind, want := range firstRunAt {
		var got time.Time
		if err := d.QueryRowContext(ctx,
			"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = 'global'", kind,
		).Scan(&got); err != nil {
			t.Fatalf("select run_at %s: %v", kind, err)
		}
		if !got.Equal(want) {
			t.Errorf("kind %s: run_at moved from %v to %v after a second EnsureScheduled call", kind, want, got)
		}
	}
}
```

Append to `internal/jobs/jobs_test.go`:

```go
// TestPayloadExpired pins the rule the admin console's 409 "payload_expired" and FailedJobView's
// payloadExpired flag both lean on: only a mail:* job can have had its payload purged (every mail
// kind is enqueued WITH one), so a NULL payload on one means deadletter:sweep cleared it.
func TestPayloadExpired(t *testing.T) {
	cases := []struct {
		kind       string
		hasPayload bool
		want       bool
	}{
		{"mail:send", false, true},
		{"mail:poll", false, true},
		{"mail:booking", false, true},
		{"mail:send", true, false},
		{"poll.digest", false, false},
		{"deadletter:sweep", false, false},
	}
	for _, c := range cases {
		if got := jobs.PayloadExpired(c.kind, c.hasPayload); got != c.want {
			t.Errorf("PayloadExpired(%q, hasPayload=%v) = %v, want %v", c.kind, c.hasPayload, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/jobs/ -run 'TestDeadletterSweep|TestEnsureScheduledSeedsAllFour|TestPayloadExpired' -v`
Expected: compile FAIL — `undefined: jobs.PayloadExpired`. (After Step 3's `jobs.go` change alone, the sweep tests fail with `old mail row after sweep = {found:true hasPayload:true …}` because `RunOnce` dead-letters the unknown kind, and `TestEnsureScheduledSeedsAllFourSingletons` fails with `count deadletter:sweep: sql: no rows in result set`.)

- [ ] **Step 3: Add `PayloadExpired` to jobs.go**

Add `"strings"` to `internal/jobs/jobs.go`'s import block, and append to the file:

```go
// PayloadExpired reports whether a dead-lettered job's payload has been purged by the
// deadletter:sweep housekeeping job (housekeeping.go): a "mail:*" job whose payload is NULL. Every
// mail kind is enqueued WITH a payload — mailer.Enqueue's rendered Message for "mail:send", the
// ids-only payloads of internal/polls' "mail:poll" and internal/bookings' "mail:booking" — so a
// missing one can only mean the sweep cleared it, and such a job can never be retried: its
// handler would have nothing to send. Non-mail kinds legitimately run with no payload at all
// (the housekeeping chains, poll.deadline) and are never "expired".
func PayloadExpired(kind string, hasPayload bool) bool {
	return strings.HasPrefix(kind, "mail:") && !hasPayload
}
```

- [ ] **Step 4: Add the sweep to housekeeping.go**

In `internal/jobs/housekeeping.go`:

(a) Extend the kinds `const` block:

```go
// Housekeeping job kinds and the interval each reschedules itself for after a run.
const (
	roomsPruneKind          = "rooms:prune"
	roomsPruneInterval      = 10 * time.Minute
	presenceSweepKind       = "presence:sweep"
	presenceSweepInterval   = time.Minute
	ratelimitSweepKind      = "ratelimit:sweep"
	ratelimitSweepInterval  = time.Hour
	deadletterSweepKind     = "deadletter:sweep"
	deadletterSweepInterval = time.Hour
)

// Dead-letter retention, as Postgres interval literals spliced into sweepDeadLetters' SQL (fixed
// constants, never caller input). Age is measured on run_at: fail (jobs.go) leaves run_at
// untouched once the attempt budget is spent (its CASE's ELSE branch), so on a dead row it is
// the moment the final attempt became due — the closest thing this table has to a "died at"
// timestamp without a migration (Retry resets it to now(), so a re-dead job ages from its second
// death, which is also right).
//
// 24h for payloads is well past the whole retry window (10 mail attempts with backoff capped at
// an hour is roughly four hours), so nothing that could still succeed on its own is purged, and a
// staff member has had a full day to Retry from the console. 30 days for rows because the
// dead-letter screen is a to-do list, not an archive.
const (
	deadletterPayloadRetention = "interval '24 hours'"
	deadletterRowRetention     = "interval '30 days'"
)
```

(b) In `RegisterHousekeeping`'s doc comment, change "wires the three self-rescheduling housekeeping jobs into w: pruning old room_events rows, sweeping stale ws_presence rows, and sweeping expired rate_limits rows." to "wires the four self-rescheduling housekeeping jobs into w: pruning old room_events rows, sweeping stale ws_presence rows, sweeping expired rate_limits rows, and sweeping the dead-letter queue (sweepDeadLetters)." Then add a fourth registration at the end of the function body, after the `ratelimitSweepKind` one:

```go
	w.Register(deadletterSweepKind, func(ctx context.Context, _ Job) error {
		if err := sweepDeadLetters(ctx, sqlDB); err != nil {
			return err
		}
		return rescheduleHousekeeping(ctx, sqlDB, deadletterSweepKind, deadletterSweepInterval)
	})
```

(c) Add this function after `deleteStalePresenceRows`:

```go
// sweepDeadLetters is the dead-letter queue's only reclaimer (jobs.go's Dead: "Nothing reclaims
// these" — until this). Two statements:
//
//  1. NULL the payload of every dead-lettered "mail:*" row older than deadletterPayloadRetention.
//     mailer.Enqueue stores the fully rendered Message as the payload — the recipient address
//     and, for verify_email/reset_password (internal/auth's enqueueTokenMail), the raw token in
//     Data.URL — and a dead row otherwise keeps it forever, readable by anyone with DB access.
//     kind/attempts/last_error are kept so the admin console's failed-jobs screen still shows
//     WHAT failed and WHY; only the sensitive part goes. Retry of such a row is refused with 409
//     "payload_expired" (internal/admin/handlers.go, via PayloadExpired). Non-mail kinds carry ids
//     only and are left alone so Retry keeps working for them.
//  2. DELETE every dead-lettered row of any kind older than deadletterRowRetention.
//
// Both predicates require attempts >= max_attempts: a live row is never touched however old its
// run_at is (an overdue job is the worker's business, not this sweep's). Housekeeping chains have
// housekeepingMaxAttempts and can never be dead, so the sweep cannot eat its own kind.
func sweepDeadLetters(ctx context.Context, sqlDB *sql.DB) error {
	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE scheduled_jobs SET payload = NULL
		WHERE kind LIKE 'mail:%'
		  AND attempts >= max_attempts
		  AND payload IS NOT NULL
		  AND run_at < now() - `+deadletterPayloadRetention); err != nil {
		return err
	}
	_, err := sqlDB.ExecContext(ctx, `
		DELETE FROM scheduled_jobs
		WHERE attempts >= max_attempts
		  AND run_at < now() - `+deadletterRowRetention)
	return err
}
```

(d) In `EnsureScheduled`: change its doc comment's "seeds all three housekeeping jobs" to "seeds all four housekeeping jobs", and add `{deadletterSweepKind, deadletterSweepInterval},` after `{ratelimitSweepKind, ratelimitSweepInterval},` in the seed slice.

- [ ] **Step 5: Run the jobs package tests**

Run: `go build ./... && go test ./internal/jobs/ -v`
Expected: PASS for every test including `TestDeadletterSweepPurgesOldMailPayloadsOnly`, `TestDeadletterSweepDeletesDeadRowsOlderThan30Days`, `TestEnsureScheduledSeedsAllFourSingletons`, `TestPayloadExpired`. `grep -n "AllThree" internal/jobs/` must print nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/jobs/jobs.go internal/jobs/jobs_test.go internal/jobs/housekeeping.go internal/jobs/housekeeping_test.go
git commit -m "$(cat <<'EOF'
feat(jobs): deadletter:sweep purges dead mail payloads after 24h, dead rows after 30d

A dead-lettered mail:send row kept the rendered Message — recipient address
and, for verify/reset mail, the raw token — in scheduled_jobs indefinitely.
A fourth self-rescheduling housekeeping chain now NULLs the payload of dead
mail:* rows older than 24h (kind/attempts/last_error kept for the console)
and deletes dead rows of any kind older than 30 days, measured on run_at.
jobs.PayloadExpired names the "mail job with no payload" rule the admin
retry endpoint will refuse on.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 5: Retry of a purged-payload job is `409 payload_expired`; `FailedJobView.payloadExpired`

**Files:**
- Modify: `internal/admin/handlers.go:36-39` (package doc table), `internal/admin/handlers.go:318-333` (`FailedJobView`), `handleFailedJobs`, `handleRetryJob`
- Modify: `web/src/api/types.ts:246-252` (`FailedJobView`)
- Test: `internal/admin/handlers_test.go` (append)

**Interfaces:**
- Consumes: `jobs.PayloadExpired(kind string, hasPayload bool) bool` (Task 4), `countAuditRows(t, d)` (Task 2), `seedDeadJob(t, d)` (existing).
- Produces:
  - `GET /api/v1/admin/jobs/failed` rows gain `"payloadExpired": boolean`.
  - `POST /api/v1/admin/jobs/{id}/retry` → `409 {"error":{"code":"payload_expired",…}}` for a dead `mail:*` job with a NULL payload; no `job.retry` audit row is written; the row stays dead.
  - TS `FailedJobView` gains `payloadExpired: boolean` (`web/src/api/types.ts`), consumed by Task 7.

- [ ] **Step 1: Write the failing handler tests**

Append to `internal/admin/handlers_test.go`:

```go
// seedExpiredDeadJob is seedDeadJob after deadletter:sweep (internal/jobs/housekeeping.go) has
// purged its payload — simulated with the same UPDATE the sweep runs rather than by running the
// sweep, which is that package's own test's job.
func seedExpiredDeadJob(t *testing.T, d *sql.DB) string {
	t.Helper()
	id := seedDeadJob(t, d)
	if _, err := d.ExecContext(context.Background(), `UPDATE scheduled_jobs SET payload = NULL WHERE id = $1`, id); err != nil {
		t.Fatalf("purging payload of %s: %v", id, err)
	}
	return id
}

// TestHandleRetryJob_PurgedPayloadReturns409PayloadExpired: once the sweep has nulled a dead mail
// job's payload there is nothing left to send, so Retry is refused with its own code (not the
// generic "conflict", so the console can say why), the row stays dead and unaudited.
func TestHandleRetryJob_PurgedPayloadReturns409PayloadExpired(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-retry-expired@example.com")
	jobID := seedExpiredDeadJob(t, d)
	auditBefore := countAuditRows(t, d)

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
	if body.Error.Code != "payload_expired" {
		t.Errorf("error.code = %q, want payload_expired", body.Error.Code)
	}

	var stillDead bool
	if err := d.QueryRowContext(context.Background(),
		`SELECT attempts >= max_attempts FROM scheduled_jobs WHERE id = $1`, jobID,
	).Scan(&stillDead); err != nil {
		t.Fatalf("reading job state: %v", err)
	}
	if !stillDead {
		t.Error("a refused retry must leave the job dead-lettered")
	}
	if got := countAuditRows(t, d); got != auditBefore {
		t.Errorf("admin_audit_log rows went from %d to %d; a refused retry must not be audited", auditBefore, got)
	}
}

// TestHandleFailedJobs_FlagsPurgedPayload: the dead-letter list tells the console which rows can
// still be retried, so it can hide Retry instead of letting staff discover the 409 by clicking.
func TestHandleFailedJobs_FlagsPurgedPayload(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-jobs-expired@example.com")
	expiredID := seedExpiredDeadJob(t, d)
	retryableID := seedDeadJob(t, d)

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/jobs/failed", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Jobs []admin.FailedJobView `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	flags := map[string]bool{}
	for _, j := range out.Jobs {
		flags[j.ID] = j.PayloadExpired
	}
	if v, ok := flags[expiredID]; !ok || !v {
		t.Errorf("payloadExpired for purged job %s = %v (present=%v), want true", expiredID, v, ok)
	}
	if v, ok := flags[retryableID]; !ok || v {
		t.Errorf("payloadExpired for retryable job %s = %v (present=%v), want false", retryableID, v, ok)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/admin/ -run 'TestHandleRetryJob_PurgedPayload|TestHandleFailedJobs_FlagsPurgedPayload' -v`
Expected: compile FAIL — `j.PayloadExpired undefined (type admin.FailedJobView has no field or method PayloadExpired)`. (With only the struct field added, the retry test fails with `status = 200, want 409`.)

- [ ] **Step 3: Implement in handlers.go**

(a) In the package doc comment table at the top of `internal/admin/handlers.go`, replace the two `POST jobs/{id}/retry` lines with:

```go
//	POST jobs/{id}/retry     -> {"ok": true} (404 "not_found" for an unknown job id, 409
//	                            "conflict" for one that exists but isn't dead-lettered yet, 409
//	                            "payload_expired" for a dead mail job whose payload
//	                            deadletter:sweep has already purged — jobs.PayloadExpired)
```

(b) Replace the `FailedJobView` struct with:

```go
type FailedJobView struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Attempts  int     `json:"attempts"`
	LastError *string `json:"lastError"`
	RunAt     string  `json:"runAt"`
	// PayloadExpired is true once deadletter:sweep (internal/jobs/housekeeping.go) has purged this
	// mail job's payload — jobs.PayloadExpired's rule. The console hides Retry for such a row,
	// since handleRetryJob would answer 409 "payload_expired" anyway. Derived from whether a
	// payload is present, never from its contents — see the field-set rationale above.
	PayloadExpired bool `json:"payloadExpired"`
}
```

(c) In `handleFailedJobs`, add `PayloadExpired: jobs.PayloadExpired(j.Kind, j.Payload != nil),` after `RunAt: formatISO(j.RunAt),`.

(d) In `handleRetryJob`, replace the `var attempts, maxAttempts int` declaration, the `QueryRowContext(...).Scan(...)` and the `switch` with:

```go
		var attempts, maxAttempts int
		var kind string
		var hasPayload bool
		err = tx.QueryRowContext(r.Context(),
			`SELECT attempts, max_attempts, kind, payload IS NOT NULL FROM scheduled_jobs WHERE id = $1 FOR UPDATE`, id,
		).Scan(&attempts, &maxAttempts, &kind, &hasPayload)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			httpserver.Err(w, http.StatusNotFound, "not_found", "job not found", nil)
			return
		case err != nil:
			writeInternalError(w, err)
			return
		case attempts < maxAttempts:
			httpserver.Err(w, http.StatusConflict, "conflict", "job is not dead-lettered", nil)
			return
		case jobs.PayloadExpired(kind, hasPayload):
			// deadletter:sweep (internal/jobs/housekeeping.go) purged this mail job's payload —
			// the recipient address and any token it carried — once it had sat dead for 24h.
			// Retrying would hand the mailer an empty Message, so refuse it with a code of its own
			// (not "conflict") that the console maps to a "can't be retried" explanation.
			httpserver.Err(w, http.StatusConflict, "payload_expired", "job payload was purged after it dead-lettered; it can no longer be retried", nil)
			return
		}
```

The existing `FOR UPDATE` comment above that block stays as is.

- [ ] **Step 4: Mirror the field in the TS type**

In `web/src/api/types.ts` replace the `FailedJobView` type with:

```ts
/** internal/admin/handlers.go's FailedJobView — deliberately no `payload` field (it may hold
 * addresses/tokens). `payloadExpired` is true once the deadletter:sweep housekeeping job has
 * purged a dead mail job's payload; the backend answers `409 payload_expired` to a retry of it. */
export type FailedJobView = {
  id: string
  kind: string
  attempts: number
  lastError: string | null
  runAt: string
  payloadExpired: boolean
}
```

- [ ] **Step 5: Run the tests and the web typecheck**

Run: `go build ./... && go test ./internal/admin/ -v`
Expected: PASS for every test (the pre-existing `TestHandleRetryJob_*` and `TestHandleFailedJobs_OmitsPayloadIncludesOtherFields` included).

Run: `cd web && bun run typecheck`
Expected: exit 0 (no consumer of `FailedJobView` constructs one yet).

- [ ] **Step 6: Commit**

```bash
git add internal/admin/handlers.go internal/admin/handlers_test.go web/src/api/types.ts
git commit -m "$(cat <<'EOF'
feat(admin): refuse retry of a purged-payload job with 409 payload_expired

Once deadletter:sweep has nulled a dead mail job's payload there is nothing
left to send. POST jobs/{id}/retry now answers 409 payload_expired (no audit
row, job stays dead) and GET jobs/failed carries payloadExpired so the
console can hide Retry up front. TS FailedJobView mirrors the field.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 6: Lock, unlock and delete from the user page (`UserActions`)

**Files:**
- Create: `web/src/components/admin/UserActions.tsx`
- Test: `web/src/components/admin/__tests__/UserActions.test.tsx`
- Modify: `web/src/routes/admin/users.$id.tsx` (full rewrite below), `web/messages/en.json`, `web/messages/nb.json` (append keys after `"admin_cancel"`)

**Interfaces:**
- Consumes: `lockUser(userId, reason)`, `unlockUser(userId, reason)`, `deleteAdminUser(userId, reason)` from `#/api/admin`; `ApiError` from `#/api/client`; `AdminUserDetail` from `#/api/types`; `ReasonDialog` (`#/components/admin/ReasonDialog`, props `open, onOpenChange, title, description?, confirmLabel, destructive?, onConfirm(reason)`); `useSession()` from `#/lib/use-session` (`Session | null`, `session.user.id: string`); `useRouter().invalidate()` and `Route.useNavigate()` from TanStack Router; `toast` from `sonner`.
- Produces: `export function UserActions(props: { user: AdminUserDetail; isSelf: boolean; onChanged: () => Promise<void> | void; onDeleted: () => Promise<void> | void }): JSX.Element`.

- [ ] **Step 1: Add the messages**

Append to `web/messages/en.json` immediately after the `"admin_cancel": "Cancel"` line (add the comma to that line):

```json
  "admin_actions_title": "Actions",
  "admin_action_lock": "Lock account",
  "admin_action_unlock": "Unlock account",
  "admin_action_delete": "Delete account",
  "admin_confirm_lock": "Lock",
  "admin_confirm_unlock": "Unlock",
  "admin_confirm_delete": "Delete",
  "admin_action_self": "You can't lock or delete your own account. Ask another staff member.",
  "admin_lock_title": "Lock this account?",
  "admin_lock_description": "Every session is revoked immediately and sign-in is blocked until someone unlocks it.",
  "admin_unlock_title": "Unlock this account?",
  "admin_unlock_description": "The user can sign in again right away.",
  "admin_delete_title": "Delete this account?",
  "admin_delete_description": "Removes the user, their sessions and any organization only they own — including its polls and booking pages. This cannot be undone.",
  "admin_toast_locked": "Account locked.",
  "admin_toast_unlocked": "Account unlocked.",
  "admin_toast_deleted": "Account deleted.",
  "admin_action_failed": "That didn't go through: {message}"
```

Append to `web/messages/nb.json` after its `"admin_cancel": "Avbryt"` line (add the comma):

```json
  "admin_actions_title": "Handlinger",
  "admin_action_lock": "Lås konto",
  "admin_action_unlock": "Lås opp konto",
  "admin_action_delete": "Slett konto",
  "admin_confirm_lock": "Lås",
  "admin_confirm_unlock": "Lås opp",
  "admin_confirm_delete": "Slett",
  "admin_action_self": "Du kan ikke låse eller slette din egen konto. Be en annen ansatt.",
  "admin_lock_title": "Låse denne kontoen?",
  "admin_lock_description": "Alle sesjoner avsluttes umiddelbart, og innlogging blokkeres til noen låser opp igjen.",
  "admin_unlock_title": "Låse opp denne kontoen?",
  "admin_unlock_description": "Brukeren kan logge inn igjen med en gang.",
  "admin_delete_title": "Slette denne kontoen?",
  "admin_delete_description": "Fjerner brukeren, sesjonene og alle organisasjoner bare de eier – inkludert avstemninger og bookingsider. Dette kan ikke angres.",
  "admin_toast_locked": "Kontoen er låst.",
  "admin_toast_unlocked": "Kontoen er låst opp.",
  "admin_toast_deleted": "Kontoen er slettet.",
  "admin_action_failed": "Det gikk ikke gjennom: {message}"
```

Run: `cd web && bunx paraglide-js compile --project ./project.inlang --outdir ./src/paraglide && bunx vitest run messages`
Expected: paraglide compiles without error; `messages.test.ts` PASS (key sets and placeholders match).

- [ ] **Step 2: Write the failing component test**

Create `web/src/components/admin/__tests__/UserActions.test.tsx`:

```tsx
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { UserActions } from '#/components/admin/UserActions'
import type { AdminUserDetail } from '#/api/types'

// Real `api()` client against msw — the same pattern as api/__tests__/client.test.ts — so what is
// under test is the whole wire contract (method, path, `{reason}` body), not a mocked module.
const server = setupServer()
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))
beforeEach(() => {
  toast.success.mockReset()
  toast.error.mockReset()
})

// Radix marks the body `pointer-events: none` while a dialog is open, which user-event refuses
// to click through by default (see ReasonDialog.test.tsx).
const user = () => userEvent.setup({ pointerEventsCheck: 0 })

function makeUser(overrides: Partial<AdminUserDetail> = {}): AdminUserDetail {
  return {
    id: '42',
    email: 'ada@example.com',
    name: 'Ada',
    emailVerified: true,
    staff: false,
    locked: false,
    createdAt: '2026-08-01T10:00:00.000Z',
    lockReason: null,
    orgs: [],
    counts: { polls: 0, bookingPages: 0, bookings: 0 },
    ...overrides,
  }
}

/** Opens the dialog behind `trigger`, types `reason`, and presses `confirm`. */
async function confirmWithReason(trigger: string, confirm: string, reason: string) {
  const u = user()
  await u.click(screen.getByRole('button', { name: trigger }))
  await u.type(screen.getByLabelText(/why/i), reason)
  // Dispatched directly rather than through user-event: the Radix overlay swallows the simulated
  // pointer sequence, and what is under test is the handler, not Radix.
  fireEvent.click(screen.getByRole('button', { name: confirm }))
}

describe('UserActions', () => {
  it('locks with the typed reason and asks the page to refetch', async () => {
    let seenBody: unknown = null
    server.use(
      http.post('/api/v1/admin/users/42/lock', async ({ request }) => {
        seenBody = await request.json()
        return HttpResponse.json({ ok: true })
      }),
    )
    const onChanged = vi.fn()
    render(<UserActions user={makeUser()} isSelf={false} onChanged={onChanged} onDeleted={vi.fn()} />)

    await confirmWithReason('Lock account', 'Lock', 'ticket 481')

    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce())
    expect(seenBody).toEqual({ reason: 'ticket 481' })
    expect(toast.success).toHaveBeenCalledWith('Account locked.')
  })

  it('offers unlock, not lock, for a locked account', async () => {
    let unlocked = false
    server.use(
      http.post('/api/v1/admin/users/42/unlock', () => {
        unlocked = true
        return HttpResponse.json({ ok: true })
      }),
    )
    const onChanged = vi.fn()
    render(
      <UserActions
        user={makeUser({ locked: true, lockReason: 'abuse report' })}
        isSelf={false}
        onChanged={onChanged}
        onDeleted={vi.fn()}
      />,
    )
    expect(screen.queryByRole('button', { name: 'Lock account' })).not.toBeInTheDocument()

    await confirmWithReason('Unlock account', 'Unlock', 'resolved')

    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce())
    expect(unlocked).toBe(true)
    expect(toast.success).toHaveBeenCalledWith('Account unlocked.')
  })

  it('deletes and hands off to onDeleted instead of refetching a row that no longer exists', async () => {
    let seenBody: unknown = null
    server.use(
      http.delete('/api/v1/admin/users/42', async ({ request }) => {
        seenBody = await request.json()
        return HttpResponse.json({ ok: true })
      }),
    )
    const onChanged = vi.fn()
    const onDeleted = vi.fn()
    render(<UserActions user={makeUser()} isSelf={false} onChanged={onChanged} onDeleted={onDeleted} />)

    await confirmWithReason('Delete account', 'Delete', 'gdpr request')

    await waitFor(() => expect(onDeleted).toHaveBeenCalledOnce())
    expect(onChanged).not.toHaveBeenCalled()
    expect(seenBody).toEqual({ reason: 'gdpr request' })
    expect(toast.success).toHaveBeenCalledWith('Account deleted.')
  })

  it('surfaces a backend rejection as an error toast and does not refetch', async () => {
    server.use(
      http.post('/api/v1/admin/users/42/lock', () =>
        HttpResponse.json(
          { error: { code: 'invalid', message: 'you cannot lock your own account' } },
          { status: 400 },
        ),
      ),
    )
    const onChanged = vi.fn()
    render(<UserActions user={makeUser()} isSelf={false} onChanged={onChanged} onDeleted={vi.fn()} />)

    await confirmWithReason('Lock account', 'Lock', 'oops')

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('you cannot lock your own account')),
    )
    expect(onChanged).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
  })

  // The backend 400s a staff member targeting themselves (internal/admin/handlers.go's
  // writeCannotTargetSelf); the UI must not make that 400 the way anyone finds out.
  it('shows no controls for the signed-in staff member themselves', () => {
    render(<UserActions user={makeUser()} isSelf onChanged={vi.fn()} onDeleted={vi.fn()} />)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByText(/your own account/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd web && bunx vitest run src/components/admin/__tests__/UserActions.test.tsx`
Expected: FAIL — `Failed to resolve import "#/components/admin/UserActions"`.

- [ ] **Step 4: Create the component**

Create `web/src/components/admin/UserActions.tsx`:

```tsx
import { useState } from 'react'
import { toast } from 'sonner'
import { deleteAdminUser, lockUser, unlockUser } from '#/api/admin'
import { ApiError } from '#/api/client'
import type { AdminUserDetail } from '#/api/types'
import { ReasonDialog } from '#/components/admin/ReasonDialog'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'

type Action = 'lock' | 'unlock' | 'delete'

function copyFor(action: Action): { title: string; description: string; confirmLabel: string } {
  switch (action) {
    case 'lock':
      return {
        title: m.admin_lock_title(),
        description: m.admin_lock_description(),
        confirmLabel: m.admin_confirm_lock(),
      }
    case 'unlock':
      return {
        title: m.admin_unlock_title(),
        description: m.admin_unlock_description(),
        confirmLabel: m.admin_confirm_unlock(),
      }
    case 'delete':
      return {
        title: m.admin_delete_title(),
        description: m.admin_delete_description(),
        confirmLabel: m.admin_confirm_delete(),
      }
  }
}

/**
 * Lock / unlock / delete for one account — the console's only mutating controls — each behind
 * `ReasonDialog`, because the backend (internal/admin/handlers.go) requires a non-blank reason
 * and records it in the audit log. The same backend rejects a staff member targeting themselves
 * with 400; `isSelf` hides the controls up front so that 400 is never how anyone finds out.
 *
 * Lock and unlock leave the row in place, so `onChanged` lets the page refetch it; delete does
 * not, so `onDeleted` lets the page navigate away instead of reloading a 404.
 */
export function UserActions({
  user,
  isSelf,
  onChanged,
  onDeleted,
}: {
  user: AdminUserDetail
  isSelf: boolean
  onChanged: () => Promise<void> | void
  onDeleted: () => Promise<void> | void
}) {
  const [pending, setPending] = useState<Action | null>(null)

  async function run(action: Action, reason: string) {
    try {
      if (action === 'lock') {
        await lockUser(user.id, reason)
        toast.success(m.admin_toast_locked())
        await onChanged()
      } else if (action === 'unlock') {
        await unlockUser(user.id, reason)
        toast.success(m.admin_toast_unlocked())
        await onChanged()
      } else {
        await deleteAdminUser(user.id, reason)
        toast.success(m.admin_toast_deleted())
        await onDeleted()
      }
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(m.admin_action_failed({ message }))
    }
  }

  if (isSelf) {
    return <p className="text-sm text-muted-foreground">{m.admin_action_self()}</p>
  }

  const copy = pending ? copyFor(pending) : null

  return (
    <div className="flex flex-wrap gap-2">
      {user.locked ? (
        <Button type="button" variant="outline" onClick={() => setPending('unlock')}>
          {m.admin_action_unlock()}
        </Button>
      ) : (
        <Button type="button" variant="outline" onClick={() => setPending('lock')}>
          {m.admin_action_lock()}
        </Button>
      )}
      <Button type="button" variant="destructive" onClick={() => setPending('delete')}>
        {m.admin_action_delete()}
      </Button>

      {pending && copy && (
        <ReasonDialog
          key={pending}
          open
          onOpenChange={(open) => {
            if (!open) setPending(null)
          }}
          title={copy.title}
          description={copy.description}
          confirmLabel={copy.confirmLabel}
          destructive={pending === 'delete'}
          onConfirm={(reason) => run(pending, reason)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 5: Run the component test to verify it passes**

Run: `cd web && bunx vitest run src/components/admin`
Expected: PASS — 5 `UserActions` tests and the 3 existing `ReasonDialog` tests.

- [ ] **Step 6: Wire it into the user detail route**

Replace the whole of `web/src/routes/admin/users.$id.tsx` with:

```tsx
import { createFileRoute, Link, useRouter } from '@tanstack/react-router'
import { fetchAdminUserDetail, fetchAuditLog } from '#/api/admin'
import { UserActions } from '#/components/admin/UserActions'
import { Badge } from '#/components/ui/badge'
import { m } from '#/lib/i18n'
import { useSession } from '#/lib/use-session'

export const Route = createFileRoute('/admin/users/$id')({
  loader: async ({ params }) => {
    const detail = await fetchAdminUserDetail(params.id)
    // `AdminUserDetail` (internal/admin/users.go) no longer carries `recentActions` — the console
    // reads that off the shared audit endpoint instead, filtered to this user (see
    // `internal/admin/handlers.go`'s own package doc comment).
    const audit = detail
      ? await fetchAuditLog({ targetType: 'user', targetId: params.id, limit: 20 })
      : null
    return { detail, recentActions: audit?.entries ?? [] }
  },
  component: AdminUserDetailPage,
})

function Field({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-sm">{value}</span>
    </div>
  )
}

function AdminUserDetailPage() {
  const { detail, recentActions } = Route.useLoaderData()
  const router = useRouter()
  const navigate = Route.useNavigate()
  const session = useSession()

  if (!detail) {
    return <p className="text-sm text-muted-foreground">{m.admin_user_not_found()}</p>
  }

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-4 rounded-lg border p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg">{detail.name}</h2>
          {detail.staff && <Badge>{m.admin_badge_staff()}</Badge>}
          {detail.locked && <Badge variant="destructive">{m.admin_badge_locked()}</Badge>}
          {!detail.emailVerified && <Badge variant="outline">{m.admin_badge_unverified()}</Badge>}
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={m.admin_col_email()} value={detail.email} />
          <Field
            label={m.admin_col_created()}
            value={new Date(detail.createdAt).toLocaleDateString()}
          />
          {detail.lockReason && <Field label={m.admin_badge_locked()} value={detail.lockReason} />}
        </div>
      </section>

      {/* Lock/unlock refetch this page's loader (the row changes); delete leaves for the list,
          since there is no row left to reload — see UserActions' own doc comment. */}
      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_actions_title()}</h3>
        <UserActions
          user={detail}
          isSelf={session?.user.id === detail.id}
          onChanged={() => router.invalidate()}
          onDeleted={() => navigate({ to: '/admin/users' })}
        />
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_orgs()}</h3>
        {detail.orgs.length === 0 ? (
          <p className="text-sm text-muted-foreground">—</p>
        ) : (
          <ul className="flex flex-col gap-1 text-sm">
            {detail.orgs.map((org) => (
              <li key={org.id} className="flex flex-wrap items-center gap-2">
                <span>{org.name}</span>
                {org.roles.map((role) => (
                  <Badge key={role} variant="outline">
                    {role}
                  </Badge>
                ))}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_content()}</h3>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={m.admin_stat_polls()} value={detail.counts.polls} />
          <Field label={m.admin_stat_booking_pages()} value={detail.counts.bookingPages} />
          <Field label={m.admin_stat_bookings()} value={detail.counts.bookings} />
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_history()}</h3>
        {recentActions.length === 0 ? (
          <p className="text-sm text-muted-foreground">{m.admin_audit_empty()}</p>
        ) : (
          <ul className="flex flex-col gap-1 text-sm">
            {recentActions.map((entry) => (
              <li key={entry.id} className="flex flex-wrap items-center gap-2">
                <span className="text-muted-foreground">
                  {new Date(entry.createdAt).toLocaleString()}
                </span>
                <span className="font-mono text-xs">{entry.action}</span>
                <span className="text-muted-foreground">{entry.actorEmail}</span>
                {entry.reason && <span>“{entry.reason}”</span>}
              </li>
            ))}
          </ul>
        )}
      </section>

      <Link to="/admin/users" className="text-sm underline underline-offset-2">
        ← {m.admin_nav_users()}
      </Link>
    </div>
  )
}
```

- [ ] **Step 7: Run the web gates**

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: all three exit 0; vitest reports every file passing (the new `UserActions.test.tsx` included). Routes have no unit tests in this repo (only static legal pages do); the route wiring is covered by typecheck now and by Plan F's Playwright spec later.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/admin/UserActions.tsx web/src/components/admin/__tests__/UserActions.test.tsx 'web/src/routes/admin/users.$id.tsx' web/messages/en.json web/messages/nb.json
git commit -m "$(cat <<'EOF'
feat(web/admin): lock, unlock and delete from the user page

The Go API had lock/unlock/delete (reason required, audited) and the TS
client wrapped them, but nothing in the SPA called them and ReasonDialog had
no importer. UserActions wires the three through ReasonDialog with success/
error toasts, hides itself for the signed-in staff member (the backend 400s
self-targets), refetches the loader after lock/unlock and navigates back to
the list after delete.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 7: `FailedJobsTable` — the dead-letter list with Retry

**Files:**
- Create: `web/src/components/admin/FailedJobsTable.tsx`
- Test: `web/src/components/admin/__tests__/FailedJobsTable.test.tsx`
- Modify: `web/messages/en.json`, `web/messages/nb.json` (append keys)

**Interfaces:**
- Consumes: `retryJob(jobId): Promise<void>` from `#/api/admin`; `FailedJobView` (with `payloadExpired`, Task 5) from `#/api/types`; `ApiError`; `Button` (`size="sm" variant="outline"`); `toast`.
- Produces: `export function FailedJobsTable(props: { jobs: FailedJobView[]; onRetried: () => Promise<void> | void }): JSX.Element`. Task 8's route renders it.

- [ ] **Step 1: Add the messages**

Append to `web/messages/en.json` after the `"admin_action_failed"` line from Task 6 (add the comma):

```json
  "admin_nav_jobs": "Failed jobs",
  "admin_jobs_intro": "Jobs that used up every retry — mail that never left, reminders that never fired. Retry queues a job to run again right away; the audit log records who did it.",
  "admin_jobs_empty": "No failed jobs. Everything queued has either run or is still retrying.",
  "admin_jobs_col_kind": "Kind",
  "admin_jobs_col_attempts": "Attempts",
  "admin_jobs_col_error": "Last error",
  "admin_jobs_col_run_at": "Last run",
  "admin_jobs_col_actions": "Actions",
  "admin_jobs_retry": "Retry",
  "admin_jobs_payload_expired": "Payload purged — can't be retried",
  "admin_jobs_toast_retried": "Job queued to run again.",
  "admin_jobs_retry_failed": "Retry failed: {message}",
  "admin_mail_view_failed": "View failed jobs"
```

Append to `web/messages/nb.json` after its `"admin_action_failed"` line (add the comma):

```json
  "admin_nav_jobs": "Mislykkede jobber",
  "admin_jobs_intro": "Jobber som har brukt opp alle forsøk – e-post som aldri gikk ut, påminnelser som aldri ble sendt. «Prøv igjen» legger jobben i kø for å kjøre straks; revisjonsloggen registrerer hvem som gjorde det.",
  "admin_jobs_empty": "Ingen mislykkede jobber. Alt som er lagt i kø har enten kjørt eller prøver fortsatt.",
  "admin_jobs_col_kind": "Type",
  "admin_jobs_col_attempts": "Forsøk",
  "admin_jobs_col_error": "Siste feil",
  "admin_jobs_col_run_at": "Sist kjørt",
  "admin_jobs_col_actions": "Handlinger",
  "admin_jobs_retry": "Prøv igjen",
  "admin_jobs_payload_expired": "Innholdet er slettet – kan ikke prøves igjen",
  "admin_jobs_toast_retried": "Jobben er lagt i kø igjen.",
  "admin_jobs_retry_failed": "Kunne ikke prøve igjen: {message}",
  "admin_mail_view_failed": "Se mislykkede jobber"
```

Run: `cd web && bunx paraglide-js compile --project ./project.inlang --outdir ./src/paraglide && bunx vitest run messages`
Expected: compile OK; `messages.test.ts` PASS.

- [ ] **Step 2: Write the failing component test**

Create `web/src/components/admin/__tests__/FailedJobsTable.test.tsx`:

```tsx
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { FailedJobsTable } from '#/components/admin/FailedJobsTable'
import type { FailedJobView } from '#/api/types'

const server = setupServer()
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))
beforeEach(() => {
  toast.success.mockReset()
  toast.error.mockReset()
})

const retryable: FailedJobView = {
  id: 'j1',
  kind: 'mail:send',
  attempts: 10,
  lastError: 'smtp: connection refused',
  runAt: '2026-09-01T10:00:00.000Z',
  payloadExpired: false,
}
const purged: FailedJobView = {
  id: 'j2',
  kind: 'mail:booking',
  attempts: 10,
  lastError: 'smtp: 550 mailbox unavailable',
  runAt: '2026-08-01T10:00:00.000Z',
  payloadExpired: true,
}

describe('FailedJobsTable', () => {
  it('lists kind, attempts and the last error for every dead job', () => {
    render(<FailedJobsTable jobs={[retryable, purged]} onRetried={vi.fn()} />)

    expect(screen.getByText('mail:send')).toBeInTheDocument()
    expect(screen.getByText('mail:booking')).toBeInTheDocument()
    expect(screen.getByText('smtp: connection refused')).toBeInTheDocument()
    expect(screen.getByText('smtp: 550 mailbox unavailable')).toBeInTheDocument()
    expect(screen.getAllByText('10')).toHaveLength(2)
  })

  it('retries a job and asks the page to refetch', async () => {
    let hit = false
    server.use(
      http.post('/api/v1/admin/jobs/j1/retry', () => {
        hit = true
        return HttpResponse.json({ ok: true })
      }),
    )
    const onRetried = vi.fn()
    render(<FailedJobsTable jobs={[retryable]} onRetried={onRetried} />)

    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(onRetried).toHaveBeenCalledOnce())
    expect(hit).toBe(true)
    expect(toast.success).toHaveBeenCalledWith('Job queued to run again.')
  })

  // internal/jobs/housekeeping.go's deadletter:sweep nulls a dead mail job's payload after 24h and
  // the backend answers 409 payload_expired to a retry of it — so the table must not offer one.
  it('offers no retry for a purged payload and says why', () => {
    render(<FailedJobsTable jobs={[retryable, purged]} onRetried={vi.fn()} />)

    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1)
    expect(screen.getByText(/payload purged/i)).toBeInTheDocument()
  })

  it('shows an error toast and does not refetch when the backend refuses', async () => {
    server.use(
      http.post('/api/v1/admin/jobs/j1/retry', () =>
        HttpResponse.json(
          { error: { code: 'conflict', message: 'job is not dead-lettered' } },
          { status: 409 },
        ),
      ),
    )
    const onRetried = vi.fn()
    render(<FailedJobsTable jobs={[retryable]} onRetried={onRetried} />)

    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('job is not dead-lettered')),
    )
    expect(onRetried).not.toHaveBeenCalled()
  })

  it('says so when nothing has failed', () => {
    render(<FailedJobsTable jobs={[]} onRetried={vi.fn()} />)

    expect(screen.getByText(/no failed jobs/i)).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd web && bunx vitest run src/components/admin/__tests__/FailedJobsTable.test.tsx`
Expected: FAIL — `Failed to resolve import "#/components/admin/FailedJobsTable"`.

- [ ] **Step 4: Create the component**

Create `web/src/components/admin/FailedJobsTable.tsx`:

```tsx
import { useState } from 'react'
import { toast } from 'sonner'
import { retryJob } from '#/api/admin'
import { ApiError } from '#/api/client'
import type { FailedJobView } from '#/api/types'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'

/**
 * The dead-letter queue (spec §5: a parked job must "surface in the admin console" — the
 * `fix/mail-failure-visibility` lesson). One row per job that exhausted its retry budget, with
 * the failure text the worker recorded; Retry calls `POST /api/v1/admin/jobs/{id}/retry`, which
 * re-queues the job to run immediately and writes a `job.retry` audit row.
 *
 * A row whose payload the `deadletter:sweep` housekeeping job has purged (`payloadExpired`) can
 * never be retried — the backend would answer `409 payload_expired` — so it gets an explanation
 * instead of a button. Payloads themselves are never in this view at all: they may hold
 * recipient addresses and tokens (see `FailedJobView` in internal/admin/handlers.go).
 */
export function FailedJobsTable({
  jobs,
  onRetried,
}: {
  jobs: FailedJobView[]
  onRetried: () => Promise<void> | void
}) {
  const [busyId, setBusyId] = useState<string | null>(null)

  async function retry(id: string) {
    setBusyId(id)
    try {
      await retryJob(id)
      toast.success(m.admin_jobs_toast_retried())
      await onRetried()
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(m.admin_jobs_retry_failed({ message }))
    } finally {
      setBusyId(null)
    }
  }

  if (jobs.length === 0) {
    return <p className="text-sm text-muted-foreground">{m.admin_jobs_empty()}</p>
  }

  return (
    <div className="overflow-x-auto rounded-lg border">
      <table className="w-full text-sm">
        <thead className="border-b text-left text-muted-foreground">
          <tr>
            <th className="p-3 font-medium">{m.admin_jobs_col_kind()}</th>
            <th className="p-3 font-medium">{m.admin_jobs_col_attempts()}</th>
            <th className="p-3 font-medium">{m.admin_jobs_col_error()}</th>
            <th className="p-3 font-medium">{m.admin_jobs_col_run_at()}</th>
            <th className="p-3 font-medium">
              <span className="sr-only">{m.admin_jobs_col_actions()}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((job) => (
            <tr key={job.id} className="border-b align-top last:border-0">
              <td className="p-3 font-mono text-xs">{job.kind}</td>
              <td className="p-3 tabular-nums">{job.attempts}</td>
              <td className="max-w-md p-3 break-words text-muted-foreground">
                {job.lastError ?? '—'}
              </td>
              <td className="p-3 whitespace-nowrap text-muted-foreground">
                {new Date(job.runAt).toLocaleString()}
              </td>
              <td className="p-3 text-right">
                {job.payloadExpired ? (
                  <span className="text-xs text-muted-foreground italic">
                    {m.admin_jobs_payload_expired()}
                  </span>
                ) : (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={busyId === job.id}
                    onClick={() => void retry(job.id)}
                  >
                    {m.admin_jobs_retry()}
                  </Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
```

- [ ] **Step 5: Run the test and the web gates**

Run: `cd web && bunx vitest run src/components/admin && bun run typecheck && bun run lint`
Expected: 5 `FailedJobsTable` tests PASS (plus `UserActions`/`ReasonDialog`); typecheck and lint exit 0.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/admin/FailedJobsTable.tsx web/src/components/admin/__tests__/FailedJobsTable.test.tsx web/messages/en.json web/messages/nb.json
git commit -m "$(cat <<'EOF'
feat(web/admin): failed-jobs table with retry

Renders GET /api/v1/admin/jobs/failed rows (kind, attempts, last error, last
run) with a Retry action against POST jobs/{id}/retry; a row whose payload
the deadletter:sweep purged shows why it can't be retried instead of a
button. Payloads never reach this view.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 8: `/admin/jobs` route, "Failed jobs" nav tab, dashboard link, regenerated route tree

**Files:**
- Create: `web/src/routes/admin/jobs.tsx`
- Modify: `web/src/routes/admin/route.tsx:27-31` (`TABS`), `web/src/routes/admin/index.tsx` (imports + mail section)
- Regenerate: `web/src/routeTree.gen.ts` (via `bun run generate-routes`; never hand-edit)

**Interfaces:**
- Consumes: `fetchFailedJobs(): Promise<FailedJobView[]>` (`#/api/admin`), `FailedJobsTable` (Task 7), messages `admin_nav_jobs`, `admin_jobs_intro`, `admin_mail_view_failed` (Task 7).
- Produces: route `/admin/jobs` (file route id `/admin/jobs`, registered in `routeTree.gen.ts`) — the target `Link`s below and Plan F's e2e can navigate to.

- [ ] **Step 1: Create the route**

Create `web/src/routes/admin/jobs.tsx`:

```tsx
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { fetchFailedJobs } from '#/api/admin'
import { FailedJobsTable } from '#/components/admin/FailedJobsTable'
import { m } from '#/lib/i18n'

/**
 * The dead-letter screen (spec §5). Staff-gated by the parent `/admin` layout's `beforeLoad`
 * (route.tsx) for navigation, and by `RequireStaff` on every request underneath — the gate that
 * actually matters. The loader re-runs on `router.invalidate()`, which is how a successful Retry
 * makes the row disappear (the worker claims it within its poll interval; until then it is
 * simply no longer dead-lettered and drops out of GET jobs/failed).
 */
export const Route = createFileRoute('/admin/jobs')({
  loader: () => fetchFailedJobs(),
  component: AdminJobs,
})

function AdminJobs() {
  const jobs = Route.useLoaderData()
  const router = useRouter()

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">{m.admin_jobs_intro()}</p>
      <FailedJobsTable jobs={jobs} onRetried={() => router.invalidate()} />
    </div>
  )
}
```

- [ ] **Step 2: Add the nav tab**

In `web/src/routes/admin/route.tsx` replace the `TABS` constant with:

```tsx
const TABS = [
  { to: '/admin', label: () => m.admin_nav_dashboard(), exact: true },
  { to: '/admin/users', label: () => m.admin_nav_users(), exact: false },
  { to: '/admin/jobs', label: () => m.admin_nav_jobs(), exact: false },
  { to: '/admin/audit', label: () => m.admin_nav_audit(), exact: false },
] as const
```

- [ ] **Step 3: Link the dashboard's failed-jobs card to the screen**

In `web/src/routes/admin/index.tsx` change the first import line to `import { createFileRoute, Link } from '@tanstack/react-router'`, and replace the mail `<section>` (the one containing `m.admin_mail_title()`) with:

```tsx
      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-semibold">{m.admin_mail_title()}</h2>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label={m.admin_mail_queue_depth()} value={stats.mailQueueDepth} />
          <StatCard label={m.admin_mail_failed_jobs()} value={stats.failedJobs} />
        </div>
        {/* The count alone is a dead end — the whole point of surfacing failed jobs (spec §5) is
            that an operator can see WHAT failed and resend it. */}
        <Link to="/admin/jobs" className="text-sm underline underline-offset-2">
          {m.admin_mail_view_failed()}
        </Link>
      </section>
```

- [ ] **Step 4: Regenerate the route tree and verify**

Run: `cd web && bun run generate-routes && grep -c "/admin/jobs" src/routeTree.gen.ts`
Expected: `tsr generate` exits 0 and the grep prints a positive count (the file gains an `AdminJobsRouteImport` plus `'/admin/jobs'` entries in every path map). `git status` must show `src/routeTree.gen.ts` modified — it is committed, not ignored.

- [ ] **Step 5: Run the web gates**

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: all exit 0. (Before Step 4, typecheck fails on `createFileRoute('/admin/jobs')` and `Link to="/admin/jobs"` — the route tree is what makes those paths valid types; that is the expected red before regenerating.)

- [ ] **Step 6: Commit**

```bash
git add web/src/routes/admin/jobs.tsx web/src/routes/admin/route.tsx web/src/routes/admin/index.tsx web/src/routeTree.gen.ts
git commit -m "$(cat <<'EOF'
feat(web/admin): /admin/jobs dead-letter screen, nav tab and dashboard link

fetchFailedJobs/retryJob had zero call sites; the dashboard showed a bare
count. The new route renders FailedJobsTable and invalidates on retry, the
console nav gains a "Failed jobs" tab, and the mail card links to the screen.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

### Task 9: Runbook — console actions and dead-letter retention

**Files:**
- Modify: `docs/admin-console.md` ("Revoking" paragraph, "What staff can do", "Reading the audit log" first bullet, new "Failed jobs" section)

**Interfaces:**
- Consumes: behaviour delivered by Tasks 4–8 (24h payload purge, 30-day delete, `409 payload_expired`, user-page buttons, `/admin/jobs`).
- Produces: documentation only.

- [ ] **Step 1: Make the "Revoking" section describe the console button**

In `docs/admin-console.md`, in the paragraph beginning `Staff status is re-checked on every request`, replace the sentence fragment `also **lock** their account from the console (or the API directly), which additionally revokes every one of their existing sessions immediately:` with:

```markdown
also **lock** their account — open them at `/admin/users`, press **Lock account**, type a
reason — which additionally revokes every one of their existing sessions immediately. The
same thing from the API directly:
```

- [ ] **Step 2: Rewrite "What staff can do"**

Replace the paragraph that begins `List and search users; view an account with its organisations and content;` (the whole paragraph, ending `Google Calendar sync).`) with:

```markdown
List and search users; view an account with its organisations and content; **lock**
(revokes every session, blocks sign-in) and **unlock**; **delete**; view platform stats;
and view/retry failed background jobs. Lock, unlock and delete are buttons on the user's
page (`/admin/users/<id>`) — each opens a dialog that requires a reason, which is what ends
up in the audit log. Failed jobs have their own tab, `/admin/jobs` — see
[Failed jobs](#failed-jobs) below.
```

- [ ] **Step 3: Mention the retry code in the audit-log notes**

In the "Reading the audit log" section, replace the sentence `A job retry carries no reason (there's no one's rights to weigh against resuming work the system itself already scheduled).` with:

```markdown
A job retry (`job.retry`) carries no reason (there's no one's rights to weigh against
resuming work the system itself already scheduled). A *refused* retry — the job isn't
actually dead-lettered, or its payload has expired (below) — leaves no row at all.
```

- [ ] **Step 4: Add the "Failed jobs" section**

Insert this section immediately before `## If an admin account is compromised`:

```markdown
## Failed jobs

Every background job — mail delivery (`mail:send` for sign-in/verification/reset/invite
mail, `mail:poll` and `mail:booking` for notifications), poll deadlines and digests,
booking reminders, Google Calendar sync — is a row in `scheduled_jobs`, retried with
backoff until its attempt budget is spent (10 for mail, 5 otherwise). A job that still
fails then **parks as dead-lettered**: it stops retrying and appears at `/admin/jobs` with
its kind, attempt count, last error and last run time. The dashboard's *Failed jobs* count
is the size of that list; *Mail queue depth* is every `mail:*` job still waiting or
retrying.

**Retry** re-queues the job to run immediately (the worker picks it up within its poll
interval) and writes a `job.retry` audit row. A retry is refused with `409 conflict` for a
job that isn't dead-lettered — a stale tab, or the worker already has it — and with
`409 payload_expired` in the case below.

### Dead-letter retention

A queued mail's payload is the rendered message: the recipient address and, for
verification and password-reset mail, the raw token in the link. A dead-lettered row would
otherwise keep that readable to anyone with database access for as long as it sat there.
So a housekeeping job (`deadletter:sweep`, hourly) does two things:

- **24 hours** after a `mail:*` job dead-letters, its **payload is nulled**. Kind, attempt
  count and last error are kept, so the row still tells you what failed and why — but it
  can no longer be retried (there is nothing left to send), and the console shows
  *Payload purged — can't be retried* in place of the button; the API answers
  `409 payload_expired`. If a batch of mail bounced and you want it resent, the console
  gives you a day. After that, the user re-requests (a new verification or reset mail) —
  which is also the only safe answer, since the original token has expired anyway.
  Non-mail jobs carry ids only and are never purged.
- **30 days** after any job dead-letters, the **row is deleted**. The failed-jobs screen
  is a to-do list, not an archive; the audit log keeps the `job.retry` entries.

Age is measured on the job's last scheduled run, so a job you retried that dies again gets
a fresh 24 hours.
```

- [ ] **Step 5: Verify and commit**

Run: `grep -n "payload_expired\|Failed jobs\|Lock account" docs/admin-console.md`
Expected: hits in the Revoking paragraph (`Lock account`), "What staff can do", the audit notes (`payload has expired`), and the new section (`## Failed jobs`, `409 payload_expired`).

```bash
git add docs/admin-console.md
git commit -m "$(cat <<'EOF'
docs(admin): console lock/unlock/delete and failed-jobs screen; dead-letter retention

The runbook now describes what the console actually does: lock/unlock/delete
buttons on the user page, the /admin/jobs tab with Retry, and the
deadletter:sweep retention (mail payload purged after 24h -> 409
payload_expired; dead rows deleted after 30 days).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
EOF
)"
```

---

## Final gate (run once after Task 9)

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`
Expected: every package PASS, no lint findings.

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: all exit 0.

## Self-review notes

- **Scope coverage:** finding 1 (lock/unlock/delete UI) → Task 6; finding 2 (dead-letter screen, nav tab, dashboard link) → Tasks 7–8; finding 3 (queue depth) → Task 1; finding 4 (dead mail payloads: null after 24h, delete after 30d, retry → 409 `payload_expired`) → Tasks 4–5; finding 5 item 4 (read-only not audited) → Task 2; item 7 (dangling membership fallback) → Task 3, as a fix plus test because the behaviour was genuinely absent; finding 6 (runbook) → Task 9.
- **Type consistency:** `jobs.PayloadExpired(kind string, hasPayload bool) bool` (Task 4) is what Task 5 calls in both handlers; `FailedJobView.PayloadExpired`/`payloadExpired` (Task 5) is what Task 7's component and tests read; `UserActions`/`FailedJobsTable` prop names match between component, test and route; message keys used in TSX (`admin_confirm_lock`, `admin_jobs_col_actions`, `admin_mail_view_failed`, …) all appear in both JSON additions; test helper `countAuditRows` (Task 2) is used by Task 5; `oldestMembership`/`membershipDangling` (Task 3) are the only names `session.go` references after the rewrite.
- **No migrations, no e2e:** the `run_at`-based age avoids a schema change; Playwright coverage of the new screens is Plan F's.
