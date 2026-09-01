# Go Rewrite Plan 1/8 — Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A hardened scratch container that boots, validates config, migrates Postgres, and serves an embedded placeholder SPA with `/healthz`.

**Architecture:** Single Go binary (`cmd/whenweall`) with `internal/` packages: config loading (port of `src/config.ts`), pgx-backed `database/sql` pool, embedded goose migrations run under an advisory lock, an `net/http` server with security middleware and SPA fallback, and a testcontainers-go harness every later plan reuses.

**Tech Stack:** Go 1.25, `jackc/pgx/v5` (stdlib driver), `pressly/goose/v3`, `testcontainers-go`, Docker multi-stage → `FROM scratch`.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md`

## Global Constraints (all 8 plans)

- Module path: `github.com/refsdal/whenweall`. Go **1.25** (Limen's floor).
- Build always `CGO_ENABLED=0`; release flags `-trimpath -ldflags "-s -w"`.
- No web framework, no ORM, no Redis. Postgres is the only stateful service.
- All times `timestamptz`, all JSON columns `jsonb` (spec §6 baseline re-cut).
- IDs are `text` primary keys generated app-side via `crypto/rand` (nanoid-style, 21 chars, alphabet `A-Za-z0-9_-`) — matches existing TS `nanoid` IDs.
- Config env names are frozen by `compose.yaml`/spec: `APP_URL`, `APP_ENV`, `PORT`, `DATABASE_URL`, `DATABASE_POOL_SIZE`, `AUTH_SECRET`, `SMTP_HOST/PORT/USER/PASSWORD/SECURE`, `EMAIL_FROM`, `TURNSTILE_SITE_KEY/SECRET_KEY`, `GOOGLE_CLIENT_ID/SECRET`, `OIDC_ISSUER/CLIENT_ID/CLIENT_SECRET/NAME`, `ENABLE_TEST_ROUTES`, `TRUST_PROXY`, `MIGRATE_ON_BOOT`.
- Tests: `go test ./...` with real Postgres via `internal/testdb` (testcontainers). Never mock the database.
- Every commit message follows the repo's conventional-commit style (`feat(scope): …`).
- The old TypeScript app under `src/` stays in place and untouched until plan 8 (it is the behavioral reference for ports); `go` code and `src/` coexist on the branch meanwhile.

---

### Task 1: Go module and repo scaffolding

**Files:**
- Create: `go.mod`, `.golangci.yml`
- Modify: `.gitignore` (add `dist-go/`, `.limen/`)

**Interfaces:**
- Produces: module path `github.com/refsdal/whenweall` that every later import uses.

- [ ] **Step 1: Initialize the module**

```bash
go mod init github.com/refsdal/whenweall
go mod edit -go=1.25
```

- [ ] **Step 2: Add golangci config**

`.golangci.yml`:

```yaml
version: "2"
linters:
  default: standard
  enable:
    - errcheck
    - govet
    - staticcheck
    - ineffassign
    - unused
```

- [ ] **Step 3: Verify toolchain**

Run: `go vet ./...`
Expected: exits 0 (no packages yet is fine).

- [ ] **Step 4: Commit**

```bash
git add go.mod .golangci.yml .gitignore
git commit -m "chore(go): initialize module github.com/refsdal/whenweall"
```

---

### Task 2: internal/config — validated boot config

Port of `src/config.ts` (read it first — its comments explain each rule). Same semantics: fail loudly on invalid, warn-and-disable on half-configured optional pairs, forbid `ENABLE_TEST_ROUTES` in production. Stripe vars are gone; OIDC vars are new.

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:

```go
type Capabilities struct{ Turnstile, Google, OIDC bool }

type Config struct {
    AppEnv           string // development | test | production
    AppURL           string // absolute http(s) URL, no trailing slash
    Port             int    // default 3000
    DatabaseURL      string
    DatabasePoolSize int // default 10, max 100
    AuthSecret       string // >= 32 chars
    LimenSecret      []byte // sha256(AuthSecret) — exactly 32 bytes, Limen requires this
    SMTPHost         string // required
    SMTPPort         int    // default 587
    SMTPUser         string
    SMTPPassword     string
    SMTPSecure       bool
    EmailFrom        string // default "whenweall <no-reply@localhost>"
    TurnstileSiteKey, TurnstileSecretKey   string
    GoogleClientID, GoogleClientSecret     string
    OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCName string // OIDCName default "sso"
    EnableTestRoutes bool
    TrustProxy       bool // default true
    MigrateOnBoot    bool // default true
    Capabilities     Capabilities
    IsProduction     bool
}

