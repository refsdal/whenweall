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
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
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

// ErrInternal is returned by RequireOrgMember when the membership check itself fails for a
// reason other than "not a member" (e.g. a database error underneath
// CheckMemberExistsInOrganization) — the underlying error is wrapped with %v, not %w, so it never
// leaks a Limen type out of this package; errors.Is(err, ErrInternal) is still true. Handlers
// translate this into a 500, distinct from the 403 ErrForbidden gets.
var ErrInternal = errors.New("auth: internal error")

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
			// Matches this app's own password policy, not the plugin's defaults (8 chars,
			// uppercase+digit required): web/src/routes/signup.tsx's client-side check and its
			// "Use at least 12 characters" hint (auth_password_hint) only ever enforce length,
			// mirroring the old Better-Auth config this replaced (src/server/auth/auth.ts's
			// `emailAndPassword` had no character-class policy of its own either). Leaving the
			// plugin's stricter defaults in place would silently reject any signup the frontend
			// itself told the user was fine — a real "I typed a strong 20-character passphrase
			// and it was rejected" bug, not just an e2e inconvenience.
			credentialpassword.WithPasswordMinLength(12),
			credentialpassword.WithPasswordRequireUppercase(false),
			credentialpassword.WithPasswordRequireNumbers(false),
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
		HTTP: limen.NewDefaultHTTPConfig(httpConfigOptions(cfg, s)...),
		CLI:  cliCfg,
	}
}

// httpConfigOptions builds buildLimenConfig's HTTP options, split out so the test-mode rate
// limiter override below reads as one clearly-labeled addition rather than a change buried inside
// an already-long literal.
func httpConfigOptions(cfg *config.Config, s *Service) []limen.HTTPConfigOption {
	opts := []limen.HTTPConfigOption{
		limen.WithHTTPBasePath("/api/v1/auth"),
		// Secure only when the app itself is served over https: an http-served local/dev
		// deployment (cfg.AppURL "http://...") would otherwise get a cookie the browser
		// silently refuses to send back over that same http connection, breaking sessions
		// entirely rather than just weakening them.
		limen.WithHTTPCookieSecure(strings.HasPrefix(cfg.AppURL, "https://")),
		// Every signin/signup/me (and two-factor/magic-link/oauth) response is routed
		// through this transformer instead of Limen's default user serialization — see
		// sessionTransformer's own doc comment for why the default is missing a usable id.
		limen.WithHTTPSessionTransformer(s.sessionTransformer),
		// No limen.WithHTTPHooks here on purpose: an After hook on "signup"/"oauth-callback"
		// (this package's first attempt at the personal-org invariant) turns out to miss most
		// real signups. ctx.GetAuthResult() is only populated when the route handler itself
		// calls Responder.SessionResponse — but this config's oauth plugin runs in its
		// default redirect mode (RedirectWithSession), which redirects the browser instead of
		// calling SessionResponse, and magic-link's verify (autoCreateUser default true) has
		// the same gap. A hook keyed to specific route IDs silently no-ops for both. The
		// invariant is instead enforced lazily, once per user per process, in
		// resolveSession (session.go) — see ensurePersonalOrgOnce.
	}

	// EnableTestRoutes means /api/test/seed (internal/httpserver's Task 5 route) is live, and
	// with it, e2e specs signing up a fresh user per fixture against ONE long-lived server
	// process. Limen's own built-in rate limiter (NewDefaultRateLimiterConfig, unconditionally
	// wired in whenever this option is absent) is a single in-memory bucket per (IP, path) —
	// credential-password's own PluginHTTPConfig caps /signup/credential and /signin/credential
	// at 5 requests/10s each, which a real Playwright run blows through in seconds (one seed call
	// per fixture, one sign-in per spec) regardless of how many distinct e2e users those requests
	// are for. A deployment that has already accepted "the seed route resets/creates data on
	// demand" (EnableTestRoutes's whole premise, config.Load's own hard-fail keeps this off
	// production) has no reason to also defend Limen's routes against its OWN test traffic, so
	// this disables Limen's rate limiter outright rather than trying to raise its ceiling high
	// enough to guess right for an unknown suite size.
	if cfg.EnableTestRoutes {
		opts = append(opts, limen.WithHTTPRateLimiter(limen.WithRateLimiterEnabled(false)))
	}

	return opts
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

// sessionTransformer implements limen.SessionTransformer (registered via
// limen.WithHTTPSessionTransformer in buildLimenConfig), so it runs on every signin/signup/me
// (and two-factor/magic-link/oauth) response that goes through Limen's Responder.SessionResponse.
//
// It exists because Limen's default user serialization never includes a usable id: UserSchema.Serialize
// deletes the id column outright (see the pinned source's user.go), and even without that,
// LimenCore.SerializeModel drops whatever's left of the id field unless it happens to already be a
// string — ours is a BIGSERIAL, so it comes back as an int64 and gets deleted either way. The
// frontend's self-ownership checks (e.g. "is this my own poll") need the user's id in the /me
// payload, so this rebuilds the payload from user (Limen hands us result.User.Raw(), the same
// unfiltered row resolveSession above scans user_id off of) instead of Limen's own serialization:
// the password column is stripped, and the id is added back as a Go string — matching the
// convention used everywhere else in this seam (fmt.Sprint(user.ID), see the users.id comment in
// migrations/00002_auth.sql) rather than leaving it as whatever numeric type the driver returned.
//
// It also adds "isStaff", straight from staff_users, so the frontend no longer needs its own
// separate admin-only probe just to learn whether the signed-in user is staff.
func (s *Service) sessionTransformer(user map[string]any, _ *limen.SessionResult) (map[string]any, error) {
	payload := maps.Clone(user)
	delete(payload, "password")

	rawID := user["id"]
	payload["id"] = fmt.Sprint(rawID)
	payload["isStaff"] = s.lookupStaffForSessionResponse(rawID)

	return map[string]any{"user": payload}, nil
}

// lookupStaffForSessionResponse is sessionTransformer's staff_users check. Unlike
// resolveSession's combined locked_users/staff_users query, this can't take a request context —
// limen.SessionTransformer's signature carries none (Limen calls it outside any request scope) —
// so it issues its own query against context.Background(). Fails safe to false on error: a
// session response is not the place to turn a staff_users hiccup into a broken login, and
// RequireStaff (session.go) is the actual gate on anything staff-only regardless of what this
// reports.
func (s *Service) lookupStaffForSessionResponse(userID any) bool {
	var staff bool
	if err := s.db.QueryRowContext(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM staff_users WHERE user_id = $1)", userID,
	).Scan(&staff); err != nil {
		s.logger.Error("auth: staff_users check failed in session transformer; defaulting to false", "user_id", fmt.Sprint(userID), "error", err)
		return false
	}
	return staff
}

