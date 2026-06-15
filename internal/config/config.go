package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	kjson "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Strongly-typed enumerations.

type AccessMode string

const (
	ReadOnly     AccessMode = "ReadOnly"
	InternalOnly AccessMode = "InternalOnly"
	Unrestricted AccessMode = "Unrestricted"
)

type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportSSE   TransportType = "sse"
)

type LogFormat string

const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

type Environment string

const (
	EnvSandbox    Environment = "SANDBOX"
	EnvProduction Environment = "PRODUCTION"
)

// CountryCode is an ISO 3166-1 alpha-2 country code (e.g. "DE", "LT").
type CountryCode string

type ProviderType string

const (
	ProviderEnableBanking ProviderType = "enable-banking"
	ProviderMock          ProviderType = "mock"
)

// Connection is one authorized bank link that exposes one or more accounts. It
// is provider-agnostic and lives on ProviderConfig: a provider may hold several
// connections (e.g. one for C24 and one for Revolut). SessionID is an opaque
// session/consent handle owned by the provider; Metadata carries any
// provider-specific extras the setup flow needs to persist.
type Connection struct {
	Name              string            `json:"name"`
	Bank              string            `json:"bank"`
	Country           CountryCode       `json:"country"`
	SessionID         string            `json:"session_id,omitempty"`
	ConsentValidUntil time.Time         `json:"consent_valid_until,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

type EnableBankingConfig struct {
	AppID             string      `json:"app_id"`
	PrivateKeyPath    string      `json:"private_key_path,omitempty"`
	PrivateKeyContent string      `json:"private_key_content,omitempty"`
	PrivateKeyKeyring string      `json:"private_key_keyring,omitempty"` // OS keychain account (local only)
	Environment       Environment `json:"environment"`
	RedirectURL       string      `json:"redirect_url"`
}

// MockProviderConfig configures the in-memory mock provider (testing/demo).
type MockProviderConfig struct {
	Accounts int `json:"accounts,omitempty"`
}

// ProviderConfig is one typed, named provider instance. Exactly one typed
// sub-config must be set, matching Type. Connections are provider-agnostic and
// held here (not in the typed sub-config).
type ProviderConfig struct {
	Name          string               `json:"name"`
	Type          ProviderType         `json:"type"`
	EnableBanking *EnableBankingConfig `json:"enable_banking,omitempty"`
	Mock          *MockProviderConfig  `json:"mock,omitempty"`
	Connections   []Connection         `json:"connections,omitempty"`
}

// Connection returns a pointer to the named connection, or nil.
func (p *ProviderConfig) Connection(name string) *Connection {
	for i := range p.Connections {
		if p.Connections[i].Name == name {
			return &p.Connections[i]
		}
	}
	return nil
}

type MCPConfig struct {
	AccessMode      AccessMode    `json:"access_mode"`
	Transport       TransportType `json:"transport"`
	Port            int           `json:"port,omitempty"`
	BearerToken     string        `json:"bearer_token,omitempty"`
	CacheTTLMinutes int           `json:"cache_ttl_minutes,omitempty"`
	CachePath       string        `json:"cache_path,omitempty"` // BadgerDB dir (default ".bank.db"); set writable for read-only rootfs
	LogFormat       LogFormat     `json:"log_format,omitempty"`
	LogLevel        LogLevel      `json:"log_level,omitempty"`
}

type Config struct {
	Providers []ProviderConfig `json:"providers"`
	MCP       MCPConfig        `json:"mcp"`
}

// Provider returns a pointer to the named provider, or nil.
func (c *Config) Provider(name string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// LoadConfig assembles configuration with koanf: an optional JSON file (the
// canonical source, typically a Kubernetes ConfigMap), then MCP_* environment
// overrides for server settings.
func LoadConfig(path string) (*Config, error) {
	k := koanf.New(".")

	if _, err := os.Stat(path); err == nil {
		if err := k.Load(file.Provider(path), kjson.Parser()); err != nil {
			return nil, fmt.Errorf("failed to load config file %q: %w", path, err)
		}
	}

	if err := k.Load(env.Provider("MCP_", ".", envKey("MCP_", "mcp")), nil); err != nil {
		return nil, fmt.Errorf("failed to load MCP_* env: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		Tag: "json",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result:           &cfg,
			WeaklyTypedInput: true,
			DecodeHook:       mapstructure.StringToTimeHookFunc(time.RFC3339),
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return &cfg, nil
}

func envKey(prefix, group string) func(string) string {
	return func(s string) string {
		return group + "." + strings.ToLower(strings.TrimPrefix(s, prefix))
	}
}

// NewDefault returns an empty configuration with defaults applied.
func NewDefault() *Config {
	c := &Config{}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	for i := range c.Providers {
		if eb := c.Providers[i].EnableBanking; eb != nil && eb.Environment == "" {
			eb.Environment = EnvSandbox
		}
	}
	if c.MCP.AccessMode == "" {
		c.MCP.AccessMode = ReadOnly
	}
	if c.MCP.Transport == "" {
		c.MCP.Transport = TransportStdio
	}
	if c.MCP.CacheTTLMinutes == 0 {
		c.MCP.CacheTTLMinutes = 5
	}
	if c.MCP.CachePath == "" {
		c.MCP.CachePath = ".bank.db"
	}
	if c.MCP.LogFormat == "" {
		c.MCP.LogFormat = LogFormatText
	}
	if c.MCP.LogLevel == "" {
		c.MCP.LogLevel = LogInfo
	}
}

func (c *Config) Validate() error {
	names := make(map[string]bool, len(c.Providers))
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("providers[%d]: name is required", i)
		}
		if names[p.Name] {
			return fmt.Errorf("duplicate provider name %q", p.Name)
		}
		names[p.Name] = true

		switch p.Type {
		case ProviderEnableBanking:
			if p.EnableBanking == nil {
				return fmt.Errorf("provider %q: type 'enable-banking' requires an enable_banking block", p.Name)
			}
			if err := p.EnableBanking.validate(p.Name); err != nil {
				return err
			}
		case ProviderMock:
		default:
			return fmt.Errorf("provider %q: unknown type %q (valid: enable-banking, mock)", p.Name, p.Type)
		}

		connNames := make(map[string]bool, len(p.Connections))
		for j, conn := range p.Connections {
			if conn.Name == "" {
				return fmt.Errorf("provider %q: connections[%d] name is required", p.Name, j)
			}
			if connNames[conn.Name] {
				return fmt.Errorf("provider %q: duplicate connection name %q", p.Name, conn.Name)
			}
			connNames[conn.Name] = true
		}
	}

	switch c.MCP.AccessMode {
	case ReadOnly, InternalOnly, Unrestricted:
	default:
		return fmt.Errorf("invalid mcp.access_mode: %q (valid: ReadOnly, InternalOnly, Unrestricted)", c.MCP.AccessMode)
	}
	switch c.MCP.Transport {
	case TransportStdio, TransportSSE:
	default:
		return fmt.Errorf("invalid mcp.transport: %q (valid: stdio, sse)", c.MCP.Transport)
	}
	if c.MCP.Transport == TransportSSE && (c.MCP.Port < 0 || c.MCP.Port > 65535) {
		return fmt.Errorf("invalid mcp.port: %d (1-65535)", c.MCP.Port)
	}
	if c.MCP.CacheTTLMinutes <= 0 {
		return fmt.Errorf("mcp.cache_ttl_minutes must be > 0")
	}
	switch c.MCP.LogFormat {
	case LogFormatText, LogFormatJSON:
	default:
		return fmt.Errorf("invalid mcp.log_format: %q (valid: text, json)", c.MCP.LogFormat)
	}
	switch LogLevel(strings.ToLower(string(c.MCP.LogLevel))) {
	case LogDebug, LogInfo, LogWarn, LogError:
	default:
		return fmt.Errorf("invalid mcp.log_level: %q (valid: debug, info, warn, error)", c.MCP.LogLevel)
	}
	return nil
}

func (e *EnableBankingConfig) validate(provider string) error {
	if e.AppID != "" && len(e.AppID) != 36 {
		return fmt.Errorf("provider %q: app_id must be a valid 36-character UUID", provider)
	}
	if e.RedirectURL != "" && !strings.HasPrefix(e.RedirectURL, "http://") && !strings.HasPrefix(e.RedirectURL, "https://") {
		return fmt.Errorf("provider %q: redirect_url must be an HTTP or HTTPS URL", provider)
	}
	switch e.Environment {
	case "", EnvSandbox, EnvProduction:
	default:
		return fmt.Errorf("provider %q: invalid environment %q (valid: SANDBOX, PRODUCTION)", provider, e.Environment)
	}
	return nil
}

func SaveConfig(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("cannot save invalid config: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
