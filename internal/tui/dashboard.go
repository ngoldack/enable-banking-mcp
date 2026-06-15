package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ngoldack/fin-mcp/internal/bank"
	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/provider"
)

// viewState enumerates the operator-console screens.
type viewState int

const (
	viewLoading viewState = iota
	viewOverview
	viewDetail
	viewConfig
	viewError
)

// accountItem adapts a bank.Account to the bubbles/list Item interface.
type accountItem struct {
	acc bank.Account
	bal string
}

func (i accountItem) Title() string {
	if i.acc.Name == "" {
		return "Account"
	}
	return i.acc.Name
}

func (i accountItem) Description() string {
	bal := i.bal
	if bal == "" {
		bal = "—"
	}
	iban := i.acc.IBAN
	if iban == "" {
		iban = "(no IBAN)"
	}
	return fmt.Sprintf("%s · %s · avail %s", iban, i.acc.Currency, bal)
}

func (i accountItem) FilterValue() string { return i.acc.Name + " " + i.acc.IBAN }

// Model is the operator-console Bubble Tea model. It is a read-only tool to set
// up, inspect, and verify the Enable Banking ↔ MCP connection.
type Model struct {
	configPath string
	cfg        *config.Config
	prov       provider.Provider
	cache      bank.Cache

	state      viewState
	prevState  viewState
	width      int
	height     int
	status     string
	err        error
	showAbbrev bool

	keys     keyMap
	help     help.Model
	spinner  spinner.Model
	accounts list.Model
	balances table.Model
	txns     table.Model

	selected   bank.Account
	balanceRaw []bank.AccountBalance
	txnRaw     []bank.Transaction
}

// Bubble Tea messages.
type accountsMsg []bank.Account
type errorMsg error
type accountDetailMsg struct {
	balances     []bank.AccountBalance
	transactions []bank.Transaction
	err          error
}

// Commands.

func fetchAccountsCmd(prov provider.Provider, cache bank.Cache, refresh bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		if !refresh {
			if accounts, ok := cache.GetAccounts(ctx); ok {
				return accountsMsg(accounts)
			}
		}

		accounts, err := prov.ListAccounts(ctx)
		if err != nil {
			return errorMsg(err)
		}

		cache.SetAccounts(ctx, accounts)
		return accountsMsg(accounts)
	}
}

func fetchAccountDetailCmd(prov provider.Provider, accountID string, cache bank.Cache, refresh bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if !refresh {
			if detail, ok := cache.GetDetail(ctx, accountID); ok && len(detail.Balances) > 0 {
				return accountDetailMsg{balances: detail.Balances, transactions: detail.Transactions}
			}
		}

		balances, err := prov.GetBalances(ctx, accountID)
		if err != nil {
			return accountDetailMsg{err: fmt.Errorf("failed to fetch balances: %w", err)}
		}

		var domainTxs []bank.Transaction
		if txs, err := prov.GetTransactions(ctx, accountID, "", ""); err == nil {
			domainTxs = txs
		}

		detail, _ := cache.GetDetail(ctx, accountID)
		detail.Account.ID = accountID
		detail.Account.AvailableBalance = balances.Available
		detail.Account.BookedBalance = balances.Booked
		detail.Balances = balances.Items
		detail.Transactions = domainTxs
		cache.SetDetail(ctx, accountID, detail)

		return accountDetailMsg{balances: balances.Items, transactions: domainTxs}
	}
}

func NewModel(configPath string) (*Model, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	registry, err := provider.FromConfig(cfg, configPath)
	if err != nil {
		return nil, err
	}
	prov, ok := registry.Default()
	if !ok {
		return nil, fmt.Errorf("no bank provider configured")
	}
	bCache, err := bank.NewCache(bank.CacheOptions{
		Type: string(cfg.MCP.CacheType),
		TTL:  time.Duration(cfg.MCP.CacheTTLMinutes) * time.Minute,
		Valkey: bank.ValkeyOptions{
			Address:  cfg.MCP.CacheValkeyAddress,
			Username: cfg.MCP.CacheValkeyUsername,
			Password: cfg.MCP.CacheValkeyPassword,
			DB:       cfg.MCP.CacheValkeyDB,
			TLS:      cfg.MCP.CacheValkeyTLS,
		},
		Encrypted:     cfg.MCP.CacheType == config.CacheValkey && cfg.MCP.CacheEncryption == config.CacheEncrypted,
		EncryptionKey: cfg.MCP.CacheEncryptionKey,
	})
	if err != nil {
		return nil, err
	}

	return newModel(configPath, cfg, prov, bCache), nil
}

