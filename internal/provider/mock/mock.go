// Package mock provides an in-memory provider.Provider implementation for unit
// and integration tests. It serves canned data and supports error injection to
// exercise edge cases without touching a real bank API.
package mock

import (
	"context"
	"time"

	"github.com/ngoldack/fin-mcp/internal/bank"
)

// Provider is a configurable, in-memory bank provider for tests.
type Provider struct {
	NameValue string

	Status   bank.ConnectionStatus
	Accounts []bank.Account
	Balances map[string]bank.Balances
	Txns     map[string][]bank.Transaction

	// Error injection. When set, the matching method returns the error.
	VerifyErr   error
	ListErr     error
	BalancesErr error
	TxnsErr     error
	TransferErr error
	StatusErr   error
	SubmitErr   error

	// LastTransfer records the most recent InitiateTransfer request.
	LastTransfer bank.TransferRequest
}

// New returns a provider seeded with one account, balances, and a transaction.
func New() *Provider {
	return NewNamed("mock")
}

// NewNamed returns a seeded provider with a custom name.
func NewNamed(name string) *Provider {
	acc := bank.Account{ID: "acc-1", Name: "Main Checking", BankName: "Mock Bank", ConnectionName: "mock-conn", Country: "DE", Currency: "EUR", IBAN: "DE89370400440532013000"}
	return &Provider{
		NameValue: name,
		Status:    bank.ConnectionStatus{Authorized: true, Status: "AUTHORIZED", ConsentValidUntil: time.Now().Add(90 * 24 * time.Hour)},
		Accounts:  []bank.Account{acc},
		Balances: map[string]bank.Balances{
			"acc-1": {
				Items:     []bank.AccountBalance{{Name: "Available Balance", Amount: "120.45"}, {Name: "Booked Balance", Amount: "118.00"}},
				Available: "120.45",
				Booked:    "118.00",
			},
		},
		Txns: map[string][]bank.Transaction{
			"acc-1": {{ID: "tx-1", Date: "2026-06-14", Description: "To: Amazon", Amount: "-15.50", Currency: "EUR", Status: "Completed"}},
		},
	}
}

func (p *Provider) Name() string {
	if p.NameValue == "" {
		return "mock"
	}
	return p.NameValue
}

func (p *Provider) Info() bank.ProviderInfo {
	return bank.ProviderInfo{
		Name:        p.Name(),
		Environment: "MOCK",
		Connections: []bank.ConnectionInfo{
			{Name: "mock-conn", Bank: "Mock Bank", Country: "DE", ConsentValidUntil: p.Status.ConsentValidUntil},
		},
	}
}

func (p *Provider) VerifyConnection(context.Context) (bank.ConnectionStatus, error) {
	if p.VerifyErr != nil {
		return bank.ConnectionStatus{}, p.VerifyErr
	}
	return p.Status, nil
}

func (p *Provider) ListAccounts(context.Context) ([]bank.Account, error) {
	if p.ListErr != nil {
		return nil, p.ListErr
	}
	return p.Accounts, nil
}

func (p *Provider) GetBalances(_ context.Context, accountID string) (bank.Balances, error) {
	if p.BalancesErr != nil {
		return bank.Balances{}, p.BalancesErr
	}
	return p.Balances[accountID], nil
}

func (p *Provider) GetTransactions(_ context.Context, accountID, _, _ string) ([]bank.Transaction, error) {
	if p.TxnsErr != nil {
		return nil, p.TxnsErr
	}
	return p.Txns[accountID], nil
}

func (p *Provider) InitiateTransfer(_ context.Context, req bank.TransferRequest) (*bank.TransferResult, error) {
	p.LastTransfer = req
	if p.TransferErr != nil {
		return nil, p.TransferErr
	}
	return &bank.TransferResult{PaymentID: "mock-pay-1", Status: "PDNG", AuthURL: "https://mock.bank/sca"}, nil
}

func (p *Provider) PaymentStatus(_ context.Context, paymentID string) (*bank.TransferResult, error) {
	if p.StatusErr != nil {
		return nil, p.StatusErr
	}
	return &bank.TransferResult{PaymentID: paymentID, Status: "ACCP"}, nil
}

func (p *Provider) SubmitTransfer(_ context.Context, paymentID string) (*bank.TransferResult, error) {
	if p.SubmitErr != nil {
		return nil, p.SubmitErr
	}
	return &bank.TransferResult{PaymentID: paymentID, Status: "ACSC"}, nil
}
