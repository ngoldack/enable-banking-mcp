# syntax=docker/dockerfile:1
#
# BuildKit-optimized multi-stage build for the Enable Banking suite.
# Requires BuildKit (default in modern Docker / `docker buildx`).
#
# Cache mounts (`--mount=type=cache`) keep the Go module cache and the Go build
# cache warm across builds, dramatically speeding up incremental rebuilds without
# bloating image layers.

# =========================================================================
# Stage 1: Pull the official OpenTelemetry Go eBPF Auto-Instrumentation agent
# =========================================================================
FROM ghcr.io/open-telemetry/opentelemetry-go-instrumentation/autoinstrumentation-go:v0.15.0-alpha AS otel-agent

# =========================================================================
# Stage 2: Build the Go application
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
#    unchanged packages are never recompiled.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/fin-mcp ./cmd/fin-mcp

# NOTE: We intentionally do NOT strip the binary (no "-ldflags=-s -w").
# The eBPF auto-instrumentation agent requires the Go symbol table to remain
# intact so it can dynamically locate and hook function boundaries at runtime.

# =========================================================================
# Stage 3: Standard Runtime (clean, non-instrumented)
# =========================================================================
# Build explicitly with:  docker build --target standard-runtime -t fin-mcp:standard .
FROM alpine:latest AS standard-runtime

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /out/fin-mcp .

# Configuration is supplied at runtime via env vars or a mounted file (12-factor / K8s).
ENTRYPOINT ["./fin-mcp"]
CMD ["server", "--config", "/etc/enable-banking/config.json"]

# =========================================================================
# Stage 4: Instrumented Runtime (OTel eBPF Auto-Instrumented)
# =========================================================================
# Build explicitly with:  docker build --target instrumented-runtime -t fin-mcp:otel .
#
# eBPF probes inspect kernel-level syscalls, so this container needs elevated privileges:
#   Docker:      --privileged   (or --cap-add=SYS_ADMIN --cap-add=SYS_RESOURCE --pid=host)
#   Kubernetes:  securityContext.privileged: true  +  shareProcessNamespace: true
FROM alpine:latest AS instrumented-runtime

# libc6-compat lets the glibc-built agent run on musl-based Alpine.
RUN apk add --no-cache ca-certificates libc6-compat

WORKDIR /app
COPY --from=builder /out/fin-mcp ./fin-mcp

# The agent binary lives at the image root in the official image.
COPY --from=otel-agent /otel-go-instrumentation /otel-go-instrumentation

# Entrypoint launches the app and attaches the eBPF agent to it.
COPY docker/instrumented-entrypoint.sh /usr/local/bin/instrumented-entrypoint.sh
RUN chmod +x /usr/local/bin/instrumented-entrypoint.sh

# Standard OpenTelemetry environment (override via K8s / compose at runtime).
ENV OTEL_SERVICE_NAME=fin-mcp
ENV OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
# The eBPF agent attaches to the target binary identified by this exact path.
ENV OTEL_GO_AUTO_TARGET_EXE=/app/fin-mcp

ENTRYPOINT ["/usr/local/bin/instrumented-entrypoint.sh"]
CMD ["server", "--config", "/etc/enable-banking/config.json"]
