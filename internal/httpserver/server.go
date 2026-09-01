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

// New builds the full mux. Later plans add route registration params; for now it wires only
// the health check.
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
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	if err := s.db.PingContext(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"degraded"}`))
		return
	}
	w.Write([]byte(`{"status":"ok"}`))
}

// Handler returns the middleware-wrapped mux — Recover outermost (so it can catch panics from
// anything below it, including the logger and header middleware), then SecurityHeaders, then
// RequestLogger, then the mux itself.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = RequestLogger(s.logger)(h)
	h = SecurityHeaders(h)
	h = Recover(s.logger)(h)
	return h
}

// ListenAndServe runs the HTTP server on cfg.Port until ctx is canceled, then gracefully shuts
// down with a 10s timeout.
func (s *Server) ListenAndServe(ctx context.Context) error {
	httpSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.cfg.Port),
		Handler: s.Handler(),
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
