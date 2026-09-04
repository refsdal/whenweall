# Completion Plan B — HTTP hardening, infrastructure, tests-of-tests, docs drift

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the HTTP-layer, container, test-harness and documentation gaps the 2026-09-03 parity audit found in the Go rewrite (PR #67): CSP/HSTS/Permissions-Policy, a request-body cap, honest `/healthz` caching headers, poll/booking-page creation rate limits, a REST read for the landing-page stats, a LISTEN liveness check, graceful WebSocket shutdown, the leftover two-factor schema, tests that cannot silently skip, a supported Go line, and docs that describe the code as it is.

**Architecture:** Every change is additive to the existing packages — `internal/httpserver` grows a computed-once `SecurityPolicy` (inline-script hashes read from the embedded `dist/index.html` at boot) and a 1 MiB `/api/` body cap; `internal/rooms` gains a live-connection registry the hub drains on shutdown, a bounded `WaitForNotification` + ping in its LISTEN loop, and `GET /api/v1/stats`; `internal/testdb` becomes fail-fast under CI; migration `00010` drops the two-factor leftovers and `sqlc generate` is re-run and CI-guarded; the toolchain moves to the Go 1.26 line; compose gains `image:`; the spec gets a dated amendment. Nothing here touches auth semantics (Plan A), poll/booking domain logic (Plans C/D), admin/jobs behaviour (Plan E) or the Playwright suite (Plan F) beyond two stale comment strings in `e2e/fixtures.ts`.

**Tech Stack:** Go 1.26 line (local toolchain go1.27.0 at `~/.local/share/mise/installs/go/1.27/bin`, `sqlc` v1.31.1 / `goose` v3.27.3 / `golangci-lint` 2.13.2 in `~/go/bin`), `net/http`, `coder/websocket` v1.8.15, `jackc/pgx/v5` v5.10.0 (`pgconn.Timeout`, `Conn.Ping`), `pressly/goose/v3` (`DownToContext`), testcontainers-go, Vite/React/vitest + msw in `web/`, GitHub Actions, Docker/compose.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§2 API, §4 rooms, §6 migrations, §7 container, §8 README, §9 testing) — Task 14 appends the 2026-09-03 amendment this plan and its siblings implement. Findings and verifier notes: the audit's `findings-full.txt` (titles quoted per task).

## Global Constraints

- Repo `/home/anders/projects/refsdal/whenweall`, branch `feat/go-rewrite`. Every task ends in a commit on this branch; nothing is squashed or force-pushed.
- Put the toolchain on PATH in every shell you open: `export PATH=$HOME/.local/share/mise/installs/go/1.27/bin:$HOME/go/bin:$PATH` (plain `go` is not on the default PATH).
- Fixed decisions from the shared contract (do not re-litigate): email verification gate restored (Plan A); Google Calendar sync disabled for now, Go sync code kept, no custom consent flow; per-user locale restored (Plan A); everything lands as commits on `feat/go-rewrite`; a new CI job runs Playwright against the built image (Plan F). Never reintroduce: passkeys, billing, magic links, TOTP 2FA, staff impersonation, SSR/OG tags, web push, booking-page follower notifications.
- Execution order across plans: A → **B (this plan)** → C → D → E → F. This plan consumes nothing from Plan A. Later plans consume exactly what the **Interfaces Plan B PRODUCES** block below promises:
  - `httpserver.SecurityHeaders(policy SecurityPolicy) func(http.Handler) http.Handler` emits CSP (hashes of inline scripts computed at boot from the embedded `index.html`), `Permissions-Policy`, and HSTS when `APP_URL` is https. `httpserver.BuildSecurityPolicy(appURL string, indexHTML []byte) SecurityPolicy`, `httpserver.InlineScriptHashes(html []byte) []string`, `httpserver.EmbeddedIndexHTML() []byte`.
  - `httpserver.PublicRateLimit(db *sql.DB, cfg *config.Config, namespace, name string, limit int, window time.Duration) func(http.Handler) http.Handler` — returns a pass-through when `cfg.EnableTestRoutes` (like the auth limiter). **Signature change:** the old trailing `trustProxy bool` becomes the `cfg` parameter in position 2; this plan updates every existing call site (polls, bookings, rooms).
  - `http.MaxBytesHandler(…, 1<<20)` wraps `/api/`; `httpserver.DecodeJSON` answers a `*http.MaxBytesError` with 413 `{"error":{"code":"payload_too_large"}}`.
  - `GET /api/v1/stats` (package `rooms`, mounted by `rooms.Register`) returns `rooms.UsageStats` JSON with `Cache-Control: no-store`.
  - `compose.yaml` `app` has `image: ghcr.io/refsdal/whenweall:latest` plus `build:`.
  - Go line 1.26 everywhere (go.mod `go 1.26`, `golang:1.26-alpine`, CI `setup-go` `1.26`, README).
  - `internal/testdb` fails (`t.Fatalf`) instead of skipping when the `CI` env var is set; `testdb.Unavailable(t testing.TB, what string, err error)` is the shared helper the three Mailpit tests call.
  - `db.MigrateDownTo(ctx, sqlDB, version int64) error` (tests/ops only).
  - Migration `00010_drop_two_factor.sql` — the only migration this plan adds (Plan A owns `00009`, Plan C owns `00011`). After it: `sqlc generate` and commit the regenerated `internal/*/queries/*.go`.
- Conventions: TDD (failing test → run → implement → run → commit). Go tests use `internal/testdb` (live Postgres via testcontainers; Docker must be running). Handler tests use each package's existing httptest helpers. Web: vitest + Testing Library + msw. Error envelope `{"error":{"code","message"}}`, snake_case codes.
- Commit messages are conventional (`fix(httpserver): …`) and **every** commit ends with these two trailer lines, exactly:
  ```
  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
  ```
