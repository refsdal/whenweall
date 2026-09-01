// Package auth is the seam between whenweall and Limen (github.com/thecodearcher/limen), the
// self-hosted authentication library backing signup/signin/sessions/organizations. No other
// package may import Limen directly: everything the rest of the application needs — Service,
// Session, the middleware, and the staff/org-membership helpers — is exported from here, so a
// future replacement of Limen (or an upgrade that changes its API) only ever touches this
// package.
//
// internal/auth/schemagen is the one exception, and only because it exists to make Limen dump
// its schema definitions to disk (see GenerateSchemas) — it never touches Limen directly either;
// it just calls into this package.
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thecodearcher/limen"
	sqladapter "github.com/thecodearcher/limen/adapters/sql"
	credentialpassword "github.com/thecodearcher/limen/plugins/credential-password"
	magiclink "github.com/thecodearcher/limen/plugins/magic-link"
	"github.com/thecodearcher/limen/plugins/oauth"
	oauthgeneric "github.com/thecodearcher/limen/plugins/oauth-generic"
	oauthgoogle "github.com/thecodearcher/limen/plugins/oauth-google"
	"github.com/thecodearcher/limen/plugins/organization"
	twofactor "github.com/thecodearcher/limen/plugins/two-factor"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/mailer"
)

// ErrUnauthenticated is returned by RequireOrgMember when ctx carries no session. Handlers that
// call it directly (rather than sitting behind RequireSession) translate this into a 401.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// ErrForbidden is returned by RequireOrgMember when the caller is not a member of orgID.
// Handlers translate this into a 403.
var ErrForbidden = errors.New("auth: forbidden")

// Enqueuer matches mailer.Enqueue's signature exactly, so tests can substitute a stub that
// captures mail instead of writing to scheduled_jobs.
type Enqueuer func(ctx context.Context, tx db.DBTX, msg mailer.Message) error

// Service is the seam: everything the rest of the application needs from Limen, wrapped so
// nothing outside this package touches Limen types.
type Service struct {
	limen       *limen.Limen
	orgs        organization.API
	db          *sql.DB
	cfg         *config.Config
	enqueueMail Enqueuer
	logger      *slog.Logger

	// personalOrgEnsured is an in-process once-cache (keyed by fmt.Sprint(user.ID)) of users this
	// process has already confirmed have at least one organization — see
	// ensurePersonalOrgOnce's doc comment for why the invariant is enforced here, lazily, rather
	// than via a Limen HTTP hook.
	personalOrgEnsured sync.Map
}

// New builds the Service: constructs the Limen configuration (every plugin the product needs,
// oauth providers gated on cfg.Capabilities), and mounts it at /api/v1/auth via Handler.
func New(cfg *config.Config, sqlDB *sql.DB) (*Service, error) {
	return newService(cfg, sqlDB, mailer.Enqueue)
}

// newService is the real constructor; New and tests both funnel through it, differing only in
// which Enqueuer they pass (mailer.Enqueue for New, a capturing stub for tests).
func newService(cfg *config.Config, sqlDB *sql.DB, enqueue Enqueuer) (*Service, error) {
	s := &Service{
		db:          sqlDB,
		cfg:         cfg,
		enqueueMail: enqueue,
		logger:      slog.Default(),
	}

	a, err := limen.New(buildLimenConfig(cfg, sqlDB, s, false))
	if err != nil {
		return nil, fmt.Errorf("auth: limen.New: %w", err)
	}
	s.limen = a
	s.orgs = organization.Use(a)
	return s, nil
}

