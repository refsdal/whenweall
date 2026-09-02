// Task 4's HTTP surface: the staff-only console's frontend contract (a later plan's own client)
// over this package's existing Record/List/Stats/SearchUsers/UserDetail/LockUser/UnlockUser/
// DeleteUser (audit.go/stats.go/users.go) plus internal/jobs's dead-letter view (Dead/Retry) —
// following internal/bookings/handlers.go's own Register wiring pattern (a thin decode -> service
// -> respond layer over the promoted internal/httpserver helpers), with one deliberate deviation:
// Register here takes a concrete *auth.Service, not an Auth interface. Every other HTTP-surfaced
// package needs a narrow interface because it also needs FAKE sessions/guest tokens in its own
// tests (bookings/polls' own fakeAuth) — this package's tests instead drive a REAL auth.Service
// behind a real httptest.Server (mirroring users_test.go's own authHarness), because RequireStaff
// itself has to be exercised end-to-end (a real session, actually resolved from a cookie, actually
// carrying a real staff_users row) for the staff-gate table test to mean anything.
//
// None of this file's endpoints has a TS server-function analogue to port faithfully:
// admin.functions.ts never exposed lock/unlock/delete at all (TS drove Better-Auth's own admin
// plugin directly from the client — users.go's own package doc comment), and its three read
// endpoints (fetchAdminStats/fetchAdminUsers/fetchAdminUserDetail/fetchAdminAuditLog) took no
// cursor at all where this port's own SearchUsers/List already do (their own doc comments). Every
// response shape below is therefore this task's own design, chosen to mirror this package's
// existing Go conventions (camelCase JSON, an opaque nextCursor alongside a page) rather than any
// TS wire format:
//
//	GET  stats               -> DashboardStats, unwrapped
//	GET  users               -> {"users": [...], "nextCursor": "..."}
//	GET  users/{id}          -> AdminUserDetail, unwrapped (404 "not_found" for an unknown id)
//	POST users/{id}/lock     -> {"ok": true}
//	POST users/{id}/unlock   -> {"ok": true}
//	DELETE users/{id}        -> {"ok": true}
//	GET  audit               -> {"entries": [...], "nextCursor": "..."}
//	GET  jobs/failed         -> {"jobs": [FailedJobView, ...]} — Payload is deliberately never
//	                            read off the underlying jobs.Job at all (see FailedJobView's own
//	                            doc comment), since a dead-lettered mail job's payload can carry a
//	                            recipient address or an unsubscribe/verification token.
//	POST jobs/{id}/retry     -> {"ok": true} (404 "not_found" for an unknown job id, 409
//	                            "conflict" for one that exists but isn't dead-lettered yet)
package admin

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/jobs"
)

// failedJobsLimit bounds GET jobs/failed the same way defaultListLimit bounds audit.go's own List
// — the brief's endpoint table takes no query parameters for this route at all, so there is
// nothing for a caller to raise it with; a genuinely large dead-letter backlog is itself the
// signal something is badly wrong; this endpoint is a screen, not a bulk export.
const failedJobsLimit = 200

// Register mounts this package's whole staff-only HTTP surface on mux, every route wrapped in
// a.RequireStaff (401 unauthenticated / 403 forbidden, both the standard envelope — see
// auth.Service.RequireStaff's own doc comment).
func Register(mux *http.ServeMux, a *auth.Service, sqlDB *sql.DB) {
	mux.HandleFunc("GET /api/v1/admin/stats", a.RequireStaff(handleStats(sqlDB)))
	mux.HandleFunc("GET /api/v1/admin/users", a.RequireStaff(handleSearchUsers(sqlDB)))
	mux.HandleFunc("GET /api/v1/admin/users/{id}", a.RequireStaff(handleUserDetail(sqlDB)))
	mux.HandleFunc("POST /api/v1/admin/users/{id}/lock", a.RequireStaff(handleLockUser(sqlDB, a)))
	mux.HandleFunc("POST /api/v1/admin/users/{id}/unlock", a.RequireStaff(handleUnlockUser(sqlDB)))
	mux.HandleFunc("DELETE /api/v1/admin/users/{id}", a.RequireStaff(handleDeleteUser(sqlDB, a)))
	mux.HandleFunc("GET /api/v1/admin/audit", a.RequireStaff(handleAuditList(sqlDB)))
	mux.HandleFunc("GET /api/v1/admin/jobs/failed", a.RequireStaff(handleFailedJobs(sqlDB)))
	mux.HandleFunc("POST /api/v1/admin/jobs/{id}/retry", a.RequireStaff(handleRetryJob(sqlDB)))
}