// newModel assembles the model from injected dependencies (no I/O), which keeps
// it unit-testable with a mock provider and a temp cache.
func newModel(configPath string, cfg *config.Config, prov provider.Provider, cache bank.Cache) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accentColor)

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(accentColor).BorderForeground(accentColor)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(accentColor).BorderForeground(accentColor)

	accounts := list.New(nil, delegate, 0, 0)
	accounts.SetShowTitle(false)
	accounts.SetShowHelp(false)
	accounts.SetShowStatusBar(false)
	accounts.SetFilteringEnabled(false)

	return &Model{
		configPath: configPath,
		cfg:        cfg,
		prov:       prov,
		cache:      cache,
		state:      viewLoading,
		status:     "Loading bank accounts…",
		keys:       defaultKeyMap(),
		help:       help.New(),
		spinner:    sp,
		accounts:   accounts,
		balances:   newTable([]table.Column{{Title: "Balance Type", Width: 24}, {Title: "Amount", Width: 18}}),
		txns:       newTable([]table.Column{{Title: "Date", Width: 12}, {Title: "Description", Width: 30}, {Title: "Amount", Width: 16}, {Title: "Status", Width: 10}}),
	}
}

func newTable(cols []table.Column) table.Model {
	t := table.New(table.WithColumns(cols), table.WithFocused(true))
	st := table.DefaultStyles()
	st.Header = st.Header.Bold(true).Foreground(accentColor).BorderBottom(true).BorderForeground(accentColor)
	st.Selected = st.Selected.Foreground(textColor).Background(accentColor).Bold(true)
	t.SetStyles(st)
	return t
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchAccountsCmd(m.prov, m.cache, false),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		return m, nil

	case spinner.TickMsg:
		if m.state == viewLoading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case errorMsg:
		m.state = viewError
		m.err = msg
		return m, nil

	case accountsMsg:
		m.setAccounts(msg)
		m.state = viewOverview
		return m, nil

	case accountDetailMsg:
		if msg.err != nil {
			m.state = viewError
			m.err = msg.err
			return m, nil
		}
		m.balanceRaw = msg.balances
		m.txnRaw = msg.transactions
		m.rebuildDetailTables()
		m.state = viewDetail
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Delegate to the focused component of the active view.
	return m, m.updateFocused(msg)
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case key.Matches(msg, m.keys.Config):
		switch m.state {
		case viewConfig:
			m.state = m.prevState
		case viewOverview, viewDetail:
			m.prevState = m.state
			m.state = viewConfig
		}
		return m, nil
	}

	switch m.state {
	case viewConfig:
		if key.Matches(msg, m.keys.Back) {
			m.state = m.prevState
		}
		return m, nil

	case viewError:
		if key.Matches(msg, m.keys.Back) {
			m.err = nil
			m.state = viewOverview
		}
		return m, nil

	case viewOverview:
		switch {
		case key.Matches(msg, m.keys.Enter):
			if it, ok := m.accounts.SelectedItem().(accountItem); ok {
				m.selected = it.acc
				m.state = viewLoading
				m.status = "Loading balances & transactions…"
				return m, tea.Batch(m.spinner.Tick, fetchAccountDetailCmd(m.prov, it.acc.ID, m.cache, false))
			}
		case key.Matches(msg, m.keys.Refresh):
			m.state = viewLoading
			m.status = "Refreshing accounts…"
			return m, tea.Batch(m.spinner.Tick, fetchAccountsCmd(m.prov, m.cache, true))
		}
		var cmd tea.Cmd
		m.accounts, cmd = m.accounts.Update(msg)
		return m, cmd

	case viewDetail:
		switch {
		case key.Matches(msg, m.keys.Back):
			m.showAbbrev = false
			m.state = viewOverview
			return m, nil
		case key.Matches(msg, m.keys.Abbrev):
			m.showAbbrev = !m.showAbbrev
			return m, nil
		case key.Matches(msg, m.keys.Refresh):
			m.state = viewLoading
			m.status = "Refreshing account…"
			return m, tea.Batch(m.spinner.Tick, fetchAccountDetailCmd(m.prov, m.selected.ID, m.cache, true))
		}
		var cmd tea.Cmd
		m.txns, cmd = m.txns.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) updateFocused(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.state {
	case viewOverview:
		m.accounts, cmd = m.accounts.Update(msg)
	case viewDetail:
		m.txns, cmd = m.txns.Update(msg)
	}
	return cmd
}