- Gates before this plan is declared done: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`; `bunx playwright test`.
- Do not modify `web/messages/*.json` — this plan adds no user-facing strings.

---

### Task 1: Move the toolchain to the Go 1.26 line

Finding: "All Go-version claims agree on the 1.25 line, but that line is past Go's support window and the patch-level go.mod directive makes the Docker build non-hermetic". `golang:1.26-alpine` exists on Docker Hub (verified with `docker manifest inspect`). Do this first so every later task builds and tests on the line CI will use.

**Files:**
- Modify: `go.mod:3`
- Modify: `Dockerfile:14`
- Modify: `.github/workflows/ci.yml:18,71`
- Modify: `README.md:14,403`

**Interfaces:**
- Consumes: nothing.
- Produces: `go 1.26` module directive every later task compiles against.

- [ ] **Step 1: Set the module's Go line (no patch component)**

```bash
go mod edit -go=1.26
go mod tidy
git diff --stat go.mod go.sum
```

Expected: `go.mod` line 3 is now exactly `go 1.26`; `go mod tidy` changes nothing else (no `toolchain` directive appears — the local go1.27.0 satisfies `go 1.26` as-is). If `go.sum` shows a diff, keep it (tidy is authoritative).

- [ ] **Step 2: Dockerfile builder image**

Replace line 14 of `Dockerfile`:

```dockerfile
FROM golang:1.26-alpine AS build
```

- [ ] **Step 3: CI setup-go in both Go jobs**

In `.github/workflows/ci.yml`, change both occurrences (the `go` job at line 18 and the `e2e` job at line 71) of

```yaml
          go-version: '1.25'
```

to

```yaml
          go-version: '1.26'
```

- [ ] **Step 4: README badge and prerequisite**

`README.md` line 14:

```markdown
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
```

`README.md` line 403 (Development section):

```markdown
You need [Docker](https://www.docker.com/), [Go](https://go.dev/) 1.26+, and
```

- [ ] **Step 5: Verify nothing still says 1.25 and the tree builds**

```bash
grep -rn "1\.25" go.mod Dockerfile .github/workflows/ci.yml README.md CONTRIBUTING.md
go build ./... && go vet ./...
```

Expected: the grep prints nothing; build and vet exit 0. (The spec's `golang:1.25-alpine` mention is superseded by Task 14's amendment, not edited in place.)

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum Dockerfile .github/workflows/ci.yml README.md
git commit -m "chore(go): move every Go-version pin to the 1.26 line

go.mod carried a patch-level directive (1.25.7) that made any older
golang:1.25-alpine layer or setup-go cache download a toolchain inside
the build, and the 1.25 line left Go's two-release support window when
1.27 shipped. Dockerfile, both CI jobs and the README badge/prerequisite
now agree on 1.26; go.mod says 'go 1.26' with no patch so the pinned
builder image is what actually compiles the binary.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 2: DB/Mailpit test helpers fail hard under CI instead of skipping

Finding: "Every DB-backed test silently t.Skip()s when the Postgres or Mailpit testcontainer fails to start, so CI can go green having run almost nothing". Nine of twelve tested packages get their database from `testdb.URL`; three tests start a Mailpit container and skip the same way.

**Files:**
- Modify: `internal/testdb/testdb.go:79-103`
- Create: `internal/testdb/testdb_test.go`
- Modify: `internal/bookings/emails_test.go:379`, `internal/polls/notifications_test.go:854`, `internal/mailer/smtp_test.go:67`

**Interfaces:**
- Produces: `func Unavailable(t testing.TB, what string, err error)` in package `testdb` — Skip locally, Fatal when `os.Getenv("CI") != ""`.

- [ ] **Step 1: Write the failing test**

Create `internal/testdb/testdb_test.go` (package `testdb`, so it can reach the unexported decision function):

```go
package testdb

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recorder is a stand-in for *testing.T that records which of Skipf/Fatalf unavailable() picked,
// instead of actually skipping or failing this test.
type recorder struct {
	skipped, fatal string
}

func (r *recorder) Skipf(format string, args ...any)  { r.skipped = fmt.Sprintf(format, args...) }
func (r *recorder) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }

func TestUnavailableSkipsLocallyButFailsUnderCI(t *testing.T) {
	cause := errors.New("docker daemon not reachable")

	local := &recorder{}
	unavailable(local, false, "postgres testcontainer", cause)
	if local.fatal != "" {
		t.Fatalf("outside CI unavailable() must skip, not fail: %q", local.fatal)
	}
	if !strings.Contains(local.skipped, "docker daemon not reachable") {
		t.Fatalf("skip message = %q, want it to carry the cause", local.skipped)
	}

	ci := &recorder{}
	unavailable(ci, true, "postgres testcontainer", cause)
	if ci.skipped != "" {
		t.Fatalf("under CI unavailable() must not skip: %q", ci.skipped)
	}
	if !strings.Contains(ci.fatal, "CI") || !strings.Contains(ci.fatal, "docker daemon not reachable") {
		t.Fatalf("fatal message = %q, want it to mention CI and the cause", ci.fatal)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/testdb/ -run TestUnavailableSkipsLocallyButFailsUnderCI`
Expected: FAIL to compile — `undefined: unavailable`.

- [ ] **Step 3: Implement the helper and use it in `URL`**

In `internal/testdb/testdb.go`, add `"os"` to the imports and replace the `URL` function's skip with the helper, adding the two new functions after `urlFor`:

```go
// skipperFailer is the slice of testing.TB unavailable needs — narrow so a test can hand it a
// recorder instead of a real *testing.T.
type skipperFailer interface {
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Unavailable reports that a piece of test infrastructure (a container that would not start)
// is missing. Locally that is a Skip — a laptop without Docker should still run the pure-Go
// tests — but under CI (the CI env var GitHub Actions and every common runner set) it is a hard
// failure: nine of the twelve tested packages get their database from this package, so a
// silently skipped container would let `go test ./...` go green having run almost nothing.
func Unavailable(t testing.TB, what string, err error) {
	t.Helper()
	unavailable(t, os.Getenv("CI") != "", what, err)
}

func unavailable(t skipperFailer, ci bool, what string, err error) {
	if ci {
		t.Fatalf("%s unavailable under CI — refusing to skip: %v", what, err)
		return
	}
	t.Skipf("%s unavailable: %v", what, err)
}
```

and in `URL`, replace

```go
	if initErr != nil {
		t.Skipf("postgres testcontainer unavailable: %v", initErr)
	}
```

with

```go
	if initErr != nil {
		Unavailable(t, "postgres testcontainer", initErr)
	}
```

Also update `New`'s doc comment line `// Skips the test (t.Skip) if Docker is unavailable. Closes and drops on t.Cleanup.` to:

```go
// Skips the test if Docker is unavailable — or fails it, under CI (see Unavailable). Closes and
// drops on t.Cleanup.
```

- [ ] **Step 4: Point the three Mailpit tests at the helper**

In each of `internal/bookings/emails_test.go` (line 379), `internal/polls/notifications_test.go` (line 854) and `internal/mailer/smtp_test.go` (line 67), replace

```go
		t.Skipf("mailpit testcontainer unavailable: %v", err)
```

with

```go
		testdb.Unavailable(t, "mailpit testcontainer", err)
```

`internal/polls/notifications_test.go` already imports `github.com/refsdal/whenweall/internal/testdb`; add that import to `internal/bookings/emails_test.go` and `internal/mailer/smtp_test.go` (both are package `*_test` files — `goimports`/`gofmt` ordering, grouped with the other `internal/...` imports).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/testdb/ && go vet ./internal/bookings/ ./internal/polls/ ./internal/mailer/`
Expected: `ok  github.com/refsdal/whenweall/internal/testdb`; vet exits 0. Then `CI=1 go test ./internal/db/` — with Docker running this passes exactly as before (the helper only changes the no-container path).

- [ ] **Step 6: Commit**

```bash
git add internal/testdb internal/bookings/emails_test.go internal/polls/notifications_test.go internal/mailer/smtp_test.go
git commit -m "test(testdb): fail instead of skip when a container is unavailable under CI

testdb.URL and the three Mailpit-backed tests t.Skipf'd when their
testcontainer could not start, so a Docker hiccup on a runner turned
~90% of the suite into skips while go test still exited 0. Under CI
(CI env var set) that is now a t.Fatalf via the shared
testdb.Unavailable helper; locally it still skips.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 3: Content-Security-Policy, Permissions-Policy and HSTS

Findings: "Still open: no Content-Security-Policy, Permissions-Policy or HSTS; two hashable inline scripts in index.html", "No Content-Security-Policy emitted", "HSTS and Permissions-Policy dropped". The old `src/start.ts` sent a CSP with `script-src 'unsafe-inline'`; we do better: hash the (exactly two) src-less `<script>` bodies in the embedded `dist/index.html` at boot — no nonce plumbing, no per-request rewriting. Turnstile needs `https://challenges.cloudflare.com` in `script-src`, `frame-src` and `connect-src`. React inline `style={{}}` and `motion` need `style-src 'unsafe-inline'`.

**Files:**
- Create: `internal/httpserver/csp.go`
- Create: `internal/httpserver/csp_test.go`
- Modify: `internal/httpserver/middleware.go:300-308` (`SecurityHeaders`)
- Modify: `internal/httpserver/server.go:20-42,193-201` (`Server` struct, `New`, `Handler`)
- Modify: `internal/httpserver/middleware_test.go:18-23` (`fullChain`)

**Interfaces:**
- Produces: `type SecurityPolicy struct { CSP string; HSTS bool }`; `func BuildSecurityPolicy(appURL string, indexHTML []byte) SecurityPolicy`; `func InlineScriptHashes(html []byte) []string` (each entry `'sha256-<base64>'`); `func EmbeddedIndexHTML() []byte`; `func SecurityHeaders(policy SecurityPolicy) func(http.Handler) http.Handler` (**signature change** from `SecurityHeaders(next http.Handler) http.Handler`).

- [ ] **Step 1: Write the failing tests**

Create `internal/httpserver/csp_test.go`:

```go
package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestInlineScriptHashes_OnlySrclessScripts pins the extraction rule: every <script> WITHOUT a
// src attribute is hashed over its exact text content (whitespace included — that is what the
// browser hashes too), case-insensitively on the tag, and scripts with src= are skipped. The
// expected values were computed independently with
// `printf '<body>' | openssl dgst -sha256 -binary | base64`.
func TestInlineScriptHashes_OnlySrclessScripts(t *testing.T) {
	html := []byte("<html><head>\n" +
		"<script>console.log(1)</script>\n" +
		"<script type=\"module\" src=\"/assets/index-abc.js\"></script>\n" +
		"<SCRIPT type=\"text/javascript\">\n  alert(2)\n</SCRIPT>\n" +
		"</head><body></body></html>")

	got := httpserver.InlineScriptHashes(html)
	want := []string{
		"'sha256-CihokcEcBW4atb/CW/XWsvWwbTjqwQlE9nj9ii5ww5M='", // console.log(1)
		"'sha256-wgjd6XWUJfa5+rUf4ibLSBtQ2O4Mj1Bb0Fp7DU7zxRk='", // "\n  alert(2)\n"
	}
	if len(got) != len(want) {
		t.Fatalf("InlineScriptHashes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hash[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

// TestWebIndexInlineScriptsAreAllHashed is the guard the audit asked for: if web/index.html grows
// an inline <script> the extraction regex does not catch, this fails — the browser would block
// that script under the served CSP. The independent count below deliberately does NOT reuse the
// production regex.
func TestWebIndexInlineScriptsAreAllHashed(t *testing.T) {
	html, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatalf("reading web/index.html: %v", err)
	}

	inline := 0
	for _, after := range strings.Split(strings.ToLower(string(html)), "<script")[1:] {
		openTag := after[:strings.Index(after, ">")]
		if !strings.Contains(openTag, "src=") {
			inline++
		}
	}
	if inline < 2 {
		t.Fatalf("counted %d inline scripts in web/index.html, expected at least the theme and locale bootstraps", inline)
	}

	hashes := httpserver.InlineScriptHashes(html)
	if len(hashes) != inline {
		t.Fatalf("InlineScriptHashes found %d scripts, the independent count found %d — an inline script is not being hashed", len(hashes), inline)
	}

	policy := httpserver.BuildSecurityPolicy("https://whenweall.example", html)
	scriptSrc := directive(t, policy.CSP, "script-src")
	for _, h := range hashes {
		if !strings.Contains(scriptSrc, h) {
			t.Errorf("script-src %q is missing %s", scriptSrc, h)
		}
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Errorf("script-src must rely on hashes, not 'unsafe-inline': %q", scriptSrc)
	}
	if !strings.Contains(scriptSrc, "https://challenges.cloudflare.com") {
		t.Errorf("script-src must allow Turnstile: %q", scriptSrc)
	}
}

func TestBuildSecurityPolicy_ConnectFrameAndHSTS(t *testing.T) {
	https := httpserver.BuildSecurityPolicy("https://whenweall.example", nil)
	if !https.HSTS {
		t.Error("HSTS should be on for an https APP_URL")
	}
	if got := directive(t, https.CSP, "connect-src"); got != "connect-src 'self' https://challenges.cloudflare.com wss://whenweall.example" {
		t.Errorf("connect-src = %q", got)
	}
	if got := directive(t, https.CSP, "frame-src"); got != "frame-src https://challenges.cloudflare.com" {
		t.Errorf("frame-src = %q", got)
	}
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'self'", "form-action 'self'", "object-src 'none'", "style-src 'self' 'unsafe-inline'"} {
		if !strings.Contains(https.CSP, want) {
			t.Errorf("CSP missing %q: %s", want, https.CSP)
		}
	}

	plain := httpserver.BuildSecurityPolicy("http://localhost:3000", nil)
	if plain.HSTS {
		t.Error("HSTS must be off for an http APP_URL — a browser would otherwise refuse the plain-http dev origin for a year")
	}
	if got := directive(t, plain.CSP, "connect-src"); got != "connect-src 'self' https://challenges.cloudflare.com ws://localhost:3000" {
		t.Errorf("connect-src = %q", got)
	}
}

// TestSecurityHeadersOnEveryResponse goes through the real Server.Handler(): the SPA shell, the
// health check and an API 404 all carry the full header set, and the served CSP covers whatever
// inline scripts the embedded dist/index.html actually has (the committed placeholder has none;
// a binary built with the real SPA has two — same test, real coverage in e2e).
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig() // APP_URL http://localhost:3000
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	h := srv.Handler()

	embeddedHashes := httpserver.InlineScriptHashes(httpserver.EmbeddedIndexHTML())

	for _, path := range []string{"/", "/healthz", "/api/v1/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s: no Content-Security-Policy header", path)
		}
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP missing %q: %s", path, want, csp)
			}
		}
		for _, hash := range embeddedHashes {
			if !strings.Contains(csp, hash) {
				t.Errorf("%s: served CSP does not cover embedded inline script %s", path, hash)
			}
		}
		if got := rec.Header().Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
			t.Errorf("%s: Permissions-Policy = %q", path, got)
		}
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("%s: HSTS %q emitted for an http APP_URL", path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: nosniff = %q", path, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
	}
}

func TestSecurityHeadersHSTSWhenPolicySaysSo(t *testing.T) {
	h := httpserver.SecurityHeaders(httpserver.SecurityPolicy{CSP: "default-src 'self'", HSTS: true})(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("HSTS = %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// directive returns the one CSP directive named name (e.g. "script-src 'self' ...") from csp.
func directive(t *testing.T, csp, name string) string {
	t.Helper()
	for _, d := range strings.Split(csp, "; ") {
		if strings.HasPrefix(d, name+" ") {
			return d
		}
	}
	t.Fatalf("CSP %q has no %s directive", csp, name)
	return ""
}
```

(`okHandler`, `testConfig` and `testAuthService` already exist in this test package — `ratelimit_test.go` and `server_test.go`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/httpserver/ -run 'InlineScriptHashes|WebIndexInline|BuildSecurityPolicy|SecurityHeaders'`
Expected: FAIL to compile — `undefined: httpserver.InlineScriptHashes`, `httpserver.BuildSecurityPolicy`, `httpserver.SecurityPolicy`, `httpserver.EmbeddedIndexHTML`.

- [ ] **Step 3: Create `internal/httpserver/csp.go`**

```go
package httpserver

// Content-Security-Policy for the embedded SPA, computed ONCE per process (New) from the exact
// index.html bytes spaHandler serves. The old TanStack Start server (main:src/start.ts) shipped a
// CSP with script-src 'unsafe-inline'; this one hashes the inline scripts instead. There are
// exactly two (web/index.html: the theme bootstrap and the <html lang> cookie bootstrap), both
// copied verbatim by `vite build`, so a sha256 per script is cheaper and stricter than a nonce:
// no per-request rewrite of index.html, and an XSS payload injected as an inline <script> is
// blocked because its hash is not on the list. csp_test.go's TestWebIndexInlineScriptsAreAllHashed
// fails the build the day web/index.html grows a script this file's extraction does not see.

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
)

// turnstileOrigin is Cloudflare Turnstile's one origin: the widget script
// (challenges.cloudflare.com/turnstile/v0/api.js), its iframe, and its own fetch/XHR traffic all
// come from here (web/src/components/auth/TurnstileField.tsx via @marsidev/react-turnstile), so it
// is allowed in script-src, frame-src and connect-src. Google OAuth is a top-level redirect and
// needs nothing; Google Calendar calls are server-side only.
const turnstileOrigin = "https://challenges.cloudflare.com"

// SecurityPolicy is SecurityHeaders' computed-once input: the full Content-Security-Policy header
// value and whether Strict-Transport-Security applies (only when APP_URL is https — HSTS on a
// plain-http dev origin would make the browser refuse http://localhost for a year).
type SecurityPolicy struct {
	CSP  string
	HSTS bool
}

var (
	// inlineScriptRE matches every <script ...>...</script> element (case-insensitive, dot
	// matches newlines) and captures the attribute text and the body separately.
	inlineScriptRE = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	// srcAttrRE recognizes an external script by its src attribute — those are covered by
	// script-src 'self', not by a hash.
	srcAttrRE = regexp.MustCompile(`(?i)\bsrc\s*=`)
)

// InlineScriptHashes returns one CSP source expression — 'sha256-<base64>' — per src-less <script>
// element in html, in document order. The hash covers the element's exact text content, leading
// and trailing whitespace included, because that is precisely what a browser hashes when it
// evaluates the policy.
func InlineScriptHashes(html []byte) []string {
	matches := inlineScriptRE.FindAllSubmatch(html, -1)
	hashes := make([]string, 0, len(matches))
	for _, m := range matches {
		if srcAttrRE.Match(m[1]) {
			continue
		}
		sum := sha256.Sum256(m[2])
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return hashes
}

// EmbeddedIndexHTML returns the SPA shell exactly as embedded into this binary (dist/index.html) —
// the same bytes spaHandler serves, so hashes computed from it match what the browser receives.
// nil if the embed is somehow missing (spaHandler panics on that case anyway).
func EmbeddedIndexHTML() []byte {
	b, err := distFS.ReadFile("dist/index.html")
	if err != nil {
		return nil
	}
	return b
}

// BuildSecurityPolicy derives the policy for appURL and the given index.html bytes:
//
//   - script-src: 'self', one sha256 per inline script, and Turnstile's origin.
//   - style-src 'unsafe-inline': React style={{}} props and motion/react's animated inline styles
//     need it; scripts do not get the same latitude.
//   - connect-src: 'self' (which covers same-origin ws(s):// in every current browser), Turnstile,
//     plus the app's own ws:// or wss:// origin spelled out for older WebKit builds that did not
//     treat 'self' as covering the WebSocket scheme.
//   - frame-ancestors 'none' (the header-level form of the X-Frame-Options: DENY we already send),
//     base-uri/form-action 'self', object-src 'none'.
func BuildSecurityPolicy(appURL string, indexHTML []byte) SecurityPolicy {
	scriptSrc := append([]string{"'self'"}, InlineScriptHashes(indexHTML)...)
	scriptSrc = append(scriptSrc, turnstileOrigin)

	connectSrc := []string{"'self'", turnstileOrigin}
	hsts := false
	if u, err := url.Parse(appURL); err == nil && u.Host != "" {
		wsScheme := "ws"
		if u.Scheme == "https" {
			wsScheme = "wss"
			hsts = true
		}
		connectSrc = append(connectSrc, wsScheme+"://"+u.Host)
	}

	directives := []string{
		"default-src 'self'",
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connectSrc, " "),
		"frame-src " + turnstileOrigin,
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}
	return SecurityPolicy{CSP: strings.Join(directives, "; "), HSTS: hsts}
}
```

- [ ] **Step 4: Rewrite `SecurityHeaders` in `internal/httpserver/middleware.go`**

Replace the existing function (lines 300–308) with:

```go
// SecurityHeaders sets the fixed security headers on every response — the three the first cut
// shipped (nosniff, Referrer-Policy, X-Frame-Options) plus Permissions-Policy, the computed
// Content-Security-Policy (see csp.go) and, for an https APP_URL, Strict-Transport-Security.
// policy is built once by New; this middleware only ever copies strings.
func SecurityHeaders(policy SecurityPolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if policy.CSP != "" {
				h.Set("Content-Security-Policy", policy.CSP)
			}
			if policy.HSTS {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 5: Compute the policy in `New` and use it in `Handler`**

In `internal/httpserver/server.go`, add a field to `Server`:

```go
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
```

in `New`, set it:

```go
	s := &Server{
		cfg:     cfg,
		db:      sqlDB,
		authSvc: authSvc,
		mux:     http.NewServeMux(),
		logger:  slog.Default(),
		policy:  BuildSecurityPolicy(cfg.AppURL, EmbeddedIndexHTML()),
	}
```

and in `Handler`, change the last wrap from `h = SecurityHeaders(h)` to:

```go
	h = SecurityHeaders(s.policy)(h)
```

- [ ] **Step 6: Fix the middleware test's chain builder**

In `internal/httpserver/middleware_test.go`, `fullChain` becomes:

```go
func fullChain(logger *slog.Logger, final http.Handler) http.Handler {
	h := httpserver.Recover(logger)(final)
	h = httpserver.RequestLogger(logger)(h)
	h = httpserver.SecurityHeaders(httpserver.SecurityPolicy{})(h)
	return h
}
```

- [ ] **Step 7: Run the package tests**

Run: `go build ./... && go test ./internal/httpserver/`
Expected: `ok  github.com/refsdal/whenweall/internal/httpserver` (the existing `TestHealthzOK`/panic tests still pass; the five new tests pass).

- [ ] **Step 8: Commit**

```bash
git add internal/httpserver/csp.go internal/httpserver/csp_test.go internal/httpserver/middleware.go internal/httpserver/middleware_test.go internal/httpserver/server.go
git commit -m "feat(httpserver): CSP with boot-time inline-script hashes, Permissions-Policy, HSTS

The first cut's SecurityHeaders sent only nosniff/Referrer-Policy/
X-Frame-Options; the old start.ts CSP, Permissions-Policy and HSTS were
dropped. Every response now carries a Content-Security-Policy whose
script-src allows 'self', Turnstile's origin and one sha256 per src-less
<script> in the embedded dist/index.html (computed once in New — no
'unsafe-inline' for scripts, no nonce plumbing), Permissions-Policy
(camera/microphone/geolocation off), and Strict-Transport-Security when
APP_URL is https. A test reads web/index.html and fails if an inline
script there is not hashed.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 4: 1 MiB request-body cap on `/api/` (413) and a server `ReadTimeout`

Finding: "No request body size limit and no ReadTimeout on the server". `http.MaxBytesHandler` wraps every `/api/` request's body; `DecodeJSON` turns the resulting `*http.MaxBytesError` into a 413 envelope instead of a misleading 400 "malformed JSON". `ReadTimeout` is safe for WebSockets: `net/http`'s `conn.hijackLocked` calls `rwc.SetDeadline(time.Time{})` before handing the connection to `coder/websocket`, so no server-side deadline survives the upgrade.

**Files:**
- Modify: `internal/httpserver/server.go:193-212` (`Handler`, `ListenAndServe`)
- Modify: `internal/httpserver/domainauth.go:676-689` (`DecodeJSON`)
- Create: `internal/httpserver/bodylimit_test.go`

**Interfaces:**
- Produces: 413 `{"error":{"code":"payload_too_large","message":"request body exceeds 1 MiB"}}` from `DecodeJSON` for any `/api/` body over `1<<20` bytes.

- [ ] **Step 1: Write the failing test**

Create `internal/httpserver/bodylimit_test.go`:

```go
package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestAPIBodyLimitedToOneMiB drives a probe route through the real Server.Handler() chain: a
// small JSON body decodes normally, a body one byte over 1 MiB is answered 413 with the
// payload_too_large envelope (not a 400 "malformed JSON", which is what a bare MaxBytesReader
// error looks like to json.Decoder).
func TestAPIBodyLimitedToOneMiB(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	srv.RegisterAPI(func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/v1/probe-body", func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if !httpserver.DecodeJSON(w, r, &body) {
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	h := srv.Handler()

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/probe-body", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := post(`{"ok":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("small body: status = %d, want 204; body=%s", rec.Code, rec.Body)
	}

	huge := `{"pad":"` + strings.Repeat("a", 1<<20) + `"}` // > 1 MiB once the wrapper is counted
	rec := post(huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status = %d, want 413; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"code":"payload_too_large"`) {
		t.Errorf("oversized body envelope = %s, want code payload_too_large", rec.Body)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/httpserver/ -run TestAPIBodyLimitedToOneMiB`
Expected: FAIL — `oversized body: status = 400, want 413; body={"error":{"code":"invalid","message":"malformed JSON body"}}`.

- [ ] **Step 3: Wrap `/api/` in `MaxBytesHandler` and set `ReadTimeout`**

In `internal/httpserver/server.go`, add near the top of the file (after the imports):

```go
// maxAPIBodyBytes caps every /api/ request body. Nothing this API accepts is anywhere near it —
// the largest legitimate body is a poll with a few dozen options, a few KiB — while an unbounded
// r.Body lets one client push a multi-GB JSON document at json.Decoder and exhaust memory. The
// old Workers runtime imposed a platform-wide cap for free; a self-hosted binary has to say so
// itself. Enforced by http.MaxBytesHandler (see Handler) and reported as 413 by DecodeJSON.
const maxAPIBodyBytes = 1 << 20

// limitAPIBody is the middleware form of http.MaxBytesHandler for APIOnly.
func limitAPIBody(next http.Handler) http.Handler {
	return http.MaxBytesHandler(next, maxAPIBodyBytes)
}
```

In `Handler`, add the body cap innermost (it only rewrites `r.Body`, so it can sit anywhere, but innermost keeps CheckOrigin's own reads untouched):

```go
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = APIOnly(limitAPIBody)(h)
	h = APIOnly(CheckOrigin(s.cfg.AppURL))(h)
	h = APIOnly(s.authSvc.Middleware)(h)
	h = Recover(s.logger)(h)
	h = RequestLogger(s.logger)(h)
	h = SecurityHeaders(s.policy)(h)
	return h
}
```

In `ListenAndServe`, the `http.Server` literal becomes:

```go
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.Port),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout bounds the whole request read (headers + body): a client trickling a body
		// one byte at a time cannot hold a connection open indefinitely. Safe alongside the
		// websocket routes: net/http's Hijack (conn.hijackLocked) clears every deadline on the
		// connection before coder/websocket takes it over, so this never fires on an upgraded
		// connection.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
		// No WriteTimeout: websockets are long-lived by design.
	}
```

- [ ] **Step 4: Teach `DecodeJSON` about `*http.MaxBytesError`**

In `internal/httpserver/domainauth.go`, add `"errors"` to the imports and replace `DecodeJSON` with:

```go
// DecodeJSON decodes r's JSON body into dst, writing the standard "invalid" envelope and
// returning false on any decode failure (including a missing body). A body over the /api/ cap
// (Server.Handler's http.MaxBytesHandler, maxAPIBodyBytes) surfaces here as *http.MaxBytesError
// and is reported as 413 payload_too_large rather than a misleading "malformed JSON".
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		Err(w, http.StatusBadRequest, "invalid", "request body is required", nil)
		return false
	}
	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			Err(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 1 MiB", nil)
			return false
		}
		Err(w, http.StatusBadRequest, "invalid", "malformed JSON body", nil)
		return false
	}
	return true
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/httpserver/`
Expected: `ok` — including `TestAPIBodyLimitedToOneMiB`.

- [ ] **Step 6: Commit**

```bash
git add internal/httpserver/server.go internal/httpserver/domainauth.go internal/httpserver/bodylimit_test.go
git commit -m "fix(httpserver): cap /api/ bodies at 1 MiB (413) and set a server ReadTimeout

Every JSON handler decoded r.Body unbounded and http.Server had no
ReadTimeout, so one client could push a multi-GB body or trickle one
forever. /api/ is now wrapped in http.MaxBytesHandler(1 MiB) and
DecodeJSON reports the resulting *http.MaxBytesError as 413
payload_too_large; ReadTimeout is 30s (net/http clears deadlines on
Hijack, so websocket upgrades are unaffected).

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 5: `/healthz` is never cacheable or indexable

Finding: "/healthz lost Cache-Control: no-store (and moved from /api/health with a different body)". The old `/api/health` sent `cache-control: no-store` and `x-robots-tag: noindex` so an uptime monitor behind a caching proxy could never be served a stale 200.

**Files:**
- Modify: `internal/httpserver/server.go` (`handleHealthz`)
- Modify: `internal/httpserver/server_test.go:147-159` (`TestHealthzOK`)

**Interfaces:**
- Produces: `GET /healthz` responds with `Cache-Control: no-store` and `X-Robots-Tag: noindex` on both the 200 and the 503 path.

- [ ] **Step 1: Extend the failing test**

Replace `TestHealthzOK` in `internal/httpserver/server_test.go` with:

```go
func TestHealthzOK(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff header = %q", got)
	}
	// An uptime monitor behind a caching proxy must never be handed a stale 200 during a DB
	// outage, and a search engine has no business indexing this — the old /api/health set both.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
}
```

And extend `TestHealthzDegradedWhenDBDown` (same file) with the same header check after the status assertion:

```go
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control on the degraded path = %q, want no-store", got)
	}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/httpserver/ -run 'TestHealthz'`
Expected: FAIL — `Cache-Control = "", want no-store` and `X-Robots-Tag = "", want noindex`.

- [ ] **Step 3: Set the headers before either branch writes**

Replace `handleHealthz` in `internal/httpserver/server.go`:

```go
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	// no-store: a caching proxy/CDN in front of the app must never answer a monitor's probe with
	// a remembered 200 while the database is actually down. noindex: this URL is operational, not
	// content (the old /api/health set both — main:src/routes/api/health.ts).
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if err := s.db.PingContext(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded"}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/httpserver/ -run 'TestHealthz'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver/server.go internal/httpserver/server_test.go
git commit -m "fix(httpserver): /healthz sends Cache-Control: no-store and X-Robots-Tag: noindex

The old /api/health carried both so a caching proxy could never serve a
stale 200 during a DB outage; the Go /healthz sent only Content-Type.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 6: `PublicRateLimit` is config-aware (off under `ENABLE_TEST_ROUTES`) and poll/booking-page creation is rate limited

Findings: "Poll creation is no longer rate limited" / "Poll creation has no rate limit" (old: `rateLimitMiddleware('create')`, 20/min per IP, shared by create and duplicate), "Public rate limiter is shared by every Playwright context and CI retries, making booking.spec fragile under retry" (contract: skip `PublicRateLimit` when `cfg.EnableTestRoutes`, exactly like the auth limiter already does).

**Files:**
- Modify: `internal/httpserver/domainauth.go:714-721` (`PublicRateLimit`)
- Modify: `internal/httpserver/ratelimit_test.go` (two new tests; add `config` import)
- Modify: `internal/polls/handlers.go:64-69,72` (`Register`)
- Modify: `internal/polls/handlers_test.go` (new test)
- Modify: `internal/bookings/handlers.go:79,81` (`Register`)
- Modify: `internal/bookings/handlers_test.go` (new test)
- Modify: `internal/rooms/endpoints.go:117`
- Modify: `internal/rooms/PROTOCOL.md:45-49`

**Interfaces:**
- Produces: `func PublicRateLimit(db *sql.DB, cfg *config.Config, namespace, name string, limit int, window time.Duration) func(http.Handler) http.Handler`. Buckets added: `polls.create` (POST /api/v1/polls and POST /api/v1/polls/{id}/duplicate, 20/min per IP) and `bookings.create` (POST /api/v1/booking-pages, 20/min per IP).

- [ ] **Step 1: Write the failing httpserver tests**

Append to `internal/httpserver/ratelimit_test.go` (add `"github.com/refsdal/whenweall/internal/config"` to its imports):

```go
// TestPublicRateLimitEnforcedByDefault pins the ordinary case: with a plain config the per-IP
// budget applies and the request over it gets the standard 429 envelope.
func TestPublicRateLimitEnforcedByDefault(t *testing.T) {
	d := testdb.New(t)
	h := httpserver.PublicRateLimit(d, testConfig(), "test", "on", 1, time.Minute)(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("1st request: status = %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("2nd request: status = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
		t.Errorf("body = %q, want code rate_limited", rec.Body.String())
	}
}

// TestPublicRateLimitSkippedWhenTestRoutesEnabled mirrors what Server.routes already does for
// the auth limiter: a deployment running with ENABLE_TEST_ROUTES (the Playwright harness — one
// long-lived server, every browser context sharing one client IP, CI retries on top) has no
// reason to defend a per-IP budget against its own test traffic.
func TestPublicRateLimitSkippedWhenTestRoutesEnabled(t *testing.T) {
	d := testdb.New(t)
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "http://localhost:3000", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
		"ENABLE_TEST_ROUTES": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := httpserver.PublicRateLimit(d, cfg, "test", "off", 1, time.Minute)(okHandler())

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (limiter must be a pass-through under ENABLE_TEST_ROUTES)", i+1, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/httpserver/ -run 'TestPublicRateLimit'`
Expected: FAIL to compile — `cannot use testConfig() (value of type *config.Config) as string value` (the old signature's `namespace` position).

- [ ] **Step 3: Change `PublicRateLimit`**

Replace the function at the end of `internal/httpserver/domainauth.go`:

```go
// PublicRateLimit builds a per-IP rate limiter over db — the same fixed-window counter RateLimit
// uses for the auth surface, namespaced "<namespace>.<name>" (e.g. "polls.vote") so different
// domains' — and different routes within the same domain's — rate limits never share a bucket.
// Client identity comes from ClientIP with cfg.TrustProxy.
//
// A pass-through when cfg.EnableTestRoutes, for exactly the reason Server.routes skips the auth
// limiter then: the Playwright harness runs every browser context (and every CI retry) against
// ONE server process from ONE client IP, which is precisely the traffic shape a per-IP budget is
// sized to stop, and a deployment that has already accepted the seed route's premise
// (config.Load hard-fails it alongside APP_ENV=production) has nothing to defend here.
func PublicRateLimit(db *sql.DB, cfg *config.Config, namespace, name string, limit int, window time.Duration) func(http.Handler) http.Handler {
	if cfg.EnableTestRoutes {
		return func(next http.Handler) http.Handler { return next }
	}
	return RateLimit(db, namespace+"."+name, limit, window, func(r *http.Request) string {
		return ClientIP(r, cfg.TrustProxy)
	})
}
```

- [ ] **Step 4: Update the three existing call sites**

`internal/polls/handlers.go`, in `Register`, replace the two limiter lines and the two create-side registrations:

```go
	voteLimit := httpserver.PublicRateLimit(s.db, cfg, "polls", "vote", 30, time.Minute)
	commentLimit := httpserver.PublicRateLimit(s.db, cfg, "polls", "comment", 20, time.Minute)
	// createLimit ports the old stack's rateLimitMiddleware('create') — 20/min per IP, one bucket
	// shared by create and duplicate (main:src/server/polls/polls.functions.ts) — which the first
	// cut lost. It sits OUTSIDE WithOrgSession: an attacker's unauthenticated spray counts too,
	// and a signed-in account cannot mint unbounded polls (each with deadline/digest jobs and
	// invitee mail fan-out) either.
	createLimit := httpserver.PublicRateLimit(s.db, cfg, "polls", "create", 20, time.Minute)

	mux.Handle("POST /api/v1/polls", createLimit(httpserver.WithOrgSession(a, s.handleCreate)))
```

and

```go
	mux.Handle("POST /api/v1/polls/{id}/duplicate", createLimit(httpserver.WithOrgSession(a, s.handleDuplicate)))
```

`internal/bookings/handlers.go`, in `Register`:

```go
	bookLimit := httpserver.PublicRateLimit(s.db, cfg, "bookings", "book", 20, time.Minute)
	// createLimit is the booking-page analogue of internal/polls's 'create' bucket: page creation
	// was REQUIRE_ORG-only in the TS source too, but an account minting pages without bound is the
	// same abuse shape as unbounded poll creation, so it gets the same 20/min per-IP budget.
	createLimit := httpserver.PublicRateLimit(s.db, cfg, "bookings", "create", 20, time.Minute)

	mux.Handle("POST /api/v1/booking-pages", createLimit(httpserver.WithOrgSession(a, s.handleCreatePage)))
```

`internal/rooms/endpoints.go` line 117:

```go
	connectLimit := httpserver.PublicRateLimit(h.sqlDB, cfg, "rooms", "ws_connect", wsConnectLimit, wsConnectWindow)
```

- [ ] **Step 5: Write the failing polls and bookings handler tests**

Append to `internal/polls/handlers_test.go`:

```go
// TestHandlerCreateRateLimited pins the 'create' budget back onto POST /api/v1/polls and its
// duplicate sibling: 20/min per IP, one shared bucket, applied OUTSIDE the session gate — which is
// what lets this test exhaust it with unauthenticated requests (each a 401) rather than creating
// twenty real polls.
func TestHandlerCreateRateLimited(t *testing.T) {
	d := testdb.New(t)
	h, _, _ := newTestHandler(d, testConfig(t))

	for i := 0; i < 20; i++ {
		rec := doRequest(t, h, "POST", "/api/v1/polls", map[string]any{}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401 (under budget, no session)", i+1, rec.Code)
		}
	}

	over := doRequest(t, h, "POST", "/api/v1/polls", map[string]any{}, nil)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("21st create: status = %d, want 429; body=%s", over.Code, over.Body)
	}
	if errCode(t, over) != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", errCode(t, over))
	}

	// Duplicate shares the same bucket, exactly as the old rateLimitMiddleware('create') did.
	dup := doRequest(t, h, "POST", "/api/v1/polls/some-id/duplicate", map[string]any{}, nil)
	if dup.Code != http.StatusTooManyRequests {
		t.Fatalf("duplicate after the create budget is spent: status = %d, want 429; body=%s", dup.Code, dup.Body)
	}
}
```

Append to `internal/bookings/handlers_test.go`:

```go
// TestHandlerCreatePageRateLimited is internal/polls's TestHandlerCreateRateLimited for booking
// pages: POST /api/v1/booking-pages has its own 20/min per-IP 'create' bucket, outside the
// session gate.
func TestHandlerCreatePageRateLimited(t *testing.T) {
	d := testdb.New(t)
	h, _, _ := newTestHandler(d, testConfig(t))

	for i := 0; i < 20; i++ {
		rec := doRequest(t, h, "POST", "/api/v1/booking-pages", map[string]any{}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401 (under budget, no session)", i+1, rec.Code)
		}
	}

	over := doRequest(t, h, "POST", "/api/v1/booking-pages", map[string]any{}, nil)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("21st create: status = %d, want 429; body=%s", over.Code, over.Body)
	}
	if errCode(t, over) != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", errCode(t, over))
	}
}
```

- [ ] **Step 6: Note the test-mode exemption in PROTOCOL.md**

In `internal/rooms/PROTOCOL.md`, the paragraph beginning `All three routes now sit behind the same 30-connects-per-minute-per-IP budget` — append one sentence at its end:

```markdown
The budget (like every `PublicRateLimit` bucket in this codebase) is a pass-through when the
server runs with `ENABLE_TEST_ROUTES=true`, so the Playwright harness's single shared client IP
never trips it.
```

- [ ] **Step 7: Build and run every affected package**

Run: `go build ./... && go test ./internal/httpserver/ ./internal/polls/ -run 'RateLimit' && go test ./internal/bookings/ -run 'RateLimited' && go test ./internal/rooms/ -run 'ConnectRateLimited'`
Expected: all `ok`. (`TestPollWS_ConnectRateLimited` still passes because `newTestMux` passes `&config.Config{}`, whose `EnableTestRoutes` is false.)

- [ ] **Step 8: Commit**

```bash
git add internal/httpserver/domainauth.go internal/httpserver/ratelimit_test.go internal/polls/handlers.go internal/polls/handlers_test.go internal/bookings/handlers.go internal/bookings/handlers_test.go internal/rooms/endpoints.go internal/rooms/PROTOCOL.md
git commit -m "fix(httpserver+polls+bookings): rate-limit creation; public limiter off under test routes

POST /api/v1/polls, /polls/{id}/duplicate and /booking-pages had no
budget (the old stack capped 'create' at 20/min per IP). PublicRateLimit
now takes the *config.Config instead of a bare trustProxy bool and, like
the auth limiter, is a pass-through when ENABLE_TEST_ROUTES is set — the
Playwright harness's single client IP was tripping the shared 20/min
'book' bucket under CI retries.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 7: `GET /api/v1/stats` and a landing loader that uses it

Finding: "Landing-page usage counters render as zero until the stats websocket connects". The only read path for `stats:global` was the WebSocket snapshot frame; behind a proxy that does not forward upgrades the counters stay at zero forever. Add a REST read; keep the socket for live updates.

**Files:**
- Modify: `internal/rooms/endpoints.go` (`Register` + new `statsSnapshotHandler`)
- Modify: `internal/rooms/endpoints_test.go` (new test)
- Modify: `internal/rooms/PROTOCOL.md` (one paragraph after the routes table)
- Create: `web/src/api/stats.ts`
- Create: `web/src/api/__tests__/stats.test.ts`
- Modify: `web/src/routes/index.tsx:1-32`
- Modify: `web/src/lib/use-live-stats.ts:32-36` (comment)

**Interfaces:**
- Produces: `GET /api/v1/stats` → 200 `rooms.UsageStats` JSON (`{"pollsFinalized":n,"pollsCreated":n,"responsesYes":n,"responsesIfNeedBe":n,"responsesNo":n}`), `Cache-Control: no-store`, no auth, no rate limit. Web: `getUsageStats(): Promise<UsageStats>` and `loadLandingStats(): Promise<{ stats: UsageStats }>` in `web/src/api/stats.ts`.

- [ ] **Step 1: Write the failing Go test**

Append to `internal/rooms/endpoints_test.go` (add `"encoding/json"` and `"github.com/refsdal/whenweall/internal/testdb"` is already imported; add `"encoding/json"` only):

```go
// TestStatsREST_ReturnsCurrentCounters covers the landing page's first-paint read: the same
// UsageStats the WS snapshot frame nests under "data", as a plain, uncacheable JSON GET — so a
// deployment whose proxy drops WebSocket upgrades still shows real numbers instead of zeros.
func TestStatsREST_ReturnsCurrentCounters(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	stats := rooms.NewStatsService(sqlDB, nil)
	ctx := context.Background()

	mux := http.NewServeMux()
	rooms.Register(mux, hub, newFakeWSAuth(), &fakePollService{byID: map[string]any{}}, &fakeBookingService{}, stats, &config.Config{})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	stats.Record(ctx, map[string]int64{rooms.StatsPollsCreated: 3, rooms.StatsResponsesYes: 2})

	resp, err := http.Get(server.URL + "/api/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var got rooms.UsageStats
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PollsCreated != 3 || got.ResponsesYes != 2 {
		t.Errorf("stats = %+v, want PollsCreated=3 ResponsesYes=2", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/rooms/ -run TestStatsREST_ReturnsCurrentCounters`
Expected: FAIL — `status = 404, want 200` (the `/api/v1/stats` path is unmatched; with a full `Server` it would be `apiNotFound`, here the bare mux 404s).

- [ ] **Step 3: Mount the route**

In `internal/rooms/endpoints.go`, add to `Register` after the three `mux.Handle` lines:

```go
	// The stats room's REST read: the landing route's loader fetches this for first paint, then
	// the WS route above takes over for live updates. Unmetered (it is one cheap row read, and
	// the landing page is the most-visited URL on the site) and unauthenticated, like the WS route.
	mux.HandleFunc("GET /api/v1/stats", statsSnapshotHandler(stats))
```

and add the handler at the end of the file:

```go
// statsSnapshotHandler serves StatsService.Snapshot as plain JSON — byte-for-byte the object the
// stats WS route nests under "data" in its snapshot frame (PROTOCOL.md), so the frontend's
// UsageStats type covers both. no-store: a cached counter is exactly the stale number this
// endpoint exists to avoid.
func statsSnapshotHandler(stats *StatsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := stats.Snapshot(r.Context(), RoomKeyStats)
		if err != nil {
			httpserver.Err(w, http.StatusInternalServerError, "internal", "internal error", nil)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpserver.JSON(w, http.StatusOK, snapshot)
	}
}
```

- [ ] **Step 4: Run the Go test**

Run: `go test ./internal/rooms/ -run 'TestStatsREST|TestStatsWS'`
Expected: PASS.

- [ ] **Step 5: Document the REST read in PROTOCOL.md**

In `internal/rooms/PROTOCOL.md`, directly after the three-route table (before the paragraph starting `The booking route's path is`), insert:

```markdown
The stats room additionally has a plain REST read, `GET /api/v1/stats`, returning the same
`UsageStats` object the stats route's snapshot frame nests under `data`, with
`Cache-Control: no-store`. The landing route's loader uses it for first paint (and it is the only
source of real numbers behind a proxy that does not forward WebSocket upgrades); the socket
remains the live source once connected.
```

- [ ] **Step 6: Write the failing web test**

Create `web/src/api/__tests__/stats.test.ts`:

```ts
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { getUsageStats, loadLandingStats } from '#/api/stats'
import { EMPTY_STATS } from '#/lib/stats-types'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const LIVE = {
  pollsFinalized: 12,
  pollsCreated: 40,
  responsesYes: 300,
  responsesIfNeedBe: 20,
  responsesNo: 8,
}

describe('getUsageStats', () => {
  it('reads GET /api/v1/stats', async () => {
    server.use(http.get('/api/v1/stats', () => HttpResponse.json(LIVE)))
    await expect(getUsageStats()).resolves.toEqual(LIVE)
  })
})

describe('loadLandingStats', () => {
  it('hands the loader real numbers on first paint', async () => {
    server.use(http.get('/api/v1/stats', () => HttpResponse.json(LIVE)))
    await expect(loadLandingStats()).resolves.toEqual({ stats: LIVE })
  })

  it('never blocks the landing page on a failed read — falls back to zeros', async () => {
    server.use(http.get('/api/v1/stats', () => HttpResponse.text('upstream down', { status: 502 })))
    await expect(loadLandingStats()).resolves.toEqual({ stats: EMPTY_STATS })
  })
})
```

- [ ] **Step 7: Run to verify failure**

Run: `cd web && bunx vitest run src/api/__tests__/stats.test.ts`
Expected: FAIL — `Failed to resolve import "#/api/stats"`.

- [ ] **Step 8: Create `web/src/api/stats.ts`**

```ts
import { api } from '#/api/client'
import { EMPTY_STATS, type UsageStats } from '#/lib/stats-types'

/**
 * The landing page's usage counters over REST (`GET /api/v1/stats`, `internal/rooms/endpoints.go`)
 * — the same object the stats websocket's snapshot frame carries under `data`
 * (`internal/rooms/PROTOCOL.md`). Used for first paint; `useLiveStats` takes over live.
 */