// Load parses and validates. warnings are human-readable lines the caller logs.
func Load(env map[string]string) (cfg *Config, warnings []string, err error)

// FromOS wraps Load(envToMap(os.Environ())).
func FromOS() (*Config, []string, error)
```

- Validation rules (each is one error line; collect all and return them joined, like the zod version):
  - `APP_URL` required, absolute `http(s)` URL.
  - `DATABASE_URL` required, non-empty.
  - `AUTH_SECRET` required, ≥ 32 chars, error text mentions `openssl rand -base64 32`.
  - `SMTP_HOST` required, error text: "SMTP_HOST is required — whenweall cannot function without e-mail".
  - `ENABLE_TEST_ROUTES=true` + `APP_ENV=production` → hard error (same rationale comment as TS).
  - Bool envs accept `true`/`1` as true; empty/unset means default. Set-but-blank optional strings are treated as unset (the `optionalNonEmpty` rule — copy its comment).
  - Capabilities: `Turnstile` and `Google` need both their vars (half-set → warning + off). `OIDC` needs all three of issuer/id/secret.
  - Production without Turnstile → warning line about captcha being off.

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go` — table-driven; a `valid()` helper returns a minimal valid env map that each case mutates:

```go
package config

import (
    "strings"
    "testing"
)

func valid() map[string]string {
    return map[string]string{
        "APP_URL":      "https://whenweall.example",
        "DATABASE_URL": "postgres://x@localhost/x",
        "AUTH_SECRET":  strings.Repeat("s", 32),
        "SMTP_HOST":    "smtp.example.com",
    }
}

func TestLoadMinimalValid(t *testing.T) {
    cfg, warnings, err := Load(valid())
    if err != nil { t.Fatal(err) }
    if cfg.Port != 3000 { t.Errorf("Port = %d, want 3000", cfg.Port) }
    if cfg.SMTPPort != 587 { t.Errorf("SMTPPort = %d, want 587", cfg.SMTPPort) }
    if !cfg.TrustProxy || !cfg.MigrateOnBoot { t.Error("TrustProxy/MigrateOnBoot should default true") }
    if len(cfg.LimenSecret) != 32 { t.Errorf("LimenSecret len = %d, want 32", len(cfg.LimenSecret)) }
    if cfg.Capabilities.Google || cfg.Capabilities.Turnstile || cfg.Capabilities.OIDC {
        t.Error("no optional capability should be on")
    }
    _ = warnings
}

func TestLoadCollectsAllErrors(t *testing.T) {
    _, _, err := Load(map[string]string{})
    if err == nil { t.Fatal("want error") }
    for _, needle := range []string{"APP_URL", "DATABASE_URL", "AUTH_SECRET", "SMTP_HOST"} {
        if !strings.Contains(err.Error(), needle) {
            t.Errorf("error should mention %s; got %q", needle, err.Error())
        }
    }
}

func TestHalfConfiguredPairIsOffWithWarning(t *testing.T) {
    env := valid()
    env["GOOGLE_CLIENT_ID"] = "id-only"
    cfg, warnings, err := Load(env)
    if err != nil { t.Fatal(err) }
    if cfg.Capabilities.Google { t.Error("google must stay off") }
    found := false
    for _, w := range warnings { if strings.Contains(w, "Google") { found = true } }
    if !found { t.Errorf("want a Google warning, got %v", warnings) }
}

func TestSetButBlankOptionalIsUnset(t *testing.T) {
    env := valid()
    env["GOOGLE_CLIENT_ID"] = ""
    env["GOOGLE_CLIENT_SECRET"] = ""
    cfg, warnings, err := Load(env)
    if err != nil { t.Fatal(err) }
    if cfg.Capabilities.Google { t.Error("blank vars must not enable google") }
    if len(warnings) != 0 { t.Errorf("blank pair should not warn: %v", warnings) }
}

func TestTestRoutesForbiddenInProduction(t *testing.T) {
    env := valid()
    env["APP_ENV"] = "production"
    env["ENABLE_TEST_ROUTES"] = "true"
    if _, _, err := Load(env); err == nil { t.Fatal("want error") }
}

func TestAppURLTrailingSlashStripped(t *testing.T) {
    env := valid()
    env["APP_URL"] = "https://whenweall.example/"
    cfg, _, err := Load(env)
    if err != nil { t.Fatal(err) }
    if cfg.AppURL != "https://whenweall.example" { t.Errorf("AppURL = %q", cfg.AppURL) }
}

func TestShortAuthSecretRejected(t *testing.T) {
    env := valid()
    env["AUTH_SECRET"] = "short"
    if _, _, err := Load(env); err == nil { t.Fatal("want error") }
}

func TestOIDCNeedsAllThree(t *testing.T) {
    env := valid()
    env["OIDC_ISSUER"] = "https://id.example.com"
    env["OIDC_CLIENT_ID"] = "abc"
    cfg, _, err := Load(env)
    if err != nil { t.Fatal(err) }
    if cfg.Capabilities.OIDC { t.Error("OIDC must stay off without client secret") }
    env["OIDC_CLIENT_SECRET"] = "xyz"
    cfg, _, err = Load(env)
    if err != nil { t.Fatal(err) }
    if !cfg.Capabilities.OIDC { t.Error("OIDC should be on") }
    if cfg.OIDCName != "sso" { t.Errorf("OIDCName default = %q, want sso", cfg.OIDCName) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL — package doesn't compile (`Load` undefined).

- [ ] **Step 3: Implement `internal/config/config.go`**

Plain Go, no validation library. Shape:

```go
package config

