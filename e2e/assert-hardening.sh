#!/usr/bin/env bash
# Proves the compose hardening claims are LIVE on the running app container, not just written in
# compose.yaml: read-only root filesystem, every capability dropped, no-new-privileges, the
# unprivileged user, and a passing Docker HEALTHCHECK (`/whenweall healthcheck` — the distroless
# image has no shell, so this exercises the binary's own subcommand). Run after `compose-e2e.sh up
# -d --wait`; exits non-zero on the first claim that does not hold.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

app="$(e2e/compose-e2e.sh ps -q app)"
if [ -z "$app" ]; then
  echo "assert-hardening: no running app container (run e2e/compose-e2e.sh up -d --wait first)" >&2
  exit 1
fi

check() {
  local label="$1" want="$2" got="$3"
  if [ "$got" != "$want" ]; then
    echo "assert-hardening: $label = '$got', want '$want'" >&2
    exit 1
  fi
  echo "assert-hardening: $label = '$got' OK"
}

check "ReadonlyRootfs"      "true"          "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$app")"
check "CapDrop"             '["ALL"]'       "$(docker inspect -f '{{json .HostConfig.CapDrop}}' "$app")"
check "SecurityOpt"         '["no-new-privileges:true"]' "$(docker inspect -f '{{json .HostConfig.SecurityOpt}}' "$app")"
check "User"                "65532:65532"   "$(docker inspect -f '{{.Config.User}}' "$app")"
check "Health"              "healthy"       "$(docker inspect -f '{{.State.Health.Status}}' "$app")"
check "ENABLE_TEST_ROUTES"  "true"          "$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$app" | sed -n 's/^ENABLE_TEST_ROUTES=//p')"

# The container-level "User" check above inspects the RUNNING container, which compose.e2e.yaml's
# own `user: '65532:65532'` sets belt-and-braces (so the app never actually runs as root even if
# the image regresses) — meaning it would keep reporting 65532:65532 even if the Dockerfile's own
# `USER 65532:65532` were dropped. This second check reads the built IMAGE's own config instead,
# bypassing that override, so a dropped Dockerfile USER still fails this script even though the
# running container stayed safe.
check "Image USER"         "65532:65532"   "$(docker image inspect -f '{{.Config.User}}' whenweall:e2e)"

# And the seed route the suite depends on is actually reachable through the published port.
status="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -H 'Origin: http://localhost:3100' -d '{}' http://localhost:3100/api/test/seed)"
check "POST /api/test/seed" "200" "$status"
