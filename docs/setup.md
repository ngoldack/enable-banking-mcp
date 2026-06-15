# Setup & Providers

Setup is **provider-agnostic**. The CLI and the TUI wizard both drive a
provider's own `SetupFlow` (credential bootstrap + connection authorization), so
every provider plugs in the same way. Two providers ship today:
`enable-banking` (real) and `mock` (testing/demo).

There are three equivalent ways to configure a provider and authorize a
connection: the **interactive TUI wizard**, **flag-driven `setup`**, and the
**`config` lifecycle commands**. All write the same `config.json`.

## 1. Interactive TUI wizard

```bash
fin-mcp setup            # no flags -> launches the wizard (requires a TTY)
```

The wizard is a generic shell:

1. **Select a provider type** (from the registered flows).
2. **Name** the provider instance.
3. **Enter credentials** — the wizard renders the fields the chosen provider
   declares (for Enable Banking: App ID, key path, redirect URL, environment,
   optional keychain). Missing keys are generated automatically.
4. **Pick a bank** — country, then search the institution directory.
5. **Authorize** — opens your bank's SCA page; a local callback server captures
   the returned code automatically.
6. The connection is **merged** into `config.json` (existing config is never
   overwritten).

## 2. Flag-driven setup (non-interactive / scripted)

```bash
# Enable Banking: bootstrap credentials + start authorization (prints SCA URL)
fin-mcp setup --type enable-banking --app-id <UUID> \
  --bank "C24 Bank" --country DE

# ...complete it with the code from the redirect
fin-mcp setup --type enable-banking --bank "C24 Bank" --country DE --code <CODE>

# Mock provider needs no credentials or authorization
fin-mcp setup --type mock --provider demo --bank "Mock Bank" --country DE
```

Useful flags: `--provider` (instance name), `--environment`, `--redirect-url`,
`--private-key`, `--keychain` (store the key in the OS keychain, local only),
`--days` (consent validity).

## 3. `config` lifecycle commands

```bash
fin-mcp config init                                   # bootstrap config.json
fin-mcp config provider add --name enable-banking --type enable-banking --app-id <UUID>
fin-mcp config connection add --bank "C24 Bank" --country DE          # prints SCA URL
fin-mcp config connection add --bank "C24 Bank" --country DE --code <CODE>
fin-mcp config connection list
fin-mcp config connection refresh                     # re-verify + refresh consent
fin-mcp config connection remove c24
fin-mcp config provider list
fin-mcp config provider remove enable-banking
fin-mcp config show                                   # secrets redacted
fin-mcp config validate
```

`provider add` delegates credential bootstrap (e.g. RSA key generation) to the
provider's flow; `connection add` dispatches the start/complete authorization to
the same flow. Both work for any registered provider type.

## Secrets

The Enable Banking private key has three sources:

- **`private_key_path`** — a file (mount a Kubernetes Secret here in production).
- **`private_key_content`** — inline PEM.
- **`private_key_keyring`** — the OS keychain, **local only** (`setup --keychain`,
  via `zalando/go-keyring`). Never used in Kubernetes.

`config show` redacts key material and the bearer token.

## Extending: add a new provider

A provider is two small pieces — a **runtime adapter** (`provider.Provider`) and
a **setup flow** (`setupflow.Flow`):

1. Implement `provider.Provider` (account/balance/transaction/payment methods) in
   `internal/provider/<name>`.
2. Implement `setupflow.Flow` in the same package and self-register it:

   ```go
   func init() { setupflow.Register(&Flow{}) }

   type Flow struct{}

   func (*Flow) Type() config.ProviderType { return config.ProviderType("<name>") }
   func (*Flow) NeedsAuthorization() bool  { return true /* or false */ }
   func (*Flow) CredentialFields() []setupflow.Field { /* declare inputs */ }
   func (*Flow) ApplyCredentials(pc *config.ProviderConfig, values map[string]string) (string, error) { /* persist creds, side effects */ }
   func (*Flow) Banks(ctx, pc, country) ([]setupflow.Bank, error) { /* directory or nil */ }
   func (*Flow) StartConnection(ctx, pc, req) (authURL string, err error) { /* begin SCA */ }
   func (*Flow) CompleteConnection(ctx, pc, req) (config.Connection, error) { /* finalize */ }
   ```

3. Wire the runtime adapter into `provider.FromConfig`, and blank-import the
   package from `internal/setup` so the flow registers.

The CLI and TUI then drive your provider with **no further changes** — they only
speak the `setupflow.Flow` interface.