export function getUsageStats(): Promise<UsageStats> {
  return api<UsageStats>('GET', '/api/v1/stats')
}

/**
 * Route loader for `/`: real numbers before the first render, so the section never flashes 0/0/0
 * and is correct even where a reverse proxy drops WebSocket upgrades. A failed read must never
 * take the landing page down with it — it degrades to zeros and the socket fills them in later.
 */
export async function loadLandingStats(): Promise<{ stats: UsageStats }> {
  try {
    return { stats: await getUsageStats() }
  } catch {
    return { stats: EMPTY_STATS }
  }
}
```

- [ ] **Step 9: Use it in the route**

In `web/src/routes/index.tsx`, replace lines 5 (`import { EMPTY_STATS, type UsageStats } from '#/lib/stats-types'`) with:

```ts
import { loadLandingStats } from '#/api/stats'
```

and replace the doc comment + `loader` (lines 16–24) with:

```ts
/**
 * The loader reads `GET /api/v1/stats` (`loadLandingStats`) so the counters are right on first
 * paint — no zero-flash, and correct behind a proxy that drops WebSocket upgrades.
 * `UsageStatsSection`'s socket (opened once the section scrolls into view) then keeps them live.
 */
export const Route = createFileRoute('/')({
  loader: loadLandingStats,
```

(the rest of the `createFileRoute` options — `head`, `component` — stay as they are).

- [ ] **Step 10: Fix the stale hook comment**

In `web/src/lib/use-live-stats.ts`, replace the paragraph

```ts
 * `stats:global` has no REST snapshot endpoint of its own (unlike polls/booking-pages) — the
 * websocket's own `snapshot` frame IS this room's one source of a fresh read, both for an ordinary
 * connect and for `resync`: `internal/rooms/PROTOCOL.md`'s "on resync, refetch a snapshot from the
 * ordinary REST endpoint" rule is satisfied here by tearing down and reopening the socket, since
 * reopening is exactly what re-triggers that same snapshot frame.
```

with

```ts
 * `stats:global` also has a REST read (`GET /api/v1/stats`, `web/src/api/stats.ts`) that the route
 * loader uses for first paint; this hook deliberately does not call it. The websocket's own
 * `snapshot` frame is this hook's source of a fresh read, both for an ordinary connect and for
 * `resync`: `internal/rooms/PROTOCOL.md`'s "on resync, refetch a snapshot" rule is satisfied here
 * by tearing down and reopening the socket, since reopening re-triggers that same snapshot frame.
```

- [ ] **Step 11: Run the web gates**

Run: `cd web && bunx vitest run src/api/__tests__/stats.test.ts src/components/landing && bun run typecheck && bun run lint`
Expected: the stats tests and the existing `UsageStats.test.tsx` pass; typecheck and lint exit 0 (the removed `EMPTY_STATS`/`UsageStats` import in `index.tsx` would otherwise be flagged as unused).

- [ ] **Step 12: Commit**

```bash
git add internal/rooms/endpoints.go internal/rooms/endpoints_test.go internal/rooms/PROTOCOL.md web/src/api/stats.ts web/src/api/__tests__/stats.test.ts web/src/routes/index.tsx web/src/lib/use-live-stats.ts
git commit -m "feat(rooms+web): GET /api/v1/stats and a landing loader that reads it

The landing counters had no read path but the stats websocket's snapshot
frame, so they rendered 0/0/0 until the socket connected — and forever
behind a proxy that drops upgrades. rooms.Register now also mounts
GET /api/v1/stats (UsageStats JSON, no-store); the / route's loader
fetches it for first paint and falls back to zeros on failure; the
socket still drives live updates.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 8: LISTEN connection liveness — bounded `WaitForNotification` plus `Ping`

Finding: "LISTEN connection has no application-level liveness check; a half-open session stalls fan-out silently". pgx's `WaitForNotification(ctx)` with a timed-out ctx returns a `pgconn.Timeout` error and leaves the connection usable (its `DeadlineContextWatcherHandler` resets the net deadline on Unwatch; `receiveMessage` does not close on a timeout), so the loop can wake up periodically, `Ping`, and treat a failed ping as a lost connection — which fires the existing reconnect + `resyncAll` path. Notifications arriving during the ping are buffered by `pgx.Conn.bufferNotifications`, not lost.

**Files:**
- Modify: `internal/rooms/hub.go:123-148,154-169` (two new exported timing fields on `Hub`, seeded in `NewHub`)
- Modify: `internal/rooms/listener.go:14-24,147-161` (constants, `listenLoop`; add `pgconn` import)
- Modify: `internal/rooms/hub_test.go` (new test)

**Interfaces:**
- Produces: `Hub.ListenIdleTimeout time.Duration` (default 60s) and `Hub.ListenPingTimeout time.Duration` (default 10s), exported like `KeepaliveInterval` so tests can shrink them.

- [ ] **Step 1: Write the failing test**

Append to `internal/rooms/hub_test.go`:

```go
// TestListenerIdleTimeoutPingsWithoutReconnecting pins the liveness check's SHAPE: an idle LISTEN
// session is interrupted every ListenIdleTimeout and pinged — it is NOT torn down and redialed.
// Observable from outside as (a) the backend pid behind the listener staying the same across
// several idle windows, (b) no resync frame reaching a subscriber (a reconnect would send one —
// see Run), and (c) delivery still working afterwards. A genuinely half-open TCP session cannot
// be simulated against a local container, so the ping's failure branch is covered by the ordinary
// connection-loss tests (TestReconnectSendsResyncAndResumesDelivery): a failed ping returns an
// error from listenLoop and takes exactly that path.
func TestListenerIdleTimeoutPingsWithoutReconnecting(t *testing.T) {
	url, sqlDB := testdb.URL(t)

	appName := "rooms_hub_test_" + db.NewID()
	listenURL := url
	if strings.Contains(listenURL, "?") {
		listenURL += "&application_name=" + appName
	} else {
		listenURL += "?application_name=" + appName
	}

	hub := rooms.NewHub(listenURL, sqlDB, nil)
	hub.ListenIdleTimeout = 100 * time.Millisecond
	hub.ListenPingTimeout = 2 * time.Second
	runHub(t, hub)
	awaitListening(t, hub, sqlDB)

	pidOf := func() int {
		t.Helper()
		var pid int
		if err := sqlDB.QueryRowContext(context.Background(),
			`SELECT pid FROM pg_stat_activity WHERE application_name = $1`, appName,
		).Scan(&pid); err != nil {
			t.Fatalf("finding listener backend pid: %v", err)
		}
		return pid
	}
	before := pidOf()

	const roomKey = "poll:idle-ping"
	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	// Seven idle windows with nothing to deliver: each must end in a ping, never a reconnect.
	select {
	case frame := <-frames:
		t.Fatalf("unexpected frame during an idle stretch (a resync means the listener reconnected instead of pinging): %s", frame)
	case <-time.After(700 * time.Millisecond):
	}
	if after := pidOf(); after != before {
		t.Fatalf("listener backend pid changed %d -> %d: the idle timeout reconnected instead of pinging", before, after)
	}

	emitCommitted(t, sqlDB, roomKey, "poll.changed", nil)
	got := mustReceiveFrame(t, frames, 5*time.Second)
	if got["type"] != "poll.changed" {
		t.Fatalf("frame after idle pings = %v, want poll.changed", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/rooms/ -run TestListenerIdleTimeoutPingsWithoutReconnecting`
Expected: FAIL to compile — `hub.ListenIdleTimeout undefined`.

- [ ] **Step 3: Add the timing fields to `Hub`**

In `internal/rooms/hub.go`, after the `PingTimeout time.Duration` field, add:

```go
	// ListenIdleTimeout/ListenPingTimeout bound Run's LISTEN session's liveness check
	// (listener.go's listenLoop): a WaitForNotification that has heard nothing for
	// ListenIdleTimeout is interrupted and the connection pinged, bounded by ListenPingTimeout;
	// a failed ping is treated as a lost connection so the ordinary reconnect + resyncAll path
	// fires. Without this a half-open TCP session (NAT timeout, DB failover behind a load
	// balancer) stalled every client on this replica silently for as long as OS keepalives take
	// to notice — ten minutes or more on Linux defaults. Exported for the same reason as
	// KeepaliveInterval: so a test can shrink them.
	ListenIdleTimeout time.Duration
	ListenPingTimeout time.Duration
```

and in `NewHub`'s literal, after `PingTimeout: defaultPingTimeout,`:

```go
		ListenIdleTimeout: defaultListenIdleTimeout,
		ListenPingTimeout: defaultListenPingTimeout,
```

- [ ] **Step 4: Rewrite `listenLoop`**

In `internal/rooms/listener.go`, add `"github.com/jackc/pgx/v5/pgconn"` to the imports, add to the `const (` block:

```go
	// defaultListenIdleTimeout/defaultListenPingTimeout are Hub.ListenIdleTimeout/
	// Hub.ListenPingTimeout's production defaults — see those fields. 60s is well inside every
	// common NAT/LB idle cutoff (typically 5–15 minutes) while costing one trivial `-- ping`
	// round trip per minute per replica on a quiet deployment.
	defaultListenIdleTimeout = 60 * time.Second
	defaultListenPingTimeout = 10 * time.Second
```

and replace `listenLoop`:

```go
// listenLoop reads notifications from an established LISTEN session until it errors (connection
// lost, a failed liveness ping, or ctx done) — the one thing it never does is return
// successfully, since there's no "end" to a LISTEN session short of losing it.
//
// Each WaitForNotification is bounded by h.ListenIdleTimeout. A timeout is not an error here: pgx
// leaves the connection usable after a context deadline (its context watcher resets the net
// deadline on unwatch, and a timed-out read does not close the session), so the loop pings the
// server (h.ListenPingTimeout) to prove the TCP session is still two-way. A ping that fails is
// what a half-open session looks like from this side — it is returned as the connection loss it
// is, and Run's reconnect + resyncAll path takes over. A NOTIFY that lands mid-ping is buffered by
// pgx.Conn (bufferNotifications) and returned by the next WaitForNotification, not dropped.
func (h *Hub) listenLoop(ctx context.Context, conn *pgx.Conn) error {
	for {
		waitCtx, cancelWait := context.WithTimeout(ctx, h.ListenIdleTimeout)
		notification, err := conn.WaitForNotification(waitCtx)
		cancelWait()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !pgconn.Timeout(err) {
				return err
			}
			pingCtx, cancelPing := context.WithTimeout(ctx, h.ListenPingTimeout)
			pingErr := conn.Ping(pingCtx)
			cancelPing()
			if pingErr != nil {
				return fmt.Errorf("rooms: listener liveness ping failed after %s idle: %w", h.ListenIdleTimeout, pingErr)
			}
			continue
		}

		roomKey, id, err := parseNotifyPayload(notification.Payload)
		if err != nil {
			h.log.Error("rooms: malformed room_events notify payload", "payload", notification.Payload, "error", err)
			continue
		}
		h.handleNotify(ctx, roomKey, id)
	}
}
```

- [ ] **Step 5: Run the rooms suite under the race detector**

Run: `go test -race ./internal/rooms/`
Expected: `ok` — the new test plus every existing hub/ws/reconnect test.

- [ ] **Step 6: Commit**

```bash
git add internal/rooms/hub.go internal/rooms/listener.go internal/rooms/hub_test.go
git commit -m "fix(rooms): liveness-check the LISTEN session with a bounded wait plus ping

listenLoop blocked in WaitForNotification with the process-lifetime ctx
and never pinged, so a half-open TCP session (NAT timeout, DB failover)
silently stopped fan-out on that replica until OS keepalives noticed —
ten-plus minutes on Linux defaults. Each wait is now bounded by
Hub.ListenIdleTimeout (60s); on timeout the connection is pinged
(ListenPingTimeout, 10s) and a failed ping is returned as a lost
connection so the existing reconnect + resync path fires.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 9: Graceful shutdown — close WebSockets with `GoingAway`, clear this replica's presence rows, wait for hub and worker

Findings: "Shutdown does not close websockets cleanly or wait for the job worker / hub before closing the pool", "Presence over-counts for up to ~2.5 minutes after a replica exits". `http.Server.Shutdown` ignores hijacked connections by documented design; the hub must own the goodbye. On `ctx.Done`, the hub closes every live connection with `StatusGoingAway` (each handler then unwinds and runs its deferred `presenceLeave`), waits for the handlers, deletes this replica's `ws_presence` rows, and `serve()` waits for `hub.Run` and `worker.Run` before the pool closes.

**Files:**
- Modify: `internal/rooms/hub.go` (`Hub` struct: connection registry fields; `NewHub`; import `coder/websocket`)
- Modify: `internal/rooms/ws.go:73-140` (`ServeWS` registers the connection; new `trackConn`/`untrackConn`/`closeAllConns`)
- Modify: `internal/rooms/listener.go:60-142` (`Run` → `runListener` + `shutdown`)
- Modify: `internal/rooms/presence.go:101-112` (`presenceBootSweep` comment; new `presenceShutdownSweep`)
- Modify: `internal/rooms/ws_test.go` (new test; add `"errors"` import)
- Modify: `cmd/whenweall/main.go:106-124,148,169-173` (`serve`; add `"sync"` import)

**Interfaces:**
- Produces: `Hub.Run` returns only after every live WebSocket has been sent `websocket.StatusGoingAway` and its handler has unwound (bounded by 8s), and this replica's `ws_presence` rows are deleted. `serve()` waits for `hub.Run`/`worker.Run` before `sqlDB.Close()`.

- [ ] **Step 1: Write the failing test**

Append to `internal/rooms/ws_test.go` (add `"errors"` to its imports):

```go
// TestServeWS_ShutdownClosesGoingAwayAndClearsPresence is the graceful-shutdown proof: cancelling
// Run's ctx must (1) send every live client a StatusGoingAway close frame — a well-behaved client
// reconnects, to whichever replica survives the deploy — rather than a bare TCP drop, (2) let each
// handler unwind (its deferred presenceLeave lands), and (3) delete this replica's ws_presence
// rows, so the "N viewing" pill on other replicas never inherits a dead replica's viewers for the
// ~90s it used to take internal/jobs's presence:sweep to notice.
func TestServeWS_ShutdownClosesGoingAwayAndClearsPresence(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := rooms.NewHub(url, sqlDB, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- hub.Run(ctx) }()
	awaitListening(t, hub, sqlDB)
	const roomKey = "poll:ws-shutdown"

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
		Authorize: func(r *http.Request) (string, error) { return roomKey, nil },
		Presence:  true,
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn := dialWS(t, server, "/ws")
	defer func() { _ = conn.CloseNow() }()
	_ = readWSFrame(t, conn, 5*time.Second) // snapshot
	awaitPresenceFrame(t, conn, 1)

	cancel()
	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("hub.Run returned %v, want context.Canceled", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("hub.Run did not return within 15s of cancel — shutdown is not draining connections")
	}

	// The client must have been told why: a GoingAway close frame. A trailing presence frame
	// (this connection's own leave) may precede it and is fine.
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	for {
		_, _, err := conn.Read(readCtx)
		if err == nil {
			continue
		}
		if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
			t.Fatalf("close status = %v (err %v), want StatusGoingAway", got, err)
		}
		break
	}

	var rows int
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM ws_presence WHERE room_key = $1`, roomKey,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("ws_presence rows for %s after shutdown = %d, want 0 (this replica's rows must be deleted on the way out)", roomKey, rows)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/rooms/ -run TestServeWS_ShutdownClosesGoingAwayAndClearsPresence`
Expected: FAIL — `close status = -1 (err ...), want StatusGoingAway` (today the connection is simply left open; the read times out), or the ws_presence row count is 1.

- [ ] **Step 3: Add the connection registry to `Hub`**

In `internal/rooms/hub.go`, add `"github.com/coder/websocket"` to the imports, and add these fields to `Hub` (after `OriginPatterns`):

```go
	// connsMu guards conns and closing — the live-connection registry ServeWS maintains
	// (ws.go: trackConn/untrackConn) so Run's exit path (listener.go: shutdown → ws.go:
	// closeAllConns) can send every open WebSocket a StatusGoingAway close frame and wait for its
	// handler to unwind. http.Server.Shutdown will not do this for us: by documented design it
	// "does not attempt to close nor wait for hijacked connections such as WebSockets", so
	// without this registry a SIGTERM left every client with a bare TCP drop and every handler's
	// deferred presenceLeave unrun. connWG counts handlers that have registered and not yet
	// unregistered; closing, once set, refuses new registrations.
	connsMu sync.Mutex
	conns   map[*websocket.Conn]struct{}
	closing bool
	connWG  sync.WaitGroup
```

and in `NewHub`'s literal add:

```go
		conns:             make(map[*websocket.Conn]struct{}),
```

- [ ] **Step 4: Register connections in `ServeWS` and add the drain**

In `internal/rooms/ws.go`, inside `ServeWS`, directly after `defer func() { _ = conn.CloseNow() }()`, insert:

```go
		if !h.trackConn(conn) {
			// Shutdown began between Accept and here: say goodbye properly rather than let the
			// deferred CloseNow drop the peer without a close frame.
			_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
			return
		}
		defer h.untrackConn(conn)
```

(Deferred order matters and is already right: `untrackConn` is registered before `unsubscribe` and the presence-leave defer, so — LIFO — presenceLeave and unsubscribe run first, then `untrackConn` marks the handler done, then `CloseNow`.)

Add at the end of `ws.go`:

```go
// shutdownGrace bounds how long closeAllConns waits for handlers to unwind after every connection
// has been sent its close frame. conn.Close itself waits up to 5s for the peer's answering close
// frame before giving up, so this is "that, plus a little" — and comfortably inside compose.yaml's
// stop_grace_period. http.Server.Shutdown's own 10s drain runs concurrently, not additively.
const shutdownGrace = 8 * time.Second

// trackConn registers conn in the hub's live registry. Reports false — registering nothing — once
// shutdown has begun, so a connection that wins the race against closeAllConns is refused instead
// of being left open unnoticed.
func (h *Hub) trackConn(conn *websocket.Conn) bool {
	h.connsMu.Lock()
	defer h.connsMu.Unlock()
	if h.closing {
		return false
	}
	h.conns[conn] = struct{}{}
	h.connWG.Add(1)
	return true
}

// untrackConn is trackConn's deferred counterpart: it runs after the handler's presenceLeave and
// unsubscribe defers, so connWG reaching zero means every connection's cleanup has landed.
func (h *Hub) untrackConn(conn *websocket.Conn) {
	h.connsMu.Lock()
	delete(h.conns, conn)
	h.connsMu.Unlock()
	h.connWG.Done()
}

// closeAllConns flips the hub into shutdown, sends every live connection a StatusGoingAway close
// frame, and waits (bounded by shutdownGrace) for every ServeWS handler to run its deferred
// cleanup. The close handshake completing — or the peer vanishing — is what unblocks each
// handler's readPumpLoop, so no per-connection ctx needs cancelling here.
func (h *Hub) closeAllConns() {
	h.connsMu.Lock()
	h.closing = true
	open := make([]*websocket.Conn, 0, len(h.conns))
	for conn := range h.conns {
		open = append(open, conn)
	}
	h.connsMu.Unlock()

	for _, conn := range open {
		// Close performs the full handshake (write the frame, wait up to 5s for the peer's) and
		// so can block for seconds per peer — every connection gets its own goroutine rather
		// than a sequential walk; a thousand idle tabs must not take a thousand × 5s to leave.
		go func(c *websocket.Conn) {
			_ = c.Close(websocket.StatusGoingAway, "server shutting down")
		}(conn)
	}

	done := make(chan struct{})
	go func() {
		h.connWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
		h.log.Warn("rooms: websocket handlers did not unwind within shutdown grace", "grace", shutdownGrace, "open", len(open))
	}
}
```

- [ ] **Step 5: Give `Run` an exit path**

In `internal/rooms/listener.go`, rename the existing `Run` body: change the signature line `func (h *Hub) Run(ctx context.Context) error {` to `func (h *Hub) runListener(ctx context.Context) error {`, and delete these lines from the top of it (they move to the new `Run`):

```go
	if err := h.presenceBootSweep(ctx); err != nil {
		h.log.Error("rooms: presence boot sweep", "replica_id", h.replicaID, "error", err)
	}
	go h.presenceHeartbeatLoop(ctx)
```

Then add, directly above `runListener` (keeping `Run`'s existing long doc comment on `Run`, not on `runListener`):

```go
func (h *Hub) Run(ctx context.Context) error {
	// Presence's boot sweep and heartbeat (presence.go) are wired in here, not because they have
	// anything to do with the LISTEN session runListener manages, but because Run is this Hub's
	// one "I am now alive as a replica" entry point — the natural place to start them, and
	// (shutdown, below) the natural place to undo them when ctx ends.
	if err := h.presenceBootSweep(ctx); err != nil {
		h.log.Error("rooms: presence boot sweep", "replica_id", h.replicaID, "error", err)
	}
	go h.presenceHeartbeatLoop(ctx)

	err := h.runListener(ctx)
	h.shutdown()
	return err
}

// shutdown is Run's exit path, run once ctx is done: close every live WebSocket with a GoingAway
// frame and wait for its handler (ws.go: closeAllConns), then delete this replica's presence rows
// (presence.go: presenceShutdownSweep). Both run on fresh, bounded contexts — Run's own ctx is
// already done by the time this is called.
func (h *Hub) shutdown() {
	h.closeAllConns()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := h.presenceShutdownSweep(ctx); err != nil {
		h.log.Error("rooms: presence shutdown sweep", "replica_id", h.replicaID, "error", err)
	}
}

// runListener owns the LISTEN connection loop — see Run's doc comment for the contract (always
// returns ctx.Err(); resync after every successful LISTEN; reconnect with jittered backoff).
```

Also update the sentence in `Run`'s existing doc comment `Run always returns ctx.Err() (nil is never a possible return): the only way out of its loop is ctx ending, whether that happens before the first connection attempt or in the middle of one.` to:

```go
// Run always returns ctx.Err() (nil is never a possible return): the only way out of its loop is
// ctx ending, whether that happens before the first connection attempt or in the middle of one —
// and it returns only AFTER shutdown has drained this replica's WebSockets and presence rows.
```

- [ ] **Step 6: Add the shutdown sweep and fix the boot-sweep comment**

In `internal/rooms/presence.go`, replace `presenceBootSweep`'s doc comment with:

```go
// presenceBootSweep deletes any ws_presence rows already tagged with this replica's id, called
// once from Run before it starts listening. With NewHub's replicaID a fresh random id every
// process start (db.NewID()) no pre-existing row can carry it, so this is a defensive no-op in
// practice — presenceShutdownSweep below is the one that actually clears a replica's rows, at
// graceful shutdown. It costs one cheap DELETE to keep as a safety net against a future identity
// scheme (e.g. a stable per-pod id that could survive a crash-restart) making it meaningful again.
```

and add after `presenceBootSweep`:

```go
// presenceShutdownSweep is presenceBootSweep's counterpart at graceful shutdown — the direct port
// of presence.ts's clearReplicaPresence, run at the moment this replica goes away rather than
// only defensively at the next boot. By the time it runs, closeAllConns has let every handler's
// own presenceLeave land, so the rows it deletes should all be at count 0; a row still above 0
// means a handler did not unwind within the grace period (a wedged peer), and that is exactly the
// over-count this sweep exists to remove now instead of leaving it for internal/jobs's
// presence:sweep to notice ~90s later. Rooms whose deleted row still carried viewers get a
// corrected total broadcast, same as that job does.
func (h *Hub) presenceShutdownSweep(ctx context.Context) error {
	rows, err := h.sqlDB.QueryContext(ctx,
		`DELETE FROM ws_presence WHERE replica_id = $1 RETURNING room_key, count`, h.replicaID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var stillCounted []string
	for rows.Next() {
		var (
			roomKey string
			count   int64
		)
		if err := rows.Scan(&roomKey, &count); err != nil {
			return err
		}
		if count > 0 {
			stillCounted = append(stillCounted, roomKey)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, roomKey := range stillCounted {
		if err := BroadcastPresenceTotal(ctx, h.sqlDB, roomKey); err != nil {
			h.log.Error("rooms: presence shutdown broadcast", "room_key", roomKey, "error", err)
		}
	}
	return nil
}
```

- [ ] **Step 7: Run the rooms suite under the race detector**

Run: `go test -race ./internal/rooms/`
Expected: `ok` — the new shutdown test passes and every existing test (including `TestServeWS_KeepaliveTearsDownDeadPeer` and the replica proof) still passes. If `runHub`'s cleanup logs `hub.Run did not exit within 5s of cancel`, `shutdownGrace` is being hit — a connection did not unwind; check the LIFO defer order in `ServeWS` from Step 4.

- [ ] **Step 8: Make `serve()` wait for the hub and the worker**

In `cmd/whenweall/main.go`, add `"sync"` to the imports. Replace the line `go func() { _ = hub.Run(ctx) }()` (plus the comment above it) with:

```go
	// bg tracks the two long-lived background goroutines (hub.Run, worker.Run) so serve() can wait
	// for them to unwind — the hub closing its WebSockets with a GoingAway frame and clearing this
	// replica's presence rows, the worker finishing its current poll — BEFORE the deferred
	// sqlDB.Close() above pulls the pool out from under them. Run always returns ctx.Err() (never
	// nil, per its own doc comment) — on an ordinary SIGINT/SIGTERM that's just context.Canceled,
	// not a failure worth logging; Run's own internal logging already covers every real
	// connection/LISTEN problem along the way.
	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		_ = hub.Run(ctx)
	}()
```

Replace `go worker.Run(ctx)` with:

```go
	bg.Add(1)
	go func() {
		defer bg.Done()
		worker.Run(ctx)
	}()
```

and replace the tail of `serve()`:

```go
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
```

with:

```go
	serveErr := srv.ListenAndServe(ctx)
	// Whether ListenAndServe returned because ctx ended (SIGTERM — the hub and worker are already
	// unwinding) or because the listener itself failed (they are not), the background goroutines
	// stop the same way: cancel, then wait for them to actually exit. A job in flight at this
	// moment sees its ctx cancelled and is retried later by the at-least-once queue (5-minute lock
	// expiry) — an accepted trade for never holding shutdown hostage to a two-minute JobTimeout.
	cancel()
	bg.Wait()
	if serveErr != nil {
		fmt.Fprintln(os.Stderr, serveErr)
		return 1
	}
	return 0
```

- [ ] **Step 9: Build, vet, lint**

Run: `go build ./... && go vet ./... && golangci-lint run ./internal/rooms/... ./cmd/...`
Expected: all exit 0.

- [ ] **Step 10: Commit**

```bash
git add internal/rooms/hub.go internal/rooms/ws.go internal/rooms/listener.go internal/rooms/presence.go internal/rooms/ws_test.go cmd/whenweall/main.go
git commit -m "fix(rooms+cmd): drain websockets and presence on shutdown, wait for hub and worker

http.Server.Shutdown ignores hijacked connections by design, so a
SIGTERM left every WebSocket client with a bare TCP drop, every
handler's deferred presenceLeave unrun, and this replica's ws_presence
rows inflating the 'N viewing' count for ~90s until presence:sweep
noticed. The hub now keeps a registry of live connections; when Run's
ctx ends it sends each a StatusGoingAway close frame, waits (8s grace)
for the handlers to unwind, deletes this replica's ws_presence rows and
re-broadcasts any room still over-counted. serve() waits for hub.Run and
worker.Run to return before the pool closes.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 10: Migration `00010_drop_two_factor.sql`, `sqlc generate`, CI `sqlc diff`

Findings: "users.two_factor_enabled and two_factors table remain from the unmounted two-factor plugin" and "sqlc models.go is stale relative to the schema (missing admin_audit_log and locked_users structs) and CI has no `sqlc diff` guard". Commit 72a8306 unmounted Limen's two-factor plugin without regenerating the schema. `sqlc diff` today already exits 1 (missing `AdminAuditLog`/`LockedUser` models); after the migration `sqlc generate` fixes both drifts at once.

**Files:**
- Create: `migrations/00010_drop_two_factor.sql`
- Modify: `internal/db/db_test.go` (new test)
- Modify: `internal/auth/cascade.go` (drop the `DELETE FROM two_factors` statement — Plan A moved the user-delete cascade here from `internal/admin/users.go`; if Plan A has not landed yet, the statement is still at `internal/admin/users.go:494-503`)
- Regenerate: `internal/polls/queries/models.go`, `internal/polls/queries/polls.sql.go`, `internal/bookings/queries/models.go`, `internal/bookings/queries/bookings.sql.go` (via `sqlc generate`)
- Modify: `.github/workflows/ci.yml` (`go` job: sqlc setup + `sqlc diff`)
- Modify: `docs/limen-migrations.md:1-5`, `CONTRIBUTING.md:24-27,44-48`

**Interfaces:**
- Produces: schema without `two_factors` / `users.two_factor_enabled`; sqlc `User` model without `TwoFactorEnabled`; CI fails on generated-code drift.

- [ ] **Step 1: Write the failing test**

Append to `internal/db/db_test.go`:

```go
// TestTwoFactorLeftoversDropped pins migration 00010: Limen's two-factor plugin was unmounted in
// 72a8306, and its schema — declared by the plugin, not by Limen core — must not linger in the
// baseline (sqlc was generating a User.TwoFactorEnabled field nothing read, and admin's
// DeleteUser was clearing a table nothing wrote).
func TestTwoFactorLeftoversDropped(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	var tables int
	if err := d.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = 'two_factors'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Error("two_factors table still exists")
	}

	var columns int
	if err := d.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'two_factor_enabled'").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Error("users.two_factor_enabled column still exists")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/db/ -run TestTwoFactorLeftoversDropped`
Expected: FAIL — `two_factors table still exists` and `users.two_factor_enabled column still exists`.

- [ ] **Step 3: Write the migration**

Create `migrations/00010_drop_two_factor.sql`:

```sql
-- +goose Up

-- Limen's two-factor plugin was unmounted in 72a8306 (never wired to any UI). Its schema — the
-- users.two_factor_enabled column and the two_factors table, both declared by that plugin, not by
-- Limen core — was left behind because migrations/00002_auth.sql had been generated with the
-- plugin mounted and was never regenerated. Nothing reads either object: Limen inserts users with
-- explicit columns, and the only references were sqlc's generated User model (regenerated after
-- this migration) and admin.DeleteUser's now-removed `DELETE FROM two_factors`.
ALTER TABLE users DROP COLUMN two_factor_enabled;
DROP TABLE two_factors;

-- +goose Down
CREATE TABLE two_factors (
  id BIGSERIAL,
  user_id BIGINT NOT NULL,
  secret VARCHAR(255),
  backup_codes TEXT,
  PRIMARY KEY (id),
  CONSTRAINT fk_two_factors_users_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_two_factors_user_id ON two_factors (user_id);
ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT false;
```

- [ ] **Step 4: Regenerate sqlc code**

```bash
sqlc generate
git status --short internal/polls/queries internal/bookings/queries
```

Expected: exactly four modified files — both `models.go` (the `User` struct loses `TwoFactorEnabled bool`; `AdminAuditLog` and `LockedUser` structs appear) and both `*.sql.go` (`getUser`'s SELECT list and `Scan` drop `two_factor_enabled`). No other diff. `grep -rn TwoFactorEnabled internal/` prints nothing.

- [ ] **Step 5: Remove the admin cascade's dead statement**

In `internal/admin/users.go`, replace the block

```go
	// accounts/sessions/two_factors all reference users(id) ON DELETE RESTRICT (migrations/00002),
	// unlike organization_members (CASCADE, which the DELETE FROM users below triggers on its
	// own) — so they must be cleared explicitly first, or that statement fails a foreign key
	// check. This is also what actually revokes any session left over from the best-effort
	// RevokeUserSessions call above.
	for _, stmt := range []string{
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM accounts WHERE user_id = $1`,
		`DELETE FROM two_factors WHERE user_id = $1`,
	} {
```

with

```go
	// accounts and sessions reference users(id) ON DELETE RESTRICT (migrations/00002), unlike
	// organization_members (CASCADE, which the DELETE FROM users below triggers on its own) — so
	// they must be cleared explicitly first, or that statement fails a foreign key check. This is
	// also what actually revokes any session left over from the best-effort RevokeUserSessions
	// call above. (two_factors used to be in this list; migration 00010 dropped the table.)
	for _, stmt := range []string{
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM accounts WHERE user_id = $1`,
	} {
```

(If Plan A has already moved this cascade into `internal/auth.CascadeDeleteUser`, make the identical edit there instead — the statement list is the same.)

- [ ] **Step 6: Run the affected packages**

Run: `go build ./... && sqlc diff && go test ./internal/db/ ./internal/admin/ ./internal/polls/ ./internal/bookings/`
Expected: `sqlc diff` exits 0 with no output; all four packages `ok`.

- [ ] **Step 7: Guard generated code in CI**

In `.github/workflows/ci.yml`, in the `go` job, insert between `- run: go vet ./...` and `- uses: golangci/golangci-lint-action@v9`:

```yaml
      # Generated sqlc code must match the checked-in schema + queries: a stale models.go or
      # *.sql.go (someone edited a migration or a query and forgot `sqlc generate`) fails here
      # instead of drifting silently until the next regeneration produces an unrelated diff.
      - uses: sqlc-dev/setup-sqlc@v4
        with:
          sqlc-version: '1.31.1'
      - run: sqlc diff
```

- [ ] **Step 8: Fix the docs**

`docs/limen-migrations.md` lines 3–5: change the table list to drop `two_factors`:

```markdown
`migrations/00002_auth.sql` is Limen's own schema (`users`, `accounts`, `organizations`,
`organization_invitations`, `organization_members`, `organization_member_roles`,
`limen_rate_limits`, `sessions`, `verifications`), generated once and hand-folded
```

and append after the first paragraph (before `This is needed again whenever:`):

```markdown
00002 was generated while Limen's two-factor plugin was still mounted, so it also created
`two_factors` and `users.two_factor_enabled`; `migrations/00010_drop_two_factor.sql` removes both
(the plugin was unmounted in 72a8306). A future regeneration with the current plugin set will not
emit them.
```

`CONTRIBUTING.md`, in the "Database changes" section, extend the sqlc paragraph's last sentence:

```markdown
there, then run `sqlc generate` from the repo root to regenerate the typed Go it produces
into the same directory. Don't hand-edit generated query code — CI runs `sqlc diff` and fails
on any drift between the migrations/queries and the checked-in generated files.
```

and in "Before opening a pull request", change the Go line of the command block to:

```bash
go test ./... && go vet ./... && golangci-lint run ./... && sqlc diff
```

- [ ] **Step 9: Commit**

```bash
git add migrations/00010_drop_two_factor.sql internal/db/db_test.go internal/auth/cascade.go internal/polls/queries internal/bookings/queries .github/workflows/ci.yml docs/limen-migrations.md CONTRIBUTING.md
git commit -m "feat(db): drop two-factor leftovers (00010), regenerate sqlc, guard drift in CI

Limen's two-factor plugin was unmounted in 72a8306 but 00002's generated
schema kept two_factors and users.two_factor_enabled; sqlc's User model
still scanned the column and admin.DeleteUser still cleared the table.
Migration 00010 drops both, sqlc generate removes TwoFactorEnabled and
picks up the AdminAuditLog/LockedUser models that were already missing,
and the CI go job now runs sqlc diff so generated code cannot drift.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 11: goose `Down` is exercised, and concurrent migrate-on-boot is proven

Finding: "goose Down is never exercised and concurrent migrate-on-boot under the advisory lock is untested". Every migration carries a `-- +goose Down` that has never run; two replicas migrating together (spec §6's advisory-lock design) has no test. Also moves goose's global `SetBaseFS`/`SetDialect` behind a `sync.Once` so concurrent `Migrate` calls are race-free.

**Files:**
- Modify: `internal/db/db.go:50-77` (`Migrate`; new `MigrateDownTo`, `gooseSetup`)
- Modify: `internal/db/db_test.go` (two new tests; imports `io/fs`, `sync`, `migrations`)

**Interfaces:**
- Produces: `func MigrateDownTo(ctx context.Context, sqlDB *sql.DB, version int64) error` (tests/ops only — `serve` never calls it).

- [ ] **Step 1: Write the failing tests**

Append to `internal/db/db_test.go` (add imports `"io/fs"`, `"sync"`, `"github.com/refsdal/whenweall/migrations"`):

```go
// migrationCount is the number of goose migration files — and, since they are numbered 00001..N
// contiguously, the version_id the schema must sit at when fully migrated.
func migrationCount(t *testing.T) int {
	t.Helper()
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migration files embedded")
	}
	return len(files)
}

// TestMigrationsDownToZeroAndBackUp is the first time any Down section runs: a broken Down (a
// missing DROP, a wrong dependency order) should surface here, not during an emergency rollback.
func TestMigrationsDownToZeroAndBackUp(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := db.MigrateDownTo(ctx, d, 0); err != nil {
		t.Fatalf("goose down to 0: %v", err)
	}
	var leftover int
	if err := d.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'goose_db_version'",
	).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("%d tables survived a full Down — some migration's Down section is incomplete", leftover)
	}

	if err := db.Migrate(ctx, d); err != nil {
		t.Fatalf("goose up after down: %v", err)
	}
	var version int64
	if err := d.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if int(version) != migrationCount(t) {
		t.Fatalf("version after re-migrating = %d, want %d", version, migrationCount(t))
	}
}

// TestConcurrentMigrateAppliesEachMigrationOnce is spec §6's "auto-run at boot under a PG advisory
// lock" proof: several replicas booting against an empty database at the same instant must each
// return success and leave exactly one version row per migration.
func TestConcurrentMigrateAppliesEachMigrationOnce(t *testing.T) {
	url, d := testdb.URL(t)
	ctx := context.Background()
	if err := db.MigrateDownTo(ctx, d, 0); err != nil {
		t.Fatalf("goose down to 0: %v", err)
	}

	const replicas = 4
	pools := make([]*sql.DB, replicas)
	for i := range pools {
		pool, err := db.Open(ctx, url, 2)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = pool.Close() }()
		pools[i] = pool
	}

	start := make(chan struct{})
	errs := make(chan error, replicas)
	var wg sync.WaitGroup
	for _, pool := range pools {
		wg.Add(1)
		go func(p *sql.DB) {
			defer wg.Done()
			<-start
			errs <- db.Migrate(ctx, p)
		}(pool)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate: %v", err)
		}
	}

	var applied int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != migrationCount(t) {
		t.Fatalf("goose_db_version has %d applied rows, want %d (each migration applied exactly once)", applied, migrationCount(t))
	}
}
```

(`db_test.go` needs `"database/sql"` imported for `[]*sql.DB`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/db/ -run 'TestMigrationsDownToZeroAndBackUp|TestConcurrentMigrate'`
Expected: FAIL to compile — `undefined: db.MigrateDownTo`.

