package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ngoldack/enable-banking-go/pkg/bank"
	"github.com/ngoldack/enable-banking-go/pkg/config"
	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"
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
	Refresh bool `json:"refresh,omitempty" jsonschema:",description=Optional. If true, bypasses cache and fetches fresh data from the bank"`
}

type GetBalancesParams struct {
	AccountID string `json:"account_id" jsonschema:",description=The unique bank account ID (UUID)"`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:",description=Optional. If true, bypasses cache and fetches fresh data"`
}

type GetTransactionsParams struct {
	AccountID string `json:"account_id" jsonschema:",description=The unique bank account ID (UUID)"`
	DateFrom  string `json:"date_from,omitempty" jsonschema:",description=Optional. Filter transactions starting from this date (format: YYYY-MM-DD)"`
	DateTo    string `json:"date_to,omitempty" jsonschema:",description=Optional. Filter transactions up to this date (format: YYYY-MM-DD)"`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:",description=Optional. If true, bypasses cache and fetches fresh data"`
}

type InitiateTransferParams struct {
	DebtorIban   string `json:"debtor_iban,omitempty" jsonschema:",description=Optional. The source bank account IBAN."`
	CreditorIban string `json:"creditor_iban" jsonschema:",description=The destination bank account IBAN"`
	CreditorName string `json:"creditor_name" jsonschema:",description=The name of the destination account owner or beneficiary"`
	Amount       string `json:"amount" jsonschema:",description=The transfer amount in EUR (e.g. '10.50')"`
	Currency     string `json:"currency,omitempty" jsonschema:",description=Optional. Currency code (defaults to 'EUR')"`
	PaymentType  string `json:"payment_type,omitempty" jsonschema:",description=Optional. Payment type (SEPA, INSTANT, DOMESTIC - defaults to 'SEPA')"`
}

type PaymentStatusParams struct {
	PaymentID string `json:"payment_id" jsonschema:",description=The unique payment ID returned by initiate-transfer"`
}

type SubmitTransferParams struct {
	PaymentID string `json:"payment_id" jsonschema:",description=The unique payment ID returned by initiate-transfer"`
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
	slog.DebugContext(ctx, "checking bank session validity", "session_id", s.config.EnableBanking.SessionID)

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
		slog.InfoContext(ctx, "updating bank session consent expiration date", "new_date", sess.Access.ValidUntil)
		s.config.EnableBanking.ConsentValidUntil = sess.Access.ValidUntil
		_ = config.SaveConfig(s.configPath, s.config)
	}

	return nil
}

func (s *MCPServer) getAccountsList(ctx context.Context, refresh bool) ([]bank.Account, error) {
	if !refresh {
		if accounts, ok := s.cache.GetAccounts(ctx); ok {
			slog.DebugContext(ctx, "cache hit for bank accounts list", "count", len(accounts))
			return accounts, nil
		}
	}

	slog.DebugContext(ctx, "cache miss for bank accounts list, fetching fresh details from API")
	sess, err := s.client.GetSession(ctx, s.config.EnableBanking.SessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve session details: %w", err)
	}

	var accounts []bank.Account
	for _, accID := range sess.Accounts {
		accDetails, err := s.client.GetAccountDetails(ctx, accID)
		if err != nil {
			slog.WarnContext(ctx, "failed to fetch details for bank account", "account_id", accID, "error", err)
			continue
		}
		accounts = append(accounts, bank.MapAccountToDomain(*accDetails, s.config.EnableBanking.BankName))
	}

	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts linked or accessible in this session")
	}

	slog.DebugContext(ctx, "saving retrieved accounts list to database cache", "count", len(accounts))
	s.cache.SetAccounts(ctx, accounts)
	return accounts, nil
}

