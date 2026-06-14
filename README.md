# 🏦 Enable Banking Go Integration Suite

A high-performance, modular, and enterprise-ready Go workspace implementing an Open Banking SDK, a secure Model Context Protocol (MCP) server, and a beautiful Bubble Tea Terminal User Interface (TUI) account dashboard for **Enable Banking**.

---

## 🚀 Key Features

- **Standardized Go Open Banking SDK**: Robust API client supporting Account Information Services (AIS), balance checks, transaction history, and SEPA/Domestic payment transfers.
- **Acid-Compliant Local Caching (`bbolt`)**: Leverages `go.etcd.io/bbolt` (etcd's battle-tested embedded key-value DB) to share thread-safe, fast background cache entries (`.bank.db`) between the TUI and the MCP Server.
- **Interactive TUI Dashboard & Setup Wizard**:
  - Automatically captures auth codes using an embedded callback listener.
  - Features real-time search filtering across 680+ German/European banks.
  - Structured like a clean, beautiful mobile app (Overview -> Account Details -> Transfer).
  - High-fidelity tables built with Lipgloss.
  - Visual Help tooltips defining standardized banking abbreviations (`CLBD`, `ITBD`, etc.).
- **Kubernetes-Ready Configuration**: Advanced config loader (`pkg/config`) merging `config.json` with dynamic **Environment Variable Overrides** and automated schema/UUID validation.
- **Enterprise-Grade MCP Server**:
  - **Dual Transport Modes**: Run over `stdio` or as a remote HTTP service using `sse` (Server-Sent Events).
  - **Token-Based Security**: Complete authorization middleware protecting SSE GET connections and POST requests.
  - **Access Control Modes**: Keep your funds secure with granular write levels: `ReadOnly`, `InternalOnly` (restricted to transfers between your own linked accounts), or `Unrestricted`.

---

## 🛠️ Quick Start

### 1. Requirements
- **Go 1.25+**
- An active account on the [Enable Banking Developer Dashboard](https://enablebanking.com) to obtain your **Application ID**.

### 2. Interactive Setup
Run the setup wizard to generate private keys, register certificates, select your bank, and complete Strong Customer Authentication (SCA):
```bash
go run ./cmd/enable-banking-go setup
```

### 3. Launch TUI Account Dashboard
Once authorized, open your gorgeous, mobile-style terminal dashboard:
```bash
go run ./cmd/enable-banking-go tui
```

### 4. Run MCP Server
Start the Model Context Protocol server to connect your bank accounts to any AI Agent (e.g., Claude Desktop, Gemini):
```bash
go run ./cmd/enable-banking-go server --config config.json
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

The config file separates the Enable Banking API parameters from MCP server configurations:

```json
{
  "enable_banking": {
    "app_id": "your-36-char-uuid",
    "private_key_path": "private.key",
    "private_key_content": "-----BEGIN RSA PRIVATE KEY-----\nMIIE...",
    "environment": "SANDBOX",
    "redirect_url": "http://localhost:8080/callback",
    "bank_name": "Mock Bank DE",
    "bank_country": "DE",
    "session_id": "authenticated-session-uuid",
    "consent_valid_until": "2026-09-14T15:00:00Z"
  },
  "mcp": {
    "access_mode": "ReadOnly",
    "transport": "stdio",
    "port": 8090,
    "bearer_token": "highly-secure-mcp-access-token"
  }
}
```

### ⛵ Kubernetes Environment Variables
You can override any setting inside your Kubernetes manifests (or local terminal) using these environment variables:

- `ENABLE_BANKING_APP_ID` (e.g. `ENABLE_BANKING_APP_ID="ad3c5dd5-..."`)
- `ENABLE_BANKING_PRIVATE_KEY_CONTENT` (allows putting the raw private key PEM inline in K8s Secrets!)
- `ENABLE_BANKING_ENVIRONMENT` (`SANDBOX` or `PRODUCTION`)
- `ENABLE_BANKING_REDIRECT_URL`
- `ENABLE_BANKING_SESSION_ID`
- `MCP_ACCESS_MODE` (`ReadOnly`, `InternalOnly`, `Unrestricted`)
- `MCP_TRANSPORT` (`stdio` or `sse`)
- `MCP_PORT` (e.g. `8090`)
- `MCP_BEARER_TOKEN` (used to authorize incoming HTTP/SSE requests)

---

## 🔌 Integrating with Claude Desktop / Cursor

To connect the MCP server to **Claude Desktop**, add the following block to your `claude_desktop_config.json`:

### Stdio Transport (Default)
```json
{
  "mcpServers": {
    "enable-banking": {
      "command": "go",
      "args": ["run", "./cmd/enable-banking-go", "server", "--config", "/absolute/path/to/config.json"]
    }
  }
}
```

### SSE Transport (HTTP Remote)
If running the server on a remote cluster or container:
```json
{
  "mcpServers": {
    "enable-banking": {
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
                                    │ (Raw SDK Models)
                                    ▼
                       ┌─────────────────────────┐
                       │  pkg/bank/mapping.go    │
                       └────────────┬────────────┘
                                    │ (Simplified Clean Domain Models)
                                    ▼
┌───────────────────────────────────┴───────────────────────────────────┐
│                          pkg/bank/cache.go                            │
│                  - Persistent bbolt DB File (.bank.db)                │
│                  - 5-Minute Key-Value Expirations                     │
└───────────┬───────────────────────────────────────────┬───────────────┘
            │                                           │
            ▼                                           ▼
┌───────────────────────┐                   ┌───────────────────────┐
│    pkg/tui/app.go     │                   │   pkg/mcp/server.go   │
│ (Bubble Tea Dashboard)│                   │  (Remote/Local MCP)   │
└───────────────────────┘                   └───────────────────────┘
```

---

## 🧪 Running Tests

Verify the entire SDK suite (utilizing dynamic rsa key generation and mocked loopback API servers):
```bash
go test -v ./...
```