import (
    "crypto/sha256"
    "fmt"
    "net/url"
    "os"
    "strconv"
    "strings"
)

// (carry over the "single source of application configuration" doc comment from src/config.ts)

func Load(env map[string]string) (*Config, []string, error) {
    var errs, warnings []string
    get := func(k string) string { return strings.TrimSpace(env[k]) }
    // optional: present-and-non-empty or unset (see src/config.ts optionalNonEmpty comment)
    opt := func(k string) string { return get(k) }
    boolEnv := func(k string, def bool) bool {
        v := get(k)
        if v == "" { return def }
        return v == "true" || v == "1"
    }
    intEnv := func(k string, def int) int {
        v := get(k)
        if v == "" { return def }
        n, err := strconv.Atoi(v)
        if err != nil || n <= 0 { errs = append(errs, fmt.Sprintf("%s must be a positive integer", k)); return def }
        return n
    }

    cfg := &Config{ /* defaults, then fill from env via the helpers */ }
    // ... each required-field check appends to errs with the exact messages from the tests ...
    // AppURL: url.Parse + scheme http/https check; strings.TrimSuffix(u.String(), "/")
    // LimenSecret: sum := sha256.Sum256([]byte(cfg.AuthSecret)); cfg.LimenSecret = sum[:]
    // capabilities via a pair() helper mirroring deriveCapabilities() in src/config.ts,
    //   including its warning wording; OIDC is a triple, not a pair.
    if len(errs) > 0 {
        return nil, nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
    }
    return cfg, warnings, nil
}
```

`FromOS()` splits `os.Environ()` on the first `=` into the map and calls `Load`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): validated boot config, ported from src/config.ts"
```

---

### Task 3: migrations package + infra baseline + db.Open/Migrate

