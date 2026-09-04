#!/usr/bin/env bash
# Drops the SPA that scripts/build-artifacts.sh overlaid into the go:embed directory and puts the
# committed placeholder back, leaving the working tree clean.
#
# `internal/httpserver/dist/.gitignore` ignores everything in that directory except itself and the
# placeholder `index.html`, so the overlay's files are IGNORED, not merely untracked — the clean
# needs -x to see them. This is the same pair of commands e2e/global-teardown.ts runs to undo
# e2e/run-server.sh's overlay; keep the two in step.
#
# Idempotent, and a no-op outside a git checkout (an exported tarball has nothing to restore from,
# so it simply keeps the overlay).
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=internal/httpserver/dist

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git clean -qfdx -- "$EMBED_DIR"
  git checkout -q -- "$EMBED_DIR"
fi