// GenerateSchemas is a dev-only entry point for internal/auth/schemagen: it builds the exact
// Limen configuration newService builds (same plugins, same options — the controller's dispatch
// ruling requires schemagen to construct the config Task 2 actually uses, so both call paths run
// through buildLimenConfig rather than two copies that could drift apart), but with
// CLI.Enabled true. Calling limen.New with that config is what makes Limen write
// .limen/schemas.json — see Limen's Config.prepareCLIConfig. The mail callbacks are wired to a
// no-op Enqueuer: schemagen never signs anyone up, so they're never invoked.
func GenerateSchemas(cfg *config.Config, sqlDB *sql.DB) error {
	s := &Service{
		db:  sqlDB,
		cfg: cfg,
		enqueueMail: func(context.Context, db.DBTX, mailer.Message) error {
			return nil
		},
		logger: slog.Default(),
	}
	_, err := limen.New(buildLimenConfig(cfg, sqlDB, s, true))
	return err
}

// buildLimenConfig is the one place the Limen configuration is assembled — shared by newService
// (cliEnabled false) and GenerateSchemas (cliEnabled true).
func buildLimenConfig(cfg *config.Config, sqlDB *sql.DB, s *Service, cliEnabled bool) *limen.Config {
	plugins := []limen.Plugin{
		credentialpassword.New(
			credentialpassword.WithSendPasswordResetEmail(func(email, token string) {
				s.enqueueTokenMail(email, "reset_password", "/reset-password", token)
			}),
		),
		organization.New(
			organization.WithSendInvitationMail(func(ctx context.Context, d *organization.SendInvitationMailData) {
				s.enqueueInviteMail(ctx, d)
			}),
		),
		magiclink.New(
			magiclink.WithSendMagicLink(func(m magiclink.MagicLinkMessage) {
				s.enqueueMagicLinkMail(m)
			}),
		),
		twofactor.New(),
	}

	// oauth is only added when at least one provider is actually configured — an empty oauth
	// plugin would register /oauth/:provider/* routes that 404 on every provider anyway, so
	// leaving it out entirely when neither capability is on is both simpler and lets
	// TestOAuthRoutesAbsentWithoutConfig assert on a real 404 rather than a "provider unknown"
	// error from an oauth plugin with no providers.
	if cfg.Capabilities.Google || cfg.Capabilities.OIDC {
		var providers []oauth.Provider
		if cfg.Capabilities.Google {
			providers = append(providers, oauthgoogle.New(
				oauthgoogle.WithClientID(cfg.GoogleClientID),
				oauthgoogle.WithClientSecret(cfg.GoogleClientSecret),
			))
		}
		if cfg.Capabilities.OIDC {
			providers = append(providers, oauthgeneric.New(
				oauthgeneric.WithName(cfg.OIDCName),
				oauthgeneric.WithDiscoveryURL(cfg.OIDCIssuer),
				oauthgeneric.WithClientID(cfg.OIDCClientID),
				oauthgeneric.WithClientSecret(cfg.OIDCClientSecret),
			))
		}
		plugins = append(plugins, oauth.New(oauth.WithProviders(providers...)))
	}

	var cliCfg *limen.CLIConfig
	if cliEnabled {
		cliCfg = &limen.CLIConfig{Enabled: true}
	}

	return &limen.Config{
		BaseURL:  cfg.AppURL,
		Database: sqladapter.NewPostgreSQL(sqlDB),
		Secret:   cfg.LimenSecret,
		Plugins:  plugins,
		// Limen's core schema set always includes a "rate_limits" table (RateLimit is a
		// standing field on SchemaConfig, discovered unconditionally — see schema_discovery.go
		// — regardless of whether the HTTP rate limiter actually uses it; the default
		// NewDefaultRateLimiterConfig uses StoreTypeCache, an in-memory store, so nothing ever
		// writes to it). That name collides with our own rate_limits table (migrations/00001,
		// used by the mailer/room rate limiter — see internal/jobs/housekeeping.go), whose
		// columns (key, count, reset_at) are incompatible with what Limen would otherwise try to
		// ALTER in (id, last_request_at NOT NULL) — confirmed against the pinned CLI generator,
		// which emitted exactly that ALTER TABLE. Renaming Limen's own (unused) table via its
		// supported schema-customization option is the documented fix, not a workaround: it
		// never touches our table, and Limen's DB-backed rate limiting isn't in use anyway.
		Schema: limen.NewDefaultSchemaConfig(
			limen.WithSchemaRateLimit(limen.WithRateLimitTableName("limen_rate_limits")),
		),
		Email: limen.NewDefaultEmailConfig(limen.WithEmailVerification(
			limen.WithSendEmailVerificationMail(func(email, token string) {
				s.enqueueTokenMail(email, "verify_email", "/verify-email", token)
			}),
		)),
		HTTP: limen.NewDefaultHTTPConfig(
			limen.WithHTTPBasePath("/api/v1/auth"),
			// No limen.WithHTTPHooks here on purpose: an After hook on "signup"/"oauth-callback"
			// (this package's first attempt at the personal-org invariant) turns out to miss most
			// real signups. ctx.GetAuthResult() is only populated when the route handler itself
			// calls Responder.SessionResponse — but this config's oauth plugin runs in its
			// default redirect mode (RedirectWithSession), which redirects the browser instead of
			// calling SessionResponse, and magic-link's verify (autoCreateUser default true) has
			// the same gap. A hook keyed to specific route IDs silently no-ops for both. The
			// invariant is instead enforced lazily, once per user per process, in
			// resolveSession (session.go) — see ensurePersonalOrgOnce.
		),
		CLI: cliCfg,
	}
}