**Files:**
- Create: `migrations/migrations.go`, `migrations/00001_infra.sql`
- Create: `internal/db/db.go`
- Test: (covered by Task 4's harness — this task ends at compile + goose validate)

**Interfaces:**
- Produces:

```go
// package migrations
//go:embed *.sql
var FS embed.FS

// package db
func Open(ctx context.Context, databaseURL string, poolSize int) (*sql.DB, error)
func Migrate(ctx context.Context, sqlDB *sql.DB) error // goose up on migrations.FS under advisory lock 727272
// DBTX is the query surface shared by *sql.DB and *sql.Tx (same shape sqlc generates against).
type DBTX interface {
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    QueryContext(context.Context, string, ...any) (*sql.Rows, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}
func NewID() string // 21-char nanoid-style, crypto/rand, alphabet A-Za-z0-9_-
```

- [ ] **Step 1: Write `migrations/00001_infra.sql`**

The infra tables from the Bun-branch design (`drizzle/0001_great_blazing_skull.sql` + `0002_rate_limits_unlogged.sql`), carried over unchanged except that `push_subscriptions` is dropped (web push is out of scope). The dead-letter state is `attempts >= max_attempts` — no status column, matching `src/server/jobs/scheduler.ts`. Copy the UNLOGGED rationale comment from `drizzle/0002` verbatim.

```sql
-- +goose Up
CREATE UNLOGGED TABLE rate_limits (
  key text PRIMARY KEY,
  count integer NOT NULL,
  reset_at timestamptz NOT NULL
);
CREATE INDEX rate_limits_reset_idx ON rate_limits (reset_at);

CREATE TABLE room_events (
  id bigserial PRIMARY KEY,
  room_key text NOT NULL,
  event jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX room_events_room_id_idx ON room_events (room_key, id);
CREATE INDEX room_events_created_idx ON room_events (created_at);

CREATE TABLE room_state (
  room_key text PRIMARY KEY,
  data jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scheduled_jobs (
  id text PRIMARY KEY,
  kind text NOT NULL,
  room_key text,
  run_at timestamptz NOT NULL,
  payload jsonb,
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 5,
  locked_by text,
  locked_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scheduled_jobs_due_idx ON scheduled_jobs (run_at);
CREATE UNIQUE INDEX scheduled_jobs_room_kind_idx ON scheduled_jobs (kind, room_key) WHERE room_key IS NOT NULL;

CREATE TABLE ws_presence (
  room_key text NOT NULL,
  replica_id text NOT NULL,
  count integer NOT NULL,
  heartbeat_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (room_key, replica_id)
);
CREATE INDEX ws_presence_heartbeat_idx ON ws_presence (heartbeat_at);

-- +goose Down
DROP TABLE ws_presence;
DROP TABLE scheduled_jobs;
DROP TABLE room_state;
DROP TABLE room_events;
DROP TABLE rate_limits;
```

- [ ] **Step 2: Write `migrations/migrations.go`**

```go
// Package migrations embeds the goose SQL migrations so the binary migrates itself.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 3: Write `internal/db/db.go`**

```go
package db

import (
    "context"
    "crypto/rand"
    "database/sql"
    "fmt"

    _ "github.com/jackc/pgx/v5/stdlib"
    "github.com/pressly/goose/v3"

    "github.com/refsdal/whenweall/migrations"
)

func Open(ctx context.Context, databaseURL string, poolSize int) (*sql.DB, error) {
    d, err := sql.Open("pgx", databaseURL)
    if err != nil { return nil, err }
    d.SetMaxOpenConns(poolSize)
    d.SetMaxIdleConns(poolSize)
    if err := d.PingContext(ctx); err != nil {
        d.Close()
        return nil, fmt.Errorf("database unreachable: %w", err)
    }
    return d, nil
}

// migrationLock is an arbitrary fixed key: replicas booting together must not race goose.
const migrationLock = 727272

func Migrate(ctx context.Context, sqlDB *sql.DB) error {
    conn, err := sqlDB.Conn(ctx)
    if err != nil { return err }
    defer conn.Close()
    if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil { return err }
    defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock)

    goose.SetBaseFS(migrations.FS)
    if err := goose.SetDialect("postgres"); err != nil { return err }
    return goose.UpContext(ctx, sqlDB, ".")
}

const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

func NewID() string {
    b := make([]byte, 21)
    if _, err := rand.Read(b); err != nil { panic(err) } // crypto/rand failing means the host is broken
    for i := range b { b[i] = idAlphabet[int(b[i])&63] }
    return string(b)
}
```

(DBTX as declared in Interfaces, same file.)

- [ ] **Step 4: Compile**

Run: `go get github.com/jackc/pgx/v5 github.com/pressly/goose/v3 && go build ./...`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add migrations internal/db go.mod go.sum
git commit -m "feat(db): pgx pool, embedded goose migrations, infra baseline"
```

