# Deployment (Kubernetes, Helm & kagent)

The chart deploys the MCP server over the **SSE** transport — the right choice
for Kubernetes (`stdio` is for local clients). The image is a single, unprivileged,
non-root container.

## Install

```bash
helm install fin-mcp ./deploy/helm/fin-mcp \
  --set config.existingSecret=fin-mcp-config
```

The chart applies a hardened `securityContext` (non-root uid 10001,
`readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, all capabilities
dropped). The cache is in-memory by default (no volume). See the
[chart README](../deploy/helm/fin-mcp/README.md) for the full values reference.

## Secrets: the whole config is a Secret

`config.json` carries bank **session IDs / consents**, the **bearer token**, and
any **cache secrets** (Valkey password, encryption key). It is therefore rendered
into a Kubernetes **Secret** (never a ConfigMap) and mounted at
`/etc/fin-mcp/config.json`.

| Option | How | Use |
|---|---|---|
| **`config.existingSecret`** | A Secret you create out-of-band with a `config.json` key. | **Preferred** — nothing sensitive passes through Helm values, CI logs, or release history. |
| Chart-rendered | The chart builds `config.json` from `config.*` values into its own Secret. | Dev, or GitOps with sealed-secrets / SOPS. |
| _(no token)_ | Empty `bearer_token`. | Auth disabled — loopback/dev only; the server logs a startup warning. |

### Preferred: `config.existingSecret`

```bash
# Build config.json locally with `fin-mcp config ...`, then:
kubectl create secret generic fin-mcp-config \
  --from-file=config.json=./config.json \
  --from-literal=authorization="Bearer $(jq -r .mcp.bearer_token config.json)"   # for kagent
```

```yaml
# values.yaml
config:
  existingSecret: fin-mcp-config
```

Embed the private key inline in `config.json`
(`enable_banking.private_key_content`) so the whole credential set lives in this
one Secret. The `authorization` key (`Bearer <token>`) is for kagent.

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
            name: fin-mcp-config   # same Secret as the server
            key: authorization     # holds "Bearer <token>"
```

This is the secure pattern: the token lives **only** in the Kubernetes Secret,
referenced by both the server (inside the mounted `config.json`) and the agent
(as the `Authorization` header). Nothing is templated into agent specs or Helm
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

The cache is configured under `config.mcp.cache` and rendered into the config
Secret:

```yaml
config:
  mcp:
    cache:
      type: valkey                      # none | memory | valkey
      ttlMinutes: 5
      encryption: encrypted             # valkey: AES-256-GCM at rest (default)
      encryptionKey: "<base64-32-bytes>"
      valkey:
        address: valkey.cache.svc:6379
        password: "<password>"
        tls: true
```

- **`memory`** (default) is per-process — with `replicaCount > 1` each replica
  has its own cache. Use **`valkey`** for a shared cache across replicas (and
  across the TUI and the server).
- For `valkey`, cached values are **encrypted at rest** by default. Generate the
  key with `fin-mcp config init` or `openssl rand -base64 32`. The password and
  key are sensitive and live in the config Secret.
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
