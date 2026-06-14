package bank

import "time"

// TransferRequest is a provider-agnostic payment initiation request.
type TransferRequest struct {
	DebtorIBAN   string
	CreditorIBAN string
	CreditorName string
	Amount       string
	Currency     Currency
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

// ConnectionInfo is display metadata for one authorized bank link.
type ConnectionInfo struct {
	Name              string
	Bank              string
	Country           string
	ConsentValidUntil time.Time
}

// ProviderInfo is static, display-oriented metadata about a connected provider
// and its connections.
type ProviderInfo struct {
	Name        string
	Environment string
	Connections []ConnectionInfo
}
