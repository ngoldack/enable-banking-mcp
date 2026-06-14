package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngoldack/enable-banking-go/pkg/config"
	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"
	"github.com/ngoldack/enable-banking-go/pkg/setup"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type CountryOption struct {
	Code string
	Name string
}

var countryOptions = []CountryOption{
	{Code: "DE", Name: "Germany (Deutschland)"},
	{Code: "FI", Name: "Finland (Suomi)"},
	{Code: "FR", Name: "France"},
	{Code: "ES", Name: "Spain (España)"},
	{Code: "IT", Name: "Italy (Italia)"},
	{Code: "NL", Name: "Netherlands (Nederland)"},
	{Code: "AT", Name: "Austria (Österreich)"},
	{Code: "BE", Name: "Belgium (Belgique)"},
	{Code: "DK", Name: "Denmark (Danmark)"},
	{Code: "EE", Name: "Estonia (Eesti)"},
	{Code: "LV", Name: "Latvia (Latvija)"},
	{Code: "LT", Name: "Lithuania (Lietuva)"},
	{Code: "NO", Name: "Norway (Norge)"},
	{Code: "PL", Name: "Poland (Polska)"},
	{Code: "SE", Name: "Sweden (Sverige)"},
	{Code: "GB", Name: "United Kingdom"},
}

// Setup Steps
const (
	stepKeypairChoice = iota
	stepSummary
	stepCredentials
	stepBankFetch
	stepBankSelect
	stepAuthRedirect
	stepCodeExchange
	stepSuccess
)

type SetupModel struct {
	configPath         string
	step               int
	err                error
	loading            bool
	statusMsg          string
	cfg                *config.Config
	client             enablebanking.APIClient

	// Step 0: Choice
	keypairChoiceIdx   int // 0: Existing, 1: Generate

	// Step 1: Credentials
	appIDInput         textinput.Model
	keyPathInput       textinput.Model
	redirectURLInput   textinput.Model
	focusedInputIdx    int // 0: App ID, 1: Key Path, 2: Redirect URL, 3: Environment Toggle
	envSelectedIdx     int // 0: SANDBOX, 1: PRODUCTION
	keysGenerated      bool

	// Step 2 & 3: Country / Bank select
	selectedCountryIdx int
	banks              []enablebanking.ASPSP
	filteredBanks      []enablebanking.ASPSP
	bankSearchInput    textinput.Model
	selectedBankIdx    int

	// Step 4: Redirect
	authResp           *enablebanking.StartAuthorizationResponse
	serverChan         chan string
	localServer        *http.Server

	// Step 5: Code exchange
	codeInput          textinput.Model
}

func NewSetupModel(configPath string) *SetupModel {
	appID := textinput.New()
	appID.Placeholder = "Enable Banking App ID (UUID)"
	appID.Focus()
	appID.CharLimit = 36

	keyPath := textinput.New()
	keyPath.Placeholder = "Private Key Path (default: private.key)"
	keyPath.SetValue("private.key")
	keyPath.CharLimit = 100

	code := textinput.New()
	code.Placeholder = "Paste the 'code' parameter from redirection"
	code.CharLimit = 120

	bankSearch := textinput.New()
	bankSearch.Placeholder = "Type to search your bank (e.g. C24)..."
	bankSearch.CharLimit = 50

	redirectURL := textinput.New()
	redirectURL.Placeholder = "Redirect URL (default: http://localhost:8080/callback)"
	redirectURL.SetValue("http://localhost:8080/callback")
	redirectURL.CharLimit = 150

	return &SetupModel{
		configPath:       configPath,
		step:            stepKeypairChoice,
		keypairChoiceIdx: 0,
		appIDInput:      appID,
		keyPathInput:    keyPath,
		redirectURLInput: redirectURL,
		focusedInputIdx: 0,
		envSelectedIdx:  0, // SANDBOX
		selectedCountryIdx: 0,
		codeInput:       code,
		bankSearchInput: bankSearch,
	}
}

func (m *SetupModel) Init() tea.Cmd {
	return textinput.Blink
}

// Setup Commands