func (s *MCPServer) handleListAccounts(ctx context.Context, req *mcp.CallToolRequest, args EmptyParams) (*mcp.CallToolResult, any, error) {
	slog.InfoContext(ctx, "received tool call: list-accounts", "refresh", args.Refresh)

	if err := s.checkSessionValid(ctx); err != nil {
		slog.ErrorContext(ctx, "session verification failed", "error", err)
		return makeErrorResult(err.Error())
	}

	accounts, err := s.getAccountsList(ctx, args.Refresh)
	if err != nil {
		slog.ErrorContext(ctx, "failed to retrieve bank accounts list", "error", err)
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
	slog.InfoContext(ctx, "received tool call: get-balances", "account_id", args.AccountID, "refresh", args.Refresh)

	if err := s.checkSessionValid(ctx); err != nil {
		slog.ErrorContext(ctx, "session verification failed", "error", err)
		return makeErrorResult(err.Error())
	}

	if args.AccountID == "" {
		return makeErrorResult("account_id is required")
	}

	// Try loading from cache first
	if !args.Refresh {
		if detail, ok := s.cache.GetDetail(ctx, args.AccountID); ok && len(detail.Balances) > 0 {
			slog.DebugContext(ctx, "cache hit for account balances", "account_id", args.AccountID)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: formatBalancesMarkdown(detail.Balances)},
				},
				StructuredContent: detail.Balances,
			}, nil, nil
		}
	}

	slog.DebugContext(ctx, "cache miss for account balances, fetching fresh from API", "account_id", args.AccountID)
	balances, err := s.client.GetBalances(ctx, args.AccountID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch balances from API", "account_id", args.AccountID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to fetch balances: %v", err))
	}

	domainBals, available, booked := bank.MapBalancesToDomain(balances)

	// Fetch current details from cache or construct
	detail, _ := s.cache.GetDetail(ctx, args.AccountID)
	detail.Balances = domainBals
	detail.Account.ID = args.AccountID
	detail.Account.AvailableBalance = available
	detail.Account.BookedBalance = booked

	slog.DebugContext(ctx, "updating cache with retrieved balances", "account_id", args.AccountID)
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
	slog.InfoContext(ctx, "received tool call: list-transactions", "account_id", args.AccountID, "date_from", args.DateFrom, "date_to", args.DateTo, "refresh", args.Refresh)

	if err := s.checkSessionValid(ctx); err != nil {
		slog.ErrorContext(ctx, "session verification failed", "error", err)
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
			slog.DebugContext(ctx, "cache hit for transactions list", "account_id", args.AccountID, "count", len(detail.Transactions))
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: formatTransactionsMarkdown(detail.Transactions)},
				},
				StructuredContent: detail.Transactions,
			}, nil, nil
		}
	}

	slog.DebugContext(ctx, "cache miss for transactions list, fetching fresh from API", "account_id", args.AccountID)
	txs, err := s.client.GetTransactions(ctx, args.AccountID, args.DateFrom, args.DateTo)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch transactions from API", "account_id", args.AccountID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to fetch transactions: %v", err))
	}

	domainTxs := bank.MapTransactionsToDomain(txs)

	// Only save to cache if this is a general fetch without filters
	if args.DateFrom == "" && args.DateTo == "" {
		detail, _ := s.cache.GetDetail(ctx, args.AccountID)
		detail.Account.ID = args.AccountID
		detail.Transactions = domainTxs
		slog.DebugContext(ctx, "updating cache with retrieved transactions list", "account_id", args.AccountID, "count", len(domainTxs))
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
	slog.InfoContext(ctx, "received tool call: initiate-transfer", "amount", args.Amount, "creditor_name", args.CreditorName)

	// 1. Access Control Verification
	if s.config.MCP.AccessMode == config.ReadOnly {
		slog.WarnContext(ctx, "payment transfer rejected: server is running in ReadOnly mode")
		return makeErrorResult("Access Denied: The MCP server is running in 'ReadOnly' mode. All payment transfers are strictly disabled.")
	}

	if s.config.MCP.AccessMode == config.InternalOnly {
		slog.DebugContext(ctx, "verifying ownership of destination account for InternalOnly mode")
		// Fetch the user's accounts to verify if CreditorIban matches any owned account
		accounts, err := s.getAccountsList(ctx, false)
		if err != nil {
			slog.ErrorContext(ctx, "failed to fetch owned accounts list for security verification", "error", err)
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
			slog.WarnContext(ctx, "payment transfer rejected: destination account is not owned by the user", "creditor_iban", args.CreditorIban)
			return makeErrorResult("Access Denied: The MCP server is running in 'InternalOnly' mode. Transfers are strictly restricted to your own linked bank accounts.")
		}
	}

	slog.InfoContext(ctx, "creating payment on bank API", "creditor_iban", args.CreditorIban, "amount", args.Amount)
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
		slog.ErrorContext(ctx, "failed to create payment on bank API", "error", err)
		return makeErrorResult(fmt.Sprintf("failed to initiate transfer: %v", err))
	}

	slog.InfoContext(ctx, "payment successfully created", "payment_id", paymentResp.PaymentID, "status", paymentResp.Status)

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
	slog.InfoContext(ctx, "received tool call: get-payment-status", "payment_id", args.PaymentID)

	if args.PaymentID == "" {
		return makeErrorResult("payment_id is required")
	}

	payment, err := s.client.GetPayment(ctx, args.PaymentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to retrieve payment status", "payment_id", args.PaymentID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to fetch payment: %v", err))
	}

	slog.InfoContext(ctx, "payment status retrieved", "payment_id", args.PaymentID, "status", payment.Status)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("ID: %s, Status: %s", payment.PaymentID, payment.Status)},
		},
		StructuredContent: payment,
	}, nil, nil
}

