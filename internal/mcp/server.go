package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ngoldack/fin-mcp/internal/bank"
	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/provider"
	"github.com/ngoldack/fin-mcp/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// serverVersion is reported in the MCP implementation handshake and as the
// OpenTelemetry service.version resource attribute.
const serverVersion = "1.0.0"

type MCPServer struct {
	configPath string
	config     *config.Config
	provider   provider.Provider
	cache      *bank.Cache
}

func NewMCPServer(configPath string) (*MCPServer, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	registry, err := provider.FromConfig(cfg, configPath)
	if err != nil {
		return nil, err
	}
	prov, ok := registry.Default()
	if !ok {
		return nil, fmt.Errorf("no bank provider configured")
	}

	ttl := time.Duration(cfg.MCP.CacheTTLMinutes) * time.Minute
	bCache := bank.NewCache(cfg.MCP.CachePath, ttl)

	return &MCPServer{
		configPath: configPath,
		config:     cfg,
		provider:   prov,
		cache:      bCache,
	}, nil
}

// Struct parameters for MCP Tools

type EmptyParams struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"Optional. If true, bypasses cache and fetches fresh data from the bank"`
}

type GetBalancesParams struct {
	AccountID string `json:"account_id" jsonschema:"The unique bank account ID (UUID)"`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"Optional. If true, bypasses cache and fetches fresh data"`
}