// writeInternalError logs err (unrecognized-by-design here — this package has no domain sentinel
// vocabulary of its own the way bookings/polls' mapServiceError does, since every failure mode
// this file's handlers can hit that ISN'T one of the explicit checks below is a genuine
// infrastructure fault) and reports a generic 500, mirroring httpserver.WriteDomainError's own
// log-and-500 fallback path.
func writeInternalError(w http.ResponseWriter, err error) {
	slog.Default().Error("admin: internal error", "error", err)
	httpserver.Err(w, http.StatusInternalServerError, "internal", "internal error", nil)
}

// requireExistingUser loads id via UserDetail, writing a 404 "not_found" envelope (or a 500) and
// returning ok == false when there's nothing to act on. UserDetail's own contract (nil, not an
// error, for an unknown OR malformed id — its doc comment) is exactly the "stale ticket link"
// shape a 404 should cover, so this is the only existence check every mutating handler below
// needs — there is no separate "id doesn't even parse" case to distinguish.
func requireExistingUser(w http.ResponseWriter, r *http.Request, sqlDB *sql.DB, id string) (*AdminUserDetail, bool) {
	detail, err := UserDetail(r.Context(), sqlDB, id)
	if err != nil {
		writeInternalError(w, err)
		return nil, false
	}
	if detail == nil {
		httpserver.Err(w, http.StatusNotFound, "not_found", "user not found", nil)
		return nil, false
	}
	return detail, true
}

// reasonBody is the JSON body lock/unlock/delete each require: {"reason": "..."}.
type reasonBody struct {
	Reason string `json:"reason"`
}

// decodeReason decodes r's body into a reasonBody and requires a non-blank Reason, writing the
// standard 400 "invalid" envelope (with a Fields["reason"] pointer for the frontend to highlight)
// and returning ok == false otherwise. Every one of this file's three mutating user routes needs
// this — LockUser/UnlockUser/DeleteUser themselves happily accept an empty reason (NULLIF makes
// it a SQL NULL, not a rejected call — see users.go's own INSERT), so the requirement is enforced
// here, at the HTTP boundary, not in the service layer.
func decodeReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body reasonBody
	if !httpserver.DecodeJSON(w, r, &body) {
		return "", false
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		httpserver.Err(w, http.StatusBadRequest, "invalid", "reason is required", map[string]string{"reason": "required"})
		return "", false
	}
	return reason, true
}

// writeCannotTargetSelf rejects a staff caller's lock/delete of their OWN account as a 400
// "invalid" — a validation failure, not a 403: the caller does have the general authority to
// lock/delete users, it's this specific target that is never a valid choice, the same category of
// error decodeReason's empty-reason case already reports this way. Chosen over 403 because a
// self-lock/self-delete is never a permissions question (staff, deciding to act on themselves, IS
// authorized) — it is a same-shape-as-validation "this request can never succeed" condition: a
// staff member locking their own account would revoke their own session (LockUser's own
// RevokeUserSessions call) mid-request with no recovery path short of another staff member's
// intervention, and a self-delete is simply irreversible in the same self-inflicted way. There is
// no TS analogue for this guard to mirror (see this file's package doc comment) — it is a
// Go-rewrite-only safety addition. Deliberately NOT applied to unlock: removing a restriction from
// your own account can't strand you the way imposing one can, so unlock has no self-guard at all.
func writeCannotTargetSelf(w http.ResponseWriter, action string) {
	httpserver.Err(w, http.StatusBadRequest, "invalid", "you cannot "+action+" your own account", map[string]string{"id": "self"})
}

// requireActor reads the caller's session back out of the request context. RequireStaff (which
// wraps every handler this file registers) already guarantees one is present by the time a
// handler body runs, so the ok == false branch below is unreachable in practice — kept anyway as
// a defensive, not-actually-dead check, the same shape RequireOrgMember (session.go) uses.
func requireActor(w http.ResponseWriter, r *http.Request) (*auth.Session, bool) {
	sess, ok := auth.FromContext(r.Context())
	if !ok {
		httpserver.Err(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
		return nil, false
	}
	return sess, true
}

func handleStats(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := Stats(r.Context(), sqlDB)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, stats)
	}
}

func handleSearchUsers(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		users, nextCursor, err := SearchUsers(r.Context(), sqlDB, UserFilter{
			Query:  q.Get("query"),
			Cursor: q.Get("cursor"),
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"users": users, "nextCursor": nextCursor})
	}
}

func handleUserDetail(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, ok := requireExistingUser(w, r, sqlDB, r.PathValue("id"))
		if !ok {
			return
		}
		httpserver.JSON(w, http.StatusOK, detail)
	}
}

