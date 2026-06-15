// Package enablebanking adapts the Enable Banking SDK to the provider.Provider
// port. One adapter wraps a single Enable Banking application (app credentials)
// and may hold several connections — each connection is an authorized bank
// session exposing one or more accounts (e.g. C24, Revolut).
package enablebanking

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ngoldack/fin-mcp/internal/bank"
	"github.com/ngoldack/fin-mcp/internal/config"
	"github.com/ngoldack/fin-mcp/internal/secret"
	eb "github.com/ngoldack/fin-mcp/pkg/enablebanking"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Adapter implements provider.Provider on top of the Enable Banking SDK.
type Adapter struct {
	name    string
	client  eb.APIClient
	cfg     *config.EnableBankingConfig
	persist func() // saves the owning application config (e.g. refreshed consent)
}

// New builds the adapter and its SDK client from a provider config. If
// PrivateKeyKeyring is set, the PEM is read from the OS keychain (local only).
func New(name string, cfg *config.EnableBankingConfig, persist func()) (*Adapter, error) {
	if cfg == nil {
		return nil, fmt.Errorf("enable-banking provider %q: missing enable_banking config", name)
	}

	keyContent := cfg.PrivateKeyContent
	if cfg.PrivateKeyKeyring != "" {
		v, err := secret.Get(cfg.PrivateKeyKeyring)
		if err != nil {
			return nil, fmt.Errorf("read private key from keychain account %q: %w", cfg.PrivateKeyKeyring, err)
		}
		keyContent = v
	}

	// Instrument the outbound Enable Banking HTTP calls with OpenTelemetry. When
	// no telemetry provider is configured, otelhttp is a near-zero-cost no-op.
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method + " " + r.URL.Path
			}),
		),
	}

	client := eb.NewClient(cfg.AppID, cfg.PrivateKeyPath, keyContent, string(cfg.Environment), eb.WithHTTPClient(httpClient))
	return NewWithClient(name, client, cfg, persist), nil
}

// NewWithClient injects an SDK client (used in tests).
func NewWithClient(name string, client eb.APIClient, cfg *config.EnableBankingConfig, persist func()) *Adapter {
	if persist == nil {
		persist = func() {}
	}
	return &Adapter{name: name, client: client, cfg: cfg, persist: persist}
}

func (a *Adapter) Name() string { return a.name }

func (a *Adapter) Info() bank.ProviderInfo {
	conns := make([]bank.ConnectionInfo, 0, len(a.cfg.Connections))
	for _, c := range a.cfg.Connections {
		conns = append(conns, bank.ConnectionInfo{
			Name:              c.Name,
			Bank:              c.Bank,
			Country:           string(c.Country),
			ConsentValidUntil: c.ConsentValidUntil,
		})
	}
	return bank.ProviderInfo{
		Name:        a.name,
		Environment: string(a.cfg.Environment),
		Connections: conns,
	}
}

// VerifyConnection verifies every connection's session and refreshes consent
// timestamps. It reports an aggregate status (authorized if at least one
// connection is authorized).
func (a *Adapter) VerifyConnection(ctx context.Context) (bank.ConnectionStatus, error) {
	if len(a.cfg.Connections) == 0 {
		return bank.ConnectionStatus{}, fmt.Errorf("no connections configured; run setup to link a bank")
	}

	authorized := 0
	var earliest time.Time
	var lastErr error
	changed := false

	for i := range a.cfg.Connections {
		c := &a.cfg.Connections[i]
		sess, err := a.client.GetSession(ctx, c.SessionID)
		if err != nil {
			lastErr = fmt.Errorf("connection %q: %w", c.Name, err)
			continue
		}
		if !sess.Access.ValidUntil.IsZero() && !sess.Access.ValidUntil.Equal(c.ConsentValidUntil) {
			c.ConsentValidUntil = sess.Access.ValidUntil
			changed = true
		}
		if sess.Status == "AUTHORIZED" {
			authorized++
		} else {
			lastErr = fmt.Errorf("connection %q: status %s (expected AUTHORIZED)", c.Name, sess.Status)
		}
		if earliest.IsZero() || (!c.ConsentValidUntil.IsZero() && c.ConsentValidUntil.Before(earliest)) {
			earliest = c.ConsentValidUntil
		}
	}

	if changed {
		a.persist()
	}

	status := bank.ConnectionStatus{
		Authorized:        authorized > 0,
		Status:            fmt.Sprintf("%d/%d connections authorized", authorized, len(a.cfg.Connections)),
		ConsentValidUntil: earliest,
	}
	if authorized == 0 {
		if lastErr != nil {
			return status, lastErr
		}
		return status, fmt.Errorf("no connections are authorized; re-run setup")
	}
	return status, nil
}

// ListAccounts aggregates accounts across all connections; each account is
// tagged with the connection it came from.
func (a *Adapter) ListAccounts(ctx context.Context) ([]bank.Account, error) {
	if len(a.cfg.Connections) == 0 {
		return nil, fmt.Errorf("no connections configured; run setup to link a bank")
	}

	var accounts []bank.Account
	var lastErr error
	for _, c := range a.cfg.Connections {
		sess, err := a.client.GetSession(ctx, c.SessionID)
		if err != nil {
			lastErr = fmt.Errorf("connection %q: %w", c.Name, err)
			continue
		}
		for _, accID := range sess.Accounts {
			details, err := a.client.GetAccountDetails(ctx, accID)
			if err != nil {
				continue
			}
			accounts = append(accounts, mapAccount(*details, c))
		}
	}

	if len(accounts) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no accounts accessible across %d connection(s)", len(a.cfg.Connections))
	}
	return accounts, nil
}

func (a *Adapter) GetBalances(ctx context.Context, accountID string) (bank.Balances, error) {
	raw, err := a.client.GetBalances(ctx, accountID)
	if err != nil {
		return bank.Balances{}, err
	}
	items, available, booked := mapBalances(raw)
	return bank.Balances{Items: items, Available: available, Booked: booked}, nil
}

func (a *Adapter) GetTransactions(ctx context.Context, accountID, dateFrom, dateTo string) ([]bank.Transaction, error) {
	raw, err := a.client.GetTransactions(ctx, accountID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	return mapTransactions(raw), nil
}

func (a *Adapter) InitiateTransfer(ctx context.Context, req bank.TransferRequest) (*bank.TransferResult, error) {
	state := fmt.Sprintf("pay-%d", time.Now().UnixNano())
	resp, err := a.client.CreatePayment(
		ctx, req.DebtorIBAN, req.CreditorIBAN, req.CreditorName,
		req.Amount, string(req.Currency), req.PaymentType, state, a.cfg.RedirectURL,
	)
	if err != nil {
		return nil, err
	}
	return &bank.TransferResult{PaymentID: resp.PaymentID, Status: resp.Status, AuthURL: resp.URL}, nil
}

func (a *Adapter) PaymentStatus(ctx context.Context, paymentID string) (*bank.TransferResult, error) {
	p, err := a.client.GetPayment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	return &bank.TransferResult{PaymentID: p.PaymentID, Status: p.Status}, nil
}

func (a *Adapter) SubmitTransfer(ctx context.Context, paymentID string) (*bank.TransferResult, error) {
	r, err := a.client.SubmitPayment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	res := &bank.TransferResult{PaymentID: paymentID, Status: r.Status}
	if r.Message != "" && res.Status == "" {
		res.Status = r.Message
	}
	return res, nil
}
