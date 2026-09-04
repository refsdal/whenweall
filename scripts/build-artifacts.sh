#!/usr/bin/env bash
# Builds everything the Dockerfile COPYs, NATIVELY — outside Docker, once per target platform:
#
#   bash scripts/build-artifacts.sh                 # linux/amd64 + linux/arm64 (what a release ships)
#   bash scripts/build-artifacts.sh host            # only this machine's architecture
#   VERSION=v1.2.3 bash scripts/build-artifacts.sh  # stamp `whenweall version`
#
# Use `host` for anything you are about to `docker build` locally: plain `docker build` targets the
# HOST platform, so hardcoding an architecture here produces a binary the COPY cannot find on an
# arm64 laptop or VPS.
#
# Output: dist/server/<os>/<arch>/whenweall — the layout the Dockerfile's
# `COPY dist/server/${TARGETPLATFORM}/whenweall` reads, because buildx sets TARGETPLATFORM to
# exactly that "linux/amd64" form.
#
# WHY THIS EXISTS. The Dockerfile used to carry a bun stage and a Go stage, and `docker buildx
# build --platform linux/amd64,linux/arm64` runs every stage once per platform — so the arm64 leg
# compiled the SPA *and* the binary under QEMU emulation. The SPA is byte-identical on every
# architecture, so that emulated Vite build was pure waste. Here it is built once, natively, and
# reused for both binaries; the Go compiler cross-compiles (CGO_ENABLED=0, so no cross toolchain
# is needed); and the image build is reduced to copying one file. The native build also reuses
# bun's install cache and Go's module and build caches, which an image layer cannot.
#
# The SPA is not shipped as a separate layer: it is embedded INTO each binary by
# internal/httpserver/spa.go's `//go:embed all:dist`, which is why this script has to overlay the
# build output into that directory first. A committed placeholder index.html normally sits there so
# plain `go build` and `go test` work without a frontend build; the EXIT trap puts it back.
#
# Two things make that overlay safe rather than merely tidy:
#
#   - The overlay never removes `internal/httpserver/dist/.gitignore`. That file (`*`, minus
#     index.html and itself) is the ONLY reason the copied SPA is git-ignored rather than plain
#     untracked. Deleting it and restoring it in a trap would mean a `kill -9`, an OOM kill or a
#     cancelled CI job leaves the whole minified bundle sitting in `git status` as addable files.
#   - The whole run holds an flock. Without it, two concurrent builds race on one shared directory:
#     the first one's trap restores the placeholder while the second's `go build` is still running,
#     and `go:embed` silently embeds the PLACEHOLDER — a shippable image serving a blank page, with
#     no error and a zero exit status. The pre-build check below is the belt to that braces.
set -euo pipefail
cd "$(dirname "$0")/.."

# `go` is not necessarily on the PATH a non-login shell inherits.
export PATH="$PATH:$HOME/.local/go/bin:$HOME/go/bin"

VERSION="${VERSION:-dev}"
EMBED_DIR=internal/httpserver/dist

platforms=()
for arg in "$@"; do
  if [ "$arg" = "host" ]; then
    platforms+=("$(go env GOOS)/$(go env GOARCH)")
  else
    platforms+=("$arg")
  fi
done
if [ ${#platforms[@]} -eq 0 ]; then
  platforms=(linux/amd64 linux/arm64)
fi

# Serialise against another copy of this script sharing the same checkout. `dist/` is git-ignored,
# so the lock file never shows up in `git status`. Non-fatal if flock is missing (macOS): the
# pre-build check below still catches the damage, it just cannot prevent it.
mkdir -p dist
if command -v flock >/dev/null 2>&1; then
  exec 9>dist/.build-artifacts.lock
  flock 9
fi

trap 'bash scripts/restore-embed-overlay.sh' EXIT

echo "==> SPA (bun + vite) — built once, reused for every architecture"
(cd web && bun install --frozen-lockfile && bun run build)

echo "==> embed overlay -> $EMBED_DIR"
# Everything except .gitignore: see the header. -mindepth 1 keeps the directory itself.
find "$EMBED_DIR" -mindepth 1 -not -name .gitignore -exec rm -rf {} +
cp -R web/dist/. "$EMBED_DIR/"

# The overlay must be live for every `go build` below. If a concurrent run's trap has restored the
# placeholder underneath us, go:embed would happily embed THAT and exit 0.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1 &&
  git diff --quiet -- "$EMBED_DIR/index.html" 2>/dev/null; then
  echo "build-artifacts: $EMBED_DIR/index.html is still the committed placeholder — another" >&2
  echo "  build in this checkout probably restored it. Re-run this script on its own." >&2
  exit 1
fi

rm -rf dist/server
for platform in "${platforms[@]}"; do
  goos="${platform%%/*}"
  goarch="${platform##*/}"
  out="dist/server/${platform}/whenweall"
  echo "==> go build ${platform} (VERSION=${VERSION}) -> ${out}"
  mkdir -p "$(dirname "$out")"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$out" ./cmd/whenweall

  # COPY does not inspect ELF headers, so nothing downstream would notice a binary built for the
  # wrong architecture — the image would just crash on first run. Check it here, where we can.
  built="$(go version -m "$out" | sed -n 's/.*GOARCH=//p' | head -1)"
  if [ "$built" != "$goarch" ]; then
    echo "build-artifacts: ${out} is GOARCH=${built}, want ${goarch}" >&2
    exit 1
  fi
done

echo "==> done"
ls -l dist/server/*/*/whenweall
