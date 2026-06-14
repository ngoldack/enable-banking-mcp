package setup

import (
	"bufio"
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
	"strconv"
	"strings"
	"time"

	"github.com/ngoldack/enable-banking-go/pkg/config"
	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"
	"github.com/mattn/go-isatty"
)

func GenerateRSAKeyAndCertificate(keyPath, certPath string) error {
	// Generate 4096-bit RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Save private key in PEM format
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", keyPath, err)
	}
	defer func() { _ = keyOut.Close() }()

	privBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	if err := pem.Encode(keyOut, privBlock); err != nil {
		return fmt.Errorf("failed to write key block: %w", err)
	}

	// Create self-signed X.509 certificate template
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour * 5) // 5 years validity

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Enable Banking Go Integration"},
			CommonName:   "Enable Banking Developer Cert",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Save certificate in PEM format
	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", certPath, err)
	}
	defer func() { _ = certOut.Close() }()

	certBlock := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	}
	if err := pem.Encode(certOut, certBlock); err != nil {
		return fmt.Errorf("failed to write cert block: %w", err)
	}

	return nil
}

func RunInteractiveSetup(configPath string) error {
	ctx := context.Background()

	// Enforce TTY verification using go-isatty
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return errors.New("interactive setup requires an active TTY/terminal")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("=================================================================")
	fmt.Println("             ENABLE BANKING SDK - INTERACTIVE SETUP             ")
	fmt.Println("=================================================================")
	fmt.Println()
	fmt.Println("This wizard will help you register, configure, and link your bank account.")
	fmt.Println()

	privateKeyPath := "private.key"
	if _, err := os.Stat(privateKeyPath); err != nil {
		fmt.Printf("Default private key '%s' not found.\n", privateKeyPath)
		fmt.Printf("Would you like us to generate a secure 4096-bit RSA key pair automatically? (y/n) [default: y]: ")
		genInput, _ := reader.ReadString('\n')
		genInput = strings.TrimSpace(strings.ToLower(genInput))

		if genInput == "" || genInput == "y" || genInput == "yes" {
			err := GenerateRSAKeyAndCertificate("private.key", "public.crt")
			if err != nil {
				return fmt.Errorf("failed to generate RSA key pair: %w", err)
			}
			fmt.Println()
			fmt.Println("-----------------------------------------------------------------")
			fmt.Println("🔑 SECURE KEYPAIR SUCCESSFULLY GENERATED!")
			fmt.Println("-----------------------------------------------------------------")
			fmt.Println("We saved your private key to 'private.key' and certificate to 'public.crt'.")
			fmt.Println()
			fmt.Println("Next step:")
			fmt.Println("1. Log in to the Enable Banking Dashboard (https://enablebanking.com)")
			fmt.Println("2. Create a new Application (or select an existing one).")
			fmt.Println("3. Copy the contents of the generated 'public.crt' file below into the certificate field:")
			fmt.Println()

			certBytes, err := os.ReadFile("public.crt")
			if err == nil {
				fmt.Println(strings.TrimSpace(string(certBytes)))
			}
			fmt.Println()
			fmt.Println("-----------------------------------------------------------------")
			fmt.Println()
			fmt.Printf("Press Enter once you have registered your app and are ready to continue...")
			_, _ = reader.ReadString('\n')
		}
	} else {
		fmt.Printf("Found existing private key at '%s'. Reusing it.\n", privateKeyPath)
		fmt.Printf("Press Enter once you have registered your app and are ready to continue...")
		_, _ = reader.ReadString('\n')
	}

	fmt.Printf("Enter your Enable Banking Application ID (UUID): ")
	appID, _ := reader.ReadString('\n')
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return errors.New("application ID cannot be empty")
	}

	redirectURL := "http://localhost:8080/callback"
	fmt.Printf("Enter your application redirect URL [default: %s]: ", redirectURL)
	redirInput, _ := reader.ReadString('\n')
	redirInput = strings.TrimSpace(redirInput)
	if redirInput != "" {
		redirectURL = redirInput
	}

	environment := "SANDBOX"
	fmt.Printf("Enter the Enable Banking API environment (SANDBOX / PRODUCTION) [default: SANDBOX]: ")
	envInput, _ := reader.ReadString('\n')
	envInput = strings.TrimSpace(strings.ToUpper(envInput))
	if envInput == "PRODUCTION" {
		environment = "PRODUCTION"
	}

	cfg := &config.Config{
		EnableBanking: config.EnableBankingConfig{
			AppID:          appID,
			PrivateKeyPath: privateKeyPath,
			Environment:    environment,
			RedirectURL:    redirectURL,
		},
		MCP: config.MCPConfig{
			AccessMode: config.ReadOnly, // default to ReadOnly for safety
		},
	}

	apiClient := enablebanking.NewClient(cfg.EnableBanking.AppID, cfg.EnableBanking.PrivateKeyPath, cfg.EnableBanking.PrivateKeyContent, cfg.EnableBanking.Environment)

	country := "DE"
	fmt.Printf("Enter the ISO 2-letter country code of your bank (e.g. DE, FI, FR) [default: DE]: ")
	countryInput, _ := reader.ReadString('\n')
	countryInput = strings.TrimSpace(strings.ToUpper(countryInput))
	if countryInput != "" {
		country = countryInput
	}

	fmt.Println("Fetching available banks (ASPSPs) for country", country, "...")
	allASPSPs, err := apiClient.GetASPSPs(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch available banks: %w\n(Note: double check your Application ID and Private Key setting)", err)
	}

	var filtered []enablebanking.ASPSP
	for _, aspsp := range allASPSPs {
		if strings.ToUpper(aspsp.Country) == country {
			filtered = append(filtered, aspsp)
		}
	}

	if len(filtered) == 0 {
		fmt.Println("No registered banks found for country", country, "in your Enable Banking environment.")
		fmt.Println("Here are some other banks retrieved from the API:")
		limit := 10
		if len(allASPSPs) < limit {
			limit = len(allASPSPs)
		}
		for i := 0; i < limit; i++ {
			fmt.Printf("- %s (%s)\n", allASPSPs[i].Name, allASPSPs[i].Country)
		}
		fmt.Println()
		return fmt.Errorf("no banks available for country: %s", country)
	}

	fmt.Println("\nAvailable banks (ASPSPs) for", country, ":")
	for i, aspsp := range filtered {
		fmt.Printf("[%d] %s (BIC: %s)\n", i+1, aspsp.Name, aspsp.Bic)
	}
	fmt.Println()

	var selectedASPSP enablebanking.ASPSP
	for {
		fmt.Printf("Select your bank [1-%d]: ", len(filtered))
		selStr, _ := reader.ReadString('\n')
		selStr = strings.TrimSpace(selStr)
		selIdx, err := strconv.Atoi(selStr)
		if err == nil && selIdx >= 1 && selIdx <= len(filtered) {
			selectedASPSP = filtered[selIdx-1]
			break
		}
		fmt.Println("Invalid selection. Please try again.")
	}

	cfg.EnableBanking.BankName = selectedASPSP.Name
	cfg.EnableBanking.BankCountry = selectedASPSP.Country

	state := fmt.Sprintf("state-%d", time.Now().UnixNano())
	fmt.Println()
	fmt.Println("Initiating Account Information Service (AIS) consent...")
	authResp, err := apiClient.StartAuthorization(ctx, cfg.EnableBanking.BankName, cfg.EnableBanking.BankCountry, state, cfg.EnableBanking.RedirectURL, 90)
	if err != nil {
		return fmt.Errorf("failed to start bank authorization: %w", err)
	}

	fmt.Println()
	fmt.Println("=================================================================")
	fmt.Println("             ACTION REQUIRED: AUTHORIZE AT YOUR BANK             ")
	fmt.Println("=================================================================")
	fmt.Println()
	fmt.Println("Please open the following URL in your web browser:")
	fmt.Println()
	fmt.Println(authResp.URL)
	fmt.Println()
	fmt.Println("Instructions:")
	fmt.Println("1. Navigate to the URL, select your bank credentials, and authenticate.")
	fmt.Println("2. After giving consent, you will be redirected to your Redirect URL.")
	fmt.Println("3. Copy the 'code' query parameter from the browser's address bar of the redirected page.")
	fmt.Println("   (Example: http://localhost:8080/callback?code=CODE_HERE&state=...)")
	fmt.Println()

	fmt.Printf("Paste the authorization code ('code') here: ")
	code, _ := reader.ReadString('\n')
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("authorization code cannot be empty")
	}

	fmt.Println("Exchanging authorization code for an active session...")
	sessionResp, err := apiClient.AuthorizeSession(ctx, code)
	if err != nil {
		return fmt.Errorf("failed to authorize session: %w", err)
	}

	cfg.EnableBanking.SessionID = sessionResp.SessionID
	cfg.EnableBanking.ConsentValidUntil = sessionResp.Access.ValidUntil

	fmt.Println()
	fmt.Println("Session successfully authorized!")
	fmt.Printf("- Session ID: %s\n", cfg.EnableBanking.SessionID)
	fmt.Printf("- Consent Valid Until: %s\n", cfg.EnableBanking.ConsentValidUntil.Format(time.RFC1123))
	fmt.Println("- Accounts discovered:")
	for _, acc := range sessionResp.Accounts {
		fmt.Printf("  * %s (%s, %s) - ID: %s\n", acc.Name, acc.AccountID.Iban, acc.Currency, acc.Uid)
	}

	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Println()
	fmt.Println("=================================================================")
	fmt.Println("                       SETUP COMPLETED!                          ")
	fmt.Println("=================================================================")
	fmt.Println()
	fmt.Printf("Configuration saved to: %s\n", configPath)
	fmt.Println("You can now start your MCP Server with:")
	fmt.Printf("  go run ./cmd/enable-banking-go server --config %s\n", configPath)
	fmt.Println("Or run the TUI Dashboard with:")
	fmt.Printf("  go run ./cmd/enable-banking-go tui --config %s\n", configPath)
	fmt.Println()

	return nil
}

