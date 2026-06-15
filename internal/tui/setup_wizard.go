package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/setup"
	"github.com/ngoldack/fin-mcp/internal/setupflow"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CountryOption is a selectable ISO 3166-1 country for bank discovery.
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

// Wizard steps (provider-agnostic).
const (
	stepType = iota
	stepName
	stepCredentials
	stepInstructions
	stepCountry
	stepBank
	stepAuth
	stepCode
	stepSuccess
)

// SetupModel is a provider-agnostic setup wizard. It selects a provider type,
// names the instance, collects the provider's credential fields, then runs that
// provider's setupflow.Flow (bank discovery + SCA authorization) to mint a
// connection — all without knowing anything provider-specific.
type SetupModel struct {
	configPath string
	cfg        *config.Config
	pc         *config.ProviderConfig
	flow       setupflow.Flow

	step      int
	err       error
	loading   bool
	statusMsg string

	// Step: provider type
	types   []config.ProviderType
	typeIdx int

	// Step: provider name
	nameInput textinput.Model

	// Step: credentials (rendered generically from flow.CredentialFields)
	fields       []setupflow.Field
	inputs       []textinput.Model // aligned with fields (zero value for choice fields)
	choiceIdx    []int             // aligned with fields (selected choice index)
	fieldFocus   int
	values       map[string]string
	instructions string

	// Step: country / bank
	countryIdx      int
	banks           []setupflow.Bank
	filteredBanks   []setupflow.Bank
	bankSearchInput textinput.Model
	bankIdx         int
	chosenBank      setupflow.Bank

	// Step: authorization redirect
	authURL     string
	serverChan  chan string
	localServer *http.Server

	// Step: code exchange
	codeInput textinput.Model

	connName string
}

func NewSetupModel(configPath string) *SetupModel {
	name := textinput.New()
	name.Placeholder = "Provider instance name"
	name.CharLimit = 50

	code := textinput.New()
	code.Placeholder = "Paste the 'code' parameter from the redirect"
	code.CharLimit = 200

	bankSearch := textinput.New()
	bankSearch.Placeholder = "Type to search your bank..."
	bankSearch.CharLimit = 50

	m := &SetupModel{
		configPath:      configPath,
		step:            stepType,
		types:           setupflow.Types(),
		nameInput:       name,
		codeInput:       code,
		bankSearchInput: bankSearch,
		values:          map[string]string{},
	}
	cfg, err := setup.LoadOrNew(configPath)
	if err != nil {
		m.err = err
		cfg = config.NewDefault()
	}
	m.cfg = cfg
	return m
}

func (m *SetupModel) Init() tea.Cmd { return textinput.Blink }

// Commands & messages.

type banksMsg []setupflow.Bank
type authURLMsg string
type connMsg config.Connection
type callbackResultMsg string

func fetchBanksCmd(flow setupflow.Flow, pc *config.ProviderConfig, country string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		banks, err := flow.Banks(ctx, pc, country)
		if err != nil {
			return errorMsg(err)
		}
		return banksMsg(banks)
	}
}

func startAuthCmd(flow setupflow.Flow, pc *config.ProviderConfig, req setupflow.ConnectionRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url, err := flow.StartConnection(ctx, pc, req)
		if err != nil {
			return errorMsg(err)
		}
		return authURLMsg(url)
	}
}

func completeCmd(flow setupflow.Flow, pc *config.ProviderConfig, req setupflow.ConnectionRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := flow.CompleteConnection(ctx, pc, req)
		if err != nil {
			return errorMsg(err)
		}
		return connMsg(conn)
	}
}

func waitForCallbackCmd(ch chan string) tea.Cmd {
	return func() tea.Msg { return callbackResultMsg(<-ch) }
}

func (m *SetupModel) connectionRequest() setupflow.ConnectionRequest {
	return setupflow.ConnectionRequest{
		Bank: m.chosenBank,
		Code: strings.TrimSpace(m.codeInput.Value()),
		Days: 90,
	}
}

