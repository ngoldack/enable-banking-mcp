package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/ngoldack/enable-banking-go/pkg/bank"
	"github.com/ngoldack/enable-banking-go/pkg/config"
	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// TUI Styles
var (
	accentColor  = lipgloss.Color("63") // Violet
	textColor    = lipgloss.Color("255")
	errorColor   = lipgloss.Color("196")
	successColor = lipgloss.Color("40")
	grayColor    = lipgloss.Color("242")
	amberColor   = lipgloss.Color("214")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor).
			Background(accentColor).
			Padding(0, 1).
			MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			Underline(true)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor).
			Background(accentColor)

	normalStyle = lipgloss.NewStyle().
			Foreground(textColor)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(errorColor)

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(successColor)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Padding(1, 2).
			MarginBottom(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(grayColor).
			Italic(true)

	tipStyle = lipgloss.NewStyle().
			Foreground(amberColor).
			Italic(true)
)

// State Constants
const (
	stateLoading        = "loading"
	stateAccounts       = "accounts"
	stateAccountDetail  = "account_detail"
	stateTransfer       = "transfer"
	stateTransferResult = "transfer_result"
	stateError          = "error"
)

type Model struct {
	configPath            string
	cfg                   *config.Config
	client                enablebanking.APIClient
	cache                 *bank.Cache
	state                 string
	statusMessage         string
	err                   error
	accounts              []bank.Account
	selectedAccountIdx    int
	balances              []bank.AccountBalance
	transactions          []bank.Transaction
	showAbbreviationsHelp bool

	// Transfer inputs
	inputs             []textinput.Model
	focusedInputIdx    int
	transferLoading    bool
	paymentResp        *enablebanking.CreatePaymentResponse
}

// Bubble Tea Messages
type accountsMsg []bank.Account
type errorMsg error

type accountDetailMsg struct {
	balances     []bank.AccountBalance
	transactions []bank.Transaction
	err          error
}

type transferSuccessMsg *enablebanking.CreatePaymentResponse

// Bubble Tea Commands

func fetchAccountsCmd(client enablebanking.APIClient, sessionID string, bankName string, cache *bank.Cache) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Check cache first
		if accounts, ok := cache.GetAccounts(ctx); ok {
			return accountsMsg(accounts)
		}

		sess, err := client.GetSession(ctx, sessionID)
		if err != nil {
			return errorMsg(err)
		}

		var accounts []bank.Account
		for _, accID := range sess.Accounts {
			accDetails, err := client.GetAccountDetails(ctx, accID)
			if err != nil {
				continue
			}
			accounts = append(accounts, bank.MapAccountToDomain(*accDetails, bankName))
		}

		if len(accounts) == 0 {
			return errorMsg(fmt.Errorf("no bank accounts found in this session"))
		}

		cache.SetAccounts(ctx, accounts)
		return accountsMsg(accounts)
	}
}

func fetchAccountDetailCmd(client enablebanking.APIClient, accountID string, cache *bank.Cache) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Check cache first
		if detail, ok := cache.GetDetail(ctx, accountID); ok && len(detail.Balances) > 0 {
			return accountDetailMsg{
				balances:     detail.Balances,
				transactions: detail.Transactions,
			}
		}

		balances, err := client.GetBalances(ctx, accountID)
		if err != nil {
			return accountDetailMsg{err: fmt.Errorf("failed to fetch balances: %w", err)}
		}

		domainBals, available, booked := bank.MapBalancesToDomain(balances)

		txs, err := client.GetTransactions(ctx, accountID, "", "")
		var domainTxs []bank.Transaction
		if err == nil {
			domainTxs = bank.MapTransactionsToDomain(txs)
		}

		// Save to cache
		detail, _ := cache.GetDetail(ctx, accountID)
		detail.Account.ID = accountID
		detail.Account.AvailableBalance = available
		detail.Account.BookedBalance = booked
		detail.Balances = domainBals
		detail.Transactions = domainTxs
		cache.SetDetail(ctx, accountID, detail)

		return accountDetailMsg{balances: domainBals, transactions: domainTxs}
	}
}

func executeTransferCmd(client enablebanking.APIClient, debtorIban, creditorIban, creditorName, amount, state, redirectURL string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		resp, err := client.CreatePayment(ctx, debtorIban, creditorIban, creditorName, amount, "EUR", "SEPA", state, redirectURL)
		if err != nil {
			return errorMsg(err)
		}
		return transferSuccessMsg(resp)
	}
}

