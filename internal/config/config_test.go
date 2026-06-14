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
      "enable_banking": {
        "app_id": "`+validAppID+`",
        "redirect_url": "http://localhost:8080/callback",
        "session_id": "sess-1",
        "consent_valid_until": "2026-09-12T13:36:15Z"
      },
      "mcp": { "access_mode": "ReadOnly" }
    }`)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.EnableBanking.AppID != validAppID {
		t.Errorf("app_id = %q", cfg.EnableBanking.AppID)
	}
	if cfg.EnableBanking.ConsentValidUntil.Year() != 2026 {
		t.Errorf("consent time not parsed: %v", cfg.EnableBanking.ConsentValidUntil)
	}
	// Defaults applied.
	if cfg.EnableBanking.Environment != "SANDBOX" {
		t.Errorf("env default = %q", cfg.EnableBanking.Environment)
	}
	if cfg.MCP.Transport != TransportStdio {
		t.Errorf("transport default = %q", cfg.MCP.Transport)
	}
	if cfg.MCP.CacheTTLMinutes != 5 {
		t.Errorf("cache default = %d", cfg.MCP.CacheTTLMinutes)
	}
	if cfg.MCP.LogFormat != LogFormatText || cfg.MCP.LogLevel != "info" {
		t.Errorf("log defaults = %q/%q", cfg.MCP.LogFormat, cfg.MCP.LogLevel)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	p := writeFile(t, `{
      "enable_banking": { "app_id": "`+validAppID+`", "environment": "SANDBOX" },
      "mcp": { "access_mode": "ReadOnly", "transport": "stdio" }
    }`)

	t.Setenv("ENABLE_BANKING_ENVIRONMENT", "PRODUCTION")
	t.Setenv("MCP_ACCESS_MODE", "Unrestricted")
	t.Setenv("MCP_TRANSPORT", "sse")
	t.Setenv("MCP_PORT", "9000")
	t.Setenv("MCP_CACHE_TTL_MINUTES", "15")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EnableBanking.Environment != "PRODUCTION" {
		t.Errorf("env override failed: %q", cfg.EnableBanking.Environment)
	}
	if cfg.MCP.AccessMode != Unrestricted {
		t.Errorf("access_mode override failed: %q", cfg.MCP.AccessMode)
	}
	if cfg.MCP.Transport != TransportSSE {
		t.Errorf("transport override failed: %q", cfg.MCP.Transport)
	}
	if cfg.MCP.Port != 9000 { // string env coerced to int
		t.Errorf("port override failed: %d", cfg.MCP.Port)
	}
	if cfg.MCP.CacheTTLMinutes != 15 {
		t.Errorf("cache override failed: %d", cfg.MCP.CacheTTLMinutes)
	}
}

func TestLoad_EnvOnlyNoFile(t *testing.T) {
	t.Setenv("ENABLE_BANKING_APP_ID", validAppID)
	t.Setenv("ENABLE_BANKING_REDIRECT_URL", "https://example.com/cb")
	t.Setenv("MCP_TRANSPORT", "sse")
	t.Setenv("MCP_PORT", "8090")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EnableBanking.AppID != validAppID {
		t.Errorf("env-only app_id = %q", cfg.EnableBanking.AppID)
	}
	if cfg.MCP.Transport != TransportSSE || cfg.MCP.Port != 8090 {
		t.Errorf("env-only mcp = %q/%d", cfg.MCP.Transport, cfg.MCP.Port)
	}
}

func TestValidate_Errors(t *testing.T) {
	base := func() *Config {
		c := &Config{}
		c.EnableBanking.AppID = validAppID
		c.applyDefaults()
		return c
	}

	cases := map[string]func(*Config){
		"bad app id":      func(c *Config) { c.EnableBanking.AppID = "short" },
		"bad redirect":    func(c *Config) { c.EnableBanking.RedirectURL = "ftp://x" },
		"bad access mode": func(c *Config) { c.MCP.AccessMode = "God" },
		"bad transport":   func(c *Config) { c.MCP.Transport = "carrier-pigeon" },
		"bad port":        func(c *Config) { c.MCP.Transport = TransportSSE; c.MCP.Port = 70000 },
		"zero cache ttl":  func(c *Config) { c.MCP.CacheTTLMinutes = 0 },
		"bad log level":   func(c *Config) { c.MCP.LogLevel = "loud" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(c)
			if err := c.Validate(); err == nil {
				t.Errorf("expected validation error for %s", name)
			}
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.json")
	cfg := &Config{}
	cfg.EnableBanking.AppID = validAppID
	cfg.EnableBanking.RedirectURL = "http://localhost:8080/callback"
	cfg.applyDefaults()

	if err := SaveConfig(p, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.EnableBanking.AppID != cfg.EnableBanking.AppID || got.MCP.AccessMode != cfg.MCP.AccessMode {
		t.Errorf("round trip mismatch: %+v", got)
	}
}
