# Stage 1: bun build of web/ — the built SPA is embedded into the Go binary via go:embed
# (internal/httpserver/spa.go) in stage 2, below.
FROM oven/bun:1-alpine AS web
WORKDIR /web
COPY web/package.json web/bun.lock ./
# project.inlang must be present before `bun install`: its postinstall script (`paraglide-js
# compile --project ./project.inlang`) runs as part of install and fails if the project isn't
# there yet — it can't wait for the full `COPY web/ .` below.
COPY web/project.inlang ./project.inlang
RUN bun install --frozen-lockfile
COPY web/ .
RUN bun run build

FROM golang:1.25-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The built SPA lands where spa.go's `//go:embed all:dist` expects it, before `go build` runs so
# the embed actually picks it up.
COPY --from=web /web/dist ./internal/httpserver/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /whenweall ./cmd/whenweall

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /whenweall /whenweall
USER 65532:65532
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/whenweall", "healthcheck"]
ENTRYPOINT ["/whenweall"]