// Handler returns Limen's own http.Handler, already mounted at the base path configured above
// (/api/v1/auth). The caller (internal/httpserver) mounts this at "/api/v1/auth/".
func (s *Service) Handler() http.Handler {
	return s.limen.Handler()
}

// MakeStaff flags email's user as platform staff, used by the create-staff-user bootstrap
// command. It looks up the user directly against Limen's users table (there is no public
// find-by-email API on Limen itself) rather than through any Limen plugin, since staff is purely
// our own concept — see staff_users' doc comment in migrations/00002_auth.sql for why it's a
// separate table rather than an extension of Limen's user schema.
func (s *Service) MakeStaff(ctx context.Context, email string) error {
	normalized := limen.NormalizeEmail(email)

	var userID int64
	err := s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE email = $1", normalized).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("auth: no user with email %q", email)
	}
	if err != nil {
		return fmt.Errorf("auth: looking up user by email: %w", err)
	}

	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO staff_users (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING", userID,
	); err != nil {
		return fmt.Errorf("auth: inserting staff_users: %w", err)
	}
	return nil
}

// ensurePersonalOrgOnce is called from resolveSession (session.go) for every request that carries
// a valid session, on the request's own goroutine. It guarantees user has at least one
// organization — the silent "personal org" every user gets, named from their email's local part
// (ports src/server/auth/personal-org.ts's createPersonalOrganization) — enforcing the invariant
// lazily rather than via a Limen HTTP hook (see the comment in buildLimenConfig's HTTP block for
// why: GetAuthResult() misses both redirect-mode OAuth and magic-link signins). This runs on
// literally every authenticated request until it succeeds once for a given user, at which point
// personalOrgEnsured short-circuits every later request for that user in this process — so the
// one-time cost (an advisory-lock round trip plus a ListOrganizations call) is paid at most once
// per user per process, not per request.
//
// Never fails the request: any error is logged and swallowed. The user is deliberately NOT cached
// on error, so the very next request from them retries rather than silently giving up on the
// invariant for the rest of this process's life.
func (s *Service) ensurePersonalOrgOnce(ctx context.Context, user *limen.User) {
	key := fmt.Sprint(user.ID)
	if _, done := s.personalOrgEnsured.Load(key); done {
		return
	}

	if err := s.ensurePersonalOrganizationForUser(ctx, user); err != nil {
		s.logger.Error("auth: personal-org: ensure failed", "user_id", key, "error", err)
		return
	}

	s.personalOrgEnsured.Store(key, struct{}{})
}

