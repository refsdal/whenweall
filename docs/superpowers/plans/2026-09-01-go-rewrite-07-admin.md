# Go Rewrite Plan 7/8 — Admin Console API

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The staff console backend: dashboard stats, user search/detail/management, the audit log, and the failed-jobs (dead-letter) screen — everything behind `RequireStaff`.

**Architecture:** `internal/admin` ports `src/server/admin/*` (read `admin.functions.ts`, `stats.ts`, `users.ts`, `audit.ts`, `audit-query.ts` first). The audit log's rule carries over: **every mutating staff action writes an audit row in the same transaction.** The dead-letter screen is new surface over plan 2's `jobs.Dead`/`jobs.Retry` (the `fix/mail-failure-visibility` lesson — a self-hoster must *see* failed mail).

**Tech Stack:** stdlib + hand-built dynamic SQL for the filtered user search (the one place sqlc is wrong — spec §6).

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§5 failed-jobs visibility, §2 envelope)

## Global Constraints

Plan 1's Global Constraints apply, plus plan 4's envelope codes. Migration `00005_admin.sql` recreates `admin_audit_log` from `drizzle/0000_orange_zzzax.sql` with the §6 re-cut (`created_at timestamptz`, `metadata jsonb`) and its three indexes; FK to the Limen users table name.

---

### Task 1: Audit log

**Files:**
- Create: `migrations/00005_admin.sql`, `internal/admin/audit.go`
- Test: `internal/admin/audit_test.go`

**Interfaces:**

```go
package admin

// Record writes one audit row (id, actor from Session, action, target, reason, metadata jsonb).
// Called in the same tx as the action it records — pass the tx.
func Record(ctx context.Context, tx db.DBTX, actor *auth.Session, action, targetType, targetID, reason string, metadata any) error
type AuditEntry struct { /* mirror the TS viewmodel fields */ }
// List: newest first, filter by action / actorEmail / target, cursor pagination (created_at, id).
func List(ctx context.Context, tx db.DBTX, f AuditFilter) ([]AuditEntry, nextCursor string, err error)
```

- [ ] Steps: failing tests ported from `audit.workers.test.ts` (write+read round-trip, filters, cursor walks the full set without dupes/gaps) → migration + implement → green → commit `feat(admin): audit log port`.

---

### Task 2: Dashboard stats

**Files:**
- Create: `internal/admin/stats.go`
- Test: `internal/admin/stats_test.go`

**Interfaces:** `func Stats(ctx, tx db.DBTX) (*DashboardStats, error)` — port the aggregate queries from `src/server/admin/stats.ts` (users, orgs, polls by type/status, participants, bookings, mail queue depth) **minus the subscription/MRR numbers (billing is gone)**, **plus** `FailedJobs int` (count of dead-letter rows).

- [ ] Steps: failing test (seed known rows → exact numbers, empty DB → zeros) → implement → green → commit `feat(admin): dashboard stats (billing metrics removed, failed-jobs added)`.

---

### Task 3: User management

**Files:**
- Create: `internal/admin/users.go`
- Test: `internal/admin/users_test.go`

**Interfaces:** port `src/server/admin/users.ts`:

```go
type UserFilter struct { Query string; Cursor string; Limit int } // Query matches email/name ILIKE
func SearchUsers(ctx, tx, f UserFilter) ([]AdminUserRow, next string, err error) // hand-built SQL, args always parameterized
func UserDetail(ctx, tx, userID string) (*AdminUserDetail, error) // user + orgs + counts + staff flag
// Mutations (each takes actor + reason, writes audit in-tx):
func LockUser(ctx, sqlDB, actor, userID, reason string) error    // whatever field 00002 generated for banned/locked; sessions revoked via auth seam
func UnlockUser(...) error
func DeleteUser(ctx, sqlDB, actor, userID, reason string) error  // port user-delete.workers.test.ts semantics: cascades + org handling
```

- [ ] Steps: failing tests ported from `users.workers.test.ts` + `user-delete.workers.test.ts` (search paging, lock revokes sessions — assert via auth seam that the session stops validating, delete cascades personal org but not shared orgs with other members, every mutation leaves an audit row) → implement → green → commit `feat(admin): user management with audited mutations`.

---

### Task 4: Failed-jobs screen + HTTP surface

**Files:**
- Create: `internal/admin/handlers.go`
- Test: `internal/admin/handlers_test.go`

**Interfaces:** `func Register(mux *http.ServeMux, a *auth.Service, sqlDB *sql.DB)` — all behind `RequireStaff`:

```
GET    /api/v1/admin/stats                     → Stats
GET    /api/v1/admin/users?query=&cursor=      → SearchUsers
GET    /api/v1/admin/users/{id}                → UserDetail
POST   /api/v1/admin/users/{id}/lock           → LockUser   (body: {reason})
POST   /api/v1/admin/users/{id}/unlock         → UnlockUser (body: {reason})
DELETE /api/v1/admin/users/{id}                → DeleteUser (body: {reason})
GET    /api/v1/admin/audit?action=&actor=&cursor= → List
GET    /api/v1/admin/jobs/failed               → jobs.Dead (id, kind, attempts, last_error, run_at — payloads NOT included: they may hold addresses/tokens)
POST   /api/v1/admin/jobs/{id}/retry           → jobs.Retry (audited: action "job.retry")
```

- [ ] Steps: failing handler tests (staff gate: non-staff session → 403 on every route; each route's happy path; retry makes a dead job claimable and writes an audit row; failed-jobs response omits payload) → implement → wire `Register` into httpserver + serve() → `go test ./...` green → commit `feat(admin): staff http surface incl. dead-letter queue`.