func handleLockUser(sqlDB *sql.DB, authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireActor(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		reason, ok := decodeReason(w, r)
		if !ok {
			return
		}
		if id == actor.UserID {
			writeCannotTargetSelf(w, "lock")
			return
		}
		if _, ok := requireExistingUser(w, r, sqlDB, id); !ok {
			return
		}
		if err := LockUser(r.Context(), sqlDB, authSvc, actor, id, reason); err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleUnlockUser(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireActor(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		reason, ok := decodeReason(w, r)
		if !ok {
			return
		}
		if _, ok := requireExistingUser(w, r, sqlDB, id); !ok {
			return
		}
		if err := UnlockUser(r.Context(), sqlDB, actor, id, reason); err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleDeleteUser(sqlDB *sql.DB, authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireActor(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		reason, ok := decodeReason(w, r)
		if !ok {
			return
		}
		if id == actor.UserID {
			writeCannotTargetSelf(w, "delete")
			return
		}
		if _, ok := requireExistingUser(w, r, sqlDB, id); !ok {
			return
		}
		if err := DeleteUser(r.Context(), sqlDB, authSvc, actor, id, reason); err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleAuditList(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		entries, nextCursor, err := List(r.Context(), sqlDB, AuditFilter{
			Action:     q.Get("action"),
			ActorEmail: q.Get("actor"),
			Cursor:     q.Get("cursor"),
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"entries": entries, "nextCursor": nextCursor})
	}
}

// FailedJobView is GET jobs/failed's own wire shape: id/kind/attempts/lastError/runAt, straight
// off jobs.Job — and, by construction, nothing else. It deliberately has no Payload field at all
// (rather than, say, one marked `json:"-"`) so leaving a dead job's Payload out of this response
// can never regress silently by a future field-for-field copy of jobs.Job growing one — a
// dead-lettered "mail:send" job's own payload (internal/mailer) carries the recipient's email
// address, and some mail kinds (verification/password-reset/magic-link) carry a raw token in it
// too; either is unfit for a support console screen to render.
type FailedJobView struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Attempts  int     `json:"attempts"`
	LastError *string `json:"lastError"`
	RunAt     string  `json:"runAt"`
}

func handleFailedJobs(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dead, err := jobs.Dead(r.Context(), sqlDB, failedJobsLimit)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		views := make([]FailedJobView, 0, len(dead))
		for _, j := range dead {
			views = append(views, FailedJobView{
				ID:        j.ID,
				Kind:      j.Kind,
				Attempts:  j.Attempts,
				LastError: j.LastError,
				RunAt:     formatISO(j.RunAt),
			})
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"jobs": views})
	}
}

func handleRetryJob(sqlDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireActor(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")

		tx, err := sqlDB.BeginTx(r.Context(), nil)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		defer func() { _ = tx.Rollback() }()

		// FOR UPDATE locks the row for the rest of this tx, so a concurrent claim (the worker's
		// own ClaimDue) can't slip in between this check and Retry's own UPDATE below. Distinguishes
		// three outcomes, not two: absent entirely -> 404 "not_found" (the pre-existing contract,
		// e.g. a stale link); found but still live (attempts < max_attempts, e.g. already
		// re-claimed by the worker, or never actually failed) -> 409 "conflict", since retrying a
		// job that isn't dead-lettered would stomp on whatever is already working it; found and
		// dead-lettered -> proceed. Retrying only ever makes sense for the dead-letter queue this
		// route's own handleFailedJobs surfaces (attempts >= max_attempts) — resurrecting a job
		// that's merely mid-flight is never the support console's intent.
		var attempts, maxAttempts int
		err = tx.QueryRowContext(r.Context(),
			`SELECT attempts, max_attempts FROM scheduled_jobs WHERE id = $1 FOR UPDATE`, id,
		).Scan(&attempts, &maxAttempts)
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
		}

		if err := jobs.Retry(r.Context(), tx, id); err != nil {
			writeInternalError(w, err)
			return
		}
		// "job.retry" is this route's own audit action (the brief's naming, distinct from
		// users.go's "lock-user"/"unlock-user"/"delete-user" hyphenated convention) — target type
		// "job", targetID the scheduled_jobs row's own id. No reason: a retry is never asked to
		// justify itself the way a lock/unlock/delete is (there is nothing to weigh against a
		// user's rights — it only resumes work the system itself already scheduled).
		if err := Record(r.Context(), tx, actor, "job.retry", "job", id, "", nil); err != nil {
			writeInternalError(w, err)
			return
		}
		if err := tx.Commit(); err != nil {
			writeInternalError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