---

### Task 4: internal/testdb — testcontainers harness

Every DB test in every later plan calls `testdb.New(t)`. One Postgres container per `go test` invocation (shared via `sync.Once` + testcontainers' Ryuk reaper for cleanup), one fully-migrated **template** database, then each call clones the template — `CREATE DATABASE ... TEMPLATE ...` is milliseconds.

**Files:**
- Create: `internal/testdb/testdb.go`
- Test: `internal/db/db_test.go` (proves harness + migrations together)

**Interfaces:**
- Produces:

```go
// New returns an isolated *sql.DB on a freshly cloned, fully migrated database.
// Skips the test (t.Skip) if Docker is unavailable. Closes and drops on t.Cleanup.
func New(t *testing.T) *sql.DB
// URL returns the connection string of the clone backing db from New — rooms tests need it for LISTEN.
func URL(t *testing.T) (dbURL string, sqlDB *sql.DB)
```

- [ ] **Step 1: Write the failing test**

`internal/db/db_test.go`:

```go
package db_test

import (
    "context"
    "testing"

    "github.com/refsdal/whenweall/internal/testdb"
)

func TestMigrationsCreateInfraTables(t *testing.T) {
    d := testdb.New(t)
    for _, table := range []string{"rate_limits", "room_events", "room_state", "scheduled_jobs", "ws_presence"} {
        var n int
        err := d.QueryRowContext(context.Background(),
            "SELECT count(*) FROM information_schema.tables WHERE table_name = $1", table).Scan(&n)
        if err != nil || n != 1 {
            t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
        }
    }
    // rate_limits must be UNLOGGED (relpersistence 'u')
    var persistence string
    if err := d.QueryRowContext(context.Background(),
        "SELECT relpersistence FROM pg_class WHERE relname = 'rate_limits'").Scan(&persistence); err != nil {
        t.Fatal(err)
    }
    if persistence != "u" { t.Errorf("rate_limits relpersistence = %q, want u", persistence) }
}

func TestClonesAreIsolated(t *testing.T) {
    a, b := testdb.New(t), testdb.New(t)
    ctx := context.Background()
    if _, err := a.ExecContext(ctx, "INSERT INTO room_state (room_key) VALUES ('x')"); err != nil { t.Fatal(err) }
    var n int
    if err := b.QueryRowContext(ctx, "SELECT count(*) FROM room_state").Scan(&n); err != nil { t.Fatal(err) }
    if n != 0 { t.Errorf("clone b sees %d rows from clone a", n) }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/`
Expected: FAIL — `testdb` undefined.

- [ ] **Step 3: Implement `internal/testdb/testdb.go`**

```go
// Package testdb provides per-test isolated Postgres databases.
//
// One postgres:18-alpine container serves the whole `go test` run. A template database is
// migrated once; each New(t) clones it, which costs milliseconds. Clones are dropped in
// t.Cleanup, and the container itself is reaped by testcontainers when the run exits.
package testdb

import (
    "context"
    "database/sql"
    "fmt"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/testcontainers/testcontainers-go"
    tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
    "github.com/testcontainers/testcontainers-go/wait"

    "github.com/refsdal/whenweall/internal/db"
)

var (
    once     sync.Once
    baseURL  string // connection URL to the container's postgres database
    initErr  error
    seq      atomic.Int64
)

const template = "whenweall_template"

func setup() {
    ctx := context.Background()
    ctr, err := tcpostgres.Run(ctx, "postgres:18-alpine",
        tcpostgres.WithDatabase("postgres"),
        tcpostgres.WithUsername("test"),
        tcpostgres.WithPassword("test"),
        testcontainers.WithWaitStrategy(
            wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
    )
    if err != nil { initErr = err; return }
    baseURL, initErr = ctr.ConnectionString(ctx, "sslmode=disable")
    if initErr != nil { return }

    admin, err := db.Open(ctx, baseURL, 2)
    if err != nil { initErr = err; return }
    defer admin.Close()
    if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+template); err != nil { initErr = err; return }

    tmpl, err := db.Open(ctx, urlFor(template), 2)
    if err != nil { initErr = err; return }
    defer tmpl.Close()
    initErr = db.Migrate(ctx, tmpl)
}

func urlFor(name string) string {
    // baseURL ends in "/postgres?sslmode=disable" — swap the database name.
    return strings.Replace(baseURL, "/postgres?", "/"+name+"?", 1)
}

func URL(t *testing.T) (string, *sql.DB) {
    t.Helper()
    once.Do(setup)
    if initErr != nil { t.Skipf("postgres testcontainer unavailable: %v", initErr) }
    ctx := context.Background()
    name := fmt.Sprintf("wt_%d_%d", time.Now().UnixNano(), seq.Add(1))
    admin, err := db.Open(ctx, baseURL, 2)
    if err != nil { t.Fatal(err) }
    if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template)); err != nil {
        admin.Close(); t.Fatal(err)
    }
    d, err := db.Open(ctx, urlFor(name), 5)
    if err != nil { admin.Close(); t.Fatal(err) }
    t.Cleanup(func() {
        d.Close()
        admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
        admin.Close()
    })
    return urlFor(name), d
}

func New(t *testing.T) *sql.DB { _, d := URL(t); return d }
```

(Add the missing `strings` import; template creation must serialize — Postgres forbids cloning a template while another session is connected to it, which is why `setup` closes `tmpl` before returning and clones never connect to the template.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/db/ -v`
Expected: both PASS (first run pulls the postgres image; allow a minute).

- [ ] **Step 5: Commit**

```bash
git add internal/testdb internal/db go.mod go.sum
git commit -m "feat(testdb): testcontainers postgres harness with template cloning"
```

---

### Task 5: httpserver skeleton — /healthz, middleware, graceful shutdown

**Files:**
- Create: `internal/httpserver/server.go`, `internal/httpserver/middleware.go`
- Test: `internal/httpserver/server_test.go`

**Interfaces:**
- Produces:

```go
type Server struct { /* holds cfg, db, mux */ }
// New builds the full mux. Later plans add route registration params; for now:
func New(cfg *config.Config, sqlDB *sql.DB) *Server
func (s *Server) Handler() http.Handler        // middleware-wrapped mux
func (s *Server) ListenAndServe(ctx context.Context) error // graceful shutdown on ctx cancel
// Middleware (exported for reuse): RequestLogger(slog), SecurityHeaders, Recover
```

- Behavior:
  - `GET /healthz` → `SELECT 1`; 200 `{"status":"ok"}` or 503 `{"status":"degraded"}`.
  - SecurityHeaders sets `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-Frame-Options: DENY`.
  - Recover catches panics → 500 JSON envelope `{"error":{"code":"internal","message":"internal error"}}` + stack to slog.
  - Graceful shutdown: on ctx cancel, `http.Server.Shutdown` with 10 s timeout.

- [ ] **Step 1: Write the failing tests**

```go
package httpserver_test

import (
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/refsdal/whenweall/internal/config"
    "github.com/refsdal/whenweall/internal/httpserver"
    "github.com/refsdal/whenweall/internal/testdb"
)

func testConfig() *config.Config {
    cfg, _, err := config.Load(map[string]string{
        "APP_URL": "http://localhost:3000", "DATABASE_URL": "postgres://unused/unused",
        "AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
    })
    if err != nil { panic(err) }
    return cfg
}

func TestHealthzOK(t *testing.T) {
    d := testdb.New(t)
    srv := httpserver.New(testConfig(), d)
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
    if rec.Code != 200 { t.Fatalf("status = %d, want 200", rec.Code) }
    if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
        t.Errorf("nosniff header = %q", got)
    }
}

func TestHealthzDegradedWhenDBDown(t *testing.T) {
    d := testdb.New(t)
    d.Close() // kill the pool
    srv := httpserver.New(testConfig(), d)
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
    if rec.Code != 503 { t.Fatalf("status = %d, want 503", rec.Code) }
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/httpserver/` → FAIL (undefined).

- [ ] **Step 3: Implement** `server.go` + `middleware.go` per the interface block. Health handler:

```go
mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
    defer cancel()
    w.Header().Set("Content-Type", "application/json")
    if err := s.db.PingContext(ctx); err != nil {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte(`{"status":"degraded"}`))
        return
    }
    w.Write([]byte(`{"status":"ok"}`))
})
```

- [ ] **Step 4: Run tests** — `go test ./internal/httpserver/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/httpserver
git commit -m "feat(http): server skeleton with healthz, security headers, graceful shutdown"
```

---

### Task 6: Embedded SPA serving

**Files:**
- Create: `internal/httpserver/spa.go`, `internal/httpserver/dist/index.html` (placeholder), `internal/httpserver/dist/.gitignore`
- Test: `internal/httpserver/spa_test.go`

**Interfaces:**
- Consumes: `Server` mux from Task 5.
- Produces: every non-`/api`, non-`/healthz` GET serves embedded `dist/`; unknown paths fall back to `index.html` (200); `/assets/*` (Vite's hashed output dir) gets `Cache-Control: public, max-age=31536000, immutable`; `index.html` gets `no-cache`.

- [ ] **Step 1: Placeholder dist**

`internal/httpserver/dist/index.html`:

```html
<!doctype html>
<title>whenweall</title>
<p>whenweall Go backend is running. The real SPA lands here in plan 8.</p>
```

`internal/httpserver/dist/.gitignore` (the real build overwrites this dir; only the placeholder + this file are committed):

```
*
!index.html
!.gitignore
```

- [ ] **Step 2: Write the failing tests**

```go
func TestSPAFallback(t *testing.T) {
    d := testdb.New(t)
    srv := httpserver.New(testConfig(), d)
    for _, path := range []string{"/", "/dashboard", "/p/abc123"} {
        rec := httptest.NewRecorder()
        srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
        if rec.Code != 200 { t.Errorf("%s status = %d", path, rec.Code) }
        if !strings.Contains(rec.Body.String(), "whenweall") { t.Errorf("%s did not serve index.html", path) }
        if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" { t.Errorf("%s Cache-Control = %q", path, cc) }
    }
}

func TestUnknownAPIPathIs404NotSPA(t *testing.T) {
    d := testdb.New(t)
    srv := httpserver.New(testConfig(), d)
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/nope", nil))
    if rec.Code != 404 { t.Errorf("status = %d, want 404", rec.Code) }
}
```

- [ ] **Step 3: Run to verify failure**, then implement `spa.go`:

```go
//go:embed all:dist
var distFS embed.FS

// spaHandler serves embedded static files; anything unknown outside /api gets index.html,
// because client-side routing owns those paths.
```

Logic: `fs.Sub(distFS, "dist")`; if the exact file exists serve it (with immutable cache for `/assets/`), else serve `index.html` with `no-cache`. Mount as the mux fallback pattern `"/"`; `/api/` prefix paths that miss return JSON 404 via a `"/api/"` catch-all registered separately.

- [ ] **Step 4: Run tests** → PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(http): embedded SPA serving with client-route fallback"`

---

### Task 7: cmd/whenweall — serve, migrate, healthcheck

**Files:**
- Create: `cmd/whenweall/main.go`
- Test: `cmd/whenweall/main_test.go` (argument dispatch only; boot paths are covered by e2e later)

**Interfaces:**
- Produces: binary entrypoint. `whenweall` (or `whenweall serve`) boots; `whenweall migrate` migrates and exits; `whenweall healthcheck` GETs `http://127.0.0.1:$PORT/healthz` and exits 0/1 (this is the Docker HEALTHCHECK — no shell in scratch). `version` prints `main.version` (set via ldflags).
- `serve`: load config (log warnings via slog), open DB, `db.Migrate` when `cfg.MigrateOnBoot`, start server, exit non-zero with the full config error text on invalid config.

- [ ] **Step 1: Failing test for dispatch**

```go
func TestRunDispatch(t *testing.T) {
    if got := run([]string{"whenweall", "definitely-not-a-command"}); got == 0 {
        t.Error("unknown command should exit non-zero")
    }
}
```

- [ ] **Step 2: Implement `main.go`**

```go
package main

var version = "dev" // stamped via -ldflags "-X main.version=..."

func main() { os.Exit(run(os.Args)) }

func run(args []string) int {
    cmd := "serve"
    if len(args) > 1 { cmd = args[1] }
    switch cmd {
    case "serve":       return serve()
    case "migrate":     return migrateCmd()
    case "healthcheck": return healthcheck()
    case "version":     fmt.Println(version); return 0
    default:
        fmt.Fprintf(os.Stderr, "unknown command %q (serve|migrate|healthcheck|version)\n", cmd)
        return 2
    }
}
```

`healthcheck` reads `PORT` (default 3000), `http.Get` with 3 s timeout, checks 200.

- [ ] **Step 3: Manual boot verification against compose db**

Run:
```bash
docker compose up -d db
APP_URL=http://localhost:3000 AUTH_SECRET=$(openssl rand -base64 32) SMTP_HOST=localhost \
  DATABASE_URL=postgres://whenweall:whenweall@localhost:5433/whenweall go run ./cmd/whenweall &
sleep 2 && curl -s localhost:3000/healthz && curl -s localhost:3000/ | head -3 && kill %1
```
Expected: `{"status":"ok"}` and the placeholder HTML.

- [ ] **Step 4: Commit** — `git commit -m "feat(cmd): whenweall binary with serve/migrate/healthcheck"`

---

### Task 8: Dockerfile + hardened compose.yaml

**Files:**
- Create: `Dockerfile`, `.dockerignore`
- Modify: `compose.yaml` (app service), `.env.example`

**Interfaces:**
- Consumes: Task 7 binary; `web/` does not exist yet, so the SPA build stage is added in plan 8 — this Dockerfile ships the committed placeholder `dist/`.

- [ ] **Step 1: Write `Dockerfile`**

```dockerfile
# Stage 1 (added in plan 8): bun build of web/ — the placeholder dist/ is embedded until then.

FROM golang:1.25-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /whenweall ./cmd/whenweall

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /whenweall /whenweall
USER 65532:65532
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/whenweall", "healthcheck"]
ENTRYPOINT ["/whenweall"]
```

Add `import _ "time/tzdata"` to `cmd/whenweall/main.go` (scratch has no zoneinfo; a scheduling app needs it).

`.dockerignore`: `node_modules`, `dist`, `.git`, `e2e`, `test-results`, `.wrangler`, `docs`, `src`, `spike`.

- [ ] **Step 2: Update `compose.yaml` app service**

Keep the db service and comments as-is. In `app`: drop the four `STRIPE_*` lines, rename `BETTER_AUTH_SECRET` → `AUTH_SECRET` (keep the openssl hint), add `OIDC_ISSUER/OIDC_CLIENT_ID/OIDC_CLIENT_SECRET` as optional empties, and add the hardening block:

```yaml
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
```

Update `.env.example` to match (remove Stripe, rename secret, add OIDC vars).

- [ ] **Step 3: Verify the container end-to-end**

```bash
docker compose build app && docker compose up -d
sleep 5 && docker compose ps && curl -s localhost:3000/healthz
docker image inspect whenweall-app --format '{{.Size}}'
```
Expected: app healthy, `{"status":"ok"}`, image size < 30 MB.

- [ ] **Step 4: Commit** — `git commit -m "feat(docker): scratch image, non-root, read-only, healthcheck subcommand"`

---

### Task 9: CI for Go

**Files:**
- Modify: `.github/workflows/ci.yml` — add a `go` job alongside the existing ones (the TS jobs keep running until plan 8 removes them).

- [ ] **Step 1: Add the job**

```yaml
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with: { go-version: "1.25" }
      - run: go vet ./...
      - uses: golangci/golangci-lint-action@v9
      - run: go test ./...   # testcontainers uses the runner's Docker daemon
      - run: docker build .
```

- [ ] **Step 2: Push the branch and confirm the job is green in the PR checks.**

- [ ] **Step 3: Commit** — `git commit -m "ci: go vet, lint, test (testcontainers), docker build"`
