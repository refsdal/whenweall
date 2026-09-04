# syntax=docker/dockerfile:1

# NOTHING COMPILES IN HERE. The binaries are built natively, outside Docker:
#
#   bash scripts/build-artifacts.sh   # -> dist/server/linux/{amd64,arm64}/whenweall
#
# and this file only COPYs the one matching the platform being built. That keeps a multi-arch
# `docker buildx build --platform linux/amd64,linux/arm64` down to seconds of file copying — no
# QEMU emulation, no in-container Go or Bun toolchains, and the native build reuses the developer's
# (or CI's) module and Vite caches. If the COPY below fails with "not found", run the script first.
#
# The SPA is not copied separately: it is embedded inside the binary (internal/httpserver/spa.go's
# `//go:embed all:dist`), along with the SQL migrations and the IANA zone database
# (`import _ "time/tzdata"` in cmd/whenweall/main.go).
#
# The base is distroless "static" rather than scratch: the same no-shell / no-libc /
# no-package-manager attack surface, but it ships the things a from-scratch image has to hand-roll
# — an up-to-date CA bundle (whenweall cannot send mail over TLS without one), /tmp, and the
# nonroot uid 65532. Pinned by digest; Dependabot bumps it.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

# Set automatically by buildx, one value per --platform: "linux/amd64", "linux/arm64".
ARG TARGETPLATFORM
# Where the per-platform binaries sit in the build context. The default matches
# scripts/build-artifacts.sh's layout; a release tool that hands Docker a context with the
# binaries already at linux/<arch>/whenweall passes BINARY_ROOT=. instead.
ARG BINARY_ROOT=dist/server

COPY ${BINARY_ROOT}/${TARGETPLATFORM}/whenweall /whenweall

# The base image's :nonroot tag already sets uid 65532, but as the bare number with no group.
# Spelling out both halves keeps `docker image inspect -f '{{.Config.User}}'` equal to
# "65532:65532", which is the exact string e2e/assert-hardening.sh holds this image to.
USER 65532:65532
EXPOSE 3000
# The binary is its own healthcheck client — there is no shell or curl in this image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/whenweall", "healthcheck"]
ENTRYPOINT ["/whenweall"]
