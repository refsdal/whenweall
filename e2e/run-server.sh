#!/usr/bin/env bash
# Started as playwright.config.ts's webServer.command, so Playwright supervises (and, at the end
# of the run, kills) this whole thing as one process tree.
#
# Two modes, selected by E2E_SERVER (default "go"):
#
#   go     Start the throwaway Postgres/Mailpit containers, build the SPA into
#          internal/httpserver/dist, and `exec go run ./cmd/whenweall` on :$APP_PORT. Container
#          startup lives here rather than in Playwright's own `globalSetup` hook because, on this
#          Playwright version, `globalSetup` and `webServer` do not reliably run in the documented
#          order (observed: webServer's DB connection failing with "connection refused" because the
#          container `globalSetup` was supposed to have already started hadn't been created yet).
#
#   image  The built Docker image is already running via `e2e/compose-e2e.sh up -d --wait`
#          (compose.yaml + compose.e2e.yaml). Nothing to start: wait for /healthz, then block so
#          Playwright has a process to supervise. Normally playwright.config.ts's
#          reuseExistingServer short-circuits before this script even runs in image mode; this
#          branch only matters when the stack is NOT up yet, and then it fails fast with the
#          command to run.
#
# Everything this script STARTS is recorded in $RUN_MARKER (a JSON file e2e/global-teardown.ts
# reads and deletes), so teardown only ever removes containers this run created and only restores
# dist/ when this run overwrote it — a developer who keeps `bash e2e/run-server.sh` running and
# lets Playwright reuse it never loses their database to a teardown.
#
# Container names/ports/images/credentials come in as env vars from playwright.config.ts's
# webServer.env, which builds them from e2e/e2e-env.ts — the one place they're actually defined.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

: "${APP_PORT:?APP_PORT must be set (playwright.config.ts passes it from e2e/e2e-env.ts)}"
MODE="${E2E_SERVER:-go}"
MARKER="${RUN_MARKER:-e2e/.run-server-marker.json}"
HEALTHZ="http://localhost:${APP_PORT}/healthz"

if [ "$MODE" = "image" ]; then
  echo "run-server.sh: E2E_SERVER=image — waiting for the compose stack on ${HEALTHZ}" >&2
  for _ in $(seq 1 120); do
    if curl -fsS "$HEALTHZ" >/dev/null 2>&1; then
      # Nothing was started here, so no marker: teardown must leave the compose stack alone
      # (the CI job / developer brings it down with `e2e/compose-e2e.sh down -v`).
      exec tail -f /dev/null
    fi
    sleep 1
  done
  echo "run-server.sh: ${HEALTHZ} not healthy after 120s. Start the image stack first:" >&2
  echo "  docker build -t whenweall:e2e . && e2e/compose-e2e.sh up -d --wait" >&2
  exit 1
fi

: "${DB_CONTAINER:?}" "${DB_IMAGE:?}" "${DB_PORT:?}" "${DB_USER:?}" "${DB_PASSWORD:?}" "${DB_NAME:?}"
: "${MAILPIT_CONTAINER:?}" "${MAILPIT_IMAGE:?}" "${MAILPIT_SMTP_PORT:?}" "${MAILPIT_HTTP_PORT:?}"

docker rm -f "$DB_CONTAINER" >/dev/null 2>&1 || true
docker rm -f "$MAILPIT_CONTAINER" >/dev/null 2>&1 || true

docker run -d --name "$DB_CONTAINER" \
  -e POSTGRES_USER="$DB_USER" -e POSTGRES_PASSWORD="$DB_PASSWORD" -e POSTGRES_DB="$DB_NAME" \
  -p "127.0.0.1:${DB_PORT}:5432" "$DB_IMAGE" >/dev/null

docker run -d --name "$MAILPIT_CONTAINER" \
  -p "127.0.0.1:${MAILPIT_SMTP_PORT}:1025" -p "127.0.0.1:${MAILPIT_HTTP_PORT}:8025" \
  "$MAILPIT_IMAGE" >/dev/null

# Written the moment the containers exist — before anything below can fail — so a broken build or
# a server that never comes up still gets its containers removed by global-teardown.ts.
printf '{"mode":"go","containers":["%s","%s"],"dist":true}\n' "$DB_CONTAINER" "$MAILPIT_CONTAINER" > "$MARKER"

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
# global-teardown.ts restores the committed placeholder afterwards (the marker's "dist": true).
rm -rf internal/httpserver/dist/assets
cp -r web/dist/. internal/httpserver/dist/

# exec, not a plain call: replaces this script's process with the Go server, so the SIGTERM
# Playwright sends when tearing down the webServer reaches the actual server directly instead of
# an intermediate shell that might not forward it.
exec go run ./cmd/whenweall
