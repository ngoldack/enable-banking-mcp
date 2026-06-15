# fin-mcp Helm chart

Deploys the `fin-mcp` MCP server over the **SSE** transport on Kubernetes as a
single, unprivileged, non-root container.

```bash
helm install fin-mcp ./deploy/helm/fin-mcp \
  --set config.existingConfigMap=fin-mcp-config \
  --set secrets.existingSecret=fin-mcp-secrets
```

## Config vs. secrets

Non-sensitive configuration is rendered into a **ConfigMap**; only the genuine
secrets go into a **Secret**, injected as env (bearer token, valkey password) or
a mounted file (private key). Standard 12-factor split.

| ConfigMap (`config.json`) | Secret |
|---|---|
| `providers` topology, `app_id`, connections incl. **`session_id`**, `mcp.*` operational settings, valkey **address** | private key (file), **bearer token** (`MCP_BEARER_TOKEN`), **valkey password** (`MCP_CACHE_VALKEY_PASSWORD`) |

`session_id` lives in the ConfigMap deliberately: an Enable Banking session is
only usable when each request is signed with the app **private key** (RS256 JWT),
so a session ID is inert on its own.

| Supply | ConfigMap | Secret |
|---|---|---|
| **Out-of-band (preferred)** | `config.existingConfigMap` | `secrets.existingSecret` |
| Chart-rendered | `config.providers` / `config.mcp.*` | `secrets.bearerToken` / `secrets.valkeyPassword` / `secrets.privateKeyContent` |

```bash
# Non-secret config (secret fields left blank in config.json):
kubectl create configmap fin-mcp-config --from-file=config.json=./config.json

# Secrets (any subset of these keys; all are optional):
kubectl create secret generic fin-mcp-secrets \
  --from-literal=bearer-token="$(openssl rand -hex 32)" \
  --from-literal=valkey-password="$VALKEY_PASSWORD" \
  --from-file=private.key=./private.key \
  --from-literal=authorization="Bearer $TOKEN"   # for kagent
```

> **Run behind TLS.** Terminate TLS at your ingress / mesh. The bearer token is
> header-only and compared in constant time, but must never traverse plaintext.

## Cache backends

Configured under `config.mcp.cache` (the valkey **password** is a secret —
`secrets.valkeyPassword`):

```yaml
config:
  mcp:
    cache:
      type: memory          # none | memory | valkey
      ttlMinutes: 5
      valkey:
        address: valkey.cache.svc:6379  # external server (NOT deployed by this chart)
        username: ""
        db: 0
        tls: true
secrets:
  valkeyPassword: ""        # strongly recommended
```

- **`none`** — caching disabled.
- **`memory`** — per-process, in-memory. Fast, no dependency; **not shared**
  across replicas. Scale to 1 replica or use valkey if you need a shared cache.
- **`valkey`** — shared, **external** Valkey/Redis. The chart does **not** deploy
  a Valkey server; point `address` at your own. Cached account data is stored
  there as plaintext, so set a **password** and **TLS** — the server warns at
  startup if either is missing.

## kagent integration

A kagent `Agent` injects the bearer token from a Secret via `headersFrom`. Reuse
the server's `secrets.existingSecret` (add an `authorization` key holding
`Bearer <token>`):

```yaml
apiVersion: kagent.dev/v1alpha1
kind: RemoteMCPServer
metadata: { name: fin-mcp, namespace: agents }
spec:
  url: http://fin-mcp.fin-mcp.svc.cluster.local:8090/sse
  protocol: SSE
---
apiVersion: kagent.dev/v1alpha1
kind: Agent
metadata: { name: banking-agent, namespace: agents }
spec:
  tools:
    - type: McpServer
      mcpServer:
        name: fin-mcp
        kind: RemoteMCPServer
        toolNames: [list-accounts, get-balances, list-transactions]
      headersFrom:
        - name: Authorization
          valueFrom: { type: Secret, name: fin-mcp-secrets, key: authorization }
```

See [../../../docs/deployment.md](../../../docs/deployment.md) for the full
walkthrough and the OAuth-via-gateway option.

## Values reference

| Key | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Replicas. Use `1` with the `memory` cache. |
| `image.repository` | `ghcr.io/ngoldack/fin-mcp` | Image. |
| `image.tag` | `latest` | Tag. |
| `config.existingConfigMap` | `""` | ConfigMap with a `config.json` key. Preferred. |
| `config.providers` | EB stub | Provider topology (chart-rendered path). |
| `config.mcp.accessMode` | `ReadOnly` | `ReadOnly` \| `InternalOnly` \| `Unrestricted`. |
| `config.mcp.port` | `8090` | SSE port. |
| `config.mcp.cache.*` | memory | Cache backend config (above). |
| `secrets.existingSecret` | `""` | Secret with keys `bearer-token`, `valkey-password`, `private.key`. Preferred. |
| `secrets.bearerToken` | `""` | SSE token → Secret → `MCP_BEARER_TOKEN`. |
| `secrets.valkeyPassword` | `""` | Valkey password → Secret → `MCP_CACHE_VALKEY_PASSWORD`. |
| `secrets.privateKeyContent` | `""` | PEM → Secret, mounted at `/etc/fin-mcp/keys/private.key`. |
| `otel.exporterEndpoint` | `""` | OTLP/HTTP collector; enables in-process telemetry. |
| `service.type` / `service.port` | `ClusterIP` / `8090` | Service. |
| `resources`, `nodeSelector`, `tolerations`, `affinity` | `{}`/`[]` | Standard scheduling. |

All pods run with a hardened `securityContext`: non-root uid 10001,
`readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, all capabilities
dropped.