func (m *Model) setAccounts(accounts []bank.Account) {
	items := make([]list.Item, len(accounts))
	for i, a := range accounts {
		bal := "—"
		if detail, ok := m.cache.GetDetail(context.Background(), a.ID); ok && detail.Account.AvailableBalance != "" {
			bal = fmt.Sprintf("€%s", detail.Account.AvailableBalance)
		}
		items[i] = accountItem{acc: a, bal: bal}
	}
	m.accounts.SetItems(items)
}

func (m *Model) rebuildDetailTables() {
	balRows := make([]table.Row, len(m.balanceRaw))
	for i, b := range m.balanceRaw {
		balRows[i] = table.Row{b.Name, b.Amount + " " + string(m.selected.Currency)}
	}
	m.balances.SetRows(balRows)

	txRows := make([]table.Row, len(m.txnRaw))
	for i, tx := range m.txnRaw {
		txRows[i] = table.Row{tx.Date, truncate(tx.Description, 30), tx.Amount + " " + string(tx.Currency), tx.Status}
	}
	m.txns.SetRows(txRows)
	m.txns.SetCursor(0)
}

// View renders the active screen with a header, body, and help footer.
func (m *Model) View() string {
	if m.width == 0 {
		return "Initializing…"
	}

	header := m.headerView()
	footer := helpStyle.Render(m.help.View(m.keys))
	bodyHeight := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 1
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	body := m.bodyView(bodyHeight)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m *Model) headerView() string {
	info := m.prov.Info()
	title := titleStyle.Render("🏦 " + info.Name + " · Operator Console")

	envStyle := successStyle
	if strings.EqualFold(info.Environment, "PRODUCTION") {
		envStyle = errorStyle
	}

	lines := []string{
		fmt.Sprintf("%s  %s   %s  %d",
			labelStyle.Render("Env"), envStyle.Render(info.Environment),
			labelStyle.Render("Connections"), len(info.Connections)),
	}
	for _, c := range info.Connections {
		consentText, consentStyle := consentStatus(c.ConsentValidUntil)
		lines = append(lines, fmt.Sprintf("  • %s · %s · %s",
			normalStyle.Render(c.Name),
			normalStyle.Render(fmt.Sprintf("%s (%s)", c.Bank, c.Country)),
			consentStyle.Render(consentText)))
	}
	lines = append(lines, fmt.Sprintf("%s  %s · access=%s · cache=%dm",
		labelStyle.Render("MCP"), normalStyle.Render(string(m.cfg.MCP.Transport)),
		accessStyle(m.cfg.MCP.AccessMode).Render(string(m.cfg.MCP.AccessMode)),
		m.cfg.MCP.CacheTTLMinutes))

	status := statusBoxStyle.Width(min(m.width-2, 78)).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	return lipgloss.JoinVertical(lipgloss.Left, title, status)
}

func (m *Model) bodyView(h int) string {
	switch m.state {
	case viewLoading:
		return lipgloss.NewStyle().Foreground(accentColor).Render(m.spinner.View() + " " + m.status)

	case viewError:
		fe := bank.FriendlyError(m.err)
		return lipgloss.JoinVertical(lipgloss.Left,
			errorStyle.Render("❌ "+fe.Title),
			normalStyle.Render(fe.Description),
			"",
			helpStyle.Render("Press [esc] to return, [q] to quit."),
		)

	case viewConfig:
		return m.configView()

	case viewOverview:
		m.accounts.SetSize(min(m.width, 80), h-1)
		header := headerStyle.Render(fmt.Sprintf("Accounts (%d)", len(m.accounts.Items())))
		if len(m.accounts.Items()) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, header, normalStyle.Render("No accounts in this session."))
		}
		return lipgloss.JoinVertical(lipgloss.Left, header, m.accounts.View())

	case viewDetail:
		return m.detailView(h)
	}
	return ""
}