func (s *MCPServer) handleSubmitTransfer(ctx context.Context, req *mcp.CallToolRequest, args SubmitTransferParams) (*mcp.CallToolResult, any, error) {
	slog.InfoContext(ctx, "received tool call: submit-transfer", "payment_id", args.PaymentID)

	if args.PaymentID == "" {
		return makeErrorResult("payment_id is required")
	}

	resp, err := s.client.SubmitPayment(ctx, args.PaymentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to submit payment for execution", "payment_id", args.PaymentID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to submit payment for execution: %v", err))
	}

	slog.InfoContext(ctx, "payment submitted successfully", "payment_id", args.PaymentID, "status", resp.Status)

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

		slog.Warn("unauthorized request blocked inside HTTP middleware")
		http.Error(w, "Unauthorized: Invalid or missing bearer token", http.StatusUnauthorized)
	})
}

func RunMCPServer(configPath string) error {
	server, err := NewMCPServer(configPath)
	if err != nil {
		return err
	}

	// 1. Configure and initialize structured logging (log/slog) directed to os.Stderr
	var level slog.Level
	switch strings.ToLower(server.config.MCP.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if server.config.MCP.LogFormat == config.LogFormatJSON {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

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

	// 2. Setup Signal-aware Graceful context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	g, gCtx := errgroup.WithContext(ctx)

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
		
		httpServer := &http.Server{
			Addr:    addr,
			Handler: mux,
		}

		// Group goroutine 1: Run HTTP server
		g.Go(func() error {
			slog.InfoContext(gCtx, "starting Enable Banking MCP Server over HTTPS/SSE", "addr", addr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("HTTP server ListenAndServe error: %w", err)
			}
			return nil
		})

		// Group goroutine 2: Monitor context cancellation for graceful shutdown
		g.Go(func() error {
			<-gCtx.Done()
			slog.Info("shutting down HTTP server gracefully...")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return httpServer.Shutdown(shutdownCtx)
		})

		// Wait for goroutines to complete
		if err := g.Wait(); err != nil {
			return fmt.Errorf("MCP server runtime error: %w", err)
		}
		
		slog.Info("MCP server successfully stopped.")
		return nil
	}

	// Stdio Transport
	slog.InfoContext(gCtx, "starting Enable Banking MCP Server over Stdio")

	// Group goroutine 1: Run Stdio server
	g.Go(func() error {
		if err := mcpServer.Run(gCtx, &mcp.StdioTransport{}); err != nil {
			return fmt.Errorf("Stdio server run error: %w", err)
		}
		return nil
	})

	// Group goroutine 2: Monitor signal cancellation for Stdio
	g.Go(func() error {
		<-gCtx.Done()
		slog.Info("shutting down Stdio server gracefully...")
		return nil
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("MCP server runtime error: %w", err)
	}

	slog.Info("MCP server successfully stopped.")
	return nil
}