func OpenBrowser(url string) error {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	return err
}

func NewModel(configPath string) (*Model, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	client := enablebanking.NewClient(cfg.EnableBanking.AppID, cfg.EnableBanking.PrivateKeyPath, cfg.EnableBanking.PrivateKeyContent, cfg.EnableBanking.Environment)
	ttl := time.Duration(cfg.MCP.CacheTTLMinutes) * time.Minute
	bCache := bank.NewCache(".bank.db", ttl)

	// Setup transfer inputs
	inputs := make([]textinput.Model, 3)
	inputs[0] = textinput.New()
	inputs[0].Placeholder = "Creditor IBAN (e.g., DE12345678...)"
	inputs[0].CharLimit = 34
	inputs[0].Focus()

	inputs[1] = textinput.New()
	inputs[1].Placeholder = "Creditor Name / Account Owner"
	inputs[1].CharLimit = 70

	inputs[2] = textinput.New()
	inputs[2].Placeholder = "Amount in EUR (e.g., 10.50)"
	inputs[2].CharLimit = 12

	return &Model{
		configPath:         configPath,
		cfg:                cfg,
		client:             client,
		cache:              bCache,
		state:              stateLoading,
		statusMessage:      "Loading bank accounts...",
		inputs:             inputs,
		focusedInputIdx:    0,
		selectedAccountIdx: 0,
	}, nil
}

func (m *Model) Init() tea.Cmd {
	return fetchAccountsCmd(m.client, m.cfg.EnableBanking.SessionID, m.cfg.EnableBanking.BankName, m.cache)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.state != stateTransfer {
				return m, tea.Quit
			}
		case "esc":
			if m.state == stateAccountDetail || m.state == stateTransfer {
				m.state = stateAccounts
				m.err = nil
				m.showAbbreviationsHelp = false
				return m, nil
			} else if m.state == stateTransferResult {
				m.state = stateAccounts
				return m, nil
			}
		}

	case errorMsg:
		m.state = stateError
		m.err = msg
		return m, nil

	case accountsMsg:
		m.accounts = msg
		m.state = stateAccounts
		m.statusMessage = ""
		return m, nil

	case accountDetailMsg:
		if msg.err != nil {
			m.state = stateError
			m.err = msg.err
			return m, nil
		}
		m.balances = msg.balances
		m.transactions = msg.transactions
		m.state = stateAccountDetail
		m.statusMessage = ""
		return m, nil

	case transferSuccessMsg:
		m.transferLoading = false
		m.paymentResp = msg
		m.state = stateTransferResult
		return m, nil
	}

	// State-specific Updates
	switch m.state {
	case stateAccounts:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "up":
				if m.selectedAccountIdx > 0 {
					m.selectedAccountIdx--
				}
			case "down":
				if m.selectedAccountIdx < len(m.accounts)-1 {
					m.selectedAccountIdx++
				}
			case "enter":
				if len(m.accounts) > 0 {
					m.state = stateLoading
					m.statusMessage = "Loading account details (balances & transactions)..."
					m.showAbbreviationsHelp = false
					return m, fetchAccountDetailCmd(m.client, m.accounts[m.selectedAccountIdx].ID, m.cache)
				}
			}
		}

	case stateAccountDetail:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "h", "H":
				m.showAbbreviationsHelp = !m.showAbbreviationsHelp
			case "n", "ctrl+n": // New Transfer
				if len(m.accounts) > 0 {
					m.state = stateTransfer
					m.focusedInputIdx = 0
					m.inputs[0].Focus()
					m.inputs[1].Blur()
					m.inputs[2].Blur()
					m.inputs[0].SetValue("")
					m.inputs[1].SetValue("")
					m.inputs[2].SetValue("")
				}
			}
		}

	case stateTransfer:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			keyStr := keyMsg.String()

			// Check if pressing a number key (1-9) to select an account suggestion
			if len(keyStr) == 1 && keyStr >= "1" && keyStr <= "9" {
				idx, _ := strconv.Atoi(keyStr)
				idx = idx - 1 // 1-based to 0-based
				suggestions := m.getDestinationSuggestions()
				if idx >= 0 && idx < len(suggestions) {
					target := suggestions[idx]
					m.inputs[0].SetValue(target.IBAN)
					m.inputs[1].SetValue(target.Name)
					m.inputs[0].Blur()
					m.inputs[1].Blur()
					m.focusedInputIdx = 2
					m.inputs[2].Focus()
					return m, nil
				}
			}

			switch keyStr {
			case "tab", "down":
				m.inputs[m.focusedInputIdx].Blur()
				m.focusedInputIdx = (m.focusedInputIdx + 1) % len(m.inputs)
				m.inputs[m.focusedInputIdx].Focus()
				return m, nil
			case "shift+tab", "up":
				m.inputs[m.focusedInputIdx].Blur()
				m.focusedInputIdx = (m.focusedInputIdx - 1 + len(m.inputs)) % len(m.inputs)
				m.inputs[m.focusedInputIdx].Focus()
				return m, nil
			case "enter":
				if m.focusedInputIdx == len(m.inputs)-1 {
					// Submit transfer
					creditorIban := m.inputs[0].Value()
					creditorName := m.inputs[1].Value()
					amount := m.inputs[2].Value()

					if creditorIban == "" || creditorName == "" || amount == "" {
						m.err = fmt.Errorf("all fields are required")
						return m, nil
					}

					if _, err := strconv.ParseFloat(amount, 64); err != nil {
						m.err = fmt.Errorf("invalid amount format, must be decimal")
						return m, nil
					}

					m.err = nil
					m.transferLoading = true
					state := fmt.Sprintf("pay-%d", time.Now().UnixNano())
					debtorIban := m.accounts[m.selectedAccountIdx].IBAN

					return m, executeTransferCmd(m.client, debtorIban, creditorIban, creditorName, amount, state, m.cfg.EnableBanking.RedirectURL)
				} else {
					m.inputs[m.focusedInputIdx].Blur()
					m.focusedInputIdx = (m.focusedInputIdx + 1) % len(m.inputs)
					m.inputs[m.focusedInputIdx].Focus()
					return m, nil
				}
			}
		}

		// Update focused input
		m.inputs[m.focusedInputIdx], cmd = m.inputs[m.focusedInputIdx].Update(msg)
		return m, cmd

	case stateTransferResult:
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "o":
				if m.paymentResp != nil && m.paymentResp.URL != "" {
					_ = OpenBrowser(m.paymentResp.URL)
				}
			}
		}
	}

	return m, nil
}

