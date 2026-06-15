# Deployment (Kubernetes, Helm & kagent)

The chart deploys the MCP server over the **SSE** transport — the right choice
for Kubernetes (`stdio` is for local clients). The image is a single, unprivileged,
non-root container.

## Install

```bash
helm install fin-mcp ./deploy/helm/fin-mcp \
  --set config.existingConfigMap=fin-mcp-config \
  --set secrets.existingSecret=fin-mcp-secrets
```

The chart applies a hardened `securityContext` (non-root uid 10001,
`readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, all capabilities
dropped). The cache is in-memory by default (no volume). See the
[chart README](../deploy/helm/fin-mcp/README.md) for the full values reference.

## Config vs. secrets

Non-sensitive config goes in a **ConfigMap**; only the genuinely-secret values go
in a **Secret**, injected as env or a mounted file. This is the standard
12-factor split.

| Goes in the ConfigMap (`config.json`) | Goes in the Secret |
|---|---|
| `providers` topology, `app_id`, `redirect_url`, environment | **private key** (mounted file at `private_key_path`) |
| `connections` incl. **`session_id`** | **`bearer_token`** → `MCP_BEARER_TOKEN` env |
| `mcp.*` operational settings (access mode, transport, port, cache type, valkey **address**, logging) | **valkey password** → `MCP_CACHE_VALKEY_PASSWORD` env |

> **Why is `session_id` in the ConfigMap?** An Enable Banking session is only
> usable when each request is signed with the app **private key** (an RS256 JWT).
> A leaked session ID is inert on its own, so it does not need Secret-grade
> protection — the private key (which stays in the Secret) is the real
> credential. If your policy still requires it in a Secret, supply the whole
> `config.json` via `config.existingConfigMap` pointing at a Secret-backed
> projection, or keep the connections out of the chart-rendered ConfigMap.

The secrets are injected by env layering: the server reads them from
`MCP_BEARER_TOKEN` / `MCP_CACHE_VALKEY_PASSWORD`, which override the (omitted)
fields in the mounted `config.json`. The private key is mounted at
`private_key_path`.

### Preferred: bring your own ConfigMap + Secret

```bash
# Non-secret config (built with `fin-mcp config ...`, with secret fields blank):
kubectl create configmap fin-mcp-config --from-file=config.json=./config.json

# Secrets (any subset of these keys):
kubectl create secret generic fin-mcp-secrets \
  --from-literal=bearer-token="$(openssl rand -hex 32)" \
  --from-literal=valkey-password="$VALKEY_PASSWORD" \
  --from-file=private.key=./private.key \
  --from-literal=authorization="Bearer $TOKEN"   # for kagent (see below)
```

```yaml
# values.yaml
config:
  existingConfigMap: fin-mcp-config
secrets:
  existingSecret: fin-mcp-secrets
```

Or let the chart render both from `config.*` and `secrets.*` values (dev /
GitOps with sealed-secrets / SOPS). The `authorization` key (`Bearer <token>`)
is for kagent.

> **Do I need `redirect_url`?** Only for setup (SCA) and **payment initiation**.
> A read-only deployment with already-authorized sessions can leave it empty.

> **Always run behind TLS.** Terminate TLS at your ingress / service mesh. The
> bearer token must never traverse plaintext HTTP. It is accepted only in the
> `Authorization` header (never `?token=`) and compared in constant time.

## Integrating with kagent

kagent agents call a remote MCP server through a `RemoteMCPServer` and inject
auth headers from a Secret via `headersFrom` (resolved in the **agent's**
namespace). Point it at the same Secret (the `authorization` key above).

```yaml
apiVersion: kagent.dev/v1alpha1
kind: RemoteMCPServer
metadata:
  name: fin-mcp
  namespace: agents
spec:
  # The chart's Service, SSE endpoint.
  url: http://fin-mcp.fin-mcp.svc.cluster.local:8090/sse
  protocol: SSE
---
apiVersion: kagent.dev/v1alpha1
kind: Agent
metadata:
  name: banking-agent
  namespace: agents
spec:
  tools:
    - type: McpServer
      mcpServer:
        name: fin-mcp
        kind: RemoteMCPServer
        toolNames: [list-accounts, get-balances, list-transactions]
      headersFrom:
        - name: Authorization
          valueFrom:
            type: Secret
            name: fin-mcp-secrets  # the server's Secret
            key: authorization     # holds "Bearer <token>"
```

This is the secure pattern: the token lives **only** in the Kubernetes Secret,
referenced by both the server (injected as `MCP_BEARER_TOKEN`) and the agent (as
the `Authorization` header). Nothing is templated into agent specs or Helm
values.

> Scope the agent's `toolNames` to the least privilege it needs. Keep the server
> at `accessMode: ReadOnly` unless an agent must move money — see
> [configuration.md](configuration.md#access-control-modes).

### Full OAuth 2.1 (optional)

The built-in auth is a static shared token, not a full OAuth 2.1 resource
server. For multi-tenant or public exposure, front the server with a gateway
that terminates OAuth (agentgateway, Envoy, `oauth2-proxy`) instead of exposing
the static-token endpoint directly. See [../SECURITY.md](../SECURITY.md).

## Caching

The cache is configured under `config.mcp.cache` (rendered into the ConfigMap;
the valkey **password** goes in the Secret as `valkey-password`):

```yaml
config:
  mcp:
    cache:
      type: valkey                      # none | memory | valkey
      ttlMinutes: 5
      valkey:
        address: valkey.cache.svc:6379  # your external valkey (not deployed by this chart)
        tls: true
secrets:
  valkeyPassword: "<password>"          # -> Secret, injected as MCP_CACHE_VALKEY_PASSWORD
```

- **`memory`** (default) is per-process — with `replicaCount > 1` each replica
  has its own cache. Use **`valkey`** for a shared cache across replicas (and
  across the TUI and the server).
- `valkey` is **external only** — run/operate the server yourself; the chart does
  not deploy one. Cached account data is stored there as plaintext, so set a
  **password** (`secrets.valkeyPassword`) and **TLS**. The server logs a startup
  warning if either is missing.
- Cache hit/miss and latency are exported as OpenTelemetry metrics.

## Observability (OpenTelemetry)

Telemetry is in-process (OTel Go SDK) and opt-in:

```yaml
# values.yaml
otel:
  exporterEndpoint: http://otel-collector.observability.svc:4318
  serviceName: fin-mcp
```

When `otel.exporterEndpoint` is set the chart injects `OTEL_EXPORTER_OTLP_ENDPOINT`
and `OTEL_SERVICE_NAME`; traces (incl. outbound Enable Banking calls) and request
metrics flow over OTLP/HTTP. Unset = zero overhead. No privileges required.

## Image provenance

The image ships an SBOM + SLSA provenance attestations and is Cosign-signed
(keyless) by digest:

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/ngoldack/fin-mcp/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/ngoldack/fin-mcp:latest
```