- [ ] **Step 3: Implement `MigrateDownTo` and the once-guarded goose setup**

In `internal/db/db.go`, add `"sync"` to the imports, and replace `Migrate` with:

```go
// gooseInit guards goose's process-global configuration (base FS, dialect). Migrate can be called
// from several goroutines at once — the concurrent-boot test does exactly that — and two
// unsynchronized writes to goose's package-level state would be a data race even though both
// write the same values.
var (
	gooseInit    sync.Once
	gooseInitErr error
)

func gooseSetup() error {
	gooseInit.Do(func() {
		goose.SetBaseFS(migrations.FS)
		gooseInitErr = goose.SetDialect("postgres")
	})
	return gooseInitErr
}

// withMigrationLock runs fn while holding the advisory lock on a dedicated connection: replicas
// booting together must not race goose.
func withMigrationLock(ctx context.Context, sqlDB *sql.DB, fn func() error) error {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return err
	}
	defer func() {
		// The connection is about to be released back to the pool (or closed) regardless, so a
		// failed unlock can't be retried here — but it's worth knowing about: it means the
		// advisory lock stays held until Postgres notices the session end.
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock); err != nil {
			slog.Warn("failed to release migration advisory lock", "error", err)
		}
	}()
	if err := gooseSetup(); err != nil {
		return err
	}
	return fn()
}

// Migrate applies every pending migration (goose up) under the advisory lock.
func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	return withMigrationLock(ctx, sqlDB, func() error {
		return goose.UpContext(ctx, sqlDB, ".")
	})
}

// MigrateDownTo rolls the schema back to version (0 = undo everything) under the same advisory
// lock. Tests and operator tooling only — `serve` and `migrate` never call it; it exists so the
// Down sections actually get exercised (TestMigrationsDownToZeroAndBackUp) instead of being
// trusted blind until an emergency rollback.
func MigrateDownTo(ctx context.Context, sqlDB *sql.DB, version int64) error {
	return withMigrationLock(ctx, sqlDB, func() error {
		return goose.DownToContext(ctx, sqlDB, ".", version)
	})
}
```