type callbackResultMsg string

func waitForCallbackCmd(ch chan string) tea.Cmd {
	return func() tea.Msg {
		res := <-ch
		return callbackResultMsg(res)
	}
}

type banksMsg []enablebanking.ASPSP
type authMsg *enablebanking.StartAuthorizationResponse
type exchangeMsg *enablebanking.AuthorizeSessionResponse

func fetchBanksForTUISetup(client enablebanking.APIClient, country string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		all, err := client.GetASPSPs(ctx)
		if err != nil {
			return errorMsg(err)
		}

		var filtered []enablebanking.ASPSP
		for _, aspsp := range all {
			if strings.EqualFold(aspsp.Country, country) {
				filtered = append(filtered, aspsp)
			}
		}
		return banksMsg(filtered)
	}
}

func startAuthorizationTUISetup(client enablebanking.APIClient, aspspName, aspspCountry, redirectURL string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		state := fmt.Sprintf("tuisetup-%d", time.Now().UnixNano())
		resp, err := client.StartAuthorization(ctx, aspspName, aspspCountry, state, redirectURL, 90)
		if err != nil {
			return errorMsg(err)
		}
		return authMsg(resp)
	}
}

func exchangeCodeTUISetup(client enablebanking.APIClient, code string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp, err := client.AuthorizeSession(ctx, code)
		if err != nil {
			return errorMsg(err)
		}
		return exchangeMsg(resp)
	}
}

