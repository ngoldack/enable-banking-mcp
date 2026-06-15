package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/provider"
	"github.com/ngoldack/fin-mcp/internal/setup"
	"github.com/ngoldack/fin-mcp/internal/setupflow"
)

// ConfigCmd groups configuration-file lifecycle commands.
type ConfigCmd struct {
	Init       ConfigInitCmd  `cmd:"" help:"Create a new configuration file with sane defaults."`
	Validate   ConfigValidate `cmd:"" help:"Validate the configuration file."`
	Show       ConfigShow     `cmd:"" help:"Print the configuration with secrets redacted."`
	Provider   ProviderCmd    `cmd:"" help:"Manage providers."`
	Connection ConnectionCmd  `cmd:"" help:"Manage a provider's bank connections."`
}

func loadConfigFile(path string) (*config.Config, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("config file %q not found (run 'config init')", path)
	}
	return config.LoadConfig(path)
}

func providerByName(cfg *config.Config, name string) (*config.ProviderConfig, error) {
	if name == "" {
		name = "enable-banking"
	}
	p := cfg.Provider(name)
	if p == nil {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	return p, nil
}

// ---- config init ----

type ConfigInitCmd struct {
	Config string `help:"Path to write the configuration file." default:"config.json" type:"path" short:"c"`
	Force  bool   `help:"Overwrite an existing file."`
}

func (c *ConfigInitCmd) Run() error {
	if _, err := os.Stat(c.Config); err == nil && !c.Force {
		return fmt.Errorf("%q already exists (use --force to overwrite)", c.Config)
	}
	cfg := config.NewDefault()
	cfg.MCP.AccessMode = config.ReadOnly
	if err := config.SaveConfig(c.Config, cfg); err != nil {
		return err
	}
	fmt.Printf("Wrote %s. Add a provider with 'config provider add', then a connection with 'config connection add'.\n", c.Config)
	return nil
}

// ---- config validate ----

type ConfigValidate struct {
	Config string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
}

func (c *ConfigValidate) Run() error {
	if _, err := loadConfigFile(c.Config); err != nil {
		return err
	}
	fmt.Printf("%s is valid.\n", c.Config)
	return nil
}

// ---- config show (redacted) ----

type ConfigShow struct {
	Config string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
}

func (c *ConfigShow) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	for i := range cfg.Providers {
		if eb := cfg.Providers[i].EnableBanking; eb != nil {
			if eb.PrivateKeyContent != "" {
				eb.PrivateKeyContent = "***redacted***"
			}
			if eb.PrivateKeyKeyring != "" {
				eb.PrivateKeyKeyring = "***keychain***"
			}
		}
	}
	if cfg.MCP.BearerToken != "" {
		cfg.MCP.BearerToken = "***redacted***"
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(b))
	return nil
}

// ---- provider ----

type ProviderCmd struct {
	List   ProviderListCmd   `cmd:"" help:"List configured providers."`
	Add    ProviderAddCmd    `cmd:"" help:"Add a provider."`
	Remove ProviderRemoveCmd `cmd:"" help:"Remove a provider by name."`
}

type ProviderListCmd struct {
	Config string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
}

func (c *ProviderListCmd) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	if len(cfg.Providers) == 0 {
		fmt.Println("No providers configured.")
		return nil
	}
	for _, p := range cfg.Providers {
		fmt.Printf("- %s (type=%s, connections=%d)\n", p.Name, p.Type, len(p.Connections))
	}
	return nil
}

type ProviderAddCmd struct {
	Config      string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
	Name        string `help:"Unique provider instance name." required:""`
	Type        string `help:"Provider type." enum:"enable-banking,mock" default:"enable-banking"`
	AppID       string `help:"Enable Banking Application ID (UUID)."`
	Environment string `help:"API environment." enum:"SANDBOX,PRODUCTION" default:"SANDBOX"`
	RedirectURL string `help:"Application redirect URL." default:"http://localhost:8080/callback"`
	PrivateKey  string `help:"Path to the RSA private key PEM (generated if missing)." type:"path"`
}

func (c *ProviderAddCmd) Run() error {
	cfg, err := setup.LoadOrNew(c.Config)
	if err != nil {
		return err
	}
	if cfg.Provider(c.Name) != nil {
		return fmt.Errorf("provider %q already exists", c.Name)
	}

	t := config.ProviderType(c.Type)
	flow, err := setupflow.MustFor(t)
	if err != nil {
		return err
	}
	pc := setup.EnsureProvider(cfg, c.Name, t)
	instructions, err := flow.ApplyCredentials(pc, map[string]string{
		"app_id":       c.AppID,
		"private_key":  c.PrivateKey,
		"environment":  c.Environment,
		"redirect_url": c.RedirectURL,
	})
	if err != nil {
		return err
	}
	if instructions != "" {
		fmt.Print(instructions)
	}

	if err := config.SaveConfig(c.Config, cfg); err != nil {
		return err
	}
	fmt.Printf("Added provider %q.\n", c.Name)
	return nil
}