// personalOrgLockRetryInterval is how long ensurePersonalOrganizationForUser waits between
// attempts to acquire the per-user advisory lock when it's currently held by someone else.
const personalOrgLockRetryInterval = 20 * time.Millisecond

// ensurePersonalOrganizationForUser is the reusable core: check-then-create, made safe against two
// concurrent first-requests for the same brand-new user (across goroutines in this process, or
// across replicas) via a Postgres advisory lock scoped to the user.
//
// The lock is acquired with pg_try_advisory_xact_lock in a poll loop — deliberately NOT a
// blocking pg_advisory_xact_lock, despite that being the more obvious way to write this. A
// blocking acquire holds its database/sql connection checked out of the pool for the entire wait.
// With several concurrent callers contending for the same key, every "loser" then sits blocked
// with a connection pinned; meanwhile the "winner" (the one actually holding the lock) needs a
// *second*, independent connection to run ListOrganizations/CreateOrganization on (those go
// through the organization plugin's own connection acquisition, not this function's transaction —
// there is no supported way to make them share it). Once the pool is smaller than
// (contending callers + 1), that second connection can never become free — the callers are all
// parked waiting on a lock only the goroutine that can't get its second connection will ever
// release. Self-deadlock. This is exactly what
// TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne hit (hard hang) against testdb's
// 5-connection pool with a plain blocking pg_advisory_xact_lock, which is what caught this.
// Polling with a non-blocking try-lock and releasing the connection between attempts means no
// caller ever blocks while pinning a connection, so the winner's second connection is always
// eventually available regardless of pool size.
func (s *Service) ensurePersonalOrganizationForUser(ctx context.Context, user *limen.User) error {
	for {
		acquired, err := s.withPersonalOrgTryLock(ctx, fmt.Sprint(user.ID), func() error {
			return s.createPersonalOrgIfMissing(ctx, user)
		})
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(personalOrgLockRetryInterval):
		}
	}
}

// withPersonalOrgTryLock makes a single, non-blocking attempt to take the advisory lock scoped to
// key. If acquired, it runs fn and commits (releasing the xact-scoped lock) only after fn
// succeeds; if the lock is currently held by someone else, it returns acquired == false, err ==
// nil so the caller can back off and retry.
func (s *Service) withPersonalOrgTryLock(ctx context.Context, key string, fn func() error) (acquired bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin personal-org lock tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has run

	var gotLock bool
	if err := tx.QueryRowContext(ctx,
		"SELECT pg_try_advisory_xact_lock(hashtext('personal-org:' || $1))", key,
	).Scan(&gotLock); err != nil {
		return false, fmt.Errorf("try personal-org advisory lock: %w", err)
	}
	if !gotLock {
		return false, nil
	}

	if err := fn(); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit personal-org lock tx: %w", err)
	}
	return true, nil
}

// createPersonalOrgIfMissing re-checks organization membership — necessary because another caller
// may have created the org and released the lock while this one was retrying to acquire it — and
// creates the personal organization (named from the user's email local part, plugin-generated
// slug) only if the user still has none.
func (s *Service) createPersonalOrgIfMissing(ctx context.Context, user *limen.User) error {
	page, err := s.orgs.ListOrganizations(ctx, user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}
	if page.Total > 0 {
		return nil
	}

	name := nameFromEmail(user.Email)
	if _, err := s.orgs.CreateOrganization(ctx, user, &organization.CreateOrganizationRequest{Name: name, Slug: ""}); err != nil {
		return fmt.Errorf("create personal organization: %w", err)
	}
	return nil
}

// parseLimenID converts a Session's stringified id (UserID/ActiveOrgID, both fmt.Sprint of
// Limen's `any` id) back to the type Limen's default schema actually stores — int64, since
// buildLimenConfig sets no Schema.IDGenerator. Falls back to the raw string so this keeps
// working if a later change switches to a string/UUID id generator.
func parseLimenID(id string) any {
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		return n
	}
	return id
}

