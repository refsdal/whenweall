#!/usr/bin/env bash
# The one spelling of the image-mode compose invocation, so CI, the README and a developer's shell
# can't drift: `e2e/compose-e2e.sh up -d --wait`, `e2e/compose-e2e.sh logs app`,
# `e2e/compose-e2e.sh down -v`, …
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec docker compose --env-file e2e/compose.e2e.env -f compose.yaml -f compose.e2e.yaml "$@"