func (m *SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.step > stepKeypairChoice {
				m.step--
				m.err = nil
				m.loading = false
				return m, nil
			}
		}

	case errorMsg:
		m.loading = false
		m.err = msg
		return m, nil

	case banksMsg:
		m.loading = false
		m.banks = msg
		m.filteredBanks = msg // Initially show all
		if len(m.banks) == 0 {
			m.err = fmt.Errorf("no banks found for country %s", countryOptions[m.selectedCountryIdx].Code)
			m.step = stepBankFetch
		} else {
			m.step = stepBankSelect
			m.selectedBankIdx = 0
			m.bankSearchInput.SetValue("")
			m.bankSearchInput.Focus()
		}
		return m, nil

	case authMsg:
		m.loading = false
		m.authResp = msg
		m.step = stepAuthRedirect
		m.startLocalCallbackServer()
		return m, waitForCallbackCmd(m.serverChan)

	case callbackResultMsg:
		result := string(msg)
		if strings.HasPrefix(result, "error:") {
			m.err = fmt.Errorf("bank authorization failed: %s", strings.TrimPrefix(result, "error:"))
			m.step = stepCodeExchange
			m.codeInput.Focus()
		} else {
			m.codeInput.SetValue(result)
			m.loading = true
			m.statusMsg = "Exchanging captured authorization code..."
			m.err = nil
			return m, exchangeCodeTUISetup(m.client, result)
		}
		return m, nil

	case exchangeMsg:
		m.loading = false
		m.cfg.EnableBanking.SessionID = msg.SessionID
		m.cfg.EnableBanking.ConsentValidUntil = msg.Access.ValidUntil

		// Save configuration
		err := config.SaveConfig(m.configPath, m.cfg)
		if err != nil {
			m.err = err
			m.step = stepCodeExchange
			return m, nil
		}

		m.step = stepSuccess
		return m, nil
	}

	// Step-specific Keyboard Inputs
	switch m.step {
	case stepKeypairChoice:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "up":
				if m.keypairChoiceIdx > 0 {
					m.keypairChoiceIdx--
				}
			case "down":
				if m.keypairChoiceIdx < 1 {
					m.keypairChoiceIdx++
				}
			case "enter":
				m.step = stepSummary
				return m, nil
			}
		}

	case stepSummary:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "enter":
				m.step = stepCredentials
				if m.keypairChoiceIdx == 1 {
					// Generate Keypair flow: generate keys immediately
					if !m.keysGenerated {
						m.loading = true
						m.statusMsg = "Generating secure 4096-bit RSA private key and self-signed certificate..."
						err := setup.GenerateRSAKeyAndCertificate("private.key", "public.crt")
						if err != nil {
							m.loading = false
							m.err = fmt.Errorf("failed to generate RSA keys: %w", err)
							return m, nil
						}
						m.keysGenerated = true
						m.loading = false
						m.keyPathInput.SetValue("private.key")
						// Pre-focus the App ID input since they have the keys now
						m.appIDInput.Focus()
						m.focusedInputIdx = 0
					}
				} else {
					// Existing Keypair flow: focus App ID
					m.appIDInput.Focus()
					m.focusedInputIdx = 0
				}
				return m, nil
			}
		}

	case stepCredentials:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "o":
				// Let them press [O] to open Browser for Control Panel with pre-filled parameters dynamically!
				redirectVal := strings.TrimSpace(m.redirectURLInput.Value())
				regURL := fmt.Sprintf("https://enablebanking.com/cp/applications?name=Enable+Banking+Go+MCP&redirect_urls=%s&environment=SANDBOX", strings.ReplaceAll(redirectVal, "/", "%2F"))
				_ = OpenBrowser(regURL)
				return m, nil

			case "tab", "down":
				m.appIDInput.Blur()
				m.keyPathInput.Blur()
				m.redirectURLInput.Blur()
				m.focusedInputIdx = (m.focusedInputIdx + 1) % 4
				if m.focusedInputIdx == 0 {
					m.appIDInput.Focus()
				} else if m.focusedInputIdx == 1 {
					m.keyPathInput.Focus()
				} else if m.focusedInputIdx == 2 {
					m.redirectURLInput.Focus()
				}
				return m, nil

			case "up":
				m.appIDInput.Blur()
				m.keyPathInput.Blur()
				m.redirectURLInput.Blur()
				m.focusedInputIdx = (m.focusedInputIdx - 1 + 4) % 4
				if m.focusedInputIdx == 0 {
					m.appIDInput.Focus()
				} else if m.focusedInputIdx == 1 {
					m.keyPathInput.Focus()
				} else if m.focusedInputIdx == 2 {
					m.redirectURLInput.Focus()
				}
				return m, nil

			case "left", "right", "space", "h", "l":
				if m.focusedInputIdx == 3 {
					m.envSelectedIdx = (m.envSelectedIdx + 1) % 2
					return m, nil
				}

			case "enter":
				if m.focusedInputIdx < 3 {
					m.appIDInput.Blur()
					m.keyPathInput.Blur()
					m.redirectURLInput.Blur()
					m.focusedInputIdx++
					if m.focusedInputIdx == 1 {
						m.keyPathInput.Focus()
					} else if m.focusedInputIdx == 2 {
						m.redirectURLInput.Focus()
					}
					return m, nil
				}

				// Complete Step 1!
				appID := strings.TrimSpace(m.appIDInput.Value())
				keyPath := strings.TrimSpace(m.keyPathInput.Value())
				redirectVal := strings.TrimSpace(m.redirectURLInput.Value())
				env := "SANDBOX"
				if m.envSelectedIdx == 1 {
					env = "PRODUCTION"
				}

				if appID == "" || keyPath == "" || redirectVal == "" {
					m.err = fmt.Errorf("all fields are required")
					return m, nil
				}

				// Verify Private Key exists (and generate if somehow missing in custom typing)
				if _, err := os.Stat(keyPath); err != nil {
					m.loading = true
					m.statusMsg = "Generating secure RSA key pair..."
					err := setup.GenerateRSAKeyAndCertificate(keyPath, "public.crt")
					if err != nil {
						m.loading = false
						m.err = fmt.Errorf("failed to generate RSA keys: %w", err)
						return m, nil
					}
					m.loading = false
				}

				absKeyPath, _ := filepath.Abs(keyPath)
				m.cfg = &config.Config{
					EnableBanking: config.EnableBankingConfig{
						AppID:          appID,
						PrivateKeyPath: absKeyPath,
						Environment:    env,
						RedirectURL:    redirectVal,
					},
					MCP: config.MCPConfig{
						AccessMode: config.ReadOnly,
					},
				}
				m.client = enablebanking.NewClient(m.cfg.EnableBanking.AppID, m.cfg.EnableBanking.PrivateKeyPath, m.cfg.EnableBanking.PrivateKeyContent, m.cfg.EnableBanking.Environment)

				m.step = stepBankFetch
				m.err = nil
				return m, nil
			}
		}

		if m.focusedInputIdx == 0 {
			m.appIDInput, cmd = m.appIDInput.Update(msg)
		} else if m.focusedInputIdx == 1 {
			m.keyPathInput, cmd = m.keyPathInput.Update(msg)
		} else if m.focusedInputIdx == 2 {
			m.redirectURLInput, cmd = m.redirectURLInput.Update(msg)
		}
		return m, cmd

	case stepBankFetch:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "up":
				if m.selectedCountryIdx > 0 {
					m.selectedCountryIdx--
				}
			case "down":
				if m.selectedCountryIdx < len(countryOptions)-1 {
					m.selectedCountryIdx++
				}
			case "enter":
				selected := countryOptions[m.selectedCountryIdx]
				m.loading = true
				m.statusMsg = "Fetching registered banks from API..."
				m.err = nil
				return m, fetchBanksForTUISetup(m.client, selected.Code)
			}
		}
		return m, nil

	case stepBankSelect:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "up":
				if m.selectedBankIdx > 0 {
					m.selectedBankIdx--
				}
				return m, nil
			case "down":
				if m.selectedBankIdx < len(m.filteredBanks)-1 {
					m.selectedBankIdx++
				}
				return m, nil
			case "enter":
				if len(m.filteredBanks) > 0 {
					selected := m.filteredBanks[m.selectedBankIdx]
					m.cfg.EnableBanking.BankName = selected.Name
					m.cfg.EnableBanking.BankCountry = selected.Country

					m.loading = true
					m.statusMsg = "Initiating bank authorization redirect link..."
					m.err = nil
					return m, startAuthorizationTUISetup(m.client, m.cfg.EnableBanking.BankName, m.cfg.EnableBanking.BankCountry, m.cfg.EnableBanking.RedirectURL)
				}
				return m, nil
			}
		}

		// Update search input
		m.bankSearchInput, cmd = m.bankSearchInput.Update(msg)
		
		// Update filtered list in real-time
		query := strings.ToLower(m.bankSearchInput.Value())
		m.filteredBanks = nil
		for _, bank := range m.banks {
			if strings.Contains(strings.ToLower(bank.Name), query) || strings.Contains(strings.ToLower(bank.Bic), query) {
				m.filteredBanks = append(m.filteredBanks, bank)
			}
		}

		// Recalculate index bounds
		if m.selectedBankIdx >= len(m.filteredBanks) {
			m.selectedBankIdx = len(m.filteredBanks) - 1
		}
		if m.selectedBankIdx < 0 {
			m.selectedBankIdx = 0
		}

		return m, cmd

	case stepAuthRedirect:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "o", "enter":
				if m.authResp != nil {
					_ = OpenBrowser(m.authResp.URL)
				}
				m.step = stepCodeExchange
				m.codeInput.Focus()
				return m, nil
			case "space":
				m.step = stepCodeExchange
				m.codeInput.Focus()
				return m, nil
			}
		}

	case stepCodeExchange:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "enter":
				code := strings.TrimSpace(m.codeInput.Value())
				if code == "" {
					m.err = fmt.Errorf("authorization code is required")
					return m, nil
				}
				m.loading = true
				m.statusMsg = "Exchanging authorization code for active session..."
				m.err = nil
				return m, exchangeCodeTUISetup(m.client, code)
			}
		}
		m.codeInput, cmd = m.codeInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *SetupModel) View() string {
	s := ""
	s += titleStyle.Render("🏦 ENABLE BANKING - INTERACTIVE SETUP WIZARD") + "\n\n"

	if m.err != nil {
		s += errorStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)) + "\n\n"
	}

	if m.loading {
		s += lipgloss.NewStyle().Foreground(accentColor).Render("⌛ "+m.statusMsg) + "\n"
		return s
	}

	switch m.step {
	case stepKeypairChoice:
		s += headerStyle.Render("Welcome! Choose your Application Setup:") + "\n\n"
		
		choices := []string{
			"Use an Existing Keypair & App ID\n   (Select this if you already have an application registered on enablebanking.com)",
			"Generate a New Keypair & Certificate\n   (Select this if you do not have an application yet. We will generate keys for you)",
		}

		for i, choice := range choices {
			cursor := "  "
			style := normalStyle
			if i == m.keypairChoiceIdx {
				cursor = "👉 "
				style = selectedStyle
			}
			s += style.Render(cursor+choice) + "\n\n"
		}
		s += helpStyle.Render("[Up/Down] Navigate  |  [Enter] Select Option  |  [Q] Quit")

	case stepSummary:
		s += headerStyle.Render("Setup Steps Summary") + "\n\n"
		
		if m.keypairChoiceIdx == 0 {
			s += boxStyle.Render(
				"We will go through the following steps to configure your bank:\n\n"+
				"1. Enter existing Application Credentials (App ID & Private Key Path).\n"+
				"2. Select Country & Choose your Bank (ASPSP).\n"+
				"3. Secure Redirection: Log in to your bank to authorize the session.\n"+
				"4. Code Exchange: Retrieve and save authorized bank details.\n\n"+
				"Ready? Press [Enter] to start!") + "\n\n"
		} else {
			s += boxStyle.Render(
				"We will go through the following steps to generate keys and link your bank:\n\n"+
				"1. Generate Keypair: We automatically write 'private.key' and 'public.crt'.\n"+
				"2. Register Application: Upload 'public.crt' to the Enable Banking dashboard.\n"+
				"3. Enter App ID: Paste the newly assigned App ID assigned by the control panel.\n"+
				"4. Select Country & Choose your Bank (ASPSP).\n"+
				"5. Secure Redirection: Log in to your bank to authorize the session.\n"+
				"6. Code Exchange: Retrieve and save authorized bank details.\n\n"+
				"Ready? Press [Enter] to generate keys and proceed!") + "\n\n"
		}
		s += helpStyle.Render("[Enter] Proceed  |  [Esc] Back  |  [Q] Quit")

	case stepCredentials:
		if m.keypairChoiceIdx == 1 {
			// Generate Keypair Flow
			s += headerStyle.Render("Step 1: Keys Generated & App Registration Required") + "\n\n"
			
			redirectVal := strings.TrimSpace(m.redirectURLInput.Value())
			regURL := fmt.Sprintf("https://enablebanking.com/cp/applications?name=Enable+Banking+Go+MCP&redirect_urls=%s&environment=SANDBOX", strings.ReplaceAll(redirectVal, "/", "%2F"))
			
			s += boxStyle.Render(
				"🔑 Secure Private Key written to: private.key\n"+
				"📜 Public Certificate written to: public.crt\n\n"+
				"ACTION REQUIRED:\n"+
				"1. Register a new application at:\n   "+regURL+"\n"+
				"2. Upload the contents of your generated 'public.crt' file (shown below).\n"+
				"3. Copy your newly assigned Application ID and paste it below.") + "\n\n"

			certContent := "[Could not read public.crt]"
			if certBytes, err := os.ReadFile("public.crt"); err == nil {
				certContent = strings.TrimSpace(string(certBytes))
			}
			s += lipgloss.NewStyle().Foreground(accentColor).Render("📜 public.crt (Copy the block below):") + "\n"
			s += lipgloss.NewStyle().Foreground(accentColor).Render("--------------------------------------------------------------------------------") + "\n"
			s += certContent + "\n"
			s += lipgloss.NewStyle().Foreground(accentColor).Render("--------------------------------------------------------------------------------") + "\n\n"
			
			s += helpStyle.Render("👉 Press [O] on App ID or Redirect URL field to open the Control Panel in your browser.") + "\n\n"

			// Render App ID Input
			appIDCursor := "  "
			if m.focusedInputIdx == 0 {
				appIDCursor = "👉 "
			}
			s += appIDCursor + m.appIDInput.View() + "\n\n"

			// Render Redirect URL Input
			redirCursor := "  "
			if m.focusedInputIdx == 2 {
				redirCursor = "👉 "
			}
			s += redirCursor + m.redirectURLInput.View() + "\n\n"

			// Render Environment toggle
			envCursor := "  "
			if m.focusedInputIdx == 3 {
				envCursor = "👉 "
			}
			s += envCursor + "Environment: "
			if m.envSelectedIdx == 0 {
				s += selectedStyle.Render("[X] SANDBOX") + "   [ ] PRODUCTION"
			} else {
				s += "[ ] SANDBOX   " + selectedStyle.Render("[X] PRODUCTION")
			}
			s += "\n\n"
			s += helpStyle.Render("[Tab/Arrows] Navigate  |  [Space/Left/Right] Toggle Env  |  [Enter] Next / Submit  |  [Esc] Back")
		} else {
			// Existing Keypair Flow
			s += headerStyle.Render("Step 1: Enter your credentials") + "\n"
			s += "Please enter your existing Application credentials registered on enablebanking.com.\n\n"

			// Render App ID Input
			appIDCursor := "  "
			if m.focusedInputIdx == 0 {
				appIDCursor = "👉 "
			}
			s += appIDCursor + m.appIDInput.View() + "\n\n"

			// Render Key Path Input
			keyPathCursor := "  "
			if m.focusedInputIdx == 1 {
				keyPathCursor = "👉 "
			}
			s += keyPathCursor + m.keyPathInput.View() + "\n\n"

			// Render Redirect URL Input
			redirCursor := "  "
			if m.focusedInputIdx == 2 {
				redirCursor = "👉 "
			}
			s += redirCursor + m.redirectURLInput.View() + "\n\n"

			// Render Environment multiple-choice
			envCursor := "  "
			if m.focusedInputIdx == 3 {
				envCursor = "👉 "
			}
			s += envCursor + "Environment: "
			if m.envSelectedIdx == 0 {
				s += selectedStyle.Render("[X] SANDBOX") + "   [ ] PRODUCTION"
			} else {
				s += "[ ] SANDBOX   " + selectedStyle.Render("[X] PRODUCTION")
			}
			s += "\n\n"
			s += helpStyle.Render("[Tab/Arrows] Navigate  |  [Space/Left/Right] Toggle Environment  |  [Enter] Next Step / Submit  |  [Q] Quit")
		}

	case stepBankFetch:
		s += "Step 2: Select the country of your bank\n\n"
		
		for i, country := range countryOptions {
			cursor := "  "
			style := normalStyle
			if i == m.selectedCountryIdx {
				cursor = "👉 "
				style = selectedStyle
			}
			s += style.Render(fmt.Sprintf("%s%s (%s)", cursor, country.Name, country.Code)) + "\n"
		}
		s += "\n"
		s += helpStyle.Render("[Up/Down] Navigate  |  [Enter] Fetch Banks  |  [Esc] Back  |  [Q] Quit")

	case stepBankSelect:
		selectedCountry := countryOptions[m.selectedCountryIdx]
		s += fmt.Sprintf("Step 3: Select your bank for %s (%s)\n\n", selectedCountry.Name, selectedCountry.Code)
		
		// Render search input
		s += "🔍 Search: " + m.bankSearchInput.View() + "\n"
		s += helpStyle.Render(fmt.Sprintf("  (matching %d of %d banks)\n", len(m.filteredBanks), len(m.banks))) + "\n"

		if len(m.filteredBanks) == 0 {
			s += errorStyle.Render("   No banks match your search query.") + "\n\n"
		} else {
			startIdx := 0
			endIdx := len(m.filteredBanks)
			maxVisible := 10
			if len(m.filteredBanks) > maxVisible {
				startIdx = m.selectedBankIdx - maxVisible/2
				if startIdx < 0 {
					startIdx = 0
				}
				endIdx = startIdx + maxVisible
				if endIdx > len(m.filteredBanks) {
					endIdx = len(m.filteredBanks)
					startIdx = endIdx - maxVisible
				}
			}

			for i := startIdx; i < endIdx; i++ {
				bank := m.filteredBanks[i]
				cursor := "  "
				rowStyle := normalStyle
				if i == m.selectedBankIdx {
					cursor = "👉 "
					rowStyle = selectedStyle
				}
				s += rowStyle.Render(fmt.Sprintf("%s%s (BIC: %s)", cursor, bank.Name, bank.Bic)) + "\n"
			}
			if len(m.filteredBanks) > maxVisible {
				s += "\n" + helpStyle.Render(fmt.Sprintf("  ... (showing %d-%d of %d matched banks, use Up/Down to scroll) ...", startIdx+1, endIdx, len(m.filteredBanks))) + "\n"
			}
		}
		s += "\n"
		s += helpStyle.Render("[Type to Search]  |  [Up/Down] Navigate List  |  [Enter] Select Bank  |  [Esc] Back  |  [Q] Quit")

	case stepAuthRedirect:
		s += headerStyle.Render("Step 4: Authorize connection at your bank") + "\n\n"
		s += boxStyle.Render("Press [O] or [Enter] to open your bank's secure authorization portal in your browser.\n\n"+
			"Direct Link:\n"+m.authResp.URL+"\n\n"+
			"Instructions:\n"+
			"1. Log in to your bank and authorize access to balances/transactions.\n"+
			"2. You will be redirected back. Copy the 'code' parameter from the redirected URL.") + "\n\n"
		s += helpStyle.Render("[O/Enter] Open Browser & Proceed  |  [Space] Proceed without opening  |  [Esc] Back")

	case stepCodeExchange:
		s += headerStyle.Render("Step 5: Paste Authorization Code") + "\n"
		s += "Exchange the redirected bank code for an active authorized session.\n\n"
		s += m.codeInput.View() + "\n\n"
		s += helpStyle.Render("[Enter] Complete Setup  |  [Esc] Back  |  [Q] Quit")

	case stepSuccess:
		s += headerStyle.Render("🎉 Setup Successfully Completed!") + "\n\n"
		s += boxStyle.Render(fmt.Sprintf("Your configuration has been written to: %s\n\n"+
			"The session is now authorized!\n"+
			"You can close this wizard and run the TUI Dashboard or MCP Server.", m.configPath)) + "\n\n"
		s += helpStyle.Render("Press [Q] or [Ctrl+C] to Exit.")
	}

	return s
}

