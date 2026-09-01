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

	"github.com/refsdal/whenweall/internal/config"
)

// Server holds the pieces needed to build and run the application's HTTP handler.
type Server struct {
	cfg    *config.Config
	db     *sql.DB
	mux    *http.ServeMux
	logger *slog.Logger
}

// New builds the full mux: health check, API 404 fallback, and the embedded SPA. Later plans
// add route registration params for the real API surface.
func New(cfg *config.Config, sqlDB *sql.DB) *Server {
	s := &Server{
		cfg:    cfg,
		db:     sqlDB,
		mux:    http.NewServeMux(),
		logger: slog.Default(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

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
// every response, including panics), then RequestLogger, then Recover innermost around the mux.
// Recover must sit inside RequestLogger, not outside it: Recover swallows the panic and returns
// normally, so RequestLogger's post-call log line always runs — a panicking request still
// produces both a panic log and a request log, not just the former.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
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
