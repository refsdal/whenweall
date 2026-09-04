// Package httpserver builds the application's HTTP mux: health checks, the API surface (added by
// later plans), the embedded SPA, and the middleware stack every request passes through.
package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/routekey"
)

// Server holds the pieces needed to build and run the application's HTTP handler.
type Server struct {
	cfg     *config.Config
	db      *sql.DB
	authSvc *auth.Service
	mux     *http.ServeMux
	logger  *slog.Logger
	// policy is the process-wide Content-Security-Policy/HSTS decision, computed once here from
	// the embedded index.html's inline scripts and cfg.AppURL's scheme — see csp.go.
	policy SecurityPolicy
}

// New builds the full mux: health check, the auth API (mounted at /api/v1/auth/), API 404
// fallback, and the embedded SPA. Later plans add route registration params for the rest of the
// API surface. authSvc is constructed by the caller (cmd/whenweall) since building it can fail
// (invalid Limen config) — New itself has no error return.
func New(cfg *config.Config, sqlDB *sql.DB, authSvc *auth.Service) *Server {
	s := &Server{
		cfg:     cfg,
		db:      sqlDB,
		authSvc: authSvc,
		mux:     http.NewServeMux(),
		logger:  slog.Default(),
		policy:  BuildSecurityPolicy(cfg.AppURL, EmbeddedIndexHTML()),
	}
	s.routes()
	return s
}

// authRateLimit builds the auth-endpoint limiter: 10 hits per minute per client IP, mirroring
// Better-Auth's own stricter built-in rules for sign-in/sign-up/password-reset
// (src/server/auth/auth.ts leaves those defaults alone) now that the storage moves from a
// per-isolate Map/durable object to this shared Postgres counter.
func (s *Server) authRateLimit(name string) func(http.Handler) http.Handler {
	return RateLimit(s.db, name, 10, time.Minute, func(r *http.Request) string {
		return ClientIP(r, s.cfg.TrustProxy)
	})
}

// authRateLimitedRoutes are the hot, unauthenticated auth endpoints that get a 10/min-per-IP
// budget, keyed by "METHOD canonical-path" (see authRateLimitMiddleware for how the canonical
// path is derived from a request). Every other path under /api/v1/auth/ passes straight through
// to Limen unmetered.
var authRateLimitedRoutes = map[string]string{
	"POST /api/v1/auth/signin/credential":       "auth.signin",
	"POST /api/v1/auth/signup/credential":       "auth.signup",
	"POST /api/v1/auth/passwords/request-reset": "auth.password_reset",
}