func (m *SetupModel) startLocalCallbackServer() {
	m.serverChan = make(chan string, 1)

	u, err := url.Parse(m.cfg.EnableBanking.RedirectURL)
	if err != nil {
		return
	}

	port := "8080"
	if strings.Contains(u.Host, ":") {
		port = strings.Split(u.Host, ":")[1]
	}

	path := u.Path
	if path == "" {
		path = "/callback"
	}

	mux := http.NewServeMux()
	m.localServer = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		errParam := r.URL.Query().Get("error")

		if errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, `
				<html>
				<body style="font-family: Arial, sans-serif; text-align: center; padding-top: 50px; background-color: #1e1e2e; color: #f38ba8;">
					<h2>❌ Bank Authorization Failed</h2>
					<p>The bank returned an error: <b>%s</b></p>
					<p>Please return to your terminal and try again.</p>
				</body>
				</html>
			`, errParam)
			m.serverChan <- "error:" + errParam
			return
		}

		if code != "" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, `
				<html>
				<body style="font-family: Arial, sans-serif; text-align: center; padding-top: 50px; background-color: #1e1e2e; color: #a6e3a1;">
					<h2>✅ Authorization Successful!</h2>
					<p>The bank authorization code was captured successfully.</p>
					<p>You can close this browser tab and return to your terminal.</p>
				</body>
				</html>
			`)
			m.serverChan <- code
		} else {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "Missing 'code' query parameter.")
		}
	})

	go func() {
		_ = m.localServer.ListenAndServe()
	}()

	// Clean shutdown goroutine
	go func() {
		<-m.serverChan // Wait until code is sent
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.localServer.Shutdown(ctx)
	}()
}

func RunTUISetup(configPath string) error {
	m := NewSetupModel(configPath)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
