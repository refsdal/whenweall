package httpserver_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
)

// fullChain assembles the same middleware composition Server.Handler() builds — SecurityHeaders
// outermost, then RequestLogger, then Recover innermost around final — so tests can exercise the
// stack with an arbitrary terminal handler instead of the fixed mux.
func fullChain(logger *slog.Logger, final http.Handler) http.Handler {
	h := httpserver.Recover(logger)(final)
	h = httpserver.RequestLogger(logger)(h)
	h = httpserver.SecurityHeaders(h)
	return h
}

// TestStatusRecorderUnwrapsForFlush proves that http.ResponseController can still reach the real
// ResponseWriter's Flush through RequestLogger's statusRecorder wrapper — needed so plan 4's
// websocket handlers keep working once they sit behind this middleware chain. httptest.ResponseRecorder
// doesn't implement http.Flusher, so this needs a real connection via httptest.NewServer.
func TestStatusRecorderUnwrapsForFlush(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var flushErr error
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flushErr = http.NewResponseController(w).Flush()
	})

	ts := httptest.NewServer(fullChain(logger, final))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if flushErr != nil {
		t.Errorf("Flush() through the middleware chain = %v, want nil", flushErr)
	}
}

// TestRecoverAndRequestLoggerBothLogOnPanic proves the property that matters for middleware
// ordering: a panicking handler still produces both a panic log AND a request log (with the 500
// status Recover set), and the client still gets the JSON error envelope. This requires Recover
// to sit inside RequestLogger — if it sat outside, the panic would unwind past RequestLogger's
// post-call log line and that line would never run.
func TestRecoverAndRequestLoggerBothLogOnPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	h := fullChain(logger, final)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Errorf("body = %q, want the internal error envelope", rec.Body.String())
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("SecurityHeaders header missing on a panicking request: nosniff = %q", got)
	}

	logs := buf.String()
	if !strings.Contains(logs, "panic recovered") {
		t.Errorf("logs missing the panic log line:\n%s", logs)
	}
	if !strings.Contains(logs, "http request") {
		t.Errorf("logs missing the request log line:\n%s", logs)
	}
	if !strings.Contains(logs, "status=500") {
		t.Errorf("request log did not capture the 500 status:\n%s", logs)
	}
}

// TestRequestLoggerLogsMethodPathStatus checks the request log line carries the fields callers
// grep for.
func TestRequestLoggerLogsMethodPathStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := httpserver.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/widgets", nil))

	logs := buf.String()
	for _, want := range []string{"method=GET", "path=/widgets", "status=201"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log line missing %q:\n%s", want, logs)
		}
	}
}
