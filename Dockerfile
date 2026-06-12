# syntax=docker/dockerfile:1

# ── Stage 1: build ──────────────────────────────────────────────────────────
# Pin the builder by digest for reproducible, supply-chain-verifiable builds.
FROM golang:1.26-alpine@sha256:9169234cc43b396435c64e45538fe6d4ffa237e7f988b9ab32abdfa0c3141979 AS builder

# ca-certificates is copied into the final image so the binary can validate
# HTTPS connections (e.g. future registry query features).
# git is intentionally omitted — all dependencies are fetched via
# proxy.golang.org over HTTPS; no VCS tool is needed at build time.
RUN apk add --no-cache ca-certificates

WORKDIR /src

# Copy dependency manifests first to exploit layer caching — the expensive
# `go mod download` only re-runs when go.mod / go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a fully static binary. VERSION is injected by the release workflow via
# --build-arg; falls back to "dev" for local builds.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X 'main.version=${VERSION}'" \
      -o /oci-image-detector \
      ./

# ── Stage 2: minimal runtime image ──────────────────────────────────────────
# scratch contains nothing — no shell, no package manager, no attack surface.
# The binary is statically linked so it needs no libc or runtime dependencies.
FROM scratch

# HEALTHCHECK NONE — this is a short-lived CLI tool, not a long-running
# service. The scratch base has no shell or utilities to run a health
# check command with, and none is meaningful for a batch scanner.
HEALTHCHECK NONE

# Copy TLS root certificates from the builder so the binary can validate HTTPS
# connections for future registry-query features.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /oci-image-detector /oci-image-detector

# Run as a non-root UID/GID (65532 = conventional "nonroot", as used by
# distroless). Callers must ensure the output directory is writable by this UID.
USER 65532:65532

ENTRYPOINT ["/oci-image-detector"]