- [ ] **Step 4: Run the db tests**

Run: `go test -race ./internal/db/`
Expected: `ok`. If `TestMigrationsDownToZeroAndBackUp` fails inside `goose down`, the error names the migration whose Down is broken (e.g. a `DROP TABLE` in the wrong dependency order): fix that migration's `-- +goose Down` section in place — Down sections have never run anywhere and there is no live data, so this is the one edit to a shipped migration that is safe and expected — and rerun.

- [ ] **Step 5: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "test(db): exercise every goose Down and prove concurrent migrate-on-boot

No test or CI step had ever run a Down section, and the advisory-lock
boot path was only tested single-replica. db.MigrateDownTo (tests/ops
only) rolls to 0 and Migrate brings the schema back; four pools now race
Migrate from empty and must leave exactly one version row per file.
goose's global SetBaseFS/SetDialect move behind a sync.Once so that race
is clean under -race.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

(Adjust the message to name any Down section you had to fix in Step 4.)

---

### Task 12: `-race` covers the tests it was meant to; stale no-cgo comments go; `ClaimDue` is contended for real

Findings: "The -race name filter misses the participant-row-lock race test and the personal-org race; README documents a different regex; test comments claim -race cannot run", "TestTwoWorkersGetDisjointSets is sequential; FOR UPDATE SKIP LOCKED is never contended by concurrent claimers".

