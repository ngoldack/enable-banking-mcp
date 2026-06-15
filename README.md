# 🏦 Enable Banking Go Integration Suite

A high-performance, modular, and enterprise-ready Go workspace implementing an Open Banking SDK, a secure Model Context Protocol (MCP) server, and a Bubble Tea Terminal User Interface (TUI) **operator console**. The runtime and setup are **provider-agnostic** (a provider port + a setup-flow port); **Enable Banking** is the primary provider, with a built-in `mock` provider for testing and a clear path to add more — see [docs/setup.md](docs/setup.md#extending-add-a-new-provider).

---

## 🚀 Key Features

- **Standardized Go Open Banking SDK**: Robust API client supporting Account Information Services (AIS), balance checks, transaction history, and SEPA/Domestic payment initiation.
- **Pluggable cache**: choose `none` (disabled), `memory` (in-process), or `valkey` (shared, external) with a configurable TTL. The server warns if a valkey cache runs without a password or TLS. Cache hit/miss + latency are exported as OpenTelemetry metrics.
- **Read-Only TUI Operator Console & Setup Wizard**:
  - A focused tool to **set up, inspect, and verify** the Enable Banking ↔ MCP connection — not a consumer banking app.
  - Live connection panel: environment, bank, session, **consent-expiry countdown**, and MCP transport/access-mode/cache settings.
  - Idiomatic Charm components — `list` for accounts, `table` for balances & transactions, `spinner`, and a `help`/`key` keybinding bar — with a responsive, alt-screen layout.
  - A **"copy MCP client config"** overlay (press `c`) that prints a ready-to-paste snippet for Claude Desktop / Cursor.
  - Visual balance-abbreviation guide (`CLBD`, `ITBD`, …) and the interactive setup wizard with embedded auth-callback capture and search across 680+ banks.
- **Kubernetes-Ready Configuration**: Advanced config loader (`internal/config`) merging `config.json` with dynamic **Environment Variable Overrides** and automated schema/UUID validation.
- **Enterprise-Grade MCP Server**:
  - **Dual Transport Modes**: Run over `stdio` or as a remote HTTP service using `sse` (Server-Sent Events).
  - **Hardened bearer-token auth (SSE)**: a static bearer token guards every SSE request, accepted **only** in the `Authorization` header (never the URL query string), compared in constant time, with a `WWW-Authenticate` challenge on `401`. Run behind TLS. For full OAuth 2.1, front with a gateway — see [docs/deployment.md](docs/deployment.md).
  - **Access Control Modes**: Keep your funds secure with granular write levels: `ReadOnly`, `InternalOnly` (restricted to transfers between your own linked accounts), or `Unrestricted`. Payment initiation lives **only** in the MCP server (gated by these modes), keeping the TUI read-only.

---

## 🛠️ Quick Start

### 1. Requirements
- **Go 1.26+**
- An active account on the [Enable Banking Developer Dashboard](https://enablebanking.com) to obtain your **Application ID**.

### 2. Interactive Setup
Run the setup wizard to generate private keys, register certificates, select your bank, and complete Strong Customer Authentication (SCA):
```bash
go run ./cmd/fin-mcp setup
```

### 3. Launch the TUI Operator Console
Once authorized, open the console to inspect the connection, accounts, balances, and transactions (read-only), and to copy your MCP client config:
```bash
go run ./cmd/fin-mcp tui
```

### 4. Run MCP Server
Start the Model Context Protocol server to connect your bank accounts to any AI Agent (e.g., Claude Desktop, Gemini):
```bash
go run ./cmd/fin-mcp server --config config.json
```

---

## 📚 Documentation

Full guides live in **[`docs/`](docs/)**:

- **[Configuration](docs/configuration.md)** — config schema, env overrides, transports, access modes.
- **[Setup & Providers](docs/setup.md)** — provider-agnostic setup (wizard, flags, `config` commands) and adding a provider.
- **[Deployment](docs/deployment.md)** — Helm, `existingSecret`, **kagent** integration, OpenTelemetry.
- **[Security](SECURITY.md)** — threat model, auth model & roadmap, supply chain.

---

## 🔒 Security & Access Control Modes

Configure the exact capabilities you grant to AI Agents by modifying the `mcp.access_mode` setting:

| Mode | Description | Security Level |
|---|---|---|
| **`ReadOnly`** | AI Agents can read accounts, balances, and transactions. Payments are strictly blocked. | 🛡️ Standard |
| **`InternalOnly`** | Transfers are permitted **only** if the destination IBAN matches one of your own linked bank accounts. | 🔐 High |
| **`Unrestricted`** | Transfers are allowed to any external or domestic destination IBAN. | ⚠️ Full Access |

Tools carry MCP **annotations** (`readOnlyHint` on reads; `destructiveHint` on
`initiate-transfer`/`submit-transfer`) so clients can require confirmation for
money-moving actions. Transfer inputs are validated (IBAN format, positive
amount). Full details in **[SECURITY.md](SECURITY.md)**.

---

## 📂 Configuration Layout (`config.json`)

The config holds a list of typed **providers**. An Enable Banking provider carries
the app credentials and one or more **connections** — each connection is one
authorized bank link (an Enable Banking session) exposing one or more accounts
(e.g. C24 with sub-accounts, Revolut with sub-accounts).

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
        "environment": "SANDBOX",
        "redirect_url": "http://localhost:8080/callback"
      },
      "connections": [
        { "name": "c24", "bank": "C24 Bank", "country": "DE", "session_id": "...", "consent_valid_until": "2026-09-14T15:00:00Z" },
        { "name": "revolut", "bank": "Revolut", "country": "LT", "session_id": "...", "consent_valid_until": "2026-09-20T10:00:00Z" }
      ]
    }
  ],
  "mcp": {
    "access_mode": "ReadOnly",
    "transport": "stdio",
    "port": 8090,
    "bearer_token": "highly-secure-mcp-access-token",
    "cache_type": "memory"
  }
}
```

See **[docs/configuration.md](docs/configuration.md)** for the full schema
(all `mcp.*` fields, `MCP_*` env overrides, cache backends, access modes).

Manage it with the CLI instead of editing by hand:

```bash
fin-mcp config init                                   # bootstrap the file
fin-mcp config provider add --name enable-banking --type enable-banking --app-id <UUID>
fin-mcp config connection add --bank "C24 Bank" --country DE          # prints SCA URL
fin-mcp config connection add --bank "C24 Bank" --country DE --code <CODE>
fin-mcp config connection list
fin-mcp config connection refresh                     # re-verify + refresh consent
fin-mcp config validate
fin-mcp config provider remove enable-banking
```

The private key can be stored in the OS keychain for local runs
(`setup --keychain`, sets `private_key_keyring`); never used in Kubernetes — there,
mount it as a Secret file (`private_key_path`) or inline (`private_key_content`).

### ⛵ Kubernetes
Non-secret config (provider topology + connections) goes in a **ConfigMap**; only
the genuine secrets go in a **Secret**, injected as env or a mounted file:

- **ConfigMap** (`config.existingConfigMap`): `providers`, `connections` (incl. `session_id` — inert without the private key), and all operational `mcp.*` settings.
- **Secret** (`secrets.existingSecret`): the private key (mounted file), `bearer-token` → `MCP_BEARER_TOKEN`, `valkey-password` → `MCP_CACHE_VALKEY_PASSWORD`.

Operational MCP settings are also env-overridable: `MCP_ACCESS_MODE`,
`MCP_TRANSPORT`, `MCP_PORT`, `MCP_CACHE_TYPE`, `MCP_CACHE_TTL_MINUTES`,
`MCP_LOG_FORMAT`, `MCP_LOG_LEVEL`.

See **[docs/deployment.md](docs/deployment.md)** for the config/secret split, the
chart, and **kagent** integration.

---

## 🔌 Integrating with Claude Desktop / Cursor

To connect the MCP server to **Claude Desktop**, add the following block to your `claude_desktop_config.json`:

### Stdio Transport (Default)
```json
{
  "mcpServers": {
    "fin-mcp": {
      "command": "go",
      "args": ["run", "./cmd/fin-mcp", "server", "--config", "/absolute/path/to/config.json"]
    }
  }
}
```

### SSE Transport (HTTP Remote)
If running the server on a remote cluster or container. The token goes in the
`Authorization` header (the `?token=` query string is **not** accepted):
```json
{
  "mcpServers": {
    "fin-mcp": {
      "url": "https://your-server/sse",
      "headers": { "Authorization": "Bearer highly-secure-mcp-access-token" }
    }
  }
}
```

---

## 🐳 Container Images & ☸️ Kubernetes

A single, unprivileged image is built, scanned (Trivy), attested (SBOM + SLSA
provenance) and Cosign-signed on every push to `main`:

| Image | Use | Notes |
|---|---|---|
| `ghcr.io/ngoldack/fin-mcp` | **The image.** | Runs **non-root** (uid 10001), no privileges. |

Observability is in-process via the **OpenTelemetry Go SDK** (traces + metrics
over OTLP/HTTP) — there is no eBPF agent and no privileged container. Telemetry
is opt-in: set `OTEL_EXPORTER_OTLP_ENDPOINT` (e.g. `http://otel-collector:4318`)
to enable it; leave it unset and the SDK installs nothing (zero overhead).

