# Regenerating Limen's schema migration

`migrations/00002_auth.sql` is Limen's own schema (`users`, `accounts`, `organizations`,
`organization_invitations`, `organization_members`, `organization_member_roles`,
`limen_rate_limits`, `sessions`, `two_factors`, `verifications`), generated once and hand-folded
into a goose migration, plus our own `staff_users` table appended at the end. It is not
hand-written and should not be hand-edited — regenerate it instead, and diff.

This is needed again whenever:

- Limen (or any of its plugins) is upgraded to a commit that changes a schema — a new column, a
  new plugin table, a changed index.
- A new plugin is added to `internal/auth.buildLimenConfig`.

## Steps

**1. Bring up a throwaway Postgres.** From the repo root, with `POSTGRES_PASSWORD` set (compose
requires it):

```bash
export POSTGRES_PASSWORD=whenweall
docker compose up -d db
```

This is `compose.yaml`'s `db` service, reachable at `postgres://whenweall:whenweall@localhost:5433/whenweall`.
Apply the app's own migrations first, so the database looks like a real deployment at the point
this migration would run (in particular, `migrations/00001_infra.sql`'s `rate_limits` table needs
to already exist so schemagen's rename of Limen's own rate-limit table away from that name — see
below — can be double-checked against the real collision):

```bash
DATABASE_URL="postgres://whenweall:whenweall@localhost:5433/whenweall?sslmode=disable" \
APP_URL=http://localhost:3000 AUTH_SECRET=$(openssl rand -base64 32) SMTP_HOST=localhost \
  go run ./cmd/whenweall migrate
```

**2. Run schemagen.** `internal/auth/schemagen` builds the *exact* Limen configuration
`internal/auth.New` builds (same plugins, same options — see `auth.buildLimenConfig`), but with
Limen's CLI serialization turned on. Constructing Limen with that config is what makes Limen write
`.limen/schemas.json` (Limen's `Config.prepareCLIConfig`):

```bash
go run ./internal/auth/schemagen
```

By default it targets the same DSN as step 1; override with `SCHEMAGEN_DSN` if needed.

**3. Run Limen's own CLI to introspect and emit DDL.** Pin the commit to the same sha
`go.mod` pins Limen to (currently `c6a34aa6dcb4d51a480e2c6ed9cb43e5c6f92ac4` — check
`go.mod`'s `github.com/thecodearcher/limen` require line for the current value):

```bash
go run github.com/thecodearcher/limen/cmd/limen@<pinned-sha> generate migrations \
  --driver postgres --dsn "postgres://whenweall:whenweall@localhost:5433/whenweall?sslmode=disable" \
  --output /tmp/limen-migrations
```

This introspects the live database (already migrated in step 1) against `.limen/schemas.json` and
writes one `<version>_<table>.up.sql` / `.down.sql` pair per table that needs a change — a fresh
`CREATE TABLE` for a new table, an `ALTER TABLE` for a schema that already exists but has drifted
(e.g. after a Limen upgrade adds a column).

**4. Fold the output into a goose migration by hand.** Concatenate the generated `.up.sql` files
(in the order the CLI printed them — that order already respects foreign-key dependencies) under
`-- +goose Up` in a new (or, for the very first generation, `00002_auth.sql`'s existing) file, and
their `.down.sql` counterparts *in reverse order* under `-- +goose Down`. `staff_users` (our own
table, not Limen's) stays appended after Limen's tables in Up, and dropped first in Down.

**5. Verify.** `go test ./internal/db/` (the migration applies cleanly to the testdb template) and
`go test ./internal/auth/`.

## Why `limen_rate_limits`, not `rate_limits`

Limen's core schema set always includes a `rate_limits` table — it's a standing field on
`SchemaConfig`, discovered unconditionally regardless of whether the HTTP rate limiter actually
uses a database-backed store (the default, `NewDefaultRateLimiterConfig`, uses an in-memory
`StoreTypeCache`, so nothing at runtime ever writes to it with the config this project uses). That
name collides with whenweall's own pre-existing `rate_limits` table
(`migrations/00001_infra.sql`, columns `key, count, reset_at`, used by the app's own rate
limiting — see `internal/jobs/housekeeping.go`), which has different, incompatible columns. Left
alone, Limen's CLI generates an `ALTER TABLE rate_limits ADD COLUMN ... last_request_at BIGINT NOT
NULL` against our table — confirmed against the pinned CLI, which emitted exactly that — which
would break every existing `INSERT INTO rate_limits (key, count, reset_at)` in the app (no
default for the new NOT NULL column).

`internal/auth.buildLimenConfig` renames Limen's table via its own supported schema-customization
option:

```go
Schema: limen.NewDefaultSchemaConfig(
    limen.WithSchemaRateLimit(limen.WithRateLimitTableName("limen_rate_limits")),
),
```

This is the documented mechanism for exactly this situation, not a workaround — it never touches
whenweall's `rate_limits` table, and Limen's database-backed rate limiting isn't in use anyway. If
a future change turns on `Store: StoreTypeDatabase` for Limen's own HTTP rate limiter, it will use
`limen_rate_limits` transparently.

## Why the generated tables use bigint ids

`internal/auth.buildLimenConfig` sets no `Schema.IDGenerator`, so Limen falls back to its default:
auto-increment (`BIGSERIAL`/`BIGINT`) ids, not whenweall's usual nanoid-style text ids
(`internal/db.NewID`). `staff_users.user_id` in `migrations/00002_auth.sql` is `bigint` to match
`users.id`, not `text` — the seam (`internal/auth`) normalizes every Limen id to a Go `string`
(`fmt.Sprint(user.ID)`) regardless, so nothing outside `internal/auth` and this migration needs to
know the underlying column type.
