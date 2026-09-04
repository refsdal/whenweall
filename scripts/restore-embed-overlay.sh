#!/usr/bin/env bash
# Drops the SPA that scripts/build-artifacts.sh overlaid into the go:embed directory and puts the
# committed placeholder back, leaving the working tree clean. Idempotent.
#
# Equivalent to what e2e/global-teardown.ts runs to undo e2e/run-server.sh's own overlay of the
# same directory (it checks out just index.html rather than the whole path); keep the two in step.
#
# `internal/httpserver/dist/.gitignore` ignores everything in that directory except itself and the
# placeholder index.html, so the overlay's files are ignored, not merely untracked — the clean
# needs -x to see them. build-artifacts.sh deliberately leaves that .gitignore in place while it
# works, so this holds even if this script never runs.
#
# Guarded on the placeholder actually being tracked HERE, not merely on being inside some git
# worktree: an exported tarball unpacked inside an unrelated repository would otherwise have its
# own files cleaned. And nothing in here may change the caller's exit status — this runs from an
# EXIT trap, where a failing command would turn a perfectly good build red.
set -euo pipefail
cd "$(dirname "$0")/.."

EMBED_DIR=internal/httpserver/dist

if git cat-file -e "HEAD:$EMBED_DIR/index.html" >/dev/null 2>&1; then
  git clean -qfdx -- "$EMBED_DIR" || true
  git checkout -q -- "$EMBED_DIR" || true
fi
exit 0