// RevokeUserSessions revokes every session belonging to userID, for internal/admin's LockUser and
// DeleteUser. It delegates straight to Limen's own RevokeAllSessions (which the pinned source
// confirms deletes the user's rows from Limen's `sessions` table via its own connection — see
// session_store_database.go's DeleteByUserID) rather than this package deleting those rows
// directly: Limen exposes exactly this as public API, so there is no reason to reach past the seam
// into a table it owns. This is deliberately not part of any caller's own transaction — it can't
// be, since it runs on Limen's own connection acquisition, not tx-scoped — so a caller that also
// needs sessions gone for a hard FK reason (DeleteUser, against sessions.user_id's ON DELETE
// RESTRICT) still deletes those rows itself, in its own tx, as the real enforcement; this call is
// what reaches any Limen-side bookkeeping beyond that one table, best-effort.
func (s *Service) RevokeUserSessions(ctx context.Context, userID string) error {
	return s.limen.RevokeAllSessions(ctx, parseLimenID(userID))
}

// ensurePersonalOrgOnce is called from resolveSession (session.go) for every request that carries
// a valid session, on the request's own goroutine. It guarantees user has at least one
// organization — the silent "personal org" every user gets, named from their email's local part
// (ports src/server/auth/personal-org.ts's createPersonalOrganization) — enforcing the invariant
// lazily rather than via a Limen HTTP hook (see the comment in buildLimenConfig's HTTP block for
// why: GetAuthResult() misses both redirect-mode OAuth and magic-link signins). This runs on
// literally every authenticated request until it succeeds once for a given user, at which point
// personalOrgEnsured short-circuits every later request for that user in this process — so the
// one-time cost (a ListOrganizations call, plus a CreateOrganization on a brand-new user's very
// first request) is paid at most once per user per process, not per request.
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

