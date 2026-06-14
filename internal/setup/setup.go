package setup

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/secret"
	"github.com/ngoldack/fin-mcp/pkg/enablebanking"
)

// GenerateRSAKeyAndCertificate writes a 4096-bit RSA private key and a matching
// self-signed certificate (to upload to the Enable Banking dashboard).
func GenerateRSAKeyAndCertificate(keyPath, certPath string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", keyPath, err)
	}
	defer func() { _ = keyOut.Close() }()
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}); err != nil {
		return fmt.Errorf("failed to write key block: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}
	template := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{Organization: []string{"fin-mcp"}, CommonName: "fin-mcp developer cert"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", certPath, err)
	}
	defer func() { _ = certOut.Close() }()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("failed to write cert block: %w", err)
	}
	return nil
}

const ebProviderName = "enable-banking"

// EnsureEBProvider returns the enable-banking provider's config, creating the
// provider entry if it does not yet exist.
func EnsureEBProvider(cfg *config.Config) *config.EnableBankingConfig {
	for i := range cfg.Providers {
		if cfg.Providers[i].Type == config.ProviderEnableBanking && cfg.Providers[i].EnableBanking != nil {
			return cfg.Providers[i].EnableBanking
		}
	}
	eb := &config.EnableBankingConfig{}
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{
		Name:          ebProviderName,
		Type:          config.ProviderEnableBanking,
		EnableBanking: eb,
	})
	return eb
}

// LoadOrNew loads the config file, or returns an empty (defaulted) config when
// the file does not yet exist.
func LoadOrNew(configPath string) (*config.Config, error) {
	if _, err := os.Stat(configPath); err != nil {
		return config.NewDefault(), nil
	}
	return config.LoadConfig(configPath)
}

func storeKeyInKeychain(eb *config.EnableBankingConfig, keyPath string) error {
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read private key for keychain: %w", err)
	}
	account := eb.AppID
	if account == "" {
		account = ebProviderName
	}
	if err := secret.Set(account, string(b)); err != nil {
		return fmt.Errorf("failed to store private key in OS keychain: %w", err)
	}
	eb.PrivateKeyKeyring = account
	eb.PrivateKeyPath = ""
	return nil
}

// resolveKeyContent returns the PEM content for the provider, from the keychain,
// inline content, or the key file.
func resolveKeyContent(eb *config.EnableBankingConfig) (string, error) {
	if eb.PrivateKeyKeyring != "" {
		return secret.Get(eb.PrivateKeyKeyring)
	}
	if eb.PrivateKeyContent != "" {
		return eb.PrivateKeyContent, nil
	}
	if eb.PrivateKeyPath != "" {
		b, err := os.ReadFile(eb.PrivateKeyPath)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", errors.New("no private key configured")
}

func newClient(eb *config.EnableBankingConfig) (enablebanking.APIClient, error) {
	content, err := resolveKeyContent(eb)
	if err != nil {
		return nil, err
	}
	return enablebanking.NewClient(eb.AppID, "", content, string(eb.Environment)), nil
}

// StartConnection begins bank authorization and returns the SCA redirect URL.
func StartConnection(ctx context.Context, eb *config.EnableBankingConfig, bank, country string, days int) (string, error) {
	client, err := newClient(eb)
	if err != nil {
		return "", err
	}
	state := fmt.Sprintf("state-%d", time.Now().UnixNano())
	resp, err := client.StartAuthorization(ctx, bank, country, state, eb.RedirectURL, days)
	if err != nil {
		return "", fmt.Errorf("failed to start bank authorization: %w", err)
	}
	return resp.URL, nil
}

// ExchangeConnection exchanges an authorization code for a session and returns
// the resulting connection.
func ExchangeConnection(ctx context.Context, eb *config.EnableBankingConfig, code, name, bank, country string) (config.Connection, error) {
	client, err := newClient(eb)
	if err != nil {
		return config.Connection{}, err
	}
	resp, err := client.AuthorizeSession(ctx, code)
	if err != nil {
		return config.Connection{}, fmt.Errorf("failed to exchange code: %w", err)
	}
	if name == "" {
		name = connectionSlug(bank)
	}
	return config.Connection{
		Name:              name,
		Bank:              bank,
		Country:           config.CountryCode(country),
		SessionID:         resp.SessionID,
		ConsentValidUntil: resp.Access.ValidUntil,
	}, nil
}

// UpsertConnection replaces a connection with the same name, or appends it.
func UpsertConnection(eb *config.EnableBankingConfig, conn config.Connection) {
	for i := range eb.Connections {
		if eb.Connections[i].Name == conn.Name {
			eb.Connections[i] = conn
			return
		}
	}
	eb.Connections = append(eb.Connections, conn)
}

func connectionSlug(bank string) string {
	s := strings.ToLower(strings.TrimSpace(bank))
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		s = "connection"
	}
	return s
}

