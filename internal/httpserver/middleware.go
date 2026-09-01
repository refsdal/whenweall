package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// writeErrorEnvelope writes the standard JSON error envelope, shared by RateLimit and CheckOrigin
// (mirrors internal/auth's unexported helper of the same shape).
func writeErrorEnvelope(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// statusRecorder captures the status code written by downstream handlers so RequestLogger can
// log it — http.ResponseWriter has no getter for what WriteHeader was called with.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real ResponseWriter, so websocket
// hijacking and flushing still work through the logging middleware.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// RequestLogger logs one structured line per request: method, path, status, duration.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start),
			)
		})
	}
}

// SecurityHeaders sets the fixed set of security headers on every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// Recover catches panics from downstream handlers, logs the stack, and responds with the
// standard JSON error envelope instead of letting net/http close the connection bare.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"error", rec,
						"stack", string(debug.Stack()),
					)
					// If the handler already wrote a header (or body) before panicking, the
					// response is already committed: a second WriteHeader would just log
					// net/http's "superfluous response.WriteHeader call" and the JSON envelope
					// would corrupt whatever was already sent. Only write the envelope when we
					// know for certain nothing has gone out yet.
					if sr, ok := w.(*statusRecorder); ok && sr.wroteHeader {
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal error"}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
