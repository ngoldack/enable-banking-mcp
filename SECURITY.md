# Security

## Reporting a vulnerability

Please report security issues privately via a GitHub Security Advisory
(`Security` → `Report a vulnerability`) on this repository, or by email to the
maintainer. Do not open public issues for vulnerabilities. You will receive an
acknowledgement within a few days.

## Assets & trust boundaries

| Asset | Sensitivity | Where it lives |
|---|---|---|
| Enable Banking app **private key** (PEM) | Critical | file, inline config, or OS keychain (local); mounted Secret (k8s) |
| Bank **session IDs / consents** | Low-Medium | config file / ConfigMap (`connections[]`) — inert without the private key |
| MCP **bearer token** (SSE) | High | k8s Secret → `MCP_BEARER_TOKEN` env |
| Valkey **password** | High | k8s Secret → `MCP_CACHE_VALKEY_PASSWORD` env |
| Cached accounts/balances/transactions | Medium | process memory, or an external valkey (plaintext — protect with password + TLS) |

Trust boundaries:

- **MCP client (AI agent) → server.** Treated as semi-trusted. Write actions are
  gated by access mode; inputs are validated.
- **Server → Enable Banking API.** Outbound TLS; authenticated with a per-request
  RS256 JWT signed by the app private key.
- **Operator → config file.** The config file is the source of truth and must be
  protected (it holds session IDs); secrets should be referenced, not inlined.

## Controls in place

**Access control**
- Payment tools are gated by `mcp.access_mode`: `ReadOnly` (default — all
  transfers blocked), `InternalOnly` (transfers only to your own linked IBANs),
  `Unrestricted`.
- `initiate-transfer` / `submit-transfer` are annotated `destructiveHint: true`,
  `readOnlyHint: false`; read tools are annotated `readOnlyHint: true`, so MCP
  clients can require confirmation for writes.
- Transfer inputs are validated (IBAN length/charset, positive decimal amount).

**Transport / network (SSE)**
- A static **bearer token** guards every SSE request. Hardened per the MCP
  authorization spec:
  - The token is accepted **only** in the `Authorization: Bearer <token>` header.
    The URL query-string token (`?token=`) is rejected — it leaks via proxy
    access logs, the `Referer` header, and browser history.
  - The token is compared in **constant time** (`crypto/subtle`) to avoid a
    timing side-channel.
  - A `401` response carries a `WWW-Authenticate: Bearer` header.
  - Starting SSE without a token logs a startup warning; do not do this off
    loopback.
- The SDK's SSE handler enforces **DNS-rebinding protection** by default.
- **Run behind TLS** (ingress / service mesh). The bearer token must never
  traverse plaintext HTTP. `stdio` needs no token (the parent process is the
  trust boundary; credentials come from the environment).

**Authentication model & roadmap**
- The built-in mechanism is a **static shared bearer token** — appropriate for
  cluster-internal use and for kagent (which injects the token from a Kubernetes
  Secret via `RemoteMCPServer` + `headersFrom`). It is **not** a full OAuth 2.1
  resource server: no per-token audience binding, expiry, or rotation.
- For multi-tenant or public exposure requiring the full MCP OAuth 2.1 flow
  (RFC 9728 protected-resource metadata, RFC 8707 audience-bound tokens), **front
  the server with a gateway** that terminates OAuth — e.g. agentgateway, Envoy,
  or `oauth2-proxy` — rather than exposing the static-token endpoint directly.
  This is the idiomatic Kubernetes pattern and keeps the server itself simple.

**Secrets**
- The private key is never baked into the image. Sources: file path, inline
  content, **OS keychain** (local only, via `zalando/go-keyring`), or a mounted
  Kubernetes Secret.
- In Kubernetes, only the genuine secrets are a **Secret**: the private key
  (mounted file), the bearer token (`MCP_BEARER_TOKEN`), and the valkey password
  (`MCP_CACHE_VALKEY_PASSWORD`). The rest — provider topology and connections
  (incl. `session_id`) — is a **ConfigMap**. A session ID is inert without the
  private key (every Enable Banking request is signed with an RS256 JWT), so it
  does not need Secret-grade protection.
- The **valkey cache is external and stores values in plaintext**; it is a
  shared store for already-fetched account data. Protect it with a
  **password** (`cache_valkey_password`) and **TLS** (`cache_valkey_tls`) — the
  server logs a startup warning if either is missing. The `memory` and `none`
  backends keep nothing in external storage.
- `config show` redacts secrets. Logs go to **stderr** as structured `slog` and
  do not print key material; stdout carries only the JSON-RPC stream.

**Container / supply chain**
- The **image runs as non-root** (uid 10001), `allowPrivilegeEscalation:
  false`, `readOnlyRootFilesystem: true`, all capabilities dropped (see the Helm
  chart). The default `memory` cache needs no writable volume; the optional
  `valkey` cache is an external service.
- Observability is **in-process** (OpenTelemetry Go SDK, OTLP/HTTP). There is no
  eBPF agent and no privileged container; telemetry never widens the runtime
  attack surface.
- The single image is scanned with **Trivy** (CRITICAL/HIGH → GitHub Security
  tab), ships **SBOM + SLSA provenance** attestations, and is **Cosign-signed**
  (keyless, OIDC) by digest.

## Residual risks & hardening guidance

- Telemetry is opt-in via `OTEL_EXPORTER_OTLP_ENDPOINT`. Point it only at a
  trusted collector; spans/metrics may carry account identifiers and operational
  metadata.
- Use `Environment: PRODUCTION` deliberately — it moves real money.
- Rotate the bearer token; scope and renew bank consents; keep `access_mode` at
  the least privilege the use case needs.
- Provider/API errors are surfaced to clients; avoid attaching debug detail in
  forks. Rate limiting is not yet implemented (out of scope for stdio).

## Verifying a released image

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/ngoldack/fin-mcp/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/ngoldack/fin-mcp:latest
```