// ensurePersonalOrganizationForUser is the reusable core: check-then-create. This used to be
// guarded by a Postgres advisory lock (poll loop around pg_try_advisory_xact_lock) to make two
// concurrent first-requests for the same brand-new user race-safe; that machinery is gone now
// that createPersonalOrgIfMissing passes an explicitly unique slug and treats a slug collision as
// "someone else already won" (see its doc comment) — the create itself is the race-safe point, so
// no lock is needed around the check-then-create at all. The lock was also a standing
// pool-deadlock hazard: a blocking acquire would hold a connection for the entire wait while the
// eventual winner needed a *second*, independent connection (through the organization plugin's
// own connection acquisition) to run ListOrganizations/CreateOrganization on, and
// TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne could hang testdb's 5-connection pool
// solid if that second connection was never free. Conflict-tolerant create sidesteps the hazard
// entirely rather than working around it.
func (s *Service) ensurePersonalOrganizationForUser(ctx context.Context, user *limen.User) error {
	return s.createPersonalOrgIfMissing(ctx, user)
}

// createPersonalOrgIfMissing creates the personal organization (named from the user's email local
// part) only if the user still has none, using a slug unique per user
// (normalized-local-part + "-" + user ID) rather than Limen's default name-derived slug — the
// bug this replaced: two users sharing an email local part on different domains (ada@foo.com,
// ada@bar.com) would both generate the same slug from the name alone, so the second one's
// CreateOrganization would fail with ErrOrganizationSlugAlreadyExists on every single request
// forever, permanently leaving that user without a personal org.
//
// A slug collision is now expected and benign, not an error: it means either this exact user's
// slug already exists (another goroutine or replica's concurrent first-request for them won the
// race between this function's own ListOrganizations check and its CreateOrganization call) or,
// vanishingly unlikely, a hash-style collision — either way the invariant this function exists to
// enforce (at least one organization) is already satisfied, so it's treated as success rather
// than surfaced as an error that would just make the very next request retry the same failure.
func (s *Service) createPersonalOrgIfMissing(ctx context.Context, user *limen.User) error {
	page, err := s.orgs.ListOrganizations(ctx, user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 1})
	if err != nil {
		return fmt.Errorf("list organizations: %w", err)
	}
	if page.Total > 0 {
		return nil
	}

	name := nameFromEmail(user.Email)
	slug := personalOrgSlug(user.Email, fmt.Sprint(user.ID))
	if _, err := s.orgs.CreateOrganization(ctx, user, &organization.CreateOrganizationRequest{Name: name, Slug: slug}); err != nil {
		if errors.Is(err, organization.ErrOrganizationSlugAlreadyExists) || isOrganizationSlugConflict(err) {
			return nil
		}
		return fmt.Errorf("create personal organization: %w", err)
	}
	return nil
}

// isOrganizationSlugConflict reports whether err is a raw Postgres unique-violation on
// organizations.slug that slipped past CreateOrganization's own pre-check (a SELECT for the slug
// followed, non-atomically, by the INSERT) — exactly what happens when two callers race for the
// very same personal-org slug (same user, same deterministic slug) between that check and the
// insert, which TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne can trigger. A loser of
// *that* race gets pgconn's own error type straight from the driver, not Limen's
// ErrOrganizationSlugAlreadyExists (Limen only ever returns that sentinel from its own pre-check
// losing, never by classifying a lower-level constraint violation), so it has to be recognized
// separately. Scoped to the specific slug index by name, not just SQLSTATE 23505, so an unrelated
// unique-constraint violation elsewhere in the same CreateOrganization transaction (e.g. on the
// owner membership row) is never mistaken for "the org already exists."
func isOrganizationSlugConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_organizations_slug"
}

// personalOrgSlug derives a per-user-unique slug from email's local part and userID: lowercased,
// every run of non-alphanumeric characters collapsed to a single '-'. Uniqueness comes from
// userID, not the local part alone — see createPersonalOrgIfMissing's doc comment for why that
// matters. Limen's own slug generator would additionally normalize this the same way if
// normalizeSlugs is left at its default (true), but this doesn't rely on that: the slug handed to
// CreateOrganization is already exactly what ends up stored.
func personalOrgSlug(email, userID string) string {
	local := nameFromEmail(email)
	var b strings.Builder
	for _, r := range strings.ToLower(local) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	normalized := b.String()
	if normalized == "" {
		normalized = "org"
	}
	return normalized + "-" + userID
}

// firstOrganization returns user's first organization by Limen's default ordering, used by
// resolveSession (session.go) to pick a default active organization for a session that doesn't
// have one yet. ensurePersonalOrgOnce (called just before this in resolveSession) guarantees at
// least one organization exists by the time this runs.
func (s *Service) firstOrganization(ctx context.Context, user *limen.User) (*organization.Organization, bool) {
	page, err := s.orgs.ListOrganizations(ctx, user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 1})
	if err != nil || len(page.Items) == 0 {
		return nil, false
	}
	return page.Items[0], true
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
