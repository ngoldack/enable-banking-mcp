# =========================================================================
# Stage 1: Copy the official OpenTelemetry Go Auto-Instrumentation Agent
# =========================================================================
FROM ghcr.io/open-telemetry/opentelemetry-go-instrumentation/autoinstrumentation-go:v0.15.0-alpha AS otel-agent

# =========================================================================
# Stage 2: Build the clean Go application
# =========================================================================
FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CRITICAL SECURITY & INSTRUMENTATION NOTE:
# Do NOT strip the binary with "-ldflags='-s -w'"! The eBPF auto-instrumentation
# agent requires the Go symbol table to remain fully intact to dynamically
# locate and hook function boundaries at runtime.
RUN CGO_ENABLED=0 go build -o enable-banking-go ./cmd/enable-banking-go

# =========================================================================
# Stage 3: Standard Runtime (Clean / Non-Instrumented Stage)
# =========================================================================
# To build this stage specifically, run:
#   docker build --target standard-runtime -t enable-banking-go:standard .
FROM alpine:latest AS standard-runtime

# libc6-compat is required for musl/glibc compatibility on Alpine
RUN apk add --no-cache libc6-compat ca-certificates

WORKDIR /app

# Copy our pure Go application
COPY --from=builder /app/enable-banking-go .

ENTRYPOINT ["./enable-banking-go"]
CMD ["server", "--config", "/etc/enable-banking/config.json"]

# =========================================================================
# Stage 4: Instrumented Runtime (OTel Auto-Instrumented Stage)
# =========================================================================
# To build this stage specifically, run:
#   docker build --target instrumented-runtime -t enable-banking-go:otel .
#
# CRITICAL DOCKER/K8S EXECUTION NOTE:
# Because eBPF probes inspect kernel-level system calls, running this container
# in production requires elevated privileges.
# In Docker, run with: `--privileged` or `--cap-add=SYS_ADMIN`
# In Kubernetes, specify: `securityContext.privileged: true` or add `SYS_ADMIN` capability.
FROM alpine:latest AS instrumented-runtime

# libc6-compat is required for musl/glibc compatibility on Alpine
RUN apk add --no-cache libc6-compat ca-certificates

WORKDIR /app

# Copy our pure Go application
COPY --from=builder /app/enable-banking-go .

# Copy the OTel Auto-Instrumentation agent
COPY --from=otel-agent /otel-go-instrumentation .

# Standard OpenTelemetry environment variables (can be overridden by K8s / compose)
ENV OTEL_SERVICE_NAME=enable-banking-mcp
ENV OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318

# CRITICAL AUTO-INSTRUMENTATION SPECIFICATION:
# The OTel Go Auto-Instrumentation agent requires the exact path of the target
# executable to dynamically hook and trace functions.
ENV OTEL_GO_AUTO_TARGET_EXE=/app/enable-banking-go

ENTRYPOINT ["./otel-go-instrumentation", "./enable-banking-go"]
CMD ["server", "--config", "/etc/enable-banking/config.json"]