type ProviderRemoveCmd struct {
	Config string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
	Name   string `arg:"" help:"Provider name to remove."`
}

func (c *ProviderRemoveCmd) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	out := cfg.Providers[:0]
	removed := false
	for _, p := range cfg.Providers {
		if p.Name == c.Name {
			removed = true
			continue
		}
		out = append(out, p)
	}
	if !removed {
		return fmt.Errorf("provider %q not found", c.Name)
	}
	cfg.Providers = out
	if err := config.SaveConfig(c.Config, cfg); err != nil {
		return err
	}
	fmt.Printf("Removed provider %q.\n", c.Name)
	return nil
}

// ---- connection ----

type ConnectionCmd struct {
	List    ConnectionListCmd    `cmd:"" help:"List a provider's connections."`
	Add     ConnectionAddCmd     `cmd:"" help:"Authorize and add a bank connection."`
	Remove  ConnectionRemoveCmd  `cmd:"" help:"Remove a connection by name."`
	Refresh ConnectionRefreshCmd `cmd:"" help:"Re-verify connections and refresh consent expiry."`
}

type ConnectionListCmd struct {
	Config   string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
	Provider string `help:"Provider name." default:"enable-banking"`
}

func (c *ConnectionListCmd) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	p, err := providerByName(cfg, c.Provider)
	if err != nil {
		return err
	}
	if len(p.Connections) == 0 {
		fmt.Println("No connections.")
		return nil
	}
	for _, conn := range p.Connections {
		fmt.Printf("- %s · %s (%s) · consent %s\n", conn.Name, conn.Bank, conn.Country, conn.ConsentValidUntil.Format(time.RFC3339))
	}
	return nil
}

type ConnectionAddCmd struct {
	Config   string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
	Provider string `help:"Provider name." default:"enable-banking"`
	Bank     string `help:"Bank (ASPSP) name." required:""`
	Country  string `help:"ISO 3166-1 alpha-2 country code." required:""`
	Name     string `help:"Connection name (defaults to a slug of the bank)."`
	Code     string `help:"Authorization code from the bank redirect (second step)."`
	Days     int    `help:"Consent validity in days." default:"90"`
}

func (c *ConnectionAddCmd) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	p, err := providerByName(cfg, c.Provider)
	if err != nil {
		return err
	}
	flow, err := setupflow.MustFor(p.Type)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := setupflow.ConnectionRequest{
		Bank: setupflow.Bank{Name: c.Bank, Country: c.Country},
		Name: c.Name,
		Code: c.Code,
		Days: c.Days,
	}

	// Providers that require SCA: first call returns a redirect URL.
	if flow.NeedsAuthorization() && c.Code == "" {
		url, err := flow.StartConnection(ctx, p, req)
		if err != nil {
			return err
		}
		fmt.Printf("Authorize at your bank:\n\n%s\n\nThen run the same command with --code <CODE>.\n", url)
		return nil
	}

	conn, err := flow.CompleteConnection(ctx, p, req)
	if err != nil {
		return err
	}
	setupflow.Upsert(p, conn)
	if err := config.SaveConfig(c.Config, cfg); err != nil {
		return err
	}
	fmt.Printf("Connection %q added.\n", conn.Name)
	return nil
}

type ConnectionRemoveCmd struct {
	Config   string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
	Provider string `help:"Provider name." default:"enable-banking"`
	Name     string `arg:"" help:"Connection name to remove."`
}

func (c *ConnectionRemoveCmd) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	p, err := providerByName(cfg, c.Provider)
	if err != nil {
		return err
	}
	out := p.Connections[:0]
	removed := false
	for _, conn := range p.Connections {
		if conn.Name == c.Name {
			removed = true
			continue
		}
		out = append(out, conn)
	}
	if !removed {
		return fmt.Errorf("connection %q not found", c.Name)
	}
	p.Connections = out
	if err := config.SaveConfig(c.Config, cfg); err != nil {
		return err
	}
	fmt.Printf("Removed connection %q.\n", c.Name)
	return nil
}

type ConnectionRefreshCmd struct {
	Config string `help:"Path to the configuration file." default:"config.json" type:"path" short:"c"`
}

func (c *ConnectionRefreshCmd) Run() error {
	cfg, err := loadConfigFile(c.Config)
	if err != nil {
		return err
	}
	reg, err := provider.FromConfig(cfg, c.Config)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, p := range reg.All() {
		status, err := p.VerifyConnection(ctx)
		if err != nil {
			fmt.Printf("- %s: %v\n", p.Name(), err)
			continue
		}
		fmt.Printf("- %s: %s\n", p.Name(), status.Status)
	}
	return nil
}