// initCredentialFields prepares the generic input widgets for the selected flow.
func (m *SetupModel) initCredentialFields() {
	m.fields = m.flow.CredentialFields()
	m.inputs = make([]textinput.Model, len(m.fields))
	m.choiceIdx = make([]int, len(m.fields))
	m.fieldFocus = 0
	for i, f := range m.fields {
		if f.Kind == setupflow.FieldChoice {
			for j, ch := range f.Choices {
				if ch == f.Default {
					m.choiceIdx[i] = j
				}
			}
			continue
		}
		in := textinput.New()
		in.Placeholder = f.Label
		in.CharLimit = 200
		if f.Kind == setupflow.FieldSecret {
			in.EchoMode = textinput.EchoPassword
		}
		in.SetValue(f.Default)
		m.inputs[i] = in
	}
	m.focusField(0)
}

func (m *SetupModel) focusField(idx int) {
	for i := range m.fields {
		if m.fields[i].Kind != setupflow.FieldChoice {
			m.inputs[i].Blur()
		}
	}
	m.fieldFocus = idx
	if idx >= 0 && idx < len(m.fields) && m.fields[idx].Kind != setupflow.FieldChoice {
		m.inputs[idx].Focus()
	}
}

// afterCredentials routes past credential collection: providers needing SCA go
// to bank discovery; others complete the connection immediately.
func (m *SetupModel) afterCredentials() (tea.Model, tea.Cmd) {
	m.err = nil
	if !m.flow.NeedsAuthorization() {
		m.loading = true
		m.statusMsg = "Finalizing connection..."
		return m, completeCmd(m.flow, m.pc, m.connectionRequest())
	}
	m.step = stepCountry
	return m, nil
}

func (m *SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.step == stepCredentials || m.step == stepName || m.step == stepCode || m.step == stepBank {
				// 'q' is a valid character in these text-entry steps; only quit on ctrl+c.
				if msg.String() == "q" {
					break
				}
			}
			return m, tea.Quit
		case "esc":
			if m.step > stepType && !m.loading {
				m.step--
				m.err = nil
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
		m.filteredBanks = msg
		if len(m.banks) == 0 {
			m.err = fmt.Errorf("no banks found for %s", countryOptions[m.countryIdx].Code)
			return m, nil
		}
		m.step = stepBank
		m.bankIdx = 0
		m.bankSearchInput.SetValue("")
		m.bankSearchInput.Focus()
		return m, nil

	case authURLMsg:
		m.loading = false
		m.authURL = string(msg)
		m.step = stepAuth
		m.startCallbackServer()
		return m, waitForCallbackCmd(m.serverChan)

	case callbackResultMsg:
		result := string(msg)
		if strings.HasPrefix(result, "error:") {
			m.err = fmt.Errorf("bank authorization failed: %s", strings.TrimPrefix(result, "error:"))
			m.step = stepCode
			m.codeInput.Focus()
			return m, nil
		}
		m.codeInput.SetValue(result)
		m.loading = true
		m.statusMsg = "Exchanging captured authorization code..."
		m.err = nil
		return m, completeCmd(m.flow, m.pc, m.connectionRequest())

	case connMsg:
		m.loading = false
		conn := config.Connection(msg)
		setupflow.Upsert(m.pc, conn)
		if err := config.SaveConfig(m.configPath, m.cfg); err != nil {
			m.err = err
			return m, nil
		}
		m.connName = conn.Name
		m.step = stepSuccess
		return m, nil
	}

	switch m.step {
	case stepType:
		return m.updateType(msg)
	case stepName:
		return m.updateName(msg)
	case stepCredentials:
		return m.updateCredentials(msg)
	case stepInstructions:
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
			return m.afterCredentials()
		}
	case stepCountry:
		return m.updateCountry(msg)
	case stepBank:
		return m.updateBank(msg)
	case stepAuth:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "o", "enter":
				if m.authURL != "" {
					_ = OpenBrowser(m.authURL)
				}
				m.step = stepCode
				m.codeInput.Focus()
			case "space":
				m.step = stepCode
				m.codeInput.Focus()
			}
		}
	case stepCode:
		return m.updateCode(msg)
	}

	return m, cmd
}

