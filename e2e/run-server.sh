#!/usr/bin/env bash
# Started as playwright.config.ts's webServer.command, so Playwright supervises (and, at the end
# of the run, kills) this whole thing as one process tree.
#
# This folds "start the throwaway Postgres/Mailpit containers" into the same script that builds
# and runs the server, rather than Playwright's own `globalSetup` hook — empirically, on this
# Playwright version, `globalSetup` and `webServer` do not reliably run in the order the docs
# describe (observed: webServer's DB connection failing with "connection refused" because the
# container `globalSetup` was supposed to have already started hadn't been created yet). A single
# script that does both steps in order removes the ordering assumption entirely.
#
# Container names/ports/credentials come in as env vars from playwright.config.ts's
# webServer.env, which builds them from e2e/e2e-env.ts — the one place they're actually defined.
# `global-teardown.ts` stops these same two containers (and restores internal/httpserver/dist)
# once the whole suite finishes; this script only ever starts them.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

docker rm -f "$DB_CONTAINER" >/dev/null 2>&1 || true
docker rm -f "$MAILPIT_CONTAINER" >/dev/null 2>&1 || true

docker run -d --name "$DB_CONTAINER" \
  -e POSTGRES_USER="$DB_USER" -e POSTGRES_PASSWORD="$DB_PASSWORD" -e POSTGRES_DB="$DB_NAME" \
  -p "127.0.0.1:${DB_PORT}:5432" postgres:18-alpine >/dev/null

docker run -d --name "$MAILPIT_CONTAINER" \
  -p "127.0.0.1:${MAILPIT_SMTP_PORT}:1025" -p "127.0.0.1:${MAILPIT_HTTP_PORT}:8025" \
  axllent/mailpit:latest >/dev/null

# Poll pg_isready rather than a fixed sleep: a fresh postgres:18-alpine container is usually ready
# in well under a second, but never assume that under load.
ready=0
for _ in $(seq 1 60); do
  if docker exec "$DB_CONTAINER" pg_isready -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [ "$ready" -ne 1 ]; then
  echo "run-server.sh: $DB_CONTAINER did not become ready within 30s" >&2
  exit 1
fi

# `go` isn't necessarily on the default PATH a spawned child process inherits.
export PATH="$PATH:$HOME/.local/go/bin:$HOME/go/bin"

(cd web && bun run build)

# The build output lands where internal/httpserver/spa.go's `//go:embed all:dist` expects it —
# global-teardown.ts restores the committed placeholder afterwards.
rm -rf internal/httpserver/dist/assets
cp -r web/dist/. internal/httpserver/dist/

# exec, not a plain call: replaces this script's process with the Go server, so the SIGTERM
# Playwright sends when tearing down the webServer reaches the actual server directly instead of
# an intermediate shell that might not forward it.
exec go run ./cmd/whenweall
