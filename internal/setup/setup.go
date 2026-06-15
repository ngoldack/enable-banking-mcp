// Package setup provides provider-agnostic setup orchestration used by the CLI
// and the TUI wizard. Provider-specific logic (credential bootstrap, bank
// authorization) lives behind the setupflow.Flow port, implemented by each
// provider adapter. This package blank-imports the adapters so their flows are
// registered with the setupflow registry.
package setup

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/setupflow"

	// Register the provider setup flows.
	_ "github.com/ngoldack/fin-mcp/internal/provider/enablebanking"
	_ "github.com/ngoldack/fin-mcp/internal/provider/mock"
)

// LoadOrNew loads the config file, or returns an empty (defaulted) config when
// the file does not yet exist.
func LoadOrNew(configPath string) (*config.Config, error) {
	if _, err := os.Stat(configPath); err != nil {
		return config.NewDefault(), nil
	}
	return config.LoadConfig(configPath)
}

// EnsureProvider returns the named provider (creating it with the given type if
// absent). An empty name defaults to the provider type.
func EnsureProvider(cfg *config.Config, name string, t config.ProviderType) *config.ProviderConfig {
	if name == "" {
		name = string(t)
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			return &cfg.Providers[i]
		}
	}
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: name, Type: t})
	return &cfg.Providers[len(cfg.Providers)-1]
}

// RunFlagSetup bootstraps a provider and adds a connection, driven entirely by
// flags (non-interactive). It dispatches to the provider's setupflow.Flow, so
// it works for any registered provider type.
func RunFlagSetup(configPath string, t config.ProviderType, name string, creds map[string]string, req setupflow.ConnectionRequest) error {
	flow, err := setupflow.MustFor(t)
	if err != nil {
		return err
	}
	ctx := context.Background()

	cfg, err := LoadOrNew(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	pc := EnsureProvider(cfg, name, t)

	// Exchange branch: turn an auth code into a connection.
	if req.Code != "" {
		if req.Bank.Name == "" || req.Bank.Country == "" {
			return errors.New("--bank and --country are required together with --code")
		}
		conn, err := flow.CompleteConnection(ctx, pc, req)
		if err != nil {
			return err
		}
		setupflow.Upsert(pc, conn)
		if err := config.SaveConfig(configPath, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Connection %q added and authorized.\n", conn.Name)
		return nil
	}

	// Credential bootstrap (key generation, environment, etc.).
	instructions, err := flow.ApplyCredentials(pc, creds)
	if err != nil {
		return err
	}
	if instructions != "" {
		fmt.Print(instructions)
	}

	// No bank requested yet — just persist provider credentials.
	if req.Bank.Name == "" || req.Bank.Country == "" {
		if err := config.SaveConfig(configPath, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Provider %q saved to %s. To add a connection, rerun with --bank and --country.\n", pc.Name, configPath)
		return nil
	}

	// Providers that require SCA: emit the redirect URL and stop.
	if flow.NeedsAuthorization() {
		url, err := flow.StartConnection(ctx, pc, req)
		if err != nil {
			return err
		}
		if err := config.SaveConfig(configPath, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Println()
		fmt.Println("ACTION REQUIRED — authorize at your bank:")
		fmt.Println()
		fmt.Println(url)
		fmt.Println()
		fmt.Printf("Then complete it with:\n  fin-mcp setup --code <CODE> --bank %q --country %s\n", req.Bank.Name, req.Bank.Country)
		return nil
	}

	// Providers without authorization: complete the connection directly.
	conn, err := flow.CompleteConnection(ctx, pc, req)
	if err != nil {
		return err
	}
	setupflow.Upsert(pc, conn)
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Connection %q added.\n", conn.Name)
	return nil
}