Deploy the SSE server with the Helm chart:

```bash
# Non-secret config -> ConfigMap; the genuine secrets -> Secret:
kubectl create configmap fin-mcp-config --from-file=config.json=./config.json
kubectl create secret generic fin-mcp-secrets \
  --from-literal=bearer-token="$(openssl rand -hex 32)" \
  --from-file=private.key=./private.key
helm install fin-mcp ./deploy/helm/fin-mcp \
  --set config.existingConfigMap=fin-mcp-config \
  --set secrets.existingSecret=fin-mcp-secrets \
  --set otel.exporterEndpoint="http://otel-collector:4318"   # optional
```

The chart puts non-secret config (providers + connections + operational `mcp.*`)
in a **ConfigMap** and only the real secrets (private key, bearer token, valkey
password) in a **Secret** — the latter two injected as `MCP_*` env. It mounts
everything read-only and applies a hardened `securityContext` (non-root,
read-only rootfs, dropped capabilities). The default `memory` cache needs no
volume. See the **[chart README](deploy/helm/fin-mcp/README.md)**.

See **[SECURITY.md](SECURITY.md)** for the threat model, controls, and image
verification with Cosign.

---

## 🏛️ Domain Architecture

```
                       ┌─────────────────────────┐
                       │   Enable Banking API    │
                       └────────────┬────────────┘
                                    │ (Raw SDK Models — pkg/enablebanking)
                                    ▼
                       ┌─────────────────────────┐
                       │ internal/bank/mapping.go│
                       └────────────┬────────────┘
                                    │ (Simplified Clean Domain Models)
                                    ▼
┌───────────────────────────────────┴───────────────────────────────────┐
│                       internal/bank/cache.go                          │
│           - Pluggable cache: none | memory | valkey (shared)          │
│           - Configurable TTL; OpenTelemetry hit/miss metrics          │
└───────────┬───────────────────────────────────────────┬───────────────┘
            │                                           │
            ▼                                           ▼
┌────────────────────────┐                  ┌───────────────────────┐
│ internal/tui/dashboard │                  │ internal/mcp/server.go│
│ (Bubble Tea Dashboard) │                  │  (Remote/Local MCP)   │
└────────────────────────┘                  └───────────────────────┘
```

### Project Layout

```
cmd/fin-mcp/   # Thin main() entrypoint
internal/                # Private application code
  cli/                   #   Kong command tree (Run() pattern)
  config/                #   Config loading, env overrides & validation
  bank/                  #   Domain models, pluggable cache, error mapping
  provider/              #   Provider-agnostic runtime port + registry + adapters
  setupflow/             #   Provider-agnostic setup/auth flow port + registry
  setup/                 #   Provider-agnostic setup orchestration
  telemetry/             #   In-process OpenTelemetry (traces + metrics)
  mcp/                   #   MCP server (stdio + SSE transports)
  tui/                   #   Bubble Tea dashboard, wizard & shared styles
pkg/
  enablebanking/         # Externally consumable Open Banking SDK
```


---

## 🧪 Running Tests

Verify the entire SDK suite (utilizing dynamic rsa key generation and mocked loopback API servers):
```bash
go test -v ./...
```