// RunFlagSetup bootstraps the enable-banking provider and adds a connection,
// driven entirely by flags (non-interactive).
func RunFlagSetup(configPath, appID, keyPath, environment, redirectURL, country, bank, code string, days int, keychain bool) error {
	ctx := context.Background()

	cfg, err := LoadOrNew(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	eb := EnsureEBProvider(cfg)

	// Exchange branch: turn an auth code into a connection.
	if code != "" {
		if bank == "" || country == "" {
			return errors.New("--bank and --country are required together with --code")
		}
		fmt.Println("Exchanging authorization code for an active session...")
		conn, err := ExchangeConnection(ctx, eb, code, "", bank, country)
		if err != nil {
			return err
		}
		UpsertConnection(eb, conn)
		if err := config.SaveConfig(configPath, cfg); err != nil {
			return fmt.Errorf("failed to save configuration: %w", err)
		}
		fmt.Printf("Connection %q added and authorized.\n", conn.Name)
		return nil
	}

	// Provider credential bootstrap.
	if appID != "" {
		eb.AppID = appID
	}
	if eb.AppID == "" {
		return errors.New("--app-id is required")
	}
	if environment != "" {
		eb.Environment = config.Environment(environment)
	}
	if redirectURL != "" {
		eb.RedirectURL = redirectURL
	}

	if eb.PrivateKeyKeyring == "" && eb.PrivateKeyContent == "" {
		if keyPath == "" {
			keyPath = "private.key"
		}
		if _, err := os.Stat(keyPath); err != nil {
			fmt.Printf("Private key %q not found. Generating a 4096-bit RSA key pair...\n", keyPath)
			if err := GenerateRSAKeyAndCertificate(keyPath, "public.crt"); err != nil {
				return fmt.Errorf("failed to generate RSA key pair: %w", err)
			}
			fmt.Println("Generated key pair. Upload 'public.crt' to the Enable Banking dashboard.")
		}
		abs, _ := filepath.Abs(keyPath)
		eb.PrivateKeyPath = abs
	}

	if keychain && eb.PrivateKeyPath != "" {
		if err := storeKeyInKeychain(eb, eb.PrivateKeyPath); err != nil {
			return err
		}
		fmt.Printf("Stored private key in the OS keychain (account %q); config references the keychain.\n", eb.PrivateKeyKeyring)
	}

	if bank == "" || country == "" {
		if err := config.SaveConfig(configPath, cfg); err != nil {
			return fmt.Errorf("failed to save configuration: %w", err)
		}
		fmt.Printf("Provider credentials saved to %s. To add a connection, rerun with --bank and --country.\n", configPath)
		return nil
	}

	fmt.Println("Initiating Account Information Service (AIS) consent...")
	url, err := StartConnection(ctx, eb, bank, country, days)
	if err != nil {
		return err
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Println()
	fmt.Println("ACTION REQUIRED — authorize at your bank:")
	fmt.Println()
	fmt.Println(url)
	fmt.Println()
	fmt.Printf("Then complete it with:\n  fin-mcp setup --code <CODE> --bank %q --country %s\n", bank, country)
	return nil
}