func (m *Model) getDestinationSuggestions() []bank.Account {
	var suggestions []bank.Account
	if len(m.accounts) <= 1 {
		return suggestions
	}
	currentIban := m.accounts[m.selectedAccountIdx].IBAN
	for _, a := range m.accounts {
		if a.IBAN != currentIban {
			suggestions = append(suggestions, a)
		}
	}
	return suggestions
}

// Views Layouts

func (m *Model) View() string {
	s := ""

	// Top Title banner (Minimalist, modern banking style)
	s += titleStyle.Render("💳 PocketPay Mobile") + "  " + lipgloss.NewStyle().Foreground(grayColor).Render("v1.1.0") + "\n\n"

	if m.err != nil && m.state != stateTransfer {
		s += errorStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)) + "\n\n"
		s += helpStyle.Render("Press [Esc] to go back, [Q] to quit.")
		return s
	}

	switch m.state {
	case stateLoading:
		s += lipgloss.NewStyle().Foreground(accentColor).Render("⌛ "+m.statusMessage) + "\n"

	case stateAccounts:
		// 1. Calculate Combined Net Worth across all bank accounts
		totalAvailable := 0.0
		totalBooked := 0.0
		currency := "EUR"
		hasBalances := false

		for _, a := range m.accounts {
			if detail, ok := m.cache.GetDetail(context.Background(), a.ID); ok {
				hasBalances = true
				if a.Currency != "" {
					currency = a.Currency
				}
				if detail.Account.AvailableBalance != "" {
					val, _ := strconv.ParseFloat(detail.Account.AvailableBalance, 64)
					totalAvailable += val
				}
				if detail.Account.BookedBalance != "" {
					val, _ := strconv.ParseFloat(detail.Account.BookedBalance, 64)
					totalBooked += val
				}
			}
		}

		s += headerStyle.Render("👤 MY PORTFOLIO") + "\n"
		if hasBalances {
			s += lipgloss.NewStyle().Foreground(grayColor).Render("Combined Net Worth") + "\n"
			s += lipgloss.NewStyle().Bold(true).Foreground(successColor).Render(fmt.Sprintf("   €%.2f %s", totalAvailable, currency)) + "\n"
			s += lipgloss.NewStyle().Foreground(grayColor).Render(fmt.Sprintf("   (Booked Balance: €%.2f %s)", totalBooked, currency)) + "\n\n"
		} else {
			s += lipgloss.NewStyle().Foreground(grayColor).Render("Combined Net Worth") + "\n"
			s += lipgloss.NewStyle().Italic(true).Foreground(amberColor).Render("   € --.-- (Load an account once to fetch balances)") + "\n\n"
		}

		s += headerStyle.Render("Active Cards & Accounts") + "\n"
		s += helpStyle.Render("Select an account to view detailed transactions, balances, or transfer money.") + "\n\n"

		if len(m.accounts) == 0 {
			s += normalStyle.Render("No active bank accounts discovered in this session.") + "\n"
		} else {
			for i, a := range m.accounts {
				// Get cached balance if exists
				balStr := "--.--"
				if detail, ok := m.cache.GetDetail(context.Background(), a.ID); ok && detail.Account.AvailableBalance != "" {
					balStr = fmt.Sprintf("€%s", detail.Account.AvailableBalance)
				}

				cursor := "  "
				widgetStyle := lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(grayColor).
					Padding(0, 1).
					Width(60)

				if i == m.selectedAccountIdx {
					cursor = "👉"
					widgetStyle = widgetStyle.BorderForeground(accentColor).Bold(true)
				}

				nameStr := a.Name
				if nameStr == "" {
					nameStr = "Standard Checking"
				}

				cardContent := fmt.Sprintf("%s %-25s %18s\n   %-30s",
					cursor, nameStr, balStr,
					lipgloss.NewStyle().Foreground(grayColor).Render(a.IBAN),
				)

				s += widgetStyle.Render(cardContent) + "\n"
			}
		}
		s += "\n"
		s += helpStyle.Render("[Up/Down] Navigate Accounts  |  [Enter] Open Account Details  |  [Q] Quit")

	case stateAccountDetail:
		a := m.accounts[m.selectedAccountIdx]

		s += headerStyle.Render("💳 ACTIVE DEBIT CARD") + "\n"
		
		// Render virtual debit card
		balStr := "--.--"
		if a.AvailableBalance != "" {
			balStr = a.AvailableBalance
		} else if len(m.balances) > 0 {
			balStr = m.balances[0].Amount
		}

		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Background(lipgloss.Color("234")). // Dark virtual card background
			Padding(1, 2).
			Width(50).
			MarginBottom(1)

		cardContent := fmt.Sprintf(
			"🏦 %-38s\n\n"+
			"   %-38s\n"+
			"   %-38s\n\n"+
			"   Balance: %s %s",
			m.cfg.EnableBanking.BankName,
			a.Name,
			lipgloss.NewStyle().Foreground(grayColor).Render(a.IBAN),
			successStyle.Render("€"+balStr),
			a.Currency,
		)

		s += cardStyle.Render(cardContent) + "\n"

		// Balances Subsection
		s += successStyle.Render("💰 Balances") + "\n"
		if m.showAbbreviationsHelp {
			s += m.renderAbbreviationsHelp() + "\n"
		} else {
			if len(m.balances) == 0 {
				s += normalStyle.Render("   No balances found for this account.") + "\n\n"
			} else {
				tbl := table.New().
					Border(lipgloss.RoundedBorder()).
					BorderStyle(lipgloss.NewStyle().Foreground(accentColor)).
					Headers("Balance Type", "Amount")

				for _, bal := range m.balances {
					tbl.Row(bal.Name, bal.Amount+" "+a.Currency)
				}

				tbl.StyleFunc(func(row, col int) lipgloss.Style {
					if row <= 0 {
						return headerStyle.Padding(0, 1)
					}
					if col == 1 {
						return successStyle.Padding(0, 1)
					}
					return normalStyle.Padding(0, 1)
				})

				s += tbl.String() + "\n"
				s += tipStyle.Render("💡 Standardized banking abbreviations found in Type. Press [H] for a description guide.") + "\n\n"
			}
		}

		// Transactions Subsection
		s += successStyle.Render("📋 Recent Transactions") + "\n"
		if len(m.transactions) == 0 {
			s += normalStyle.Render("   No recent transactions found or accessible for this account.") + "\n\n"
		} else {
			tbl := table.New().
				Border(lipgloss.RoundedBorder()).
				BorderStyle(lipgloss.NewStyle().Foreground(accentColor)).
				Headers("Date", "Description", "Amount", "Status")

			limit := 10
			if len(m.transactions) < limit {
				limit = len(m.transactions)
			}
			for i := 0; i < limit; i++ {
				tx := m.transactions[i]
				tbl.Row(tx.Date, tx.Description, tx.Amount+" "+tx.Currency, tx.Status)
			}

			tbl.StyleFunc(func(row, col int) lipgloss.Style {
				txIdx := row - 1
				if row <= 0 {
					return headerStyle.Padding(0, 1)
				}
				if col == 2 {
					if txIdx >= 0 && txIdx < len(m.transactions) && m.transactions[txIdx].IsIncoming {
						return successStyle.Padding(0, 1)
					}
					return errorStyle.Padding(0, 1)
				}
				return normalStyle.Padding(0, 1)
			})

			s += tbl.String() + "\n\n"
		}

		s += helpStyle.Render("[Esc] Back to Overview  |  [N] New Transfer  |  [H] Toggle Help Guide  |  [Q] Quit")

	case stateTransfer:
		a := m.accounts[m.selectedAccountIdx]
		s += headerStyle.Render("Initiate Bank Transfer") + "\n"
		s += fmt.Sprintf("Source Account: %s (%s)\n\n", successStyle.Render(a.Name), a.IBAN)

		if m.err != nil {
			s += errorStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)) + "\n\n"
		}

		if m.transferLoading {
			s += lipgloss.NewStyle().Foreground(accentColor).Render("⌛ Initiating payment...") + "\n"
		} else {
			for i, input := range m.inputs {
				prompt := "  "
				if i == m.focusedInputIdx {
					prompt = "👉 "
				}
				s += fmt.Sprintf("%s%s\n", prompt, input.View()) + "\n"
			}
			s += "\n"

			// Shortcuts
			suggestions := m.getDestinationSuggestions()
			if len(suggestions) > 0 {
				s += tipStyle.Render("💡 Account Shortcuts (Press 1-9 to instantly pre-fill destination account):") + "\n"
				for idx, sug := range suggestions {
					if idx >= 9 {
						break
					}
					s += normalStyle.Render(fmt.Sprintf("   [%d] %s (%s)\n", idx+1, sug.Name, sug.IBAN))
				}
				s += "\n"
			}

			s += helpStyle.Render("[Tab/Down] Next Field  |  [Up] Prev Field  |  [Enter] Submit  |  [Esc] Cancel")
		}

	case stateTransferResult:
		s += headerStyle.Render("Transfer Result") + "\n\n"
		if m.paymentResp != nil {
			s += fmt.Sprintf("ID: %s\n", m.paymentResp.PaymentID)
			s += fmt.Sprintf("Status: %s\n\n", successStyle.Render(m.paymentResp.Status))

			if m.paymentResp.URL != "" {
				s += boxStyle.Render("⚠️  Strong Customer Authentication (SCA) Required!\n\nYou must authorize this transfer at your bank.") + "\n\n"
				s += helpStyle.Render("Press [O] to open the authorization link in your default browser.") + "\n\n"
			} else {
				s += "The payment is accepted or pending. Some banks submit automatically." + "\n\n"
			}
		}
		s += helpStyle.Render("[Esc] Back to Overview  |  [Q] Quit")
	}

	return s
}

func (m *Model) renderAbbreviationsHelp() string {
	helpText := "  📚 BALANCE TYPE ABBREVIATIONS GUIDE\n" +
		"  ──────────────────────────────────────────────────────────\n" +
		"  • CLBD : Closing Booked Balance\n" +
		"           Final balance at end of day, officially booked by bank.\n" +
		"  • ITBD : Interim Booked Balance\n" +
		"           Current booked balance, can change during the day.\n" +
		"  • OPBD : Opening Booked Balance\n" +
		"           Booked balance at start of the day.\n" +
		"  • XPBD : Expected Balance\n" +
		"           Includes pending transactions / authorized holds.\n" +
		"  • CLAV : Closing Available Balance\n" +
		"           Closing balance available for immediate withdrawal.\n" +
		"  • ITAV : Interim Available Balance\n" +
		"           Interim balance available for immediate use.\n" +
		"  ──────────────────────────────────────────────────────────\n" +
		"  Press [H] again to close."

	return boxStyle.Render(helpText)
}

func RunTUI(configPath string) error {
	model, err := NewModel(configPath)
	if err != nil {
		return err
	}

	p := tea.NewProgram(model)
	_, err = p.Run()
	return err
}
