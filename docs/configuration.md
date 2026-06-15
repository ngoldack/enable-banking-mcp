# Configuration

`fin-mcp` is configured by a JSON file (default `config.json`) layered with
`MCP_*` environment-variable overrides. The file is the source of truth (a
Kubernetes **ConfigMap** in production); the genuine secrets — bearer token,
valkey password, private key — are injected via env / a mounted file from a
**Secret** (see [deployment.md](deployment.md#config-vs-secrets)). Env always
overrides the file (12-factor).

> Manage the file with the `fin-mcp config` / `fin-mcp setup` commands rather
> than editing by hand — see [setup.md](setup.md).

## Schema

```json
{
  "providers": [
    {
      "name": "enable-banking",
      "type": "enable-banking",
      "enable_banking": {
        "app_id": "your-36-char-uuid",
        "private_key_path": "private.key",
        "private_key_keyring": "",
        "private_key_content": "",
        "environment": "SANDBOX",
        "redirect_url": "http://localhost:8080/callback"
      },
      "connections": [
        { "name": "c24",     "bank": "C24 Bank", "country": "DE", "session_id": "...", "consent_valid_until": "2026-09-14T15:00:00Z" },
        { "name": "revolut", "bank": "Revolut",  "country": "LT", "session_id": "...", "consent_valid_until": "2026-09-20T10:00:00Z" }
      ]
    }
  ],
  "mcp": {
    "access_mode": "ReadOnly",
    "transport": "stdio",
    "port": 8090,
    "bearer_token": "",
    "cache_ttl_minutes": 5,
    "cache_type": "memory",
    "log_format": "text",
    "log_level": "info"
  }
}
```

### `providers[]`

A list of typed, named provider instances. Each has a `name`, a `type`, a
type-specific credentials block, and a provider-agnostic `connections[]` list.

| Field | Notes |
|---|---|
| `name` | Unique instance name (referenced by `--provider`). |
| `type` | `enable-banking` or `mock`. |
| `enable_banking` | Credentials block for `type: enable-banking` (below). |
| `mock` | `{ "accounts": N }` for `type: mock` (testing/demo). |
| `connections[]` | **Provider-agnostic, first-class.** One authorized bank link exposing one or more accounts. |

> **Schema note:** connections live on the provider (`providers[].connections`),
> not inside `enable_banking`. This is the only supported layout — the legacy
> nested location has been removed.

#### `enable_banking`

| Field | Notes |
|---|---|
| `app_id` | Enable Banking Application ID (36-char UUID). |
| `private_key_path` | Path to the RSA private key PEM. |
| `private_key_content` | Inline PEM (alternative to a path). |
| `private_key_keyring` | OS keychain account (local only; never in Kubernetes). |
| `environment` | `SANDBOX` or `PRODUCTION`. **`PRODUCTION` moves real money.** |
| `redirect_url` | SCA callback URL registered with the application. |

#### `connections[]`

| Field | Notes |
|---|---|
| `name` | Unique connection name within the provider. |
| `bank` | Institution (ASPSP) display name. |
| `country` | ISO 3166-1 alpha-2 code (e.g. `DE`, `LT`). |
| `session_id` | Opaque provider session/consent handle. |
| `consent_valid_until` | RFC 3339 consent expiry. |
| `metadata` | Optional `map[string]string` for provider-specific extras. |

### `mcp`

| Field | Default | Env override | Notes |
|---|---|---|---|
| `access_mode` | `ReadOnly` | `MCP_ACCESS_MODE` | `ReadOnly` \| `InternalOnly` \| `Unrestricted`. |
| `transport` | `stdio` | `MCP_TRANSPORT` | `stdio` \| `sse`. |
| `port` | `8090` | `MCP_PORT` | SSE listen port. |
| `bearer_token` | _(empty)_ | `MCP_BEARER_TOKEN` | SSE auth token; empty disables auth (loopback only). |
| `cache_type` | `memory` | `MCP_CACHE_TYPE` | `none` \| `memory` \| `valkey`. |
| `cache_ttl_minutes` | `5` | `MCP_CACHE_TTL_MINUTES` | Entry TTL. |
| `cache_valkey_address` | _(empty)_ | `MCP_CACHE_VALKEY_ADDRESS` | `host:port` (required for valkey). |
| `cache_valkey_username` | _(empty)_ | `MCP_CACHE_VALKEY_USERNAME` | Valkey ACL user. |
| `cache_valkey_password` | _(empty)_ | `MCP_CACHE_VALKEY_PASSWORD` | Valkey password (**secret**). |
| `cache_valkey_db` | `0` | `MCP_CACHE_VALKEY_DB` | Valkey logical DB. |
| `cache_valkey_tls` | `false` | `MCP_CACHE_VALKEY_TLS` | Use TLS to Valkey (recommended). |
| `log_format` | `text` | `MCP_LOG_FORMAT` | `text` \| `json`. |
| `log_level` | `info` | `MCP_LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error`. |

## Caching

The cache is pluggable and optional. `cache_type` selects the backend:

| Backend | Shared? | Notes |
|---|---|---|
| `none` | — | Caching disabled; every read hits the provider. |
| `memory` | **No** (per-process) | Default. Fast, no dependency. The TUI and the server keep **independent** caches. |
| `valkey` | **Yes** | External Valkey/Redis; shared across processes/replicas. |

`valkey` connects to an **external** server (run/operate it yourself). Cached
account data is stored there, so harden the connection:

- Set `cache_valkey_password` — without it, anyone who can reach the server can
  read cached account data. The server logs a warning at startup if it is empty.
- Set `cache_valkey_tls: true` — without TLS the data and password cross the
  network in plaintext. The server also warns if TLS is off.

Cache hit/miss counts and operation latency are exported as OpenTelemetry
metrics (`fin_mcp.cache.requests`, `fin_mcp.cache.operation.duration`).

> The valkey **password** is a secret — supply it via `MCP_CACHE_VALKEY_PASSWORD`
> (a k8s Secret), not the config file/ConfigMap. See
> [deployment.md](deployment.md#config-vs-secrets) for the config/secret split.

## Environment overrides

Every `mcp.*` key maps to an `MCP_<UPPER_SNAKE>` variable. Env always wins over
the file, so the same image runs across environments by overriding settings:

```bash
MCP_TRANSPORT=sse MCP_PORT=8090 MCP_LOG_FORMAT=json \
  fin-mcp server --config /etc/fin-mcp/config.json
```

Secrets (`MCP_BEARER_TOKEN`, and the private key) should come from a mounted
Secret or the environment — never committed to the config file in production.

## Access-control modes

| Mode | Behavior |
|---|---|
| `ReadOnly` | Reads only; all payment tools are blocked. **Default.** |
| `InternalOnly` | Transfers allowed only to your own linked IBANs. |
| `Unrestricted` | Transfers allowed to any destination IBAN. |

Payment initiation lives only in the MCP server (gated by these modes); the TUI
is read-only.

## Observability

Telemetry is opt-in and in-process (OpenTelemetry Go SDK). Set
`OTEL_EXPORTER_OTLP_ENDPOINT` (and optionally `OTEL_SERVICE_NAME`) to export
traces + metrics over OTLP/HTTP; leave it unset for zero overhead. See
[deployment.md](deployment.md#observability-opentelemetry).
