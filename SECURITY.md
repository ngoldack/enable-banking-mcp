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
| Bank **session IDs / consents** | High | config file (`connections[]`) |
| MCP **bearer token** (SSE) | High | env / k8s Secret |
| Cached accounts/balances/transactions | Medium | BadgerDB at `cache_path` |

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
- Optional **bearer-token** middleware guards every SSE GET and POST.
- The SDK's SSE handler enforces **DNS-rebinding protection** by default.
- Run behind TLS (ingress / service mesh); the bearer token must never traverse
  plaintext HTTP.

**Secrets**
- The private key is never baked into the image. Sources: file path, inline
  content, **OS keychain** (local only, via `zalando/go-keyring`), or a mounted
  Kubernetes Secret.
- `config show` redacts secrets. Logs go to **stderr** as structured `slog` and
  do not print key material; stdout carries only the JSON-RPC stream.

**Container / supply chain**
- The **image runs as non-root** (uid 10001), `allowPrivilegeEscalation:
  false`, `readOnlyRootFilesystem: true`, all capabilities dropped (see the Helm
  chart). The writable cache is an `emptyDir`.
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
