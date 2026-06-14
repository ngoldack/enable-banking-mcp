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

type EnableBankingConfig struct {
	AppID             string    `json:"app_id"`
	PrivateKeyPath    string    `json:"private_key_path,omitempty"`
	PrivateKeyContent string    `json:"private_key_content,omitempty"`
	Environment       string    `json:"environment"`
	RedirectURL       string    `json:"redirect_url"`
	BankName          string    `json:"bank_name"`
	BankCountry       string    `json:"bank_country"`
	SessionID         string    `json:"session_id,omitempty"`
	ConsentValidUntil time.Time `json:"consent_valid_until,omitempty"`
}

type MCPConfig struct {
	AccessMode      AccessMode    `json:"access_mode"`
	Transport       TransportType `json:"transport"`
	Port            int           `json:"port,omitempty"`
	BearerToken     string        `json:"bearer_token,omitempty"`
	CacheTTLMinutes int           `json:"cache_ttl_minutes,omitempty"`
	LogFormat       LogFormat     `json:"log_format,omitempty"`
	LogLevel        string        `json:"log_level,omitempty"`
}

type Config struct {
	EnableBanking EnableBankingConfig `json:"enable_banking"`
	MCP           MCPConfig           `json:"mcp"`
}

// LoadConfig assembles configuration in layers using koanf: an optional JSON
// file, then environment-variable overrides. Env names are unchanged and map to
// nested keys (e.g. ENABLE_BANKING_APP_ID -> enable_banking.app_id,
// MCP_ACCESS_MODE -> mcp.access_mode), which keeps Kubernetes ConfigMap/Secret
// wiring trivial.
func LoadConfig(path string) (*Config, error) {
	k := koanf.New(".")

	// 1. Optional config file.
	if _, err := os.Stat(path); err == nil {
		if err := k.Load(file.Provider(path), kjson.Parser()); err != nil {
			return nil, fmt.Errorf("failed to load config file %q: %w", path, err)
		}
	}

	// 2. Environment overrides (Kubernetes-friendly).
	if err := k.Load(env.Provider("ENABLE_BANKING_", ".", envKey("ENABLE_BANKING_", "enable_banking")), nil); err != nil {
		return nil, fmt.Errorf("failed to load ENABLE_BANKING_* env: %w", err)
	}
	if err := k.Load(env.Provider("MCP_", ".", envKey("MCP_", "mcp")), nil); err != nil {
		return nil, fmt.Errorf("failed to load MCP_* env: %w", err)
	}

	// 3. Unmarshal via json tags, converting RFC3339 strings to time.Time and
	//    coercing string env values to their target types.
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

// envKey maps an env var name to a nested koanf key, e.g.
// ("ENABLE_BANKING_", "enable_banking")("ENABLE_BANKING_APP_ID") -> "enable_banking.app_id".
func envKey(prefix, group string) func(string) string {
	return func(s string) string {
		return group + "." + strings.ToLower(strings.TrimPrefix(s, prefix))
	}
}

func (c *Config) applyDefaults() {
	if c.EnableBanking.Environment == "" {
		c.EnableBanking.Environment = "SANDBOX"
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
	if c.MCP.LogFormat == "" {
		c.MCP.LogFormat = LogFormatText
	}
	if c.MCP.LogLevel == "" {
		c.MCP.LogLevel = "info"
	}
}

func (c *Config) Validate() error {
	if c.EnableBanking.AppID != "" && len(c.EnableBanking.AppID) != 36 {
		return fmt.Errorf("enable_banking.app_id must be a valid 36-character UUID")
	}

	if c.EnableBanking.RedirectURL != "" {
		if !strings.HasPrefix(c.EnableBanking.RedirectURL, "http://") && !strings.HasPrefix(c.EnableBanking.RedirectURL, "https://") {
			return fmt.Errorf("enable_banking.redirect_url must be a valid HTTP or HTTPS URL")
		}
	}

	switch c.MCP.AccessMode {
	case ReadOnly, InternalOnly, Unrestricted:
	default:
		return fmt.Errorf("invalid mcp.access_mode: %q. Valid modes are ReadOnly, InternalOnly, Unrestricted", c.MCP.AccessMode)
	}

	switch c.MCP.Transport {
	case TransportStdio, TransportSSE:
	default:
		return fmt.Errorf("invalid mcp.transport: %q. Valid transports are stdio, sse", c.MCP.Transport)
	}

	if c.MCP.Transport == TransportSSE && (c.MCP.Port < 0 || c.MCP.Port > 65535) {
		return fmt.Errorf("invalid mcp.port: %d. Port must be between 1 and 65535", c.MCP.Port)
	}

	if c.MCP.CacheTTLMinutes <= 0 {
		return fmt.Errorf("mcp.cache_ttl_minutes must be a positive integer greater than 0")
	}

	switch c.MCP.LogFormat {
	case LogFormatText, LogFormatJSON:
	default:
		return fmt.Errorf("invalid mcp.log_format: %q. Valid formats are text, json", c.MCP.LogFormat)
	}

	switch strings.ToLower(c.MCP.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid mcp.log_level: %q. Valid levels are debug, info, warn, error", c.MCP.LogLevel)
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

func (c *Config) IsSessionValid() bool {
	if c.EnableBanking.SessionID == "" {
		return false
	}
	if c.EnableBanking.ConsentValidUntil.IsZero() {
		return true
	}
	return time.Now().Add(5 * time.Minute).Before(c.EnableBanking.ConsentValidUntil)
}