**Files:**
- Modify: `internal/polls/claims_test.go:576-583,639-646`
- Modify: `internal/bookings/bookings_test.go:747-753`
- Modify: `internal/jobs/jobs_test.go:63-101` (replace `TestTwoWorkersGetDisjointSets`; imports `fmt`, `sync`)
- Modify: `.github/workflows/ci.yml:23-30`
- Modify: `README.md:472`

**Interfaces:**
- Produces: test names `TestClaimSharedParticipantMaxClaimsRaceAcrossOptions` (polls) and `TestConcurrentClaimersGetDisjointCompleteSets` (jobs); CI `-race` run covers `./internal/polls/... ./internal/bookings/... ./internal/auth/... ./internal/jobs/...`.

- [ ] **Step 1: Write the failing concurrent-claim test**

In `internal/jobs/jobs_test.go`, delete `TestTwoWorkersGetDisjointSets` (lines 63–101) and add in its place (add `"fmt"` and `"sync"` to the imports):

```go
// TestConcurrentClaimersGetDisjointCompleteSets contends ClaimDue's FOR UPDATE SKIP LOCKED for
// real: eight goroutines claim in batches from the same due set until it is empty. Every job must
// be claimed exactly once, by exactly one worker. (Its sequential predecessor called ClaimDue
// twice in a row, which a bare `UPDATE ... WHERE locked_by IS NULL` would also have passed.)
func TestConcurrentClaimersGetDisjointCompleteSets(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	const total = 40
	for i := 0; i < total; i++ {
		if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
			Kind:  "mail:send",
			RunAt: time.Now().Add(-time.Second),
		}); err != nil {
			t.Fatalf("Schedule[%d]: %v", i, err)
		}
	}

	const workers = 8
	var (
		mu        sync.Mutex
		claimedBy = make(map[string]string, total)
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(replica string) {
			defer wg.Done()
			<-start
			for {
				batch, err := jobs.ClaimDue(ctx, d, replica, 3)
				if err != nil {
					t.Errorf("%s: ClaimDue: %v", replica, err)
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, j := range batch {
					if prev, dup := claimedBy[j.ID]; dup {
						t.Errorf("job %s claimed by both %s and %s", j.ID, prev, replica)
					}
					claimedBy[j.ID] = replica
				}
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	close(start)
	wg.Wait()

	if len(claimedBy) != total {
		t.Fatalf("claimed %d distinct jobs, want %d (every due job claimed exactly once)", len(claimedBy), total)
	}
}
```

- [ ] **Step 2: Run it (it should pass — this test pins existing correct behaviour) and run it under `-race`**

