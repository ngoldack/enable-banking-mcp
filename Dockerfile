# syntax=docker/dockerfile:1
#
# BuildKit-optimized multi-stage build for fin-mcp.
# Requires BuildKit (default in modern Docker / `docker buildx`).
#
# Observability is provided in-process by the OpenTelemetry Go SDK (traces +
# metrics over OTLP). There is therefore a single, unprivileged runtime image —
# no eBPF agent, no privileged container, no shared process namespace.
#
# Cache mounts (`--mount=type=cache`) keep the Go module cache and the Go build
# cache warm across builds, dramatically speeding up incremental rebuilds without
# bloating image layers.

# =========================================================================
# Stage 1: Build the Go application
# =========================================================================
FROM golang:1.26-alpine AS builder
WORKDIR /app

# Pin Go cache locations so the BuildKit cache mounts below target them precisely.
ENV GOMODCACHE=/go/pkg/mod
ENV GOCACHE=/root/.cache/go-build

# 1. Download dependencies first (best layer caching: only re-runs when go.mod/go.sum change).
#    The module cache mount avoids re-downloading modules on every build.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 2. Build the binary. Both the module cache and the build cache are mounted so that
#    unchanged packages are never recompiled. -trimpath + stripped symbols
#    (-s -w) yield a smaller, more reproducible binary.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fin-mcp ./cmd/fin-mcp

# =========================================================================
# Stage 2: Runtime (clean, unprivileged)
# =========================================================================
FROM alpine:latest AS runtime

RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 app

WORKDIR /app
COPY --from=builder /out/fin-mcp .

# Run as an unprivileged user.
USER app

# Configuration is supplied at runtime via env vars or a mounted file (12-factor / K8s).
ENTRYPOINT ["./fin-mcp"]
CMD ["server", "--config", "/etc/fin-mcp/config.json"]
