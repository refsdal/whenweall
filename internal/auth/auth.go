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
	"github.com/thecodearcher/limen/plugins/oauth"
	oauthgeneric "github.com/thecodearcher/limen/plugins/oauth-generic"
	oauthgoogle "github.com/thecodearcher/limen/plugins/oauth-google"
	"github.com/thecodearcher/limen/plugins/organization"

	"github.com/refsdal/whenweall/internal/clientip"
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
	passwords   credentialpassword.API // ComparePassword for the delete-account re-check
	db          *sql.DB
	cfg         *config.Config
	enqueueMail Enqueuer
	logger      *slog.Logger

	// personalOrgEnsured is an in-process once-cache (keyed by fmt.Sprint(user.ID)) of users this
	// process has already confirmed have at least one organization — see
	// ensurePersonalOrgOnce's doc comment for why the invariant is enforced here, lazily, rather
	// than via a Limen HTTP hook.
	personalOrgEnsured sync.Map

	// pendingSignupProfiles is a mail-dispatch hint ONLY: it bridges a signup request's submitted
	// name/locale (captured by a Before hook, keyed by normalized email) across to
	// enqueueTokenMail's verify_email dispatch, which happens synchronously inside
	// credential-password's own SignUpWithCredentialAndPassword — strictly before the HTTP
	// handler calls SessionResponse, and therefore strictly before this package's own After hook
	// (afterSignup, signup_hook.go) ever runs. Without this, the very first mail a new user gets
	// would always be built from the pre-signup state (no row in user_preferences yet, first/last
	// name still blank) regardless of what they typed on the signup form.
	//
	// afterSignup does NOT read this map — it recomputes name/locale straight from its own
	// request (see afterSignup's doc comment for why that's the correct fix for a real,
	// code-review-caught race: keying this map by email with last-write-wins meant two concurrent
	// signup attempts for the same address could persist one account with the OTHER attempt's
	// name/locale). What remains, because Limen's WithSendEmailVerificationMail callback receives
	// only an email and a token — no request, no hook context, nothing else to correlate by — is
	// a narrower, accepted limitation: if two signup attempts for the very same (not-yet-existing)
	// address race each other, the one that wins and gets a verify_email may occasionally have its
	// mail worded from the losing attempt's submitted name/locale rather than its own. This never
	// touches what gets persisted (afterSignup is immune to it, per above) and is bounded to
	// mail wording alone; a fully race-free fix for this one narrow case would require Limen to
	// pass more than (email, token) into that callback, which it does not. The entry is always
	// removed once the request completes, success or failure — see afterSignup's doc comment.
	pendingSignupProfiles sync.Map
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
	s.passwords = credentialpassword.Use(a)
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
			// A fresh signup must NOT get a session: the account is unusable until the mailed
			// verification token is consumed (see AuthMountGuard/RequireSession in session.go),
			// and handing out a cookie first would only let the SPA discover that one request
			// later. Signup responds with the user payload (through sessionTransformer, so the
			// after-hook in signup_hook.go still sees the auth result) and no Set-Cookie; the
			// user signs in explicitly afterwards.
			credentialpassword.WithAutoSignInOnSignUp(false),
		),
		organization.New(
			organization.WithSendInvitationMail(func(ctx context.Context, d *organization.SendInvitationMailData) {
				s.enqueueInviteMail(ctx, d)
			}),
			organization.WithHooks(organizationHooks()),
		),
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
		plugins = append(plugins, oauth.New(
			oauth.WithProviders(providers...),
			// See verifiedEmailUserInfo: only the generic OIDC provider is guarded; when OIDC is
			// off, cfg.OIDCName matches no provider and this is a pure pass-through.
			oauth.WithGetUserInfo(verifiedEmailUserInfo(providers, cfg.OIDCName)),
		))
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
		// Every signin/signup/me (and oauth) response is routed through this transformer
		// instead of Limen's default user serialization — see sessionTransformer's own doc
		// comment for why the default is missing a usable id.
		limen.WithHTTPSessionTransformer(s.sessionTransformer),
		// After hook on credential-password's "signup" route: stores the display name and locale
		// Limen's own handler ignores (see signup_hook.go for why body/auth-result access is safe
		// there — and why an earlier hook-based attempt at the personal-org invariant was NOT:
		// the OAuth callback redirects without calling SessionResponse, so it never had an auth
		// result to read; the personal org therefore stays lazily enforced in resolveSession).
		limen.WithHTTPHooks(s.signupHooks()),
	}

	// Limen's own built-in limiter (NewDefaultRateLimiterConfig, unconditionally wired in whenever
	// WithHTTPRateLimiter is absent) has two defaults this deployment cannot live with:
	//
	//   - Its key generator returns the raw X-Forwarded-For header verbatim (then X-Real-IP, then
	//     RemoteAddr — utils.go's ipExtractorFromRemoteAddr). Without a trusted proxy that header
	//     is attacker-controlled, so its 5-per-10s sign-in rule could be bypassed by varying the
	//     header, or aimed at a victim by forging their address. Keying on clientip.FromRequest —
	//     the same value internal/httpserver's own Postgres limiter uses — makes both limiters
	//     agree on who the client is and honour TRUST_PROXY the same way.
	//   - Its global 100/min-per-key rule covers every route, including GET /me and
	//     GET /organizations/active, which the SPA reads on every navigation; a NATed office
	//     would start seeing 429s from /me. Those two are read-only and cheap, and our own
	//     limiter still guards the hot mutating routes, so they are exempted outright.
	limiterOpts := []limen.RateLimiterOption{
		limen.WithRateLimiterKeyGenerator(func(r *http.Request) string {
			return clientip.FromRequest(r, cfg.TrustProxy)
		}),
		// Paths are joined onto the HTTP base path by Limen (path.Join("/api/v1/auth", "/me")).
		limen.WithRateLimiterDisableForPaths("/me", "/organizations/active"),
	}

	// EnableTestRoutes means /api/test/seed (internal/httpserver's Task 5 route) is live, and
	// with it, e2e specs signing up a fresh user per fixture against ONE long-lived server
	// process. Limen's limiter is a single in-memory bucket per (IP, path) — credential-password's
	// own PluginHTTPConfig caps /signup/credential and /signin/credential at 5 requests/10s each,
	// which a real Playwright run blows through in seconds (one seed call per fixture, one
	// sign-in per spec) regardless of how many distinct e2e users those requests are for. A
	// deployment that has already accepted "the seed route resets/creates data on demand"
	// (EnableTestRoutes's whole premise, config.Load's own hard-fail keeps this off production)
	// has no reason to also defend Limen's routes against its OWN test traffic, so this disables
	// Limen's rate limiter outright rather than trying to raise its ceiling high enough to guess
	// right for an unknown suite size.
	if cfg.EnableTestRoutes {
		limiterOpts = append(limiterOpts, limen.WithRateLimiterEnabled(false))
	}
	opts = append(opts, limen.WithHTTPRateLimiter(limiterOpts...))

	// Under `bun dev` the SPA is served by Vite on :5173 and proxies /api to this process, so an
	// OAuth sign-in started from that page sends redirect_uri=http://localhost:5173/... — which
	// Limen's oauth plugin validates with IsTrustedOrigin (its own base URL, i.e. APP_URL, plus
	// this list) and would otherwise refuse with 403 "redirect_uri is not trusted". Development
	// only: production has exactly one origin, APP_URL, and it is trusted implicitly.
	//
	// WithHTTPOriginCheck(false) goes with it deliberately. Limen's origin-check middleware is a
	// no-op while this list is empty, but the moment it is non-empty it requires an Origin (or
	// Referer) header matching the list on EVERY mutating request — a bare curl, or this
	// package's own tests posting JSON without an Origin header, would start failing with 403.
	// Our own internal/httpserver.CheckOrigin already guards every mutating /api/ request (and,
	// like browsers, treats an absent Origin as "not a cross-site form"), and Limen's CSRF
	// protection stays on, so nothing is lost by switching Limen's stricter duplicate off here.
	if cfg.AppEnv == "development" {
		opts = append(opts,
			limen.WithHTTPTrustedOrigins([]string{"http://localhost:5173"}),
			limen.WithHTTPOriginCheck(false),
		)
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
// (and oauth) response that goes through Limen's Responder.SessionResponse.
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
// It also adds what the SPA needs to render an account without a second round trip: "isStaff"
// (staff_users), "locale" (user_preferences, "en" when absent), "name" (DisplayName over
// first_name/last_name/email), "emailVerified" (email_verified_at IS NOT NULL — the gate in
// session.go keys on the same fact) and "hasPassword" (whether a credential exists — the settings
// page's delete-account dialog asks for the current password only when there is one).
func (s *Service) sessionTransformer(user map[string]any, _ *limen.SessionResult) (map[string]any, error) {
	payload := maps.Clone(user)
	payload["hasPassword"] = user["password"] != nil
	delete(payload, "password")

	rawID := user["id"]
	email, _ := user["email"].(string)
	first, _ := user["first_name"].(string)
	last, _ := user["last_name"].(string)
	staff, locale := s.lookupSessionExtras(rawID)

	payload["id"] = fmt.Sprint(rawID)
	payload["isStaff"] = staff
	payload["locale"] = locale
	payload["name"] = DisplayName(first, last, email)
	payload["emailVerified"] = user["email_verified_at"] != nil

	return map[string]any{"user": payload}, nil
}

// lookupSessionExtras is sessionTransformer's staff_users + user_preferences read, in one round
// trip. Unlike resolveSession's combined query, this can't take a request context —
// limen.SessionTransformer's signature carries none (Limen calls it outside any request scope) —
// so it issues its own query against context.Background(). Fails safe (false, "en") on error: a
// session response is not the place to turn a lookup hiccup into a broken login, and RequireStaff
// (session.go) is the actual gate on anything staff-only regardless of what this reports.
func (s *Service) lookupSessionExtras(userID any) (staff bool, locale string) {
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM staff_users WHERE user_id = $1),
		       coalesce((SELECT locale FROM user_preferences WHERE user_id = $1), 'en')
	`, userID).Scan(&staff, &locale); err != nil {
		s.logger.Error("auth: staff/locale lookup failed in session transformer; defaulting", "user_id", fmt.Sprint(userID), "error", err)
		return false, "en"
	}
	return staff, normalizeLocale(locale)
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
// why: GetAuthResult() misses redirect-mode OAuth signins). This runs on
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

// maxOrgSlugLen is the upper bound ValidateOrgSlug enforces.
const maxOrgSlugLen = 30

// personalOrgSlug derives a per-user-unique slug from email's local part and userID: lowercased,
// every run of non-alphanumeric characters collapsed to a single '-', truncated so that
// local + "-" + userID never exceeds maxOrgSlugLen, with leading/trailing hyphens trimmed so the
// result satisfies ValidateOrgSlug (which organizationHooks now enforces on every create —
// including this one). Uniqueness comes from userID, not the local part alone — see
// createPersonalOrgIfMissing's doc comment for why that matters. The slug handed to
// CreateOrganization is already exactly what ends up stored (normalizeSlugs is off).
func personalOrgSlug(email, userID string) string {
	local := nameFromEmail(email)
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(local) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			b.WriteRune('-')
			lastHyphen = true
		}
	}
	normalized := b.String()

	maxLocal := maxOrgSlugLen - 1 - len(userID)
	if maxLocal < 1 {
		maxLocal = 1
	}
	if len(normalized) > maxLocal {
		normalized = normalized[:maxLocal]
	}
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		normalized = "org"
		if len(normalized) > maxLocal {
			normalized = normalized[:maxLocal]
		}
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
// callback a bare token, not a full URL) and enqueues it under templateName. path is the SPA
// route that will read ?token= and complete the flow. Name and Locale come from the user's
// profile — Limen hands over only the email, so the profile is looked up by it; a lookup failure
// falls back to the email's local part and "en" rather than dropping the mail.
//
// For the verify_email dispatch specifically, the profile row this would otherwise read doesn't
// exist yet — see pendingSignupProfiles' doc comment for why, and for the narrow, accepted limit
// of this fallback under a same-address signup race — so a pending entry from the signup's own
// Before hook is preferred over the (not-yet-written) database row when present.
func (s *Service) enqueueTokenMail(email, templateName, path, token string) {
	name, locale := nameFromEmail(email), "en"
	if pending, ok := s.pendingSignupProfiles.Load(limen.NormalizeEmail(email)); ok {
		p := pending.(pendingSignupProfile)
		if p.name != nil {
			name = *p.name
		}
		locale = p.locale
	} else if p, err := s.profileByEmail(context.Background(), email); err == nil {
		name, locale = p.Name, p.Locale
	}
	s.sendMail(mailer.Message{
		To:       email,
		Template: templateName,
		Data: map[string]any{
			"URL":    s.cfg.AppURL + path + "?token=" + token,
			"Name":   name,
			"Locale": locale,
		},
	})
}

// enqueueInviteMail sends the org_invite mail. The link points at the SPA's accept-invitation
// page (not a Limen backend route — there is no such route; the SPA reads the token from the
// path and calls organization's respond-to-invitation API itself). InviterName is the inviter's
// stored display name; Locale is the invitee's own stored locale when they already have an
// account, else the inviter's (a Norwegian team inviting a colleague is the best guess available
// for someone we know nothing about yet).
func (s *Service) enqueueInviteMail(ctx context.Context, d *organization.SendInvitationMailData) {
	if d == nil || d.Invitation == nil || d.Organization == nil {
		s.logger.Error("auth: org invite mail callback missing invitation or organization data")
		return
	}

	inviterName, locale := "", "en"
	if d.Inviter != nil {
		inviterName = nameFromEmail(d.Inviter.Email)
		if p, err := s.GetProfile(ctx, fmt.Sprint(d.Inviter.ID)); err == nil {
			inviterName, locale = p.Name, p.Locale
		}
	}
	if p, err := s.profileByEmail(ctx, d.Invitation.Email); err == nil {
		locale = p.Locale
	}

	s.sendMailCtx(ctx, mailer.Message{
		To:       d.Invitation.Email,
		Template: "org_invite",
		Data: map[string]any{
			"URL":         s.cfg.AppURL + "/accept-invitation/" + d.Invitation.Token,
			"InviterName": inviterName,
			"OrgName":     d.Organization.Name,
			"Locale":      locale,
		},
	})
}

// sendMail enqueues msg on a background context. Every Limen mail callback above fires
// synchronously inside a request/handler goroutine with no ctx of its own
// (WithSendPasswordResetEmail and WithSendEmailVerificationMail take only (email, token string))
// — using context.Background() here (rather than propagating a request context that may already
// be canceled by the time the callback runs) matches the brief's "enqueue outside any tx"
// instruction: this is a fire-and-forget queue write, not part of the request's own work.
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