type GetTransactionsParams struct {
	AccountID string `json:"account_id" jsonschema:"The unique bank account ID (UUID)"`
	DateFrom  string `json:"date_from,omitempty" jsonschema:"Optional. Filter transactions starting from this date (format: YYYY-MM-DD)"`
	DateTo    string `json:"date_to,omitempty" jsonschema:"Optional. Filter transactions up to this date (format: YYYY-MM-DD)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Optional. Maximum number of transactions to return (default 50)"`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"Optional. If true, bypasses cache and fetches fresh data"`
}

type InitiateTransferParams struct {
	DebtorIban   string `json:"debtor_iban,omitempty" jsonschema:"Optional. The source bank account IBAN."`
	CreditorIban string `json:"creditor_iban" jsonschema:"The destination bank account IBAN"`
	CreditorName string `json:"creditor_name" jsonschema:"The name of the destination account owner or beneficiary"`
	Amount       string `json:"amount" jsonschema:"The transfer amount in EUR (e.g. '10.50')"`
	Currency     string `json:"currency,omitempty" jsonschema:"Optional. Currency code (defaults to 'EUR')"`
	PaymentType  string `json:"payment_type,omitempty" jsonschema:"Optional. Payment type (SEPA, INSTANT, DOMESTIC - defaults to 'SEPA')"`
}

type PaymentStatusParams struct {
	PaymentID string `json:"payment_id" jsonschema:"The unique payment ID returned by initiate-transfer"`
}

type SubmitTransferParams struct {
	PaymentID string `json:"payment_id" jsonschema:"The unique payment ID returned by initiate-transfer"`
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

func ptr[T any](v T) *T { return &v }

// validateTransfer performs semantic validation beyond required-field presence.
func validateTransfer(args InitiateTransferParams) error {
	iban := strings.ReplaceAll(strings.ToUpper(args.CreditorIban), " ", "")
	if len(iban) < 15 || len(iban) > 34 {
		return fmt.Errorf("creditor_iban %q is not a valid IBAN length (15-34)", args.CreditorIban)
	}
	for _, r := range iban {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("creditor_iban contains invalid characters")
		}
	}
	amt, err := strconv.ParseFloat(args.Amount, 64)
	if err != nil || amt <= 0 {
		return fmt.Errorf("amount %q must be a positive decimal", args.Amount)
	}
	return nil
}

// Formatting helpers for decoupled data

func formatAccountsMarkdown(accounts []bank.Account) string {
	s := "### 🏦 Bank Accounts Overview\n\n"
	s += "| Connection | Bank | Account Name | Account ID (UID) | IBAN | Currency |\n"
	s += "|---|---|---|---|---|---|\n"
	for _, a := range accounts {
		s += fmt.Sprintf("| %s | %s | **%s** | `%s` | `%s` | %s |\n", a.ConnectionName, a.BankName, a.Name, a.ID, a.IBAN, a.Currency)
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
	slog.DebugContext(ctx, "verifying provider connection", "provider", s.provider.Name())
	if _, err := s.provider.VerifyConnection(ctx); err != nil {
		return err
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

	slog.DebugContext(ctx, "cache miss for bank accounts list, fetching from provider")
	accounts, err := s.provider.ListAccounts(ctx)
	if err != nil {
		return nil, err
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

	slog.DebugContext(ctx, "cache miss for account balances, fetching from provider", "account_id", args.AccountID)
	balances, err := s.provider.GetBalances(ctx, args.AccountID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch balances", "account_id", args.AccountID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to fetch balances: %v", err))
	}

	// Fetch current details from cache or construct
	detail, _ := s.cache.GetDetail(ctx, args.AccountID)
	detail.Balances = balances.Items
	detail.Account.ID = args.AccountID
	detail.Account.AvailableBalance = balances.Available
	detail.Account.BookedBalance = balances.Booked

	slog.DebugContext(ctx, "updating cache with retrieved balances", "account_id", args.AccountID)
	s.cache.SetDetail(ctx, args.AccountID, detail)

	md := formatBalancesMarkdown(balances.Items)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: md},
		},
		StructuredContent: balances.Items,
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
			txs := capTransactions(detail.Transactions, args.Limit)
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{Text: formatTransactionsMarkdown(txs)},
				},
				StructuredContent: txs,
			}, nil, nil
		}
	}

	slog.DebugContext(ctx, "cache miss for transactions list, fetching from provider", "account_id", args.AccountID)
	domainTxs, err := s.provider.GetTransactions(ctx, args.AccountID, args.DateFrom, args.DateTo)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch transactions", "account_id", args.AccountID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to fetch transactions: %v", err))
	}

	// Only save to cache if this is a general fetch without filters
	if args.DateFrom == "" && args.DateTo == "" {
		detail, _ := s.cache.GetDetail(ctx, args.AccountID)
		detail.Account.ID = args.AccountID
		detail.Transactions = domainTxs
		slog.DebugContext(ctx, "updating cache with retrieved transactions list", "account_id", args.AccountID, "count", len(domainTxs))
		s.cache.SetDetail(ctx, args.AccountID, detail)
	}

	md := formatTransactionsMarkdown(capTransactions(domainTxs, args.Limit))
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: md},
		},
		StructuredContent: capTransactions(domainTxs, args.Limit),
	}, nil, nil
}

// capTransactions limits the returned slice (default 50).
func capTransactions(txs []bank.Transaction, limit int) []bank.Transaction {
	if limit <= 0 {
		limit = 50
	}
	if len(txs) > limit {
		return txs[:limit]
	}
	return txs
}

func (s *MCPServer) handleInitiateTransfer(ctx context.Context, req *mcp.CallToolRequest, args InitiateTransferParams) (*mcp.CallToolResult, any, error) {
	slog.InfoContext(ctx, "received tool call: initiate-transfer", "amount", args.Amount, "creditor_name", args.CreditorName)

	if err := validateTransfer(args); err != nil {
		return makeErrorResult(err.Error())
	}

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

	slog.InfoContext(ctx, "initiating payment via provider", "creditor_iban", args.CreditorIban, "amount", args.Amount)

	paymentResp, err := s.provider.InitiateTransfer(ctx, bank.TransferRequest{
		DebtorIBAN:   args.DebtorIban,
		CreditorIBAN: args.CreditorIban,
		CreditorName: args.CreditorName,
		Amount:       args.Amount,
		Currency:     bank.Currency(args.Currency),
		PaymentType:  args.PaymentType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to initiate transfer", "error", err)
		return makeErrorResult(fmt.Sprintf("failed to initiate transfer: %v", err))
	}

	slog.InfoContext(ctx, "payment successfully created", "payment_id", paymentResp.PaymentID, "status", paymentResp.Status)

	responseMsg := fmt.Sprintf("Transfer successfully initiated!\n\n"+
		"- Payment ID: %s\n"+
		"- Current Status: %s\n",
		paymentResp.PaymentID, paymentResp.Status)

	if paymentResp.AuthURL != "" {
		responseMsg += fmt.Sprintf("\n⚠️  **ACTION REQUIRED:** The user must authorize this transfer by navigating to this URL in a browser:\n"+
			"%s\n\n"+
			"Once the user completes authentication at their bank, they will be redirected. "+
			"Then, you should call `get-payment-status` to verify it is authorized (usually accepts status 'ACCC' or similar). "+
			"If the bank supports deferred execution, call `submit-transfer` with the Payment ID to execute the payment.",
			paymentResp.AuthURL)
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

	payment, err := s.provider.PaymentStatus(ctx, args.PaymentID)
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

	resp, err := s.provider.SubmitTransfer(ctx, args.PaymentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to submit payment for execution", "payment_id", args.PaymentID, "error", err)
		return makeErrorResult(fmt.Sprintf("failed to submit payment for execution: %v", err))
	}

	slog.InfoContext(ctx, "payment submitted successfully", "payment_id", args.PaymentID, "status", resp.Status)

	msg := "Payment successfully submitted for execution!"
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

// telemetryMiddleware records an OpenTelemetry span and request metrics for
// every received MCP method. For tool calls it enriches the span/metrics with
// the tool name. It is a no-op at runtime unless a telemetry provider is
// installed (see internal/telemetry).
func telemetryMiddleware() mcp.Middleware {
	const scope = "github.com/ngoldack/fin-mcp/internal/mcp"
	tracer := otel.Tracer(scope)
	meter := otel.Meter(scope)

	reqCount, _ := meter.Int64Counter(
		"mcp.server.requests",
		metric.WithDescription("Count of MCP requests handled by the server"),
	)
	reqDuration, _ := meter.Float64Histogram(
		"mcp.server.request.duration",
		metric.WithDescription("Duration of MCP requests handled by the server"),
		metric.WithUnit("s"),
	)

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			spanName := method
			attrs := []attribute.KeyValue{attribute.String("mcp.method", method)}
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && p != nil {
				spanName = method + " " + p.Name
				attrs = append(attrs, attribute.String("mcp.tool", p.Name))
			}

			ctx, span := tracer.Start(ctx, spanName, trace.WithAttributes(attrs...))
			start := time.Now()

			res, err := next(ctx, method, req)

			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				attrs = append(attrs, attribute.Bool("error", true))
			}
			span.End()

			measure := metric.WithAttributes(attrs...)
			reqCount.Add(ctx, 1, measure)
			reqDuration.Record(ctx, time.Since(start).Seconds(), measure)
			return res, err
		}
	}
}

func RunMCPServer(configPath string) error {
	server, err := NewMCPServer(configPath)
	if err != nil {
		return err
	}

	// 1. Configure and initialize structured logging (log/slog) directed to os.Stderr
	var level slog.Level
	switch strings.ToLower(string(server.config.MCP.LogLevel)) {
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
		Name:    "fin-mcp",
		Version: serverVersion,
	}, nil)

	// Record a span + request metrics for every received MCP method.
	mcpServer.AddReceivingMiddleware(telemetryMiddleware())

	// Register hyphenated standard tools from bank-mcp

	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list-accounts",
		Description: "Retrieve a list of all bank accounts accessible across the provider's connections.",
		Annotations: readOnly,
	}, server.handleListAccounts)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get-balances",
		Description: "Fetch the detailed booked and available balances of a specific bank account using its unique account ID.",
		Annotations: readOnly,
	}, server.handleGetBalances)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "list-transactions",
		Description: "Fetch transaction history of a specific bank account with optional date filters (YYYY-MM-DD) and a result limit.",
		Annotations: readOnly,
	}, server.handleListTransactions)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "initiate-transfer",
		Description: "Initiate a payment (to your own account or external IBAN, subject to the server's access mode). Returns an SCA authorization URL. Moves money — destructive.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptr(true), IdempotentHint: false, OpenWorldHint: ptr(true)},
	}, server.handleInitiateTransfer)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "get-payment-status",
		Description: "Query details and current status of an initiated payment.",
		Annotations: readOnly,
	}, server.handleGetPaymentStatus)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name:        "submit-transfer",
		Description: "Submit an authorized payment for execution. Moves money — destructive.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)},
	}, server.handleSubmitTransfer)

	// Signal-aware context: canceled on SIGINT/SIGTERM for a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize OpenTelemetry (no-op unless an OTLP endpoint is configured).
	shutdownTelemetry, err := telemetry.Setup(ctx, serverVersion)
	if err != nil {
		return fmt.Errorf("failed to initialize telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			slog.Warn("telemetry shutdown reported an error", "error", err)
		}
	}()

	if server.config.MCP.Transport == config.TransportSSE {
		return runSSE(ctx, mcpServer, server.config)
	}
	return runStdio(ctx, mcpServer)
}

// runStdio serves the MCP server over stdio. It returns cleanly when stdin
// closes (EOF) or the context is canceled by a termination signal.
func runStdio(ctx context.Context, mcpServer *mcp.Server) error {
	slog.InfoContext(ctx, "starting Enable Banking MCP Server over stdio")
	if err := mcpServer.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("stdio server error: %w", err)
	}
	slog.Info("MCP server stopped")
	return nil
}

// runSSE serves the MCP server over HTTP/SSE behind a bearer-token guard, with a
// graceful shutdown driven by the signal-aware context.
func runSSE(ctx context.Context, mcpServer *mcp.Server, cfg *config.Config) error {
	port := cfg.MCP.Port
	if port == 0 {
		port = 8090 // Default SSE port
	}

	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	mux := http.NewServeMux()
	mux.Handle("/", authMiddleware(handler, cfg.MCP.BearerToken))

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Serve until the server is shut down.
	g.Go(func() error {
		slog.InfoContext(gCtx, "starting Enable Banking MCP Server over HTTP/SSE", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server error: %w", err)
		}
		return nil
	})

	// Trigger a graceful shutdown when the context is canceled (signal).
	g.Go(func() error {
		<-gCtx.Done()
		slog.Info("shutting down HTTP server gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("MCP server runtime error: %w", err)
	}
	slog.Info("MCP server stopped")
	return nil
}
