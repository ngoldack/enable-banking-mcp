package enablebanking

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
	"github.com/ngoldack/fin-mcp/internal/setupflow"
	eb "github.com/ngoldack/fin-mcp/pkg/enablebanking"
)

func init() { setupflow.Register(&Flow{}) }

// Flow implements setupflow.Flow for the Enable Banking provider: it bootstraps
// application credentials (RSA key pair, App ID, environment) and runs the
// bank-redirect (SCA) authorization to mint a connection.
type Flow struct{}

func (*Flow) Type() config.ProviderType { return config.ProviderEnableBanking }

func (*Flow) NeedsAuthorization() bool { return true }

func (*Flow) CredentialFields() []setupflow.Field {
	return []setupflow.Field{
		{Key: "app_id", Label: "Enable Banking App ID (UUID)", Kind: setupflow.FieldText},
		{Key: "private_key", Label: "Private key path (generated if missing)", Kind: setupflow.FieldText, Default: "private.key", Optional: true},
		{Key: "redirect_url", Label: "Redirect URL", Kind: setupflow.FieldText, Default: "http://localhost:8080/callback"},
		{Key: "environment", Label: "Environment", Kind: setupflow.FieldChoice, Choices: []string{"SANDBOX", "PRODUCTION"}, Default: "SANDBOX"},
		{Key: "keychain", Label: "Store key in OS keychain (local only)", Kind: setupflow.FieldChoice, Choices: []string{"no", "yes"}, Default: "no", Optional: true},
	}
}

func (*Flow) ApplyCredentials(pc *config.ProviderConfig, values map[string]string) (string, error) {
	if pc.EnableBanking == nil {
		pc.EnableBanking = &config.EnableBankingConfig{}
	}
	cfg := pc.EnableBanking
	if v := strings.TrimSpace(values["app_id"]); v != "" {
		cfg.AppID = v
	}
	if v := strings.TrimSpace(values["redirect_url"]); v != "" {
		cfg.RedirectURL = v
	}
	if v := strings.TrimSpace(values["environment"]); v != "" {
		cfg.Environment = config.Environment(v)
	}

	var instr strings.Builder
	if cfg.PrivateKeyKeyring == "" && cfg.PrivateKeyContent == "" {
		keyPath := strings.TrimSpace(values["private_key"])
		if keyPath == "" {
			keyPath = "private.key"
		}
		if _, err := os.Stat(keyPath); err != nil {
			if err := generateRSAKeyAndCertificate(keyPath, "public.crt"); err != nil {
				return "", fmt.Errorf("generate RSA key pair: %w", err)
			}
			fmt.Fprintf(&instr, "Generated %s and public.crt — upload public.crt to the Enable Banking dashboard.\n", keyPath)
		}
		abs, _ := filepath.Abs(keyPath)
		cfg.PrivateKeyPath = abs
	}

	if strings.EqualFold(values["keychain"], "yes") && cfg.PrivateKeyPath != "" {
		if err := storeKeyInKeychain(cfg); err != nil {
			return "", err
		}
		fmt.Fprintf(&instr, "Stored private key in the OS keychain (account %q); config references the keychain.\n", cfg.PrivateKeyKeyring)
	}
	return instr.String(), nil
}

func (*Flow) Banks(ctx context.Context, pc *config.ProviderConfig, country string) ([]setupflow.Bank, error) {
	client, err := newSetupClient(pc.EnableBanking)
	if err != nil {
		return nil, err
	}
	all, err := client.GetASPSPs(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch banks: %w", err)
	}
	var out []setupflow.Bank
	for _, a := range all {
		if country == "" || strings.EqualFold(a.Country, country) {
			out = append(out, setupflow.Bank{Name: a.Name, Country: a.Country, BIC: a.Bic})
		}
	}
	return out, nil
}

func (*Flow) StartConnection(ctx context.Context, pc *config.ProviderConfig, req setupflow.ConnectionRequest) (string, error) {
	client, err := newSetupClient(pc.EnableBanking)
	if err != nil {
		return "", err
	}
	days := req.Days
	if days <= 0 {
		days = 90
	}
	state := fmt.Sprintf("setup-%d", time.Now().UnixNano())
	resp, err := client.StartAuthorization(ctx, req.Bank.Name, req.Bank.Country, state, pc.EnableBanking.RedirectURL, days)
	if err != nil {
		return "", fmt.Errorf("start bank authorization: %w", err)
	}
	return resp.URL, nil
}

func (*Flow) CompleteConnection(ctx context.Context, pc *config.ProviderConfig, req setupflow.ConnectionRequest) (config.Connection, error) {
	if req.Code == "" {
		return config.Connection{}, errors.New("authorization code is required")
	}
	client, err := newSetupClient(pc.EnableBanking)
	if err != nil {
		return config.Connection{}, err
	}
	resp, err := client.AuthorizeSession(ctx, req.Code)
	if err != nil {
		return config.Connection{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	name := req.Name
	if name == "" {
		name = setupflow.Slug(req.Bank.Name)
	}
	return config.Connection{
		Name:              name,
		Bank:              req.Bank.Name,
		Country:           config.CountryCode(req.Bank.Country),
		SessionID:         resp.SessionID,
		ConsentValidUntil: resp.Access.ValidUntil,
	}, nil
}

// newSetupClient builds an SDK client from the provider credentials, resolving
// the private key from the keychain, inline content, or a file.
func newSetupClient(cfg *config.EnableBankingConfig) (eb.APIClient, error) {
	if cfg == nil {
		return nil, errors.New("missing enable_banking config")
	}
	content, err := resolveKeyContent(cfg)
	if err != nil {
		return nil, err
	}
	return eb.NewClient(cfg.AppID, "", content, string(cfg.Environment)), nil
}

func resolveKeyContent(cfg *config.EnableBankingConfig) (string, error) {
	if cfg.PrivateKeyKeyring != "" {
		return secret.Get(cfg.PrivateKeyKeyring)
	}
	if cfg.PrivateKeyContent != "" {
		return cfg.PrivateKeyContent, nil
	}
	if cfg.PrivateKeyPath != "" {
		b, err := os.ReadFile(cfg.PrivateKeyPath)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", errors.New("no private key configured")
}

func storeKeyInKeychain(cfg *config.EnableBankingConfig) error {
	b, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("read private key for keychain: %w", err)
	}
	account := cfg.AppID
	if account == "" {
		account = string(config.ProviderEnableBanking)
	}
	if err := secret.Set(account, string(b)); err != nil {
		return fmt.Errorf("store private key in OS keychain: %w", err)
	}
	cfg.PrivateKeyKeyring = account
	cfg.PrivateKeyPath = ""
	return nil
}

// generateRSAKeyAndCertificate writes a 4096-bit RSA private key and a matching
// self-signed certificate (to upload to the Enable Banking dashboard).
func generateRSAKeyAndCertificate(keyPath, certPath string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("generate private key: %w", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open %s for writing: %w", keyPath, err)
	}
	defer func() { _ = keyOut.Close() }()
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}); err != nil {
		return fmt.Errorf("write key block: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("generate serial number: %w", err)
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
		return fmt.Errorf("create certificate: %w", err)
	}

	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open %s for writing: %w", certPath, err)
	}
	defer func() { _ = certOut.Close() }()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("write cert block: %w", err)
	}
	return nil
}
