package mcp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ngoldack/enable-banking-go/pkg/bank"
	"github.com/ngoldack/enable-banking-go/pkg/config"
	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPServer struct {
	configPath string
	config     *config.Config
	client     enablebanking.APIClient
	cache      *bank.Cache
}

func NewMCPServer(configPath string) (*MCPServer, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	client := enablebanking.NewClient(cfg.EnableBanking.AppID, cfg.EnableBanking.PrivateKeyPath, cfg.EnableBanking.PrivateKeyContent, cfg.EnableBanking.Environment)
	ttl := time.Duration(cfg.MCP.CacheTTLMinutes) * time.Minute
	bCache := bank.NewCache(".bank.db", ttl)

	return &MCPServer{
		configPath: configPath,
		config:     cfg,
		client:     client,
		cache:      bCache,
	}, nil
}

// Struct parameters for MCP Tools

type EmptyParams struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"description=Optional. If true, bypasses cache and fetches fresh data from the bank"`
}

type GetBalancesParams struct {
	AccountID string `json:"account_id" jsonschema:"description=The unique bank account ID (UUID)"`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"description=Optional. If true, bypasses cache and fetches fresh data"`
}

type GetTransactionsParams struct {
	AccountID string `json:"account_id" jsonschema:"description=The unique bank account ID (UUID)"`
	DateFrom  string `json:"date_from,omitempty" jsonschema:"description=Optional. Filter transactions starting from this date (format: YYYY-MM-DD)"`
	DateTo    string `json:"date_to,omitempty" jsonschema:"description=Optional. Filter transactions up to this date (format: YYYY-MM-DD)"`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"description=Optional. If true, bypasses cache and fetches fresh data"`
}

type InitiateTransferParams struct {
	DebtorIban   string `json:"debtor_iban,omitempty" jsonschema:"description=Optional. The source bank account IBAN."`
	CreditorIban string `json:"creditor_iban" jsonschema:"description=The destination bank account IBAN"`
	CreditorName string `json:"creditor_name" jsonschema:"description=The name of the destination account owner or beneficiary"`
	Amount       string `json:"amount" jsonschema:"description=The transfer amount in EUR (e.g. '10.50')"`
	Currency     string `json:"currency,omitempty" jsonschema:"description=Optional. Currency code (defaults to 'EUR')"`
	PaymentType  string `json:"payment_type,omitempty" jsonschema:"description=Optional. Payment type (SEPA, INSTANT, DOMESTIC - defaults to 'SEPA')"`
}

type PaymentStatusParams struct {
	PaymentID string `json:"payment_id" jsonschema:"description=The unique payment ID returned by initiate-transfer"`
}

type SubmitTransferParams struct {
	PaymentID string `json:"payment_id" jsonschema:"description=The unique payment ID returned by initiate-transfer"`
}

// Helpers for responses

func makeErrorResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Error: " + msg},
		},
	}, nil, nil
}

func makeSuccessResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}, nil, nil
}

// Formatting helpers for decoupled data

func formatAccountsMarkdown(accounts []bank.Account) string {
	s := "### 🏦 Bank Accounts Overview\n\n"
	s += "| Account Name | Account ID (UID) | IBAN | Currency |\n"
	s += "|---|---|---|---|\n"
	for _, a := range accounts {
		s += fmt.Sprintf("| **%s** | `%s` | `%s` | %s |\n", a.Name, a.ID, a.IBAN, a.Currency)
	}
	return s
}

func formatBalancesMarkdown(balances []bank.AccountBalance) string {
	s := "### 📊 Account Balances\n\n"
	s += "| Balance Type | Amount |\n"
	s += "|---|---|\n"
	for _, b := range balances {
		s += fmt.Sprintf("| %s | **%s** |\n", b.Name, b.Amount)
	}
	return s
}

func formatTransactionsMarkdown(txs []bank.Transaction) string {
	s := "### 📋 Recent Transactions\n\n"
	s += "| Date | Description | Amount | Status |\n"
	s += "|---|---|---|---|\n"
	for _, tx := range txs {
		s += fmt.Sprintf("| %s | %s | **%s** | %s |\n", tx.Date, tx.Description, tx.Amount, tx.Status)
	}
	return s
}

func (s *MCPServer) checkSessionValid(ctx context.Context) error {
	if s.config.EnableBanking.SessionID == "" {
		return fmt.Errorf("no active bank session found. Please run the TUI or setup to link your bank account first")
	}

	if !s.config.IsSessionValid() {
		return fmt.Errorf("the bank session consent has expired. Please run the setup to re-authenticate and renew consent")
	}

	sess, err := s.client.GetSession(ctx, s.config.EnableBanking.SessionID)
	if err != nil {
		return fmt.Errorf("failed to verify bank session: %w. Your session may have been invalidated", err)
	}

	if sess.Status != "AUTHORIZED" {
		return fmt.Errorf("bank session status is %s, expected AUTHORIZED. Please run the setup again to refresh", sess.Status)
	}

	if !sess.Access.ValidUntil.IsZero() && !sess.Access.ValidUntil.Equal(s.config.EnableBanking.ConsentValidUntil) {
		s.config.EnableBanking.ConsentValidUntil = sess.Access.ValidUntil
		_ = config.SaveConfig(s.configPath, s.config)
	}

	return nil
}

func (s *MCPServer) getAccountsList(ctx context.Context, refresh bool) ([]bank.Account, error) {
	if !refresh {
		if accounts, ok := s.cache.GetAccounts(ctx); ok {
			return accounts, nil
		}
	}

	sess, err := s.client.GetSession(ctx, s.config.EnableBanking.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve session details: %w", err)
	}

	var accounts []bank.Account
	for _, accID := range sess.Accounts {
		accDetails, err := s.client.GetAccountDetails(ctx, accID)
		if err != nil {
			continue
		}
		accounts = append(accounts, bank.MapAccountToDomain(*accDetails, s.config.EnableBanking.BankName))
	}

	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts linked or accessible in this session")
	}

	s.cache.SetAccounts(ctx, accounts)
	return accounts, nil
}

func (s *MCPServer) handleListAccounts(ctx context.Context, req *mcp.CallToolRequest, args EmptyParams) (*mcp.CallToolResult, any, error) {
	if err := s.checkSessionValid(ctx); err != nil {
		return makeErrorResult(err.Error())
	}

	accounts, err := s.getAccountsList(ctx, args.Refresh)
	if err != nil {
		return makeErrorResult(err.Error())
	}

	md := formatAccountsMarkdown(accounts)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: md},
		},
		StructuredContent: accounts,
	}, nil, nil
}

func (s *MCPServer) handleGetBalances(ctx context.Context, req *mcp.CallToolRequest, args GetBalancesParams) (*mcp.CallToolResult, any, error) {
	if err := s.checkSessionValid(ctx); err != nil {
		return makeErrorResult(err.Error())
	}

	if args.AccountID == "" {
		return makeErrorResult("account_id is required")
	}

	// Try loading from cache first
	if !args.Refresh {
		if detail, ok := s.cache.GetDetail(ctx, args.AccountID); ok && len(detail.Balances) > 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: formatBalancesMarkdown(detail.Balances)},
				},
				StructuredContent: detail.Balances,
			}, nil, nil
		}
	}

	balances, err := s.client.GetBalances(ctx, args.AccountID)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("failed to fetch balances: %v", err))
	}

	domainBals, available, booked := bank.MapBalancesToDomain(balances)

	// Fetch current details from cache or construct
	detail, _ := s.cache.GetDetail(ctx, args.AccountID)
	detail.Balances = domainBals
	detail.Account.ID = args.AccountID
	detail.Account.AvailableBalance = available
	detail.Account.BookedBalance = booked

	s.cache.SetDetail(ctx, args.AccountID, detail)

	md := formatBalancesMarkdown(domainBals)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: md},
		},
		StructuredContent: domainBals,
	}, nil, nil
}

func (s *MCPServer) handleListTransactions(ctx context.Context, req *mcp.CallToolRequest, args GetTransactionsParams) (*mcp.CallToolResult, any, error) {
	if err := s.checkSessionValid(ctx); err != nil {
		return makeErrorResult(err.Error())
	}

	if args.AccountID == "" {
		return makeErrorResult("account_id is required")
	}

	for _, dt := range []string{args.DateFrom, args.DateTo} {
		if dt != "" {
			if _, err := time.Parse("2006-01-02", dt); err != nil {
				return makeErrorResult(fmt.Sprintf("invalid date format '%s'. Must be YYYY-MM-DD", dt))
			}
		}
	}

	// Try loading from cache first (if no dynamic date filter is applied)
	if !args.Refresh && args.DateFrom == "" && args.DateTo == "" {
		if detail, ok := s.cache.GetDetail(ctx, args.AccountID); ok && len(detail.Transactions) > 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: formatTransactionsMarkdown(detail.Transactions)},
				},
				StructuredContent: detail.Transactions,
			}, nil, nil
		}
	}

	txs, err := s.client.GetTransactions(ctx, args.AccountID, args.DateFrom, args.DateTo)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("failed to fetch transactions: %v", err))
	}

	domainTxs := bank.MapTransactionsToDomain(txs)

	// Only save to cache if this is a general fetch without filters
	if args.DateFrom == "" && args.DateTo == "" {
		detail, _ := s.cache.GetDetail(ctx, args.AccountID)
		detail.Account.ID = args.AccountID
		detail.Transactions = domainTxs
		s.cache.SetDetail(ctx, args.AccountID, detail)
	}

	md := formatTransactionsMarkdown(domainTxs)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: md},
		},
		StructuredContent: domainTxs,
	}, nil, nil
}

func (s *MCPServer) handleInitiateTransfer(ctx context.Context, req *mcp.CallToolRequest, args InitiateTransferParams) (*mcp.CallToolResult, any, error) {
	// 1. Access Control Verification
	if s.config.MCP.AccessMode == config.ReadOnly {
		return makeErrorResult("Access Denied: The MCP server is running in 'ReadOnly' mode. All payment transfers are strictly disabled.")
	}

	if s.config.MCP.AccessMode == config.InternalOnly {
		// Fetch the user's accounts to verify if CreditorIban matches any owned account
		accounts, err := s.getAccountsList(ctx, false)
		if err != nil {
			return makeErrorResult(fmt.Sprintf("Access Denied: 'InternalOnly' mode verification failed to fetch owned accounts: %v", err))
		}

		matched := false
		cleanCreditorIban := strings.ReplaceAll(strings.ToUpper(args.CreditorIban), " ", "")
		for _, a := range accounts {
			cleanOwnedIban := strings.ReplaceAll(strings.ToUpper(a.IBAN), " ", "")
			if cleanCreditorIban == cleanOwnedIban {
				matched = true
				break
			}
		}

		if !matched {
			return makeErrorResult("Access Denied: The MCP server is running in 'InternalOnly' mode. Transfers are strictly restricted to your own linked bank accounts.")
		}
	}

	state := fmt.Sprintf("pay-%d", time.Now().UnixNano())

	paymentResp, err := s.client.CreatePayment(
		ctx,
		args.DebtorIban,
		args.CreditorIban,
		args.CreditorName,
		args.Amount,
		args.Currency,
		args.PaymentType,
		state,
		s.config.EnableBanking.RedirectURL,
	)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("failed to initiate transfer: %v", err))
	}

	responseMsg := fmt.Sprintf("Transfer successfully initiated!\n\n"+
		"- Payment ID: %s\n"+
		"- Current Status: %s\n",
		paymentResp.PaymentID, paymentResp.Status)

	if paymentResp.URL != "" {
		responseMsg += fmt.Sprintf("\n⚠️  **ACTION REQUIRED:** The user must authorize this transfer by navigating to this URL in a browser:\n"+
			"%s\n\n"+
			"Once the user completes authentication at their bank, they will be redirected. "+
			"Then, you should call `get-payment-status` to verify it is authorized (usually accepts status 'ACCC' or similar). "+
			"If the bank supports deferred execution, call `submit-transfer` with the Payment ID to execute the payment.",
			paymentResp.URL)
	} else {
		responseMsg += "\nNo authorization URL returned. Please check `get-payment-status` to see if the payment is already accepted or pending."
	}

	return makeSuccessResult(responseMsg)
}

func (s *MCPServer) handleGetPaymentStatus(ctx context.Context, req *mcp.CallToolRequest, args PaymentStatusParams) (*mcp.CallToolResult, any, error) {
	if args.PaymentID == "" {
		return makeErrorResult("payment_id is required")
	}

	payment, err := s.client.GetPayment(ctx, args.PaymentID)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("failed to fetch payment: %v", err))
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("ID: %s, Status: %s", payment.PaymentID, payment.Status)},
		},
		StructuredContent: payment,
	}, nil, nil
}

func (s *MCPServer) handleSubmitTransfer(ctx context.Context, req *mcp.CallToolRequest, args SubmitTransferParams) (*mcp.CallToolResult, any, error) {
	if args.PaymentID == "" {
		return makeErrorResult("payment_id is required")
	}

	resp, err := s.client.SubmitPayment(ctx, args.PaymentID)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("failed to submit payment for execution: %v", err))
	}

	msg := "Payment successfully submitted for execution!"
	if resp.Message != "" {
		msg += "\nDetails: " + resp.Message
	}
	if resp.Status != "" {
		msg += "\nNew Status: " + resp.Status
	}

	return makeSuccessResult(msg)
}

func authMiddleware(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			if strings.HasPrefix(authHeader, "Bearer ") {
				reqToken := strings.TrimPrefix(authHeader, "Bearer ")
				if reqToken == token {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		reqToken := r.URL.Query().Get("token")
		if reqToken == token {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "Unauthorized: Invalid or missing bearer token", http.StatusUnauthorized)
	})
}

func RunMCPServer(configPath string) error {
	log.SetOutput(os.Stderr)

	server, err := NewMCPServer(configPath)
	if err != nil {
		return err
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "enable-banking-mcp",
		Version: "1.0.0",
	}, nil)

	// Register hyphenated standard tools from bank-mcp

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list-accounts",
		Description: "Retrieve a list of all bank accounts accessible via the authorized session.",
	}, server.handleListAccounts)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get-balances",
		Description: "Fetch the detailed booked and available balances of a specific bank account using its unique account ID.",
	}, server.handleGetBalances)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list-transactions",
		Description: "Fetch transaction history of a specific bank account with optional date filters (YYYY-MM-DD).",
	}, server.handleListTransactions)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "initiate-transfer",
		Description: "Initiate a transfer or payment (either to another of your own accounts or to an external IBAN depending on security access modes). Returns a redirect URL for SCA.",
	}, server.handleInitiateTransfer)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get-payment-status",
		Description: "Query details and current status of an initiated payment.",
	}, server.handleGetPaymentStatus)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "submit-transfer",
		Description: "Submit an authorized payment for execution.",
	}, server.handleSubmitTransfer)

	// Determine Transport Type
	if server.config.MCP.Transport == config.TransportSSE {
		port := server.config.MCP.Port
		if port == 0 {
			port = 8090 // Default SSE port
		}

		handler := mcp.NewSSEHandler(func(req *http.Request) *mcp.Server {
			return mcpServer
		}, nil)

		mux := http.NewServeMux()
		secureHandler := authMiddleware(handler, server.config.MCP.BearerToken)
		mux.Handle("/", secureHandler)

		addr := fmt.Sprintf(":%d", port)
		log.Printf("Starting Enable Banking MCP Server over HTTPS/SSE on %s...", addr)
		
		httpServer := &http.Server{
			Addr:    addr,
			Handler: mux,
		}
		return httpServer.ListenAndServe()
	}

	log.Printf("Starting Enable Banking MCP Server over Stdio...")
	if err := mcpServer.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("MCP Server runtime error: %w", err)
	}

	return nil
}
