package enablebanking

import "time"

type ErrorResponse struct {
	Code             string `json:"code,omitempty"`
	Message          string `json:"message,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type ASPSP struct {
	Name                   string `json:"name"`
	Country                string `json:"country"`
	Bic                    string `json:"bic,omitempty"`
	MaximumConsentValidity int64  `json:"maximum_consent_validity,omitempty"`
}

type GetAspspsResponse struct {
	ASPSPs []ASPSP `json:"aspsps"`
}

type Access struct {
	Balances     bool   `json:"balances"`
	Transactions bool   `json:"transactions"`
	ValidUntil   string `json:"valid_until"`
}

type StartAuthorizationRequest struct {
	Access      Access `json:"access"`
	ASPSP       ASPSP  `json:"aspsp"`
	State       string `json:"state"`
	RedirectURL string `json:"redirect_url"`
}

type StartAuthorizationResponse struct {
	URL             string `json:"url"`
	AuthorizationID string `json:"authorization_id"`
}

type AuthorizeSessionRequest struct {
	Code string `json:"code"`
}

type AccountIdentification struct {
	Iban           string `json:"iban,omitempty"`
	BBan           string `json:"bban,omitempty"`
	Identification string `json:"identification,omitempty"`
	SchemeName     string `json:"scheme_name,omitempty"`
}

type AccountResource struct {
	AccountID       AccountIdentification `json:"account_id"`
	Name            string                `json:"name,omitempty"`
	Details         string                `json:"details,omitempty"`
	Usage           string                `json:"usage,omitempty"` // e.g. "PRIV" or "ORGA"
	Currency        string                `json:"currency,omitempty"`
	Uid             string                `json:"uid"`
	Product         string                `json:"product,omitempty"`
	CashAccountType string                `json:"cash_account_type,omitempty"`
}

type AuthorizeSessionResponse struct {
	SessionID string            `json:"session_id"`
	Accounts  []AccountResource `json:"accounts"`
	ASPSP     ASPSP             `json:"aspsp"`
	Access    struct {
		ValidUntil time.Time `json:"valid_until"`
	} `json:"access"`
}

type GetSessionResponse struct {
	SessionID string            `json:"session_id"`
	Status    string            `json:"status"` // "AUTHORIZED", "EXPIRED", etc.
	Accounts  []string          `json:"accounts"`
	Aspsp     ASPSP             `json:"aspsp"`
	Access    struct {
		ValidUntil time.Time `json:"valid_until"`
	} `json:"access"`
}

type AmountType struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type BalanceResource struct {
	Name               string     `json:"name"`
	BalanceAmount      AmountType `json:"balance_amount"`
	BalanceType        string     `json:"balance_type"` // e.g. "CLAV"
	LastChangeDateTime string     `json:"last_change_date_time,omitempty"`
	ReferenceDate      string     `json:"reference_date,omitempty"`
}

type HalBalances struct {
	Balances []BalanceResource `json:"balances"`
}

type PartyIdentification struct {
	Name string `json:"name,omitempty"`
}

type Transaction struct {
	TransactionID        string                 `json:"transaction_id,omitempty"`
	EntryReference       string                 `json:"entry_reference,omitempty"`
	TransactionAmount    AmountType             `json:"transaction_amount"`
	Creditor             *PartyIdentification   `json:"creditor,omitempty"`
	CreditorAccount      *AccountIdentification `json:"creditor_account,omitempty"`
	Debtor               *PartyIdentification   `json:"debtor,omitempty"`
	DebtorAccount        *AccountIdentification `json:"debtor_account,omitempty"`
	CreditDebitIndicator string                 `json:"credit_debit_indicator"` // "CRDT" or "DBIT"
	Status               string                 `json:"status"`                 // "BOOK", "PDNG"
	BookingDate          string                 `json:"booking_date,omitempty"`
	ValueDate            string                 `json:"value_date,omitempty"`
	TransactionDate      string                 `json:"transaction_date,omitempty"`
	ReferenceNumber      string                 `json:"reference_number,omitempty"`
	RemittanceInformation []string              `json:"remittance_information,omitempty"`
}

type HalTransactions struct {
	Transactions    []Transaction `json:"transactions"`
	ContinuationKey string        `json:"continuation_key,omitempty"`
}

type Beneficiary struct {
	Creditor        PartyIdentification   `json:"creditor"`
	CreditorAccount AccountIdentification `json:"creditor_account"`
}

type CreditTransferTransaction struct {
	Beneficiary      Beneficiary `json:"beneficiary"`
	InstructedAmount AmountType  `json:"instructed_amount"`
}

type PaymentRequestResource struct {
	CreditTransferTransaction []CreditTransferTransaction `json:"credit_transfer_transaction"`
	DebtorAccount             *AccountIdentification      `json:"debtor_account,omitempty"`
}

type CreatePaymentRequest struct {
	Aspsp           ASPSP                  `json:"aspsp"`
	PaymentType     string                 `json:"payment_type"` // "SEPA", "DOMESTIC", "INSTANT"
	PaymentRequest  PaymentRequestResource `json:"payment_request"`
	State           string                 `json:"state"`
	RedirectURL     string                 `json:"redirect_url"`
	PsuType         string                 `json:"psu_type"` // "personal" or "business"
	DeferSubmission bool                   `json:"defer_submission"`
}

type CreatePaymentResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
	URL       string `json:"url,omitempty"`
}

type GetPaymentResponse struct {
	PaymentID      string                 `json:"payment_id"`
	Status         string                 `json:"status"` // e.g. "RCVD", "PDNG", "ACCC", "RJCT"
	PaymentDetails PaymentRequestResource `json:"payment_details"`
	PaymentType    string                 `json:"payment_type"`
	Aspsp          ASPSP                  `json:"aspsp"`
	FinalStatus    bool                   `json:"final_status"`
}

type SubmitPaymentResponse struct {
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}
