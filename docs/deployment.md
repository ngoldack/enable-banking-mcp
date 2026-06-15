# Deployment (Kubernetes, Helm & kagent)

The chart deploys the MCP server over the **SSE** transport — the right choice
for Kubernetes (`stdio` is for local clients). The image is a single, unprivileged,
non-root container.

## Install

```bash
helm install fin-mcp ./deploy/helm/fin-mcp \
  --set image.repository=ghcr.io/ngoldack/fin-mcp \
  --set mcp.existingSecret=fin-mcp-auth \
  --set privateKey.content="$(cat private.key)" \
  --set-file config.providers=...   # or edit values.yaml
```

The chart renders the provider topology into a ConfigMap, mounts a writable
cache (`emptyDir`, `MCP_CACHE_PATH`), and applies a hardened `securityContext`
(non-root uid 10001, `readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`,
all capabilities dropped).

## The bearer token — three options

| Option | How | Use |
|---|---|---|
| **`existingSecret`** | Reference a Secret you created out-of-band. | **Preferred** — the token never passes through Helm values, CI logs, or release history. |
| `bearerToken` | Inline value via `--set`/values. | Dev only — the value lands in the Helm release Secret. |
| _(neither)_ | Auth disabled. | Loopback/dev only. The server logs a startup warning. |

### Preferred: `existingSecret`

Create the Secret yourself (kept out of Git and CI):

```bash
TOKEN="$(openssl rand -hex 32)"
kubectl create secret generic fin-mcp-auth \
  --from-literal=bearer-token="$TOKEN" \
  --from-literal=authorization="Bearer $TOKEN"   # for kagent (see below)
```

```yaml
# values.yaml
mcp:
  existingSecret: fin-mcp-auth
  bearerTokenKey: bearer-token   # key the server reads as MCP_BEARER_TOKEN
```

The server reads `bearer-token` (the raw token). The extra `authorization` key
(`Bearer <token>`) is for kagent, which sends the full header value verbatim.

> **Always run behind TLS.** Terminate TLS at your ingress / service mesh. The
> bearer token must never traverse plaintext HTTP. The token is accepted only in
> the `Authorization` header (never `?token=`) and compared in constant time.

## Integrating with kagent

kagent agents call a remote MCP server through a `RemoteMCPServer` and inject
auth headers from a Secret via `headersFrom` (resolved in the **agent's**
namespace). Point both at the same Secret created above.

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
            name: fin-mcp-auth     # same Secret as the server
            key: authorization     # holds "Bearer <token>"
```

This is the secure pattern: the token lives **only** in the Kubernetes Secret,
referenced by both the server (as `MCP_BEARER_TOKEN`) and the agent (as the
`Authorization` header). Nothing is templated into agent specs or Helm values.

> Scope the agent's `toolNames` to the least privilege it needs. Keep the server
> at `accessMode: ReadOnly` unless an agent must move money — see
> [configuration.md](configuration.md#access-control-modes).

### Full OAuth 2.1 (optional)

The built-in auth is a static shared token, not a full OAuth 2.1 resource
server. For multi-tenant or public exposure, front the server with a gateway
that terminates OAuth (agentgateway, Envoy, `oauth2-proxy`) instead of exposing
the static-token endpoint directly. See [../SECURITY.md](../SECURITY.md).

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