Run: `go test -race ./internal/jobs/ -run TestConcurrentClaimersGetDisjointCompleteSets -count=3`
Expected: `ok`. (If it ever fails, `ClaimDue`'s `FOR UPDATE SKIP LOCKED` is broken — that is a Plan E bug, stop and report.)

- [ ] **Step 3: Rename the polls race test and delete the stale comments**

In `internal/polls/claims_test.go`:

Replace the paragraph (before `TestClaimLastSlotExactlyOneWinner`)

```go
//
// This environment has no cgo, so `go test -race` cannot run (the race detector requires cgo);
// per the task brief, this is compensated for with `-count=5` repetition instead (see the task
// report) — run the same 16-way race five times over rather than relying on -race to catch a
// hypothetical missed lock.
```

with

```go
//
// CI runs this under `go test -race` (ci.yml's polls/bookings/auth/jobs race pass); locally,
// `-count=5` is a cheap way to widen the window further.
```

Replace the tail of the comment before `TestClaimSharedParticipantMaxClaimsAcrossOptions`

```go
// option-lock race hits without artificially widening its window. This environment has no cgo, so
// `go test -race` cannot run (the race detector requires cgo) — compensated for with `-count=5`
// repetition instead, same as that test.
func TestClaimSharedParticipantMaxClaimsAcrossOptions(t *testing.T) {
```

with

```go
// option-lock race hits without artificially widening its window. "Race" is in the name on
// purpose: ci.yml's -race pass selects tests by `-run 'Race|Concurrent|Winner|LateCommitter|Slow'`,
// and this — the pinning test for the max-claims race fixed in c1dd78f — used to match none of them.
func TestClaimSharedParticipantMaxClaimsRaceAcrossOptions(t *testing.T) {
```

Then `grep -rn "TestClaimSharedParticipantMaxClaimsAcrossOptions" internal/` and update any reference (comments elsewhere) to the new name.

In `internal/bookings/bookings_test.go`, replace

```go
// package doc comment) must serialize them so exactly one wins and the rest see ErrSlotTaken — run
// with `-count=5` (no `-race`: this environment has CGO_ENABLED=0 and no cgo toolchain, and -race
// requires cgo).
```

with

```go
// package doc comment) must serialize them so exactly one wins and the rest see ErrSlotTaken. CI
// runs this under `go test -race`; `-count=5` locally widens the window further.
```

- [ ] **Step 4: Widen the CI race pass**

In `.github/workflows/ci.yml`, replace lines 23–30 (the two comments and two `-race` steps) with:

```yaml
      # internal/rooms runs UNFILTERED: this whole package IS the concurrency-sensitive surface
      # (the hub's watermark/dispatch state, presence counting, the keepalive goroutine racing
      # read/write pumps, shutdown draining, ...), not just the handful of tests a name filter
      # would happen to catch. polls/bookings/auth/jobs stay scoped by name to keep the job fast:
      # their concurrent-writer tests (claim races, double-booking, personal-org creation, job
      # claiming) all carry one of these tokens in their name — keep that true when adding one.
      - run: go test -race ./internal/rooms/...
      - run: go test -race -run 'Race|Concurrent|Winner|LateCommitter|Slow' ./internal/polls/... ./internal/bookings/... ./internal/auth/... ./internal/jobs/...
```

- [ ] **Step 5: Align the README command**

`README.md` line 472 (in "Running just one thing"):

```bash
go test -race -run 'Race|Concurrent|Winner|LateCommitter|Slow' ./internal/polls/... ./internal/bookings/... ./internal/auth/... ./internal/jobs/...
```

- [ ] **Step 6: Run the race pass exactly as CI will**

Run: `go test -race -run 'Race|Concurrent|Winner|LateCommitter|Slow' ./internal/polls/... ./internal/bookings/... ./internal/auth/... ./internal/jobs/... -v 2>&1 | grep -E '^(=== RUN|--- FAIL|ok|FAIL)' | grep -E 'MaxClaimsRace|PersonalOrgConcurrent|ConcurrentClaimers|^ok|FAIL'`
Expected: `=== RUN` lines for `TestClaimSharedParticipantMaxClaimsRaceAcrossOptions`, `TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne` and `TestConcurrentClaimersGetDisjointCompleteSets`, four `ok` lines, no `FAIL`.

- [ ] **Step 7: Commit**

```bash
git add internal/polls/claims_test.go internal/bookings/bookings_test.go internal/jobs/jobs_test.go .github/workflows/ci.yml README.md
git commit -m "test: race pass covers the max-claims, personal-org and job-claim races

ci.yml's -race filter missed TestClaimSharedParticipantMaxClaims... (no
matching token in the name) and never ran internal/auth or internal/jobs
at all; README advertised a narrower regex; three comments still said
-race cannot run for lack of cgo. The polls test is renamed with Race in
it, auth and jobs join the pass, the README matches ci.yml, and the
sequential TestTwoWorkersGetDisjointSets becomes eight goroutines
contending FOR UPDATE SKIP LOCKED until the due set is empty.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 13: Container and compose hygiene — `.dockerignore`, narrower build `COPY`, `image:` for `app`, honest upgrade docs

Findings: ".dockerignore does not exclude .env, so `COPY . .` pulls operator secrets into the build stage layer", "compose.yaml has no image: for app, so README's 'docker compose pull app' upgrade and 'builds (or pulls)' claim are wrong". Also gives compose a `stop_grace_period` long enough for Task 9's drain.

**Files:**
- Modify: `.dockerignore`
- Modify: `Dockerfile:19-23`
- Modify: `compose.yaml:33-37`
- Modify: `README.md:89-90,369-375`

**Interfaces:**
- Produces: `compose.yaml` `app` has `image: ghcr.io/refsdal/whenweall:latest` plus `build:`; the Go build stage never sees `.env`.

- [ ] **Step 1: Rewrite `.dockerignore`**

```
# Operator secrets and local state must never enter a build context: the Go stage COPYs source
# into an intermediate layer that lives on in the build host's cache and `docker history`.
.env
.env.*
*.pem

# Toolchain output and dependencies each stage installs or builds for itself.
node_modules
web/node_modules
dist
web/dist
test-results
playwright-report

# Not needed to build the binary.
.git
e2e
docs
```

- [ ] **Step 2: Narrow the build stage's COPY**

In `Dockerfile`, replace

```dockerfile
COPY . .
```

with

```dockerfile
# Only what `go build ./cmd/whenweall` needs — never `COPY . .`, which would sweep up whatever
# else sits in the checkout (an operator's .env, editor state) into this cached layer.
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
```

- [ ] **Step 3: Verify the image still builds and runs**

Run: `docker build -t whenweall:plan-b . && docker run --rm whenweall:plan-b version`
Expected: build succeeds; the container prints `dev`. Then `docker build --target build -t whenweall:plan-b-build . && docker run --rm --entrypoint sh whenweall:plan-b-build -c 'ls -a /src'` lists `cmd internal migrations go.mod go.sum` and no `.env`, `web` or `e2e`.

- [ ] **Step 4: compose `image:` and stop grace**

In `compose.yaml`, replace

```yaml
  app:
    build:
      context: .
    restart: unless-stopped
```

with

```yaml
  app:
    # Pulled from the registry .github/workflows/release.yml publishes — `docker compose pull app`
    # is the upgrade path (README "Ops"). `build:` stays so `docker compose up --build` still
    # produces the same image from a source checkout.
    image: ghcr.io/refsdal/whenweall:latest
    build:
      context: .
    # On SIGTERM the app closes every WebSocket with a GoingAway frame, clears its presence rows
    # and drains in-flight requests (up to 10 s). compose's default 10 s would SIGKILL mid-drain.
    stop_grace_period: 20s
    restart: unless-stopped
```

- [ ] **Step 5: README quickstart and Ops wording**

`README.md` lines 89–90, replace

```markdown
That pulls Postgres, builds (or pulls) the app image, runs migrations on boot, and starts
listening on `:3000`. Check it's alive:
```

with

```markdown
That pulls Postgres and the published app image (`ghcr.io/refsdal/whenweall:latest`; add
`--build` to build it from your checkout instead), runs migrations on boot, and starts
listening on `:3000`. Check it's alive:
```

In the Ops section, after the `docker compose pull app` / `docker compose up -d app` code block (line ~375), insert:

```markdown
Running from a source checkout instead of the published image? `docker compose up -d --build app`
rebuilds and restarts in one step; migrations still run themselves on boot.
```

- [ ] **Step 6: Validate compose**

Run: `POSTGRES_PASSWORD=x AUTH_SECRET=$(openssl rand -base64 32) SMTP_HOST=localhost docker compose config --quiet && echo valid`
Expected: `valid`.

- [ ] **Step 7: Commit**

```bash
git add .dockerignore Dockerfile compose.yaml README.md
git commit -m "fix(docker+compose): keep .env out of the build, publish image: for app, longer stop grace

.dockerignore did not exclude .env and the Go stage COPY'd the whole
checkout, so an operator's AUTH_SECRET/SMTP_PASSWORD landed in a cached
intermediate layer; the stage now copies cmd/, internal/ and migrations/
only. compose.yaml's app service gained image:
ghcr.io/refsdal/whenweall:latest (build: kept) so the README's
'docker compose pull app' upgrade actually pulls, plus a 20s
stop_grace_period for the WebSocket/presence drain on SIGTERM.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 14: Docs drift in one pass — spec amendment, captcha wording, dev `.env` loading, OAuth redirect URI + Vite dev origin, repo metadata, screenshots docs

Findings: "Design spec still lists magic links and TOTP 2FA as enabled Limen plugins" (and its two duplicates), "Captcha scope wording is wrong: 'poll creation' was never captcha-protected", "README dev quickstart relies on .env being loaded, but the Go binary reads only the process environment", "Stale repo metadata: dependabot groups for wrangler/better-auth (and no gomod ecosystem), PR template test scripts, SECURITY.md codeql workflow, .gitignore entries, e2e '.dev.vars' hints, one stale doc comment, placeholder dist text", "docs/screenshots/README.md and CONTRIBUTING claim committed PNGs that the README says (correctly) do not exist", "Sign-in wiring is consistent with Limen by source review; two operator/dev gaps remain (no documented redirect URI; Vite dev origin is untrusted)".

One code change rides along (the Vite dev origin), with its own test. Limen detail that shapes it: Limen's origin-check middleware is on by default but a no-op while its trusted-origins list is empty; the moment the list is non-empty it demands a trusted `Origin`/`Referer` on every mutating request — so a bare curl or this repo's own Go tests (which POST without `Origin`) would 403. Hence `WithHTTPTrustedOrigins` is paired with `WithHTTPOriginCheck(false)` in development only; our own `httpserver.CheckOrigin` already guards every mutating `/api/` request and Limen's CSRF protection stays on.

**Files:**
- Modify: `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (append amendment before `## Out of scope`)
- Modify: `internal/config/config.go:221-225`, `.env.example:20`
- Modify: `internal/auth/auth.go:209-248` (`httpConfigOptions`), `internal/auth/auth_test.go` (new test)
- Modify: `README.md` (Development section lines 407–413; Configuration table Google/OIDC rows lines 298–303)
- Modify: `.github/dependabot.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/SECURITY.md:44-45`, `.gitignore:4,7,8`
- Modify: `e2e/fixtures.ts:35,50-51,60`, `web/src/lib/notifications.ts:1-5`, `internal/httpserver/dist/index.html:3`
- Modify: `docs/screenshots/README.md`, `CONTRIBUTING.md:60-62`

**Interfaces:**
- Produces: in `APP_ENV=development`, Limen trusts `http://localhost:5173` as an OAuth `redirect_uri` origin.

- [ ] **Step 1: Write the failing auth test**

Append to `internal/auth/auth_test.go`:

```go
// TestOAuthAuthorizeTrustsViteDevOriginOnlyInDevelopment: under `bun dev` the SPA runs on Vite's
// :5173 and proxies /api to :3000, so GoogleButton sends redirect_uri=http://localhost:5173/...
// — which Limen's oauth plugin checks against IsTrustedOrigin (its base URL, i.e. APP_URL, plus
// WithHTTPTrustedOrigins). Development trusts the Vite origin; production trusts APP_URL alone.
func TestOAuthAuthorizeTrustsViteDevOriginOnlyInDevelopment(t *testing.T) {
	cfgFor := func(env string) *config.Config {
		return &config.Config{
			AppEnv:             env,
			AppURL:             "http://localhost:3000",
			LimenSecret:        make([]byte, 32),
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			Capabilities:       config.Capabilities{Google: true},
		}
	}
	const viteRedirect = "/api/v1/auth/oauth/google/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A5173%2Flogin"

	dev := newTestServiceWithConfig(t, cfgFor("development"))
	devResp := dev.get(t, viteRedirect)
	defer func() { _ = devResp.Body.Close() }()
	if devResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(devResp.Body)
		t.Fatalf("development: status %d, want 200 (Vite's origin must be a trusted redirect_uri): %s", devResp.StatusCode, body)
	}

	prod := newTestServiceWithConfig(t, cfgFor("production"))
	prodResp := prod.get(t, viteRedirect)
	defer func() { _ = prodResp.Body.Close() }()
	if prodResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(prodResp.Body)
		t.Fatalf("production: status %d, want 403 (only APP_URL's origin is trusted): %s", prodResp.StatusCode, body)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run TestOAuthAuthorizeTrustsViteDevOriginOnlyInDevelopment`
Expected: FAIL — `development: status 403, want 200 ... redirect_uri is not trusted`.

- [ ] **Step 3: Trust the Vite origin in development**

In `internal/auth/auth.go`, inside `httpConfigOptions`, directly before `return opts`, add:

```go
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
```

- [ ] **Step 4: Run the auth package**

Run: `go test ./internal/auth/`
Expected: `ok` — the new test and every existing one (they build their config with `AppEnv` unset, i.e. `""`, so the development branch does not apply to them; `internal/httpserver`'s tests use `config.Load`, which defaults `AppEnv` to `development` — confirm with `go test ./internal/httpserver/` that they still pass: they must, since the origin check is now off rather than on for them).

- [ ] **Step 5: Captcha wording**

In `internal/config/config.go`, replace

```go
	// Disabling the captcha is a real reduction in abuse protection on public, unauthenticated
	// endpoints (poll creation, voting). Fine for a private instance, worth saying out loud for
	// one on the open internet — so it is a warning in production and silent elsewhere.
	if !cfg.Capabilities.Turnstile && cfg.IsProduction {
		warnings = append(warnings, "Turnstile is not configured — captcha protection is OFF on public endpoints.")
	}
```

with

```go
	// Disabling the captcha is a real reduction in abuse protection on the public, unauthenticated
	// endpoints that demand one — guest voting, commenting, sign-up claims and booking (poll
	// creation always required a session and never had a captcha). Fine for a private instance,
	// worth saying out loud for one on the open internet — so it is a warning in production and
	// silent elsewhere.
	if !cfg.Capabilities.Turnstile && cfg.IsProduction {
		warnings = append(warnings, "Turnstile is not configured — captcha protection is OFF for guest voting, commenting, sign-up claims and booking.")
	}
```

In `.env.example`, replace line 20

```
# Captcha on public endpoints (poll creation, voting). Strongly recommended for a public instance.
```

with

```
# Captcha on guest voting, commenting, sign-up claims and booking (and, with Plan A's server-side
# check, sign-in/sign-up/password reset). Strongly recommended for a public instance.
```

Run: `go test ./internal/config/` — expected `ok` (no test asserts the old wording).

- [ ] **Step 6: README — Development section loads `.env`, Configuration documents the redirect URIs**

In `README.md`'s Development code block, replace

```bash
docker compose up -d db          # Postgres only — the app itself runs outside the container
cp .env.example .env             # then fill in AUTH_SECRET, SMTP_HOST, etc. for local use

go run ./cmd/whenweall           # migrates on boot, serves the API on :3000
```

with

```bash
docker compose up -d db          # Postgres only — the app itself runs outside the container
cp .env.example .env             # then fill in AUTH_SECRET, SMTP_HOST, POSTGRES_PASSWORD, etc.

# The binary reads only the process environment — nothing loads .env for you outside compose.
set -a; . ./.env; set +a
export DATABASE_URL="postgres://whenweall:${POSTGRES_PASSWORD}@localhost:${POSTGRES_PORT:-5433}/whenweall?sslmode=disable"
export APP_ENV=development
go run ./cmd/whenweall           # migrates on boot, serves the API on :3000
```

and after that code block's closing fence add a paragraph:

```markdown
`go run ./cmd/whenweall migrate` and `... create-staff-user` read the same variables, so run them
from the same shell (or prefix them the same way). With `APP_ENV=development` the server also
trusts `http://localhost:5173` as an OAuth `redirect_uri` origin, so "Continue with Google" works
from the Vite dev server too.
```

In the Configuration table, replace the `GOOGLE_CLIENT_ID` row's Purpose cell text `Optional "Continue with Google" and Google Calendar sync. Needs `GOOGLE_CLIENT_SECRET` too.` with:

```markdown
Optional "Continue with Google". Needs `GOOGLE_CLIENT_SECRET` too. Register `<APP_URL>/api/v1/auth/oauth/google/callback` as the **Authorized redirect URI** in Google Cloud Console — a mismatch there is the single most common OAuth misconfiguration.
```

and the `OIDC_ISSUER` row's Purpose cell `Optional external SSO. Needs `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` too (all three, not a pair).` with:

```markdown
Optional external SSO. Needs `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` too (all three, not a pair). Register `<APP_URL>/api/v1/auth/oauth/<OIDC_NAME>/callback` (default `.../oauth/sso/callback`) as the redirect URI at your provider.
```

(The Google Calendar scopes paragraph further down is Plan D's to reword under the "disabled for now" decision — leave it.)

- [ ] **Step 7: Repo metadata**

Replace `.github/dependabot.yml` entirely:

```yaml
version: 2
updates:
  # The Go module — Limen is pinned to one pseudo-version across core, adapter and plugins, so its
  # updates are grouped to land (or be declined) together.
  - package-ecosystem: gomod
    directory: /
    schedule: { interval: weekly, day: monday }
    open-pull-requests-limit: 10
    groups:
      minor-and-patch:
        update-types: [minor, patch]
      limen: { patterns: ['github.com/thecodearcher/limen*'] }
  # The SPA.
  - package-ecosystem: npm
    directory: /web
    schedule: { interval: weekly, day: monday }
    open-pull-requests-limit: 10
    groups:
      minor-and-patch:
        update-types: [minor, patch]
      tanstack: { patterns: ['@tanstack/*'] }
  # The root package.json is Playwright only (the e2e suite).
  - package-ecosystem: npm
    directory: /
    schedule: { interval: weekly, day: monday }
    groups:
      playwright: { patterns: ['@playwright/*', 'playwright'] }
  - package-ecosystem: github-actions
    directory: /
    schedule: { interval: weekly }
  # Dockerfile base images (golang, oven/bun).
  - package-ecosystem: docker
    directory: /
    schedule: { interval: weekly }
```

Replace the Test plan section of `.github/PULL_REQUEST_TEMPLATE.md`:

```markdown
## Test plan

- [ ] Go: `go test ./... && go vet ./... && golangci-lint run ./... && sqlc diff`
- [ ] Web: `cd web && bun run typecheck && bun run lint && bunx vitest run`
- [ ] End-to-end (for user-visible changes): `bunx playwright test`
```

In `.github/SECURITY.md`, replace

```markdown
- CodeQL default setup: **off** — this repository runs its own CodeQL
  workflow (`.github/workflows/codeql.yml`) instead.
```

with

```markdown
- CodeQL: GitHub's default setup — this repository does not ship a CodeQL
  workflow of its own.
```

In `.gitignore`, delete the three lines `dist-ssr`, `.tanstack` and `__unconfig*` (TanStack Start / Vite-SSR leftovers).

- [ ] **Step 8: Stale hints and comments**

`e2e/fixtures.ts` line 35 — replace

```ts
      `POST /api/test/seed responded ${response.status()} — is ENABLE_TEST_ROUTES=true in .dev.vars?`,
```

with

```ts
      `POST /api/test/seed responded ${response.status()} — is the server running with ENABLE_TEST_ROUTES=true (playwright.config.ts's webServer.env sets it)?`,
```

lines 50–51 — replace `` * Europe/Oslo, 30-minute slots, slug `intro-call` — see `sampleBookingPage` in `` / `` * `src/routes/api/test/seed.ts`. `` with

```ts
   * Europe/Oslo, 30-minute slots, slug `intro-call` — see the seed route in
   * `internal/httpserver/testroutes.go`.
```

line 60 — replace `` * non-production builds with `ENABLE_TEST_ROUTES=true`, see `.dev.vars.example`), so specs never `` with

```ts
 * non-production builds with `ENABLE_TEST_ROUTES=true`, set by `playwright.config.ts`'s
 * `webServer.env`), so specs never
```

`web/src/lib/notifications.ts` lines 1–5 — replace the header comment with:

```ts
/**
 * Browser-safe notification catalogue: the event names, digest grouping and per-poll preference
 * grid shape the settings UI edits (`components/notifications/NotificationGrid.tsx`) and the API
 * client sends (`api/polls.ts`) to the Go backend's notification_prefs endpoint. Pure data plus
 * zod schemas — no React, no API-client imports — which is why it lives in `lib/`.
 */
```

`internal/httpserver/dist/index.html` — replace line 3:

```html
<p>whenweall backend is running, but this binary was built without the web app. Run <code>cd web &amp;&amp; bun run build</code> and copy <code>web/dist</code> over <code>internal/httpserver/dist</code> before <code>go build</code> — the Dockerfile does this for you.</p>
```

- [ ] **Step 9: Screenshots docs**

Replace `docs/screenshots/README.md` entirely:

```markdown
# Screenshots

No screenshots are committed to this repository (see the root README's "Screenshots" section) —
there is no hosted instance to keep them honest against, and CI never generates them. This
directory holds only this note; `bun run screenshots` writes its output here, git-ignored.

To generate a set locally, from the running app:

```bash
bunx playwright install --with-deps chromium   # once
bun run screenshots
```

That runs [`e2e/screenshots.spec.ts`](../../e2e/screenshots.spec.ts) (with `SCREENSHOTS=1`, which
opts it back into the Playwright run — it is excluded from `bunx playwright test` and CI via
`testIgnore` in `playwright.config.ts`). It seeds a user and a poll through the test-only seed
route, drives the real UI, and captures at 1280×800:

| File                | What it shows                                                |
| ------------------- | ------------------------------------------------------------ |
| `landing-light.png` | The landing page, light theme                                |
| `landing-dark.png`  | The landing page, dark theme                                 |
| `poll.png`          | A poll with three participants voting, seen by the organiser |
| `creator.png`       | Step 2 of the poll creator, with two days picked             |
| `dashboard.png`     | The organiser's poll list                                    |
| `signup.png`        | A sign-up sheet with a claimed slot, seen by the organiser   |
| `booking.png`       | A public 1:1 booking page, with a day picked and slots shown |

Use them in a blog post, a release note, or your own fork's README — just do not commit them here.
```

Then add to `.gitignore` (under the `# Screenshots`-free Playwright block) the line:

```
docs/screenshots/*.png
```

In `CONTRIBUTING.md`, replace

```markdown
If your change alters the UI in a way the README screenshots show, regenerate them with
`bun run screenshots` and include the updated PNGs — see
[docs/screenshots/README.md](./docs/screenshots/README.md).
```

with

```markdown
No screenshots are committed to this repository. If you want to check a UI change visually the
way the README describes the product, `bun run screenshots` generates a local set from the running
app (git-ignored) — see [docs/screenshots/README.md](./docs/screenshots/README.md).
```

- [ ] **Step 10: The spec amendment**

In `docs/superpowers/specs/2026-09-01-go-rewrite-design.md`, insert before `## Out of scope`:

```markdown
## Amendment (2026-09-03, completion pass): what shipped differently

Recorded after the parity audit of PR #67 so this spec describes the code it approved. Each item
supersedes the earlier text it names; the completion plans
`docs/superpowers/plans/2026-09-03-complete-0*.md` implement them.

- **Magic links and TOTP 2FA are not mounted** (decisions table, "Auth library" row; §3 bullets 1
  and "Rate limiting"). Commit 72a8306 removed Limen's `magic-link` and `two-factor` plugins:
  neither ever had a UI, and magic-link auto-created an account on first verify
  (`autoCreateUser` default), bypassing the credential sign-up's validation on every deployment
  that mounted it. Limen features in use: email+password (verification, password reset), Google
  OAuth, generic OIDC, sessions, organizations. `WithSendMagicLink` is not wired; the Postgres
  rate limiter wraps sign-in, sign-up and password-reset requests only. Migration
  `00010_drop_two_factor.sql` removes the plugin's leftover schema (`two_factors`,
  `users.two_factor_enabled`).
- **Email verification gates the app** (§3 bullet 1 implied it; the first cut lost it). An
  unverified credential account can sign in, but every API route except `GET /api/v1/auth/me`,
  `POST .../signout`, `POST .../verify-email`, `POST .../email-verifications` and
  `GET /api/v1/config` answers 403 `email_unverified`; the SPA shows the "verify your address"
  card with a resend button. OAuth-created users count as verified.
- **Per-user locale is persisted** in `user_preferences(user_id, locale, updated_at)`
  (migration `00009_user_profile.sql`); guest forms send `locale`; mail renders in `en` or `nb`
  with locale-aware dates (§5 "Email localization keeps parity with today" — this is how).
- **Google Calendar sync is disabled for now** (decisions table, "Kept features"). The Go sync
  code (`internal/bookings/google.go`) stays, but Limen's OAuth link route cannot request the
  incremental calendar scopes, so the UI is hidden behind the capability flag, status always
  reports "not connected", and the README says the feature is not yet available. Re-enabling
  means a second Limen Google provider configured with the calendar scopes and
  `access_type=offline` — not a custom consent flow.
- **Playwright runs twice in CI** (§9): against `go run` (the existing fast job) and against the
  built Docker image started with compose's hardening flags (`read_only`, `cap_drop: [ALL]`,
  `no-new-privileges`) — the run that "keeps the hardening claims honest".
- **Security headers** (§7 said nothing; the old `src/start.ts` set them): every response carries
  a Content-Security-Policy whose `script-src` allows `'self'`, Turnstile's origin and one
  `sha256-` hash per inline `<script>` in the embedded `index.html` (computed once at boot — no
  `'unsafe-inline'` for scripts), `Permissions-Policy: camera=(), microphone=(), geolocation=()`,
  and `Strict-Transport-Security` when `APP_URL` is https. `/api/` request bodies are capped at
  1 MiB (413 `payload_too_large`); the server has a 30 s `ReadTimeout`.
- **Graceful shutdown** (§4/§7 said nothing): on SIGTERM the hub closes every WebSocket with a
  `GoingAway` close frame, waits for the handlers, deletes this replica's `ws_presence` rows, and
  the process waits for the hub and the job worker to unwind before closing the pool.
  `compose.yaml` gives it a 20 s `stop_grace_period`.
- **Landing stats have a REST read**: `GET /api/v1/stats` returns the `UsageStats` snapshot for
  first paint; the stats WebSocket remains the live source (§4 "Stats room").
- **Toolchain line: Go 1.26** (§7 `golang:1.25-alpine` superseded) — go.mod `go 1.26`,
  `golang:1.26-alpine`, CI `1.26`. The 1.25 line left Go's support window when 1.27 shipped.
- **Rate limiting** (decisions table, "Billing": abuse control = rate limiting only): poll
  creation/duplication and booking-page creation are limited at 20/min per IP alongside the
  existing vote/comment/claim/book/ws-connect buckets; every public limiter is a pass-through
  under `ENABLE_TEST_ROUTES` so the Playwright harness's single IP never trips one.
```

- [ ] **Step 11: Run the full gates**

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./... && (cd web && bun run typecheck && bun run lint && bunx vitest run)`
Expected: every command exits 0; `go test ./...` prints `ok` for every package (no `FAIL`, and — per Task 2 — no skips hidden in `ok` if Docker is up). Then `bunx playwright test` — expected: the suite passes as before (the e2e server runs with `ENABLE_TEST_ROUTES=true`, so Task 6's pass-through applies; `APP_ENV=test`, so Task 14's development branch does not).

- [ ] **Step 12: Commit**

```bash
git add docs/superpowers/specs/2026-09-01-go-rewrite-design.md internal/config/config.go .env.example internal/auth/auth.go internal/auth/auth_test.go README.md .github/dependabot.yml .github/PULL_REQUEST_TEMPLATE.md .github/SECURITY.md .gitignore e2e/fixtures.ts web/src/lib/notifications.ts internal/httpserver/dist/index.html docs/screenshots/README.md CONTRIBUTING.md
git commit -m "docs: spec amendment for the completion pass; fix drift across README, metadata, hints

Dated spec amendment records what shipped differently (magic links/TOTP
dropped in 72a8306, verification gate, user_preferences, Google
Calendar parked, two Playwright runs, security headers, shutdown, Go
1.26). Captcha wording no longer claims poll creation was protected;
README's dev quickstart exports .env into the shell the binary actually
reads and documents the OAuth redirect URIs; APP_ENV=development trusts
http://localhost:5173 as an OAuth redirect origin (with Limen's
list-triggered origin check off — our CheckOrigin covers /api/);
dependabot watches gomod/web npm/docker instead of wrangler and
better-auth; PR template, SECURITY.md, .gitignore, e2e hints, the
notifications.ts header, the dist placeholder and the screenshots docs
describe the repo as it is.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

## Self-review

**Spec/scope coverage** (each brief item → task): 1 CSP/HSTS/Permissions-Policy → Task 3; 2 body cap + ReadTimeout → Task 4; 3 `/healthz` → Task 5; 4 `.dockerignore` + narrowed COPY → Task 13; 5 compose `image:` + README → Task 13; 6 shutdown + presence → Task 9; 7 LISTEN liveness → Task 8; 8 two-factor migration + sqlc + CI diff + limen doc → Task 10; 9 testdb fail-fast + Mailpit → Task 2; 10 `-race` filter, README, stale comments, concurrent `ClaimDue`, goose Down + concurrent Migrate → Tasks 11 and 12; 11 creation rate limits + test-routes pass-through → Task 6; 12 Go 1.26 → Task 1; 13 docs drift (amendment, captcha wording, `.env`, metadata, screenshots, OAuth redirect URI + dev origin) → Task 14; 14 stats REST + loader → Task 7. Contract "Plan B PRODUCES" bullets all appear as task Interfaces.

**Placeholder scan:** every code step carries the full function/test/YAML/Markdown it asks for; no "TBD", no "similar to Task N", no "add validation". Task 11 Step 4's "fix the offending Down" is conditional on a concrete error message goose prints and names the exact edit class.

**Type/name consistency:** `SecurityPolicy{CSP, HSTS}` / `BuildSecurityPolicy(appURL, indexHTML)` / `InlineScriptHashes` / `EmbeddedIndexHTML` / `SecurityHeaders(policy)` are identical in Task 3's code, tests, `middleware_test.go` and Task 14's amendment. `PublicRateLimit(db, cfg, namespace, name, limit, window)` matches in Task 6's implementation, httpserver tests and all three call sites. `Hub.ListenIdleTimeout`/`ListenPingTimeout` match between `hub.go`, `NewHub`, `listener.go` and the test. `trackConn`/`untrackConn`/`closeAllConns`/`shutdownGrace`/`presenceShutdownSweep`/`runListener`/`shutdown` are used with the same names across Task 9's steps. `db.MigrateDownTo(ctx, sqlDB, version int64)` matches Task 11's tests. `testdb.Unavailable(t, what, err)` matches Task 2's three call sites. `loadLandingStats`/`getUsageStats` match between `stats.ts`, its test and `index.tsx`.

**Ordering / green tree:** Task 3 changes `SecurityHeaders`' signature and fixes its one external caller (`middleware_test.go`) in the same task; Task 6 changes `PublicRateLimit`'s signature and updates all three call sites in the same task; Task 9 restructures `Run` without changing its signature; Task 10's `sqlc generate` and the admin edit land in one commit so `go build` never sees a `User` model referencing a dropped column while `two_factors` is still deleted from.

## Out of scope for this plan (owned elsewhere)

- Limen's built-in rate limiter `KeyGenerator` (raw `X-Forwarded-For`) — Plan A.
- README's Google Calendar consent paragraph — Plan D under the "disabled for now" decision; the spec amendment here records the decision.
- The Playwright-against-the-image CI job — Plan F.
- A detached grace context for a job in flight at shutdown: `serve()` now waits for `worker.Run` to return, but the job's ctx is still cancelled (documented in Task 9 Step 8 — at-least-once semantics and the 5-minute lock expiry cover it; a two-minute `JobTimeout` must not hold SIGTERM hostage).