func (m *SetupModel) updateType(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "up":
		if m.typeIdx > 0 {
			m.typeIdx--
		}
	case "down":
		if m.typeIdx < len(m.types)-1 {
			m.typeIdx++
		}
	case "enter":
		flow, ok := setupflow.For(m.types[m.typeIdx])
		if !ok {
			m.err = fmt.Errorf("no setup flow for %q", m.types[m.typeIdx])
			return m, nil
		}
		m.flow = flow
		m.nameInput.SetValue(string(m.types[m.typeIdx]))
		m.nameInput.Focus()
		m.step = stepName
	}
	return m, nil
}

func (m *SetupModel) updateName(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			name = string(m.flow.Type())
		}
		if existing := m.cfg.Provider(name); existing != nil && existing.Type != m.flow.Type() {
			m.err = fmt.Errorf("provider %q already exists with type %q", name, existing.Type)
			return m, nil
		}
		m.pc = setup.EnsureProvider(m.cfg, name, m.flow.Type())
		m.initCredentialFields()
		if len(m.fields) == 0 {
			return m.afterCredentials()
		}
		m.step = stepCredentials
		return m, nil
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m *SetupModel) updateCredentials(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	focused := m.fields[m.fieldFocus]
	switch k.String() {
	case "tab", "down":
		m.focusField((m.fieldFocus + 1) % len(m.fields))
		return m, nil
	case "up":
		m.focusField((m.fieldFocus - 1 + len(m.fields)) % len(m.fields))
		return m, nil
	case "left", "right", "space":
		if focused.Kind == setupflow.FieldChoice && len(focused.Choices) > 0 {
			m.choiceIdx[m.fieldFocus] = (m.choiceIdx[m.fieldFocus] + 1) % len(focused.Choices)
			return m, nil
		}
	case "enter":
		if m.fieldFocus < len(m.fields)-1 {
			m.focusField(m.fieldFocus + 1)
			return m, nil
		}
		return m.submitCredentials()
	}

	if focused.Kind != setupflow.FieldChoice {
		var cmd tea.Cmd
		m.inputs[m.fieldFocus], cmd = m.inputs[m.fieldFocus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *SetupModel) submitCredentials() (tea.Model, tea.Cmd) {
	values := make(map[string]string, len(m.fields))
	for i, f := range m.fields {
		var v string
		if f.Kind == setupflow.FieldChoice {
			if len(f.Choices) > 0 {
				v = f.Choices[m.choiceIdx[i]]
			}
		} else {
			v = strings.TrimSpace(m.inputs[i].Value())
		}
		if v == "" && !f.Optional {
			m.err = fmt.Errorf("%s is required", f.Label)
			return m, nil
		}
		values[f.Key] = v
	}
	m.values = values

	instr, err := m.flow.ApplyCredentials(m.pc, values)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.instructions = instr
	m.err = nil
	if instr != "" {
		m.step = stepInstructions
		return m, nil
	}
	return m.afterCredentials()
}

func (m *SetupModel) updateCountry(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "up":
		if m.countryIdx > 0 {
			m.countryIdx--
		}
	case "down":
		if m.countryIdx < len(countryOptions)-1 {
			m.countryIdx++
		}
	case "enter":
		m.loading = true
		m.statusMsg = "Fetching banks..."
		m.err = nil
		return m, fetchBanksCmd(m.flow, m.pc, countryOptions[m.countryIdx].Code)
	}
	return m, nil
}

func (m *SetupModel) updateBank(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up":
			if m.bankIdx > 0 {
				m.bankIdx--
			}
			return m, nil
		case "down":
			if m.bankIdx < len(m.filteredBanks)-1 {
				m.bankIdx++
			}
			return m, nil
		case "enter":
			if len(m.filteredBanks) == 0 {
				return m, nil
			}
			m.chosenBank = m.filteredBanks[m.bankIdx]
			m.loading = true
			m.statusMsg = "Initiating bank authorization..."
			m.err = nil
			return m, startAuthCmd(m.flow, m.pc, m.connectionRequest())
		}
	}

	var cmd tea.Cmd
	m.bankSearchInput, cmd = m.bankSearchInput.Update(msg)
	query := strings.ToLower(m.bankSearchInput.Value())
	m.filteredBanks = nil
	for _, b := range m.banks {
		if strings.Contains(strings.ToLower(b.Name), query) || strings.Contains(strings.ToLower(b.BIC), query) {
			m.filteredBanks = append(m.filteredBanks, b)
		}
	}
	if m.bankIdx >= len(m.filteredBanks) {
		m.bankIdx = len(m.filteredBanks) - 1
	}
	if m.bankIdx < 0 {
		m.bankIdx = 0
	}
	return m, cmd
}

