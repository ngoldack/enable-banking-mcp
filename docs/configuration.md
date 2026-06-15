# Configuration

`fin-mcp` is configured by a JSON file (default `config.json`) layered with
`MCP_*` environment-variable overrides. The file is the source of truth (a
Kubernetes ConfigMap in production); env vars override server settings at
runtime (12-factor).

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
    "cache_path": ".bank.db",
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
| `cache_ttl_minutes` | `5` | `MCP_CACHE_TTL_MINUTES` | BadgerDB entry TTL. |
| `cache_path` | `.bank.db` | `MCP_CACHE_PATH` | Cache dir; set writable for a read-only rootfs. |
| `log_format` | `text` | `MCP_LOG_FORMAT` | `text` \| `json`. |
| `log_level` | `info` | `MCP_LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error`. |

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
