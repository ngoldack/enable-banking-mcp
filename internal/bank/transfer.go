package bank

import "time"

// TransferRequest is a provider-agnostic payment initiation request.
type TransferRequest struct {
	DebtorIBAN   string
	CreditorIBAN string
	CreditorName string
	Amount       string
	Currency     string
	PaymentType  string // SEPA, INSTANT, DOMESTIC
}

// TransferResult is the outcome of a payment operation.
type TransferResult struct {
	PaymentID string
	Status    string
	AuthURL   string // SCA redirect URL, if the provider requires authorization
}

// Balances bundles an account's balance lines with the resolved primary amounts.
type Balances struct {
	Items     []AccountBalance
	Available string
	Booked    string
}

// ConnectionStatus reports the health of a provider connection/consent.
type ConnectionStatus struct {
	Authorized        bool
	Status            string
	ConsentValidUntil time.Time
}