// enqueueTokenMail builds the reset-password/verify-email link (Limen only ever passes the
// callback a bare token; there is no URL to preserve as-is here, unlike magic-link) and enqueues
// it under templateName. path is the SPA route that will read ?token= and complete the flow.
func (s *Service) enqueueTokenMail(email, templateName, path, token string) {
	s.sendMail(mailer.Message{
		To:       email,
		Template: templateName,
		Data: map[string]any{
			"URL":  s.cfg.AppURL + path + "?token=" + token,
			"Name": nameFromEmail(email),
		},
	})
}

// enqueueMagicLinkMail sends the magic-link sign-in mail. Unlike verify-email/reset-password,
// Limen already builds a full URL for magic links (m.URL, pointed at its own
// GET /api/v1/auth/magic-link/verify route) and hands it to this callback — so this uses that
// URL as-is rather than building a separate SPA link.
func (s *Service) enqueueMagicLinkMail(m magiclink.MagicLinkMessage) {
	s.sendMail(mailer.Message{
		To:       m.Email,
		Template: "magic_link",
		Data: map[string]any{
			"URL":  m.URL,
			"Name": nameFromEmail(m.Email),
		},
	})
}

// enqueueInviteMail sends the org_invite mail. The link points at the SPA's accept-invitation
// page (not a Limen backend route — there is no such route; the SPA reads the token from the
// path and calls organization's respond-to-invitation API itself).
func (s *Service) enqueueInviteMail(ctx context.Context, d *organization.SendInvitationMailData) {
	if d == nil || d.Invitation == nil || d.Organization == nil {
		s.logger.Error("auth: org invite mail callback missing invitation or organization data")
		return
	}

	inviterName := ""
	if d.Inviter != nil {
		inviterName = nameFromEmail(d.Inviter.Email)
	}

	s.sendMailCtx(ctx, mailer.Message{
		To:       d.Invitation.Email,
		Template: "org_invite",
		Data: map[string]any{
			"URL":         s.cfg.AppURL + "/accept-invitation/" + d.Invitation.Token,
			"InviterName": inviterName,
			"OrgName":     d.Organization.Name,
		},
	})
}

// sendMail enqueues msg on a background context. Every Limen mail callback above fires
// synchronously inside a request/handler goroutine with no ctx of its own (WithSendPasswordResetEmail
// and WithSendEmailVerificationMail take only (email, token string); WithSendMagicLink takes only
// a message) — using context.Background() here (rather than propagating a request context that
// may already be canceled by the time the callback runs) matches the brief's "enqueue outside any
// tx" instruction: this is a fire-and-forget queue write, not part of the request's own work.
func (s *Service) sendMail(msg mailer.Message) {
	s.sendMailCtx(context.Background(), msg)
}

// sendMailCtx is sendMail's ctx-carrying twin, used by callbacks that do receive a context
// (organization.WithSendInvitationMail).
func (s *Service) sendMailCtx(ctx context.Context, msg mailer.Message) {
	if err := s.enqueueMail(ctx, s.db, msg); err != nil {
		// Never panic or block the caller (a Limen request handler) on a queue write failure —
		// log and move on, same contract as every other Limen mail callback here.
		s.logger.Error("auth: enqueue mail failed", "template", msg.Template, "error", err)
	}
}

// nameFromEmail is the best-effort display name used in mail copy: Limen's mail callbacks never
// hand this package a display name (User only carries ID/Email/EmailVerifiedAt), so the email's
// local part stands in for it rather than leaving the template's {{.Name}} placeholder empty.
func nameFromEmail(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
}

// writeErrorEnvelope writes the standard JSON error envelope used by RequireSession/RequireStaff.
func writeErrorEnvelope(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
