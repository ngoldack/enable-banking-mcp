package setup

import (
	"path/filepath"
	"testing"

	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/setupflow"
)

func TestFlowsRegistered(t *testing.T) {
	for _, want := range []config.ProviderType{config.ProviderEnableBanking, config.ProviderMock} {
		if _, ok := setupflow.For(want); !ok {
			t.Errorf("setup flow not registered for %q", want)
		}
	}
}

func TestEnsureProvider(t *testing.T) {
	cfg := config.NewDefault()
	pc := EnsureProvider(cfg, "", config.ProviderMock)
	if pc.Name != string(config.ProviderMock) {
		t.Fatalf("default name = %q", pc.Name)
	}
	// Idempotent: same name returns the same element.
	if again := EnsureProvider(cfg, "mock", config.ProviderMock); again != pc {
		t.Errorf("EnsureProvider not idempotent")
	}
	if len(cfg.Providers) != 1 {
		t.Errorf("providers = %d, want 1", len(cfg.Providers))
	}
}

// TestRunFlagSetup_Mock drives the whole provider-agnostic orchestration through
// the mock flow (no credentials, no SCA): it should create the provider and
// persist a connection in one shot.
func TestRunFlagSetup_Mock(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	err := RunFlagSetup(cfgPath, config.ProviderMock, "m", map[string]string{"accounts": "3"},
		setupflow.ConnectionRequest{Bank: setupflow.Bank{Name: "Mock Bank", Country: "DE"}})
	if err != nil {
		t.Fatalf("RunFlagSetup: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	pc := cfg.Provider("m")
	if pc == nil || pc.Type != config.ProviderMock {
		t.Fatalf("provider not created: %+v", cfg.Providers)
	}
	if pc.Mock == nil || pc.Mock.Accounts != 3 {
		t.Errorf("mock accounts = %+v", pc.Mock)
	}
	if len(pc.Connections) != 1 || pc.Connections[0].Name != "mock-conn" {
		t.Errorf("connections = %+v", pc.Connections)
	}
}