func (m *Model) detailView(h int) string {
	a := m.selected
	head := lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Render("🏠 "+a.Name),
		normalStyle.Render(fmt.Sprintf("IBAN %s · %s", a.IBAN, a.Currency)),
	)

	var balSection string
	if m.showAbbrev {
		balSection = m.renderAbbreviationsHelp()
	} else if len(m.balanceRaw) == 0 {
		balSection = normalStyle.Render("No balances available.")
	} else {
		m.balances.SetHeight(min(len(m.balanceRaw)+1, 7))
		m.balances.SetWidth(min(m.width, 60))
		balSection = successStyle.Render("💰 Balances") + "\n" + m.balances.View()
	}

	var txSection string
	if len(m.txnRaw) == 0 {
		txSection = normalStyle.Render("No recent transactions available.")
	} else {
		txH := h - lipgloss.Height(head) - lipgloss.Height(balSection) - 3
		if txH < 3 {
			txH = 3
		}
		m.txns.SetHeight(txH)
		m.txns.SetWidth(min(m.width, 78))
		txSection = successStyle.Render("📋 Recent Transactions") + "\n" + m.txns.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, head, "", balSection, "", txSection)
}

func (m *Model) configView() string {
	intro := headerStyle.Render("📋 MCP Client Configuration")
	hint := tipStyle.Render("Paste this into your MCP client (Claude Desktop, Cursor, …). Press [c]/[esc] to close.")
	snippet := boxStyle.Render(m.mcpClientConfigSnippet())
	return lipgloss.JoinVertical(lipgloss.Left, intro, hint, "", snippet)
}

func (m *Model) mcpClientConfigSnippet() string {
	if m.cfg.MCP.Transport == config.TransportSSE {
		port := m.cfg.MCP.Port
		if port == 0 {
			port = 8090
		}
		url := fmt.Sprintf("http://localhost:%d/", port)
		if m.cfg.MCP.BearerToken != "" {
			url += "?token=" + m.cfg.MCP.BearerToken
		}
		return fmt.Sprintf("{\n  \"mcpServers\": {\n    \"fin-mcp\": {\n      \"url\": \"%s\"\n    }\n  }\n}", url)
	}
	return fmt.Sprintf("{\n  \"mcpServers\": {\n    \"fin-mcp\": {\n      \"command\": \"fin-mcp\",\n      \"args\": [\"server\", \"--config\", \"%s\"]\n    }\n  }\n}", m.configPath)
}

func (m *Model) renderAbbreviationsHelp() string {
	guide := "📚 Balance type abbreviations\n" +
		"  CLBD  Closing Booked — final end-of-day booked balance\n" +
		"  ITBD  Interim Booked — current booked, may change intraday\n" +
		"  OPBD  Opening Booked — booked balance at start of day\n" +
		"  XPBD  Expected — includes pending / authorized holds\n" +
		"  CLAV  Closing Available — available for withdrawal\n" +
		"  ITAV  Interim Available — available for immediate use\n" +
		"Press [h] to close."
	return boxStyle.Render(guide)
}

// Helpers.

func consentStatus(vu time.Time) (string, lipgloss.Style) {
	if vu.IsZero() {
		return "unknown", tipStyle
	}
	d := time.Until(vu)
	if d <= 0 {
		return "EXPIRED — re-run setup", errorStyle
	}
	return "valid for " + humanizeDuration(d), successStyle
}

func humanizeDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func accessStyle(mode config.AccessMode) lipgloss.Style {
	switch mode {
	case config.Unrestricted:
		return errorStyle
	case config.InternalOnly:
		return tipStyle
	default:
		return successStyle
	}
}

func shorten(s string, n int) string {
	if s == "" {
		return "(none)"
	}
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// RunTUI launches the operator console in the alternate screen buffer.
func RunTUI(configPath string) error {
	model, err := NewModel(configPath)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}