// authRateLimitMiddleware wraps the entire "/api/v1/auth/" mount in one middleware that computes
// the same canonical path Limen's own router resolves the request to — path.Clean of the path
// with a trailing slash trimmed first — and, only for the routes listed in
// authRateLimitedRoutes, applies that route's budget before ever calling authHandler.
//
// This replaced four separate exact-pattern ServeMux registrations (one per rate-limited route,
// each more specific than the "/api/v1/auth/" mount so ServeMux preferred it regardless of
// registration order) that only ever matched a request path byte-for-byte. Limen's own router
// cleans the path internally before dispatching, so "POST .../signin/credential/" — or any other
// spelling ServeMux's exact patterns don't happen to cover — missed those registrations entirely,
// fell through to this same "/api/v1/auth/" mount, and reached Limen's signin handler completely
// unmetered. Matching on the same canonicalized path Limen itself will end up using, rather than
// on the raw request path, closes that gap for every such spelling at once instead of one exact
// string at a time.
func (s *Server) authRateLimitMiddleware(authHandler http.Handler) http.Handler {
	limited := make(map[string]http.Handler, len(authRateLimitedRoutes))
	for route, name := range authRateLimitedRoutes {
		limited[route] = s.authRateLimit(name)(authHandler)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := limited[routekey.Of(r)]; ok {
			h.ServeHTTP(w, r)
			return
		}
		authHandler.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	authHandler := s.authSvc.Handler()

	// AuthMountGuard sits directly around Limen's own handler, inside the rate limit — see its
	// own doc comment (internal/auth/session.go) for why resolveSession's lock/verification checks
	// alone can't stop a locked or unverified user's fresh sign-in from reaching Limen's own routes
	// (invitations, /me, ...) directly.
	guardedAuthHandler := s.authSvc.AuthMountGuard(authHandler)

	// Captcha sits between the rate limit and the guard: cheapest check first (a counter), then
	// the network round trip to Turnstile, then Limen. Not skipped under EnableTestRoutes — the
	// e2e suite configures Cloudflare's always-pass test keys and exercises the real header.
	captchaAuthHandler := s.authCaptchaMiddleware(guardedAuthHandler)

	// The whole mount goes through one middleware that applies the hot, unauthenticated auth
	// routes' 10/min-per-IP budget (see authRateLimitMiddleware for why this replaced a set of
	// ServeMux exact-pattern registrations) before ever reaching Limen's own handler, which owns
	// everything under its configured base path (WithHTTPBasePath "/api/v1/auth" in
	// internal/auth.buildLimenConfig) unmodified either way. CheckOrigin is applied globally in
	// Handler() below (scoped to /api/ via APIOnly) rather than per-route here, so it also covers
	// whatever else RegisterAPI mounts on this same mux — see Handler()'s doc comment.
	//
	// EnableTestRoutes skips this budget entirely rather than raising it: the same e2e traffic
	// this flag exists for (internal/httpserver's Task 5 seed route, one fresh signup per fixture,
	// one sign-in per spec, all against ONE long-lived server process) is exactly what this 10/min
	// ceiling is sized to catch as abuse, and a deployment that has already accepted
	// EnableTestRoutes's own premise — the seed route resets/creates data on demand, config.Load
	// hard-fails it alongside APP_ENV=production — has no reason to also defend this budget
	// against its own test traffic. Mirrors internal/auth.httpConfigOptions's identical call on
	// Limen's own built-in rate limiter, for the identical reason.
	authRouteHandler := captchaAuthHandler
	if !s.cfg.EnableTestRoutes {
		authRouteHandler = s.authRateLimitMiddleware(captchaAuthHandler)
	}
	s.mux.Handle("/api/v1/auth/", authRouteHandler)

	s.registerAccountRoutes()

	// /api/ misses land here rather than falling through to the SPA fallback: an unmatched API
	// route is a real 404, not a client-side route the SPA should render.
	s.mux.Handle("/api/", http.HandlerFunc(apiNotFound))

	// Everything else — including / — falls to the embedded SPA, which serves the exact file
	// if one exists (e.g. /assets/*.js) or index.html otherwise (client-side routing).
	s.mux.Handle("/", spaHandler())
}

// RegisterAPI lets a later plan mount additional /api/v1/* routes on this Server's own mux —
// e.g. internal/polls's Register(mux, auth, cfg) — so they inherit the same session resolution,
// origin check, panic recovery, and request logging every other /api/ route gets (see Handler's
// doc comment), without this package importing that plan's package directly (New's signature, and
// this package's own dependency graph, stay exactly as they were). Callers register their routes
// after New returns and before ListenAndServe/Handler is first called.
func (s *Server) RegisterAPI(register func(mux *http.ServeMux)) {
	register(s.mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	if err := s.db.PingContext(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded"}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// APIOnly wraps mw so it only runs for requests whose path starts with "/api/" — every other
// path calls next directly, skipping mw (and whatever work it does) entirely. Used to scope
// session resolution to the API surface: static assets and the SPA shell need no identity, and
// everything that does need one (including plan 4's websockets) lives under /api/, so there is no
// reason for the auth database lookup resolveSession does to run on every single asset request a
// browser makes.
func APIOnly(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				wrapped.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Handler returns the middleware-wrapped mux — SecurityHeaders outermost (so headers land on
// every response, including panics), then RequestLogger, then Recover, then authSvc.Middleware
// (scoped to /api/ by APIOnly), then CheckOrigin (also scoped to /api/ by APIOnly) innermost
// around the mux. Recover must sit inside RequestLogger, not outside it: Recover swallows the
// panic and returns normally, so RequestLogger's post-call log line always runs — a panicking
// request still produces both a panic log and a request log, not just the former.
// authSvc.Middleware sits inside Recover so a panic resolving the session (e.g. a database error)
// is caught the same as a panic in any other handler, and outside the mux so every /api/ handler
// — not just those under /api/v1/auth/ — can read the caller's Session via auth.FromContext.
//
// CheckOrigin is applied here, globally over every /api/ request, rather than per mux.Handle
// registration (routes()'s previous approach): a ServeMux dispatches to exactly one registered
// pattern per request, so wrapping a coarser pattern (e.g. "/api/v1/") would never actually run
// for a more specific one (e.g. "POST /api/v1/polls/{id}") that RegisterAPI mounts later — only a
// wrap around the whole mux, applied here, reaches every route regardless of which pattern it
// matched, present or future.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = APIOnly(CheckOrigin(s.cfg.AppURL))(h)
	h = APIOnly(s.authSvc.Middleware)(h)
	h = Recover(s.logger)(h)
	h = RequestLogger(s.logger)(h)
	h = SecurityHeaders(s.policy)(h)
	return h
}

// ListenAndServe runs the HTTP server on cfg.Port until ctx is canceled, then gracefully shuts
// down with a 10s timeout.
func (s *Server) ListenAndServe(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.Port),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: plan 4's websockets are long-lived by design.
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}
