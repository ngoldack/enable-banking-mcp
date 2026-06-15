# fin-mcp documentation

A provider-agnostic Open Banking suite: a reusable Go SDK, an MCP server, and a
read-only TUI operator console.

## Guides

- **[Configuration](configuration.md)** — the `config.json` schema (providers &
  connections), `MCP_*` environment overrides, transports, access modes, cache.
- **[Setup & Providers](setup.md)** — the provider-agnostic setup (TUI wizard,
  flag-driven `setup`, `config` lifecycle commands) and how to add a new provider.
- **[Deployment](deployment.md)** — Helm, the secure bearer-token options
  (`existingSecret`), **kagent** integration, OpenTelemetry, image verification.
- **[Security](../SECURITY.md)** — threat model, authentication model & roadmap,
  controls, supply-chain attestations.

## Components

| Component | Path | Purpose |
|---|---|---|
| SDK | `pkg/enablebanking` | Reusable Enable Banking API client (telemetry-agnostic). |
| Provider port | `internal/provider` | Provider-agnostic runtime interface + registry. |
| Setup port | `internal/setupflow` | Provider-agnostic setup/auth flow interface + registry. |
| MCP server | `internal/mcp` | Tools, access-control modes, SSE/stdio transports. |
| TUI | `internal/tui` | Read-only operator console + setup wizard. |
| Config | `internal/config` | Layered config loader (koanf), strong types. |

## Transports

- **`stdio`** — for local MCP clients (Claude Desktop, Cursor). No network auth;
  the parent process is the trust boundary.
- **`sse`** — HTTP/Server-Sent Events for remote/Kubernetes use, guarded by a
  bearer token. Run behind TLS. See [deployment.md](deployment.md).
