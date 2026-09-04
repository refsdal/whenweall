#!/usr/bin/env bash
# Builds everything the Dockerfile COPYs, NATIVELY — outside Docker, once per target platform:
#
#   bash scripts/build-artifacts.sh                      # linux/amd64 + linux/arm64
#   bash scripts/build-artifacts.sh linux/amd64          # just this machine's arch, for a local image
#   VERSION=v1.2.3 bash scripts/build-artifacts.sh       # stamp `whenweall version`
#
# Output: dist/server/<os>/<arch>/whenweall — the layout the Dockerfile's
# `COPY ${BINARY_ROOT}/${TARGETPLATFORM}/whenweall` reads, because buildx sets TARGETPLATFORM to
# exactly that "linux/amd64" form.
#
# WHY THIS EXISTS. The Dockerfile used to carry a bun stage and a Go stage, and `docker buildx
# build --platform linux/amd64,linux/arm64` runs every stage once per platform — so the arm64 leg
# compiled the SPA *and* the binary under QEMU emulation. The SPA is byte-identical on every
# architecture, so that emulated Vite build was pure waste. Here it is built once, natively, and
# reused for both binaries; the Go compiler cross-compiles (CGO_ENABLED=0, so no cross toolchain
# is needed); and the image build is reduced to copying one file.
#
# The SPA is not shipped as a separate layer: it is embedded INTO each binary by
# internal/httpserver/spa.go's `//go:embed all:dist`, which is why this script has to overlay the
# build output into that directory first. A committed placeholder index.html normally sits there
# so plain `go build` and `go test` work without a frontend build — the EXIT trap always puts it
# back, including on failure or Ctrl-C, so an overlaid dist/ can never be committed by accident.
set -euo pipefail
cd "$(dirname "$0")/.."

# `go` and the sqlc/goose helpers are not necessarily on the PATH a non-login shell inherits.
export PATH="$PATH:$HOME/.local/go/bin:$HOME/go/bin"

VERSION="${VERSION:-dev}"
platforms=("$@")
if [ ${#platforms[@]} -eq 0 ]; then
  platforms=(linux/amd64 linux/arm64)
fi

trap 'bash scripts/restore-embed-overlay.sh' EXIT

echo "==> SPA (bun + vite) — built once, reused for every architecture"
(cd web && bun install --frozen-lockfile && bun run build)

echo "==> embed overlay -> internal/httpserver/dist"
rm -rf internal/httpserver/dist
mkdir -p internal/httpserver/dist
cp -R web/dist/. internal/httpserver/dist/

rm -rf dist/server
for platform in "${platforms[@]}"; do
  goos="${platform%%/*}"
  goarch="${platform##*/}"
  out="dist/server/${platform}/whenweall"
  echo "==> go build ${platform} (VERSION=${VERSION}) -> ${out}"
  mkdir -p "$(dirname "$out")"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$out" ./cmd/whenweall
done

echo "==> done"
ls -l dist/server/*/*/whenweall