func (m *SetupModel) updateCode(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
		if strings.TrimSpace(m.codeInput.Value()) == "" {
			m.err = fmt.Errorf("authorization code is required")
			return m, nil
		}
		m.loading = true
		m.statusMsg = "Exchanging authorization code..."
		m.err = nil
		return m, completeCmd(m.flow, m.pc, m.connectionRequest())
	}
	var cmd tea.Cmd
	m.codeInput, cmd = m.codeInput.Update(msg)
	return m, cmd
}

func (m *SetupModel) View() string {
	s := titleStyle.Render("🏦 fin-mcp — SETUP WIZARD") + "\n\n"

	if m.err != nil {
		s += errorStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)) + "\n\n"
	}
	if m.loading {
		return s + lipgloss.NewStyle().Foreground(accentColor).Render("⌛ "+m.statusMsg) + "\n"
	}

	switch m.step {
	case stepType:
		s += headerStyle.Render("Choose a provider type:") + "\n\n"
		for i, t := range m.types {
			cursor, style := "  ", normalStyle
			if i == m.typeIdx {
				cursor, style = "👉 ", selectedStyle
			}
			s += style.Render(cursor+string(t)) + "\n"
		}
		s += "\n" + helpStyle.Render("[Up/Down] Navigate · [Enter] Select · [Ctrl+C] Quit")

	case stepName:
		s += headerStyle.Render("Name this provider instance:") + "\n\n"
		s += m.nameInput.View() + "\n\n"
		s += helpStyle.Render("[Enter] Continue · [Esc] Back")

	case stepCredentials:
		s += headerStyle.Render(fmt.Sprintf("Configure %s credentials:", m.flow.Type())) + "\n\n"
		s += m.renderFields()
		s += "\n" + helpStyle.Render("[Tab/Arrows] Navigate · [Left/Right/Space] Toggle choice · [Enter] Next/Submit · [Esc] Back")

	case stepInstructions:
		s += headerStyle.Render("Action required") + "\n\n"
		s += boxStyle.Render(strings.TrimSpace(m.instructions)) + "\n\n"
		s += helpStyle.Render("[Enter] Continue · [Esc] Back")

	case stepCountry:
		s += headerStyle.Render("Select the country of your bank:") + "\n\n"
		for i, c := range countryOptions {
			cursor, style := "  ", normalStyle
			if i == m.countryIdx {
				cursor, style = "👉 ", selectedStyle
			}
			s += style.Render(fmt.Sprintf("%s%s (%s)", cursor, c.Name, c.Code)) + "\n"
		}
		s += "\n" + helpStyle.Render("[Up/Down] Navigate · [Enter] Fetch banks · [Esc] Back")

	case stepBank:
		s += headerStyle.Render("Select your bank:") + "\n\n"
		s += "🔍 " + m.bankSearchInput.View() + "\n"
		s += helpStyle.Render(fmt.Sprintf("  (matching %d of %d)", len(m.filteredBanks), len(m.banks))) + "\n\n"
		s += m.renderBankList()
		s += "\n" + helpStyle.Render("[Type to search] · [Up/Down] Navigate · [Enter] Select · [Esc] Back")

	case stepAuth:
		s += headerStyle.Render("Authorize at your bank") + "\n\n"
		s += boxStyle.Render("Open the secure authorization portal, log in, and authorize access.\n\n"+
			"Direct link:\n"+m.authURL+"\n\n"+
			"You will be redirected back; the wizard captures the code automatically.") + "\n\n"
		s += helpStyle.Render("[O/Enter] Open browser · [Space] Continue without opening · [Esc] Back")

	case stepCode:
		s += headerStyle.Render("Paste the authorization code") + "\n\n"
		s += m.codeInput.View() + "\n\n"
		s += helpStyle.Render("[Enter] Complete · [Esc] Back")

	case stepSuccess:
		s += headerStyle.Render("🎉 Setup complete!") + "\n\n"
		s += boxStyle.Render(fmt.Sprintf("Connection %q saved to %s.\n\nProvider %q is ready.", m.connName, m.configPath, m.pc.Name)) + "\n\n"
		s += helpStyle.Render("[Ctrl+C] Exit")
	}

	return s
}

