package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validAppID = "ad3c5dd5-1711-417e-a94f-82da6e897bc2" // 36 chars

func TestLoad_FileAndDefaults(t *testing.T) {
	p := writeFile(t, `{
      "providers": [{
        "name": "eb", "type": "enable-banking",
        "enable_banking": {
          "app_id": "`+validAppID+`",
          "redirect_url": "http://localhost:8080/callback"
        },
        "connections": [
          {"name":"c24","bank":"C24 Bank","country":"DE","session_id":"s1","consent_valid_until":"2026-09-12T13:36:15Z"}
        ]
      }],
      "mcp": { "access_mode": "ReadOnly" }
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Type != ProviderEnableBanking {
		t.Fatalf("providers = %+v", cfg.Providers)
	}
	eb := cfg.Providers[0].EnableBanking
	if eb == nil || eb.AppID != validAppID {
		t.Fatalf("enable_banking not parsed: %+v", eb)
	}
	if eb.Environment != EnvSandbox { // default applied
		t.Errorf("environment default = %q", eb.Environment)
	}
	// Connections are a provider-agnostic, first-class field on ProviderConfig.
	conns := cfg.Providers[0].Connections
	if len(conns) != 1 || conns[0].Country != "DE" {
		t.Fatalf("connections = %+v", conns)
	}
	if conns[0].ConsentValidUntil.Year() != 2026 {
		t.Errorf("consent time not parsed: %v", conns[0].ConsentValidUntil)
	}
	if cfg.MCP.Transport != TransportStdio || cfg.MCP.CacheTTLMinutes != 5 || cfg.MCP.LogFormat != LogFormatText || cfg.MCP.LogLevel != LogInfo {
		t.Errorf("mcp defaults = %+v", cfg.MCP)
	}
}

func TestLoad_EnvOverridesMCP(t *testing.T) {
	p := writeFile(t, `{
      "providers": [{"name":"m","type":"mock"}],
      "mcp": { "access_mode": "ReadOnly", "transport": "stdio" }
    }`)

	t.Setenv("MCP_ACCESS_MODE", "Unrestricted")
	t.Setenv("MCP_TRANSPORT", "sse")
	t.Setenv("MCP_PORT", "9000")
	t.Setenv("MCP_CACHE_TTL_MINUTES", "15")
	// Secret fields are injected via env (omitted from the config file / ConfigMap).
	t.Setenv("MCP_BEARER_TOKEN", "tok-from-env")
	t.Setenv("MCP_CACHE_VALKEY_PASSWORD", "pw-from-env")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.MCP.AccessMode != Unrestricted || cfg.MCP.Transport != TransportSSE || cfg.MCP.Port != 9000 || cfg.MCP.CacheTTLMinutes != 15 {
		t.Errorf("env overrides failed: %+v", cfg.MCP)
	}
	if cfg.MCP.BearerToken != "tok-from-env" || cfg.MCP.CacheValkeyPassword != "pw-from-env" {
		t.Errorf("secret env injection failed: token=%q valkeyPw=%q", cfg.MCP.BearerToken, cfg.MCP.CacheValkeyPassword)
	}
}

func baseConfig() *Config {
	c := &Config{Providers: []ProviderConfig{{
		Name: "eb", Type: ProviderEnableBanking,
		EnableBanking: &EnableBankingConfig{AppID: validAppID},
	}}}
	c.applyDefaults()
	return c
}

func TestValidate_Errors(t *testing.T) {
	cases := map[string]func(*Config){
		"dup provider":      func(c *Config) { c.Providers = append(c.Providers, c.Providers[0]) },
		"unknown type":      func(c *Config) { c.Providers[0].Type = "wells-fargo" },
		"missing eb block":  func(c *Config) { c.Providers[0].EnableBanking = nil },
		"bad app id":        func(c *Config) { c.Providers[0].EnableBanking.AppID = "short" },
		"bad redirect":      func(c *Config) { c.Providers[0].EnableBanking.RedirectURL = "ftp://x" },
		"bad environment":   func(c *Config) { c.Providers[0].EnableBanking.Environment = "STAGING" },
		"dup connection":    func(c *Config) { c.Providers[0].Connections = []Connection{{Name: "a"}, {Name: "a"}} },
		"empty provider id": func(c *Config) { c.Providers[0].Name = "" },
		"bad access mode":   func(c *Config) { c.MCP.AccessMode = "God" },
		"bad transport":     func(c *Config) { c.MCP.Transport = "pigeon" },
		"bad port":          func(c *Config) { c.MCP.Transport = TransportSSE; c.MCP.Port = 70000 },
		"zero cache ttl":    func(c *Config) { c.MCP.CacheTTLMinutes = 0 },
		"bad log level":     func(c *Config) { c.MCP.LogLevel = "loud" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := baseConfig()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected validation error for %s", name)
			}
		})
	}
}

func TestValidate_OK(t *testing.T) {
	if err := baseConfig().Validate(); err != nil {
		t.Errorf("baseConfig should validate: %v", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.json")
	cfg := baseConfig()
	cfg.Providers[0].EnableBanking.RedirectURL = "http://localhost:8080/callback"
	cfg.Providers[0].Connections = []Connection{{Name: "c24", Bank: "C24 Bank", Country: "DE", SessionID: "s1"}}

	if err := SaveConfig(p, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(got.Providers) != 1 || got.Providers[0].Connections[0].Name != "c24" {
		t.Errorf("round trip mismatch: %+v", got.Providers)
	}
}

func TestLoad_MockProvider(t *testing.T) {
	p := writeFile(t, `{
      "providers": [
        {"name":"m1","type":"mock","mock":{"accounts":2}}
      ],
      "mcp": {}
    }`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Providers[0].Mock == nil || cfg.Providers[0].Mock.Accounts != 2 {
		t.Errorf("mock sub-config not parsed: %+v", cfg.Providers[0].Mock)
	}
}
