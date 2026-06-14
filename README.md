# 🏦 Enable Banking Go Integration Suite

A high-performance, modular, and enterprise-ready Go workspace implementing an Open Banking SDK, a secure Model Context Protocol (MCP) server, and a Bubble Tea Terminal User Interface (TUI) **operator console** for **Enable Banking**.

---

## 🚀 Key Features

- **Standardized Go Open Banking SDK**: Robust API client supporting Account Information Services (AIS), balance checks, transaction history, and SEPA/Domestic payment initiation.
- **Embedded Local Cache (`BadgerDB`)**: Leverages `github.com/dgraph-io/badger` (a fast, transactional, LSM-tree key-value store) with native, configurable TTLs to share cached entries (`.bank.db`) between the TUI and the MCP server.
- **Read-Only TUI Operator Console & Setup Wizard**:
  - A focused tool to **set up, inspect, and verify** the Enable Banking ↔ MCP connection — not a consumer banking app.
  - Live connection panel: environment, bank, session, **consent-expiry countdown**, and MCP transport/access-mode/cache settings.
  - Idiomatic Charm components — `list` for accounts, `table` for balances & transactions, `spinner`, and a `help`/`key` keybinding bar — with a responsive, alt-screen layout.
  - A **"copy MCP client config"** overlay (press `c`) that prints a ready-to-paste snippet for Claude Desktop / Cursor.
  - Visual balance-abbreviation guide (`CLBD`, `ITBD`, …) and the interactive setup wizard with embedded auth-callback capture and search across 680+ banks.
- **Kubernetes-Ready Configuration**: Advanced config loader (`internal/config`) merging `config.json` with dynamic **Environment Variable Overrides** and automated schema/UUID validation.
- **Enterprise-Grade MCP Server**:
  - **Dual Transport Modes**: Run over `stdio` or as a remote HTTP service using `sse` (Server-Sent Events).
  - **Token-Based Security**: Complete authorization middleware protecting SSE GET connections and POST requests.
  - **Access Control Modes**: Keep your funds secure with granular write levels: `ReadOnly`, `InternalOnly` (restricted to transfers between your own linked accounts), or `Unrestricted`. Payment initiation lives **only** in the MCP server (gated by these modes), keeping the TUI read-only.

---

## 🛠️ Quick Start

### 1. Requirements
- **Go 1.25+**
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

## 🔒 Security & Access Control Modes

Configure the exact capabilities you grant to AI Agents by modifying the `mcp.access_mode` setting:

| Mode | Description | Security Level |
|---|---|---|
| **`ReadOnly`** | AI Agents can read accounts, balances, and transactions. Payments are strictly blocked. | 🛡️ Standard |
| **`InternalOnly`** | Transfers are permitted **only** if the destination IBAN matches one of your own linked bank accounts. | 🔐 High |
| **`Unrestricted`** | Transfers are allowed to any external or domestic destination IBAN. | ⚠️ Full Access |

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
        "redirect_url": "http://localhost:8080/callback",
        "connections": [
          { "name": "c24", "bank": "C24 Bank", "country": "DE", "session_id": "...", "consent_valid_until": "2026-09-14T15:00:00Z" },
          { "name": "revolut", "bank": "Revolut", "country": "LT", "session_id": "...", "consent_valid_until": "2026-09-20T10:00:00Z" }
        ]
      }
    }
  ],
  "mcp": {
    "access_mode": "ReadOnly",
    "transport": "stdio",
    "port": 8090,
    "bearer_token": "highly-secure-mcp-access-token"
  }
}
```

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
Mount the structured `providers` config as a ConfigMap/Secret file. The MCP server
settings remain env-overridable:

- `MCP_ACCESS_MODE` (`ReadOnly`, `InternalOnly`, `Unrestricted`)
- `MCP_TRANSPORT` (`stdio` or `sse`)
- `MCP_PORT` (e.g. `8090`)
- `MCP_BEARER_TOKEN` (authorizes incoming HTTP/SSE requests)
- `MCP_CACHE_TTL_MINUTES`, `MCP_LOG_FORMAT`, `MCP_LOG_LEVEL`

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
If running the server on a remote cluster or container:
```json
{
  "mcpServers": {
    "fin-mcp": {
      "url": "http://your-server-ip:8090/sse?token=highly-secure-mcp-access-token"
    }
  }
}
```

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
│                  - Persistent BadgerDB store (.bank.db)               │
│                  - Configurable TTL key-value expirations             │
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
  bank/                  #   Domain models, BadgerDB cache, error mapping
  mcp/                   #   MCP server (stdio + SSE transports)
  setup/                 #   Non-interactive setup & key generation
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
