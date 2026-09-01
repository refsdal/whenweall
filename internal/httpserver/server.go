// Package httpserver builds the application's HTTP mux: health checks, the API surface (added by
// later plans), the embedded SPA, and the middleware stack every request passes through.
package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
)

// Server holds the pieces needed to build and run the application's HTTP handler.
type Server struct {
	cfg     *config.Config
	db      *sql.DB
	authSvc *auth.Service
	mux     *http.ServeMux
	logger  *slog.Logger
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
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	// More specific than the "/api/" catch-all below, so ServeMux prefers this regardless of
	// registration order (longest-pattern-wins). authSvc.Handler() already strips nothing itself
	// — Limen's own router owns everything under its configured base path (WithHTTPBasePath
	// "/api/v1/auth" in internal/auth.buildLimenConfig), so this mounts it directly.
	s.mux.Handle("/api/v1/auth/", s.authSvc.Handler())

	// /api/ misses land here rather than falling through to the SPA fallback: an unmatched API
	// route is a real 404, not a client-side route the SPA should render.
	s.mux.HandleFunc("/api/", apiNotFound)

	// Everything else — including / — falls to the embedded SPA, which serves the exact file
	// if one exists (e.g. /assets/*.js) or index.html otherwise (client-side routing).
	s.mux.Handle("/", spaHandler())
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

// Handler returns the middleware-wrapped mux — SecurityHeaders outermost (so headers land on
// every response, including panics), then RequestLogger, then Recover, then authSvc.Middleware
// innermost around the mux. Recover must sit inside RequestLogger, not outside it: Recover
// swallows the panic and returns normally, so RequestLogger's post-call log line always runs — a
// panicking request still produces both a panic log and a request log, not just the former.
// authSvc.Middleware sits inside Recover so a panic resolving the session (e.g. a database error)
// is caught the same as a panic in any other handler, and outside the mux so every handler —
// not just those under /api/v1/auth/ — can read the caller's Session via auth.FromContext.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.authSvc.Middleware(h)
	h = Recover(s.logger)(h)
	h = RequestLogger(s.logger)(h)
	h = SecurityHeaders(h)
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