func (m *SetupModel) renderFields() string {
	var b strings.Builder
	for i, f := range m.fields {
		cursor := "  "
		if i == m.fieldFocus {
			cursor = "👉 "
		}
		label := f.Label
		if f.Optional {
			label += " (optional)"
		}
		if f.Kind == setupflow.FieldChoice {
			b.WriteString(cursor + label + ": ")
			for j, ch := range f.Choices {
				if j == m.choiceIdx[i] {
					b.WriteString(selectedStyle.Render("[" + ch + "]"))
				} else {
					b.WriteString(" " + ch + " ")
				}
			}
			b.WriteString("\n\n")
			continue
		}
		b.WriteString(cursor + label + "\n")
		b.WriteString("  " + m.inputs[i].View() + "\n\n")
	}
	return b.String()
}

func (m *SetupModel) renderBankList() string {
	if len(m.filteredBanks) == 0 {
		return errorStyle.Render("   No banks match your search.") + "\n"
	}
	start, end, maxVisible := 0, len(m.filteredBanks), 10
	if len(m.filteredBanks) > maxVisible {
		start = m.bankIdx - maxVisible/2
		if start < 0 {
			start = 0
		}
		end = start + maxVisible
		if end > len(m.filteredBanks) {
			end = len(m.filteredBanks)
			start = end - maxVisible
		}
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		bank := m.filteredBanks[i]
		cursor, style := "  ", normalStyle
		if i == m.bankIdx {
			cursor, style = "👉 ", selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s (BIC: %s)", cursor, bank.Name, bank.BIC)) + "\n")
	}
	return b.String()
}

// startCallbackServer listens on the redirect URL to capture the SCA code.
func (m *SetupModel) startCallbackServer() {
	m.serverChan = make(chan string, 1)

	redirect := m.values["redirect_url"]
	port, path := "8080", "/callback"
	if u, err := url.Parse(redirect); err == nil {
		if strings.Contains(u.Host, ":") {
			port = strings.Split(u.Host, ":")[1]
		}
		if u.Path != "" {
			path = u.Path
		}
	}

	mux := http.NewServeMux()
	m.localServer = &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, `<html><body style="font-family:sans-serif;text-align:center;padding-top:50px;background:#1e1e2e;color:#f38ba8"><h2>❌ Authorization failed</h2><p>%s</p><p>Return to your terminal.</p></body></html>`, errParam)
			m.serverChan <- "error:" + errParam
			return
		}
		if code := r.URL.Query().Get("code"); code != "" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body style="font-family:sans-serif;text-align:center;padding-top:50px;background:#1e1e2e;color:#a6e3a1"><h2>✅ Authorized!</h2><p>You can close this tab and return to your terminal.</p></body></html>`)
			m.serverChan <- code
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, "Missing 'code' query parameter.")
	})

	go func() { _ = m.localServer.ListenAndServe() }()
	go func() {
		<-m.serverChan
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.localServer.Shutdown(ctx)
	}()
}

// RunTUISetup launches the interactive provider-agnostic setup wizard.
func RunTUISetup(configPath string) error {
	p := tea.NewProgram(NewSetupModel(configPath))
	_, err := p.Run()
	return err
}