func RunFlagSetup(configPath, appID, keyPath, environment, redirectURL, country, bank, code string, days int) error {
	ctx := context.Background()

	if code != "" {
		// Just exchange code in an existing config
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("failed to load config at %s (needed to exchange code): %w", configPath, err)
		}

		fmt.Println("Exchanging authorization code for active session...")
		client := enablebanking.NewClient(cfg.EnableBanking.AppID, cfg.EnableBanking.PrivateKeyPath, cfg.EnableBanking.PrivateKeyContent, cfg.EnableBanking.Environment)
		sessionResp, err := client.AuthorizeSession(ctx, code)
		if err != nil {
			return fmt.Errorf("failed to exchange code: %w", err)
		}

		cfg.EnableBanking.SessionID = sessionResp.SessionID
		cfg.EnableBanking.ConsentValidUntil = sessionResp.Access.ValidUntil

		err = config.SaveConfig(configPath, cfg)
		if err != nil {
			return fmt.Errorf("failed to save configuration: %w", err)
		}

		fmt.Println("Session successfully authorized and written to config!")
		return nil
	}

	if appID == "" {
		return errors.New("application ID (--app-id) is required for flag setup")
	}

	if keyPath == "" {
		keyPath = "private.key"
	}

	// Generate key/cert if not exists
	if _, err := os.Stat(keyPath); err != nil {
		fmt.Printf("Private key '%s' not found. Generating secure 4096-bit RSA key pair...\n", keyPath)
		err := GenerateRSAKeyAndCertificate(keyPath, "public.crt")
		if err != nil {
			return fmt.Errorf("failed to generate RSA key pair: %w", err)
		}
		fmt.Println("Generated key pair successfully! Upload 'public.crt' to Enable Banking Dashboard.")
	}

	absKeyPath, _ := filepath.Abs(keyPath)

	cfg := &config.Config{
		EnableBanking: config.EnableBankingConfig{
			AppID:          appID,
			PrivateKeyPath: absKeyPath,
			Environment:    environment,
			RedirectURL:    redirectURL,
			BankCountry:    country,
			BankName:       bank,
		},
		MCP: config.MCPConfig{
			AccessMode: config.ReadOnly, // default to ReadOnly for safety
		},
	}

	if country == "" || bank == "" {
		// Save initial config (before authorization)
		_ = config.SaveConfig(configPath, cfg)
		fmt.Printf("Initial configuration saved to %s. To authorize, rerun with --country and --bank.\n", configPath)
		return nil
	}

	client := enablebanking.NewClient(cfg.EnableBanking.AppID, cfg.EnableBanking.PrivateKeyPath, cfg.EnableBanking.PrivateKeyContent, cfg.EnableBanking.Environment)
	state := fmt.Sprintf("state-%d", time.Now().UnixNano())
	fmt.Println("Initiating Account Information Service (AIS) consent...")
	authResp, err := client.StartAuthorization(ctx, bank, country, state, redirectURL, days)
	if err != nil {
		return fmt.Errorf("failed to start bank authorization: %w", err)
	}

	// Save configuration
	err = config.SaveConfig(configPath, cfg)
	if err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	fmt.Println()
	fmt.Println("=================================================================")
	fmt.Println("             ACTION REQUIRED: AUTHORIZE AT YOUR BANK             ")
	fmt.Println("=================================================================")
	fmt.Println()
	fmt.Println("Please open the following URL in your web browser:")
	fmt.Println()
	fmt.Println(authResp.URL)
	fmt.Println()
	fmt.Println("After authorizing, run setup again with the code:")
	fmt.Printf("  go run ./cmd/enable-banking-go setup --code <CODE>\n")
	fmt.Println()

	return nil
}
