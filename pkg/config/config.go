package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
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

type EnableBankingConfig struct {
	AppID             string    `json:"app_id" env:"ENABLE_BANKING_APP_ID"`
	PrivateKeyPath    string    `json:"private_key_path,omitempty" env:"ENABLE_BANKING_PRIVATE_KEY_PATH"`
	PrivateKeyContent string    `json:"private_key_content,omitempty" env:"ENABLE_BANKING_PRIVATE_KEY_CONTENT"`
	Environment       string    `json:"environment" env:"ENABLE_BANKING_ENVIRONMENT"`
	RedirectURL       string    `json:"redirect_url" env:"ENABLE_BANKING_REDIRECT_URL"`
	BankName          string    `json:"bank_name" env:"ENABLE_BANKING_BANK_NAME"`
	BankCountry       string    `json:"bank_country" env:"ENABLE_BANKING_BANK_COUNTRY"`
	SessionID         string    `json:"session_id,omitempty" env:"ENABLE_BANKING_SESSION_ID"`
	ConsentValidUntil time.Time `json:"consent_valid_until,omitempty" env:"ENABLE_BANKING_CONSENT_VALID_UNTIL"`
}

type LogFormat string

const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

type MCPConfig struct {
	AccessMode      AccessMode    `json:"access_mode" env:"MCP_ACCESS_MODE"`
	Transport       TransportType `json:"transport" env:"MCP_TRANSPORT"`
	Port            int           `json:"port,omitempty" env:"MCP_PORT"`
	BearerToken     string        `json:"bearer_token,omitempty" env:"MCP_BEARER_TOKEN"`
	CacheTTLMinutes int           `json:"cache_ttl_minutes,omitempty" env:"MCP_CACHE_TTL_MINUTES"`
	LogFormat       LogFormat     `json:"log_format,omitempty" env:"MCP_LOG_FORMAT"` // "text" or "json"
	LogLevel        string        `json:"log_level,omitempty" env:"MCP_LOG_LEVEL"`   // "debug", "info", "warn", "error"
}

type Config struct {
	EnableBanking EnableBankingConfig `json:"enable_banking"`
	MCP           MCPConfig           `json:"mcp"`
}

func LoadConfig(path string) (*Config, error) {
	var cfg Config
	
	// 1. Try loading from file if it exists
	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// 2. Override / Load from Environment Variables (Kubernetes-friendly using caarlos0/env)
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse environment variables: %w", err)
	}

	// 3. Apply defaults
	if cfg.EnableBanking.Environment == "" {
		cfg.EnableBanking.Environment = "SANDBOX"
	}
	if cfg.MCP.AccessMode == "" {
		cfg.MCP.AccessMode = ReadOnly
	}
	if cfg.MCP.Transport == "" {
		cfg.MCP.Transport = TransportStdio
	}
	if cfg.MCP.CacheTTLMinutes == 0 {
		cfg.MCP.CacheTTLMinutes = 5
	}
	if cfg.MCP.LogFormat == "" {
		cfg.MCP.LogFormat = LogFormatText
	}
	if cfg.MCP.LogLevel == "" {
		cfg.MCP.LogLevel = "info"
	}

	// 4. Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	// Only validate AppID if present (allows initial blank setup)
	if c.EnableBanking.AppID != "" {
		if len(c.EnableBanking.AppID) != 36 {
			return fmt.Errorf("enable_banking.app_id must be a valid 36-character UUID")
		}
	}

	if c.EnableBanking.RedirectURL != "" {
		if !strings.HasPrefix(c.EnableBanking.RedirectURL, "http://") && !strings.HasPrefix(c.EnableBanking.RedirectURL, "https://") {
			return fmt.Errorf("enable_banking.redirect_url must be a valid HTTP or HTTPS URL")
		}
	}

	switch c.MCP.AccessMode {
	case ReadOnly, InternalOnly, Unrestricted:
	default:
		return fmt.Errorf("invalid mcp.access_mode: '%s'. Valid modes are ReadOnly, InternalOnly, Unrestricted", c.MCP.AccessMode)
	}

	switch c.MCP.Transport {
	case TransportStdio, TransportSSE:
	default:
		return fmt.Errorf("invalid mcp.transport: '%s'. Valid transports are stdio, sse", c.MCP.Transport)
	}

	if c.MCP.Transport == TransportSSE {
		if c.MCP.Port < 0 || c.MCP.Port > 65535 {
			return fmt.Errorf("invalid mcp.port: %d. Port must be between 1 and 65535", c.MCP.Port)
		}
	}

	if c.MCP.CacheTTLMinutes <= 0 {
		return fmt.Errorf("mcp.cache_ttl_minutes must be a positive integer greater than 0")
	}

	switch c.MCP.LogFormat {
	case LogFormatText, LogFormatJSON:
	default:
		return fmt.Errorf("invalid mcp.log_format: '%s'. Valid formats are text, json", c.MCP.LogFormat)
	}

	switch strings.ToLower(c.MCP.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid mcp.log_level: '%s'. Valid levels are debug, info, warn, error", c.MCP.LogLevel)
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
