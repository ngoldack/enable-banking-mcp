# fin-mcp Helm chart

Deploys the `fin-mcp` MCP server over the **SSE** transport on Kubernetes as a
single, unprivileged, non-root container.

```bash
helm install fin-mcp ./deploy/helm/fin-mcp \
  --set config.existingSecret=fin-mcp-config
```

## Secret model

The **entire `config.json` is sensitive** — it contains bank session IDs and
consents, the SSE bearer token, and any cache secrets (Valkey password,
encryption key). The chart therefore renders it into a Kubernetes **Secret**
(never a ConfigMap) and mounts it at `/etc/fin-mcp/config.json`.

Two ways to supply it:

| Mode | How | When |
|---|---|---|
| **`config.existingSecret`** | You create the Secret out-of-band (keys: `config.json`, optional `private.key`). | **Production.** Nothing sensitive passes through Helm values, CI logs, or release history. |
| Chart-rendered | The chart builds `config.json` from `config.*` values into its own Secret. | Dev, or GitOps with sealed-secrets / SOPS. |

### Production: `config.existingSecret`

```bash
# Build config.json locally (e.g. via `fin-mcp config ...`), then:
kubectl create secret generic fin-mcp-config \
  --from-file=config.json=./config.json \
  --from-literal=authorization="Bearer $(jq -r .mcp.bearer_token config.json)"   # for kagent
```

```yaml
# values.yaml
config:
  existingSecret: fin-mcp-config
```

Put the Enable Banking private key inline in `config.json`
(`enable_banking.private_key_content`) so the whole credential set lives in this
one Secret.

> **Run behind TLS.** Terminate TLS at your ingress / mesh. The bearer token is
> header-only and compared in constant time, but must never traverse plaintext.

## Cache backends

Configured under `config.mcp.cache` (rendered into `config.json`):

```yaml
config:
  mcp:
    cache:
      type: memory          # none | memory | valkey
      ttlMinutes: 5
      encryption: encrypted # valkey only (encrypted | none)
      encryptionKey: ""     # base64 32-byte AES-256 key (sensitive)
      valkey:
        address: valkey.cache.svc:6379
        username: ""
        password: ""        # sensitive
        db: 0
        tls: true
```

- **`none`** — caching disabled.
- **`memory`** — per-process, in-memory. Fast, no dependency; **not shared**
  across replicas. Scale to 1 replica or use valkey if you need a shared cache.
- **`valkey`** — shared, external Valkey/Redis. Values are AES-256-GCM encrypted
  at rest by default (`encryption: encrypted`). Generate a key with
  `fin-mcp config init` (it writes `cache_encryption_key`) or
  `openssl rand -base64 32`.

## kagent integration

A kagent `Agent` injects the bearer token from a Secret via `headersFrom`. Reuse
the same `config.existingSecret` (add an `authorization` key holding
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
          valueFrom: { type: Secret, name: fin-mcp-config, key: authorization }
```

See [../../../docs/deployment.md](../../../docs/deployment.md) for the full
walkthrough and the OAuth-via-gateway option.

## Values reference

| Key | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Replicas. Use `1` with the `memory` cache. |
| `image.repository` | `ghcr.io/ngoldack/fin-mcp` | Image. |
| `image.tag` | `latest` | Tag. |
| `config.existingSecret` | `""` | Secret with `config.json` (+ optional `private.key`). Preferred. |
| `config.providers` | EB stub | Provider topology (chart-rendered path). |
| `config.mcp.accessMode` | `ReadOnly` | `ReadOnly` \| `InternalOnly` \| `Unrestricted`. |
| `config.mcp.port` | `8090` | SSE port. |
| `config.mcp.bearerToken` | `""` | SSE token (rendered into the Secret). |
| `config.mcp.cache.*` | memory | Cache backend config (above). |
| `privateKey.content` | `""` | PEM mounted at `/etc/fin-mcp/keys/private.key` (chart path). |
| `otel.exporterEndpoint` | `""` | OTLP/HTTP collector; enables in-process telemetry. |
| `service.type` / `service.port` | `ClusterIP` / `8090` | Service. |
| `resources`, `nodeSelector`, `tolerations`, `affinity` | `{}`/`[]` | Standard scheduling. |

All pods run with a hardened `securityContext`: non-root uid 10001,
`readOnlyRootFilesystem`, `allowPrivilegeEscalation: false`, all capabilities
dropped.
