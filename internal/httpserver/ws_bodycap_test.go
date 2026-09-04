package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestWebsocketSurvivesBodyCapAndReadTimeout proves, through the real Server.Handler() chain
// (limitAPIBody, CheckOrigin, authSvc.Middleware, Recover, RequestLogger, SecurityHeaders all
// applied in the same order production uses), that a websocket upgrade under /api/ still works
// and that a server ReadTimeout does not tear down the connection once it is hijacked.
//
// The test server's ReadTimeout is deliberately much shorter (200ms) than production's 30s
// (ListenAndServe) so the test stays fast; the mechanism under test — net/http clearing every
// deadline on the connection before Hijack hands it to coder/websocket, see ListenAndServe's own
// doc comment — does not depend on the timeout's magnitude, only on whether it ever applies
// post-hijack at all. The server-side handler deliberately sleeps well past that window before
// reading the client's reply, so the round trip below only succeeds if the connection truly
// outlived it.
//
// This doubles as the regression test for limitAPIBody: http.MaxBytesHandler only ever wraps
// r.Body, never the ResponseWriter, so the underlying connection's http.Hijacker stays reachable
// through it untouched. A future change that limited request size by wrapping the ResponseWriter
// instead of the Body would break this test the moment that wrapper failed to forward
// http.Hijacker (or an Unwrap() http.ResponseWriter method for http.ResponseController to see
// through) — websocket.Accept's hijack, and this test's 101 upgrade, would fail. Verified by hand:
// temporarily swapping limitAPIBody for a version that wraps w in a bare
// `struct{ http.ResponseWriter }` (no Hijacker, no Unwrap) turns this test's "dial: ... websocket:
// bad handshake" into the observed failure — see the fix report for the exact command and output.
func TestWebsocketSurvivesBodyCapAndReadTimeout(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	srv.RegisterAPI(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/v1/probe-ws", func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("server accept: %v", err)
				return
			}
			defer func() { _ = c.CloseNow() }()
			ctx := context.Background()
			if err := c.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
				t.Errorf("server write: %v", err)
				return
			}
			// Sleep past the test server's ReadTimeout (below) before reading the client's
			// reply: if that timeout somehow still applied to this hijacked connection, either
			// this read or the client's write would fail.
			time.Sleep(500 * time.Millisecond)
			_, data, err := c.Read(ctx)
			if err != nil {
				t.Errorf("server read: %v", err)
				return
			}
			if string(data) != "ping back" {
				t.Errorf("server got %q, want %q", data, "ping back")
			}
		})
	})

	ts := httptest.NewUnstartedServer(srv.Handler())
	// Mirrors ListenAndServe's ReadTimeout field, just far shorter so the test doesn't take 30s —
	// the property under test (a hijacked connection is immune to it) doesn't depend on the value.
	ts.Config.ReadTimeout = 200 * time.Millisecond
	ts.Start()
	defer ts.Close()

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dialCtx, "ws"+ts.URL[len("http"):]+"/api/v1/probe-ws", nil)
	if err != nil {
		t.Fatalf("dial: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.CloseNow() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("client got %q, want %q", data, "hello")
	}

	// The server handler is now asleep well past its own configured ReadTimeout; this write only
	// succeeds if the connection is genuinely still alive — the assertion that matters for this
	// test. (The server handler returns and CloseNow()s right after reading it, so a subsequent
	// clean-close handshake here would race that teardown; CloseNow is enough to end the test.)
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer writeCancel()
	if err := conn.Write(writeCtx, websocket.MessageText, []byte("ping back")); err != nil {
		t.Fatalf("client write: %v", err)
	}
}
