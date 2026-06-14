// Package provider defines the bank-provider port: a provider-agnostic interface
// the application (MCP server, TUI) depends on. Concrete providers (Enable
// Banking, mock) live in subpackages and adapt their APIs to this port.
package provider

import (
	"context"
	"fmt"

	"github.com/ngoldack/fin-mcp/internal/bank"
	"github.com/ngoldack/fin-mcp/internal/config"
	ebadapter "github.com/ngoldack/fin-mcp/internal/provider/enablebanking"
)

// Provider is a bank-connection port. All methods speak the domain types in the
// bank package so callers never depend on a specific provider's SDK.
type Provider interface {
	// Name identifies the provider (e.g. "enable-banking").
	Name() string
	// VerifyConnection checks session/consent health.
	VerifyConnection(ctx context.Context) (bank.ConnectionStatus, error)
	// ListAccounts returns all accounts reachable in the current session.
	ListAccounts(ctx context.Context) ([]bank.Account, error)
	// GetBalances returns the balance lines plus resolved primary amounts.
	GetBalances(ctx context.Context, accountID string) (bank.Balances, error)
	// GetTransactions returns transactions, optionally bounded by YYYY-MM-DD dates.
	GetTransactions(ctx context.Context, accountID, dateFrom, dateTo string) ([]bank.Transaction, error)
	// InitiateTransfer starts a payment; AuthURL may carry an SCA redirect.
	InitiateTransfer(ctx context.Context, req bank.TransferRequest) (*bank.TransferResult, error)
	// PaymentStatus returns the current status of a payment.
	PaymentStatus(ctx context.Context, paymentID string) (*bank.TransferResult, error)
	// SubmitTransfer executes a previously authorized deferred payment.
	SubmitTransfer(ctx context.Context, paymentID string) (*bank.TransferResult, error)
}

// Registry holds the providers connected to the application. Today only one is
// wired, but the type supports connecting several in the future.
type Registry struct {
	order     []string
	providers map[string]Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Add registers a provider under its Name().
func (r *Registry) Add(p Provider) {
	if _, ok := r.providers[p.Name()]; !ok {
		r.order = append(r.order, p.Name())
	}
	r.providers[p.Name()] = p
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Default returns the first registered provider.
func (r *Registry) Default() (Provider, bool) {
	if len(r.order) == 0 {
		return nil, false
	}
	return r.providers[r.order[0]], true
}

// All returns the providers in registration order.
func (r *Registry) All() []Provider {
	out := make([]Provider, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.providers[n])
	}
	return out
}

// FromConfig builds the registry from configuration. Currently it wires the
// Enable Banking provider; additional providers can be added here later.
func FromConfig(cfg *config.Config, configPath string) (*Registry, error) {
	reg := NewRegistry()
	eb, err := ebadapter.New(cfg, configPath)
	if err != nil {
		return nil, fmt.Errorf("init enable-banking provider: %w", err)
	}
	reg.Add(eb)
	return reg, nil
}
