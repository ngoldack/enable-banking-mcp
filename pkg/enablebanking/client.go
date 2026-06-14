package enablebanking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Predefined validation errors for SDK consumers
var (
	ErrMissingAppID          = errors.New("missing required field: AppID")
	ErrMissingAspspName      = errors.New("missing required field: ASPSP Name")
	ErrMissingAspspCountry   = errors.New("missing required field: ASPSP Country")
	ErrMissingState          = errors.New("missing required field: State")
	ErrMissingRedirectURL    = errors.New("missing required field: RedirectURL")
	ErrMissingCode             = errors.New("missing required field: Code")
	ErrMissingSessionID      = errors.New("missing required field: SessionID")
	ErrMissingAccountID      = errors.New("missing required field: AccountID")
	ErrMissingPaymentID      = errors.New("missing required field: PaymentID")
	ErrMissingCreditorIban   = errors.New("missing required field: CreditorIBAN")
	ErrMissingCreditorName   = errors.New("missing required field: CreditorName")
	ErrMissingAmount         = errors.New("missing required field: Amount")
)

type APIClient interface {
	GetASPSPs(ctx context.Context) ([]ASPSP, error)
	StartAuthorization(ctx context.Context, aspspName, aspspCountry, state, redirectURL string, consentDays int) (*StartAuthorizationResponse, error)
	AuthorizeSession(ctx context.Context, code string) (*AuthorizeSessionResponse, error)
	GetSession(ctx context.Context, sessionID string) (*GetSessionResponse, error)
	GetBalances(ctx context.Context, accountID string) ([]BalanceResource, error)
	GetTransactions(ctx context.Context, accountID string, dateFrom, dateTo string) ([]Transaction, error)
	GetAccountDetails(ctx context.Context, accountID string) (*AccountResource, error)
	CreatePayment(ctx context.Context, debtorIban, creditorIban, creditorName, amount, currency, paymentType, state, redirectURL string) (*CreatePaymentResponse, error)
	GetPayment(ctx context.Context, paymentID string) (*GetPaymentResponse, error)
	SubmitPayment(ctx context.Context, paymentID string) (*SubmitPaymentResponse, error)
	SetBaseURL(url string)
}

type Client struct {
	appID             string
	privateKeyPath    string
	privateKeyContent string
	environment       string
	baseURL           string
	httpClient        *http.Client
}

func NewClient(appID, privateKeyPath, privateKeyContent, environment string) APIClient {
	return &Client{
		appID:             appID,
		privateKeyPath:    privateKeyPath,
		privateKeyContent: privateKeyContent,
		environment:       environment,
		baseURL:           "https://api.enablebanking.com",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetBaseURL allows overriding the base URL, which is extremely useful for Mocking and local testing!
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

func (c *Client) generateJWT() (string, error) {
	if c.appID == "" {
		return "", ErrMissingAppID
	}

	var keyBytes []byte
	var err error

	if c.privateKeyContent != "" {
		keyBytes = []byte(c.privateKeyContent)
	} else {
		keyBytes, err = os.ReadFile(c.privateKeyPath)
		if err != nil {
			return "", fmt.Errorf("failed to read private key from %s: %w", c.privateKeyPath, err)
		}
	}

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(keyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse RSA private key: %w", err)
	}

	iat := time.Now()
	exp := iat.Add(1 * time.Hour)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "enablebanking.com",
		"aud": "api.enablebanking.com",
		"iat": iat.Unix(),
		"exp": exp.Unix(),
	})

	token.Header["kid"] = c.appID
	token.Header["typ"] = "JWT"

	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return tokenString, nil
}

func (c *Client) sendRequest(ctx context.Context, method, path string, body any, responseTarget any) error {
	token, err := c.generateJWT()
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBytes, &errResp); err == nil && errResp.Message != "" {
			return fmt.Errorf("API error (status %d): %s (code: %s)", resp.StatusCode, errResp.Message, errResp.Code)
		}
		if err := json.Unmarshal(respBytes, &errResp); err == nil && errResp.ErrorDescription != "" {
			return fmt.Errorf("API error (status %d): %s (code: %s)", resp.StatusCode, errResp.ErrorDescription, errResp.Error)
		}
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	if responseTarget != nil {
		if err := json.Unmarshal(respBytes, responseTarget); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w (body: %s)", err, string(respBytes))
		}
	}

	return nil
}

// SDK Client API Methods with Context Support and Required Field Validations

func (c *Client) GetASPSPs(ctx context.Context) ([]ASPSP, error) {
	var resp GetAspspsResponse
	err := c.sendRequest(ctx, "GET", "/aspsps", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.ASPSPs, nil
}

func (c *Client) StartAuthorization(ctx context.Context, aspspName, aspspCountry, state, redirectURL string, consentDays int) (*StartAuthorizationResponse, error) {
	if aspspName == "" {
		return nil, ErrMissingAspspName
	}
	if aspspCountry == "" {
		return nil, ErrMissingAspspCountry
	}
	if state == "" {
		return nil, ErrMissingState
	}
	if redirectURL == "" {
		return nil, ErrMissingRedirectURL
	}

	if consentDays <= 0 {
		consentDays = 90
	}
	validUntil := time.Now().AddDate(0, 0, consentDays).UTC().Format("2006-01-02T15:04:05.000000+00:00")

	req := StartAuthorizationRequest{
		Access: Access{
			Balances:     true,
			Transactions: true,
			ValidUntil:   validUntil,
		},
		ASPSP: ASPSP{
			Name:    aspspName,
			Country: aspspCountry,
		},
		State:       state,
		RedirectURL: redirectURL,
	}

	var resp StartAuthorizationResponse
	err := c.sendRequest(ctx, "POST", "/auth", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) AuthorizeSession(ctx context.Context, code string) (*AuthorizeSessionResponse, error) {
	if code == "" {
		return nil, ErrMissingCode
	}

	req := AuthorizeSessionRequest{
		Code: code,
	}
	var resp AuthorizeSessionResponse
	err := c.sendRequest(ctx, "POST", "/sessions", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetSession(ctx context.Context, sessionID string) (*GetSessionResponse, error) {
	if sessionID == "" {
		return nil, ErrMissingSessionID
	}

	var resp GetSessionResponse
	err := c.sendRequest(ctx, "GET", "/sessions/"+sessionID, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetBalances(ctx context.Context, accountID string) ([]BalanceResource, error) {
	if accountID == "" {
		return nil, ErrMissingAccountID
	}

	var resp HalBalances
	err := c.sendRequest(ctx, "GET", "/accounts/"+accountID+"/balances", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

func (c *Client) GetTransactions(ctx context.Context, accountID string, dateFrom, dateTo string) ([]Transaction, error) {
	if accountID == "" {
		return nil, ErrMissingAccountID
	}

	path := "/accounts/" + accountID + "/transactions"
	queryParams := ""
	if dateFrom != "" {
		queryParams += "date_from=" + dateFrom
	}
	if dateTo != "" {
		if queryParams != "" {
			queryParams += "&"
		}
		queryParams += "date_to=" + dateTo
	}
	if queryParams != "" {
		path += "?" + queryParams
	}

	var resp HalTransactions
	err := c.sendRequest(ctx, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Transactions, nil
}

func (c *Client) GetAccountDetails(ctx context.Context, accountID string) (*AccountResource, error) {
	if accountID == "" {
		return nil, ErrMissingAccountID
	}

	var resp AccountResource
	err := c.sendRequest(ctx, "GET", "/accounts/"+accountID+"/details", nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreatePayment(
	ctx context.Context, debtorIban, creditorIban, creditorName, amount, currency, paymentType, state, redirectURL string,
) (*CreatePaymentResponse, error) {
	if creditorIban == "" {
		return nil, ErrMissingCreditorIban
	}
	if creditorName == "" {
		return nil, ErrMissingCreditorName
	}
	if amount == "" {
		return nil, ErrMissingAmount
	}
	if state == "" {
		return nil, ErrMissingState
	}
	if redirectURL == "" {
		return nil, ErrMissingRedirectURL
	}

	if currency == "" {
		currency = "EUR"
	}
	if paymentType == "" {
		paymentType = "SEPA"
	}

	req := CreatePaymentRequest{
		Aspsp: ASPSP{
			Name:    "",
			Country: "",
		},
		PaymentType:     paymentType,
		PsuType:         "personal",
		State:           state,
		RedirectURL:     redirectURL,
		DeferSubmission: true,
		PaymentRequest: PaymentRequestResource{
			CreditTransferTransaction: []CreditTransferTransaction{
				{
					Beneficiary: Beneficiary{
						Creditor: PartyIdentification{
							Name: creditorName,
						},
						CreditorAccount: AccountIdentification{
							Iban:       creditorIban,
							SchemeName: "IBAN",
						},
					},
					InstructedAmount: AmountType{
						Currency: currency,
						Amount:   amount,
					},
				},
			},
		},
	}

	if debtorIban != "" {
		req.PaymentRequest.DebtorAccount = &AccountIdentification{
			Iban:       debtorIban,
			SchemeName: "IBAN",
		}
	}

	var resp CreatePaymentResponse
	err := c.sendRequest(ctx, "POST", "/payments", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetPayment(ctx context.Context, paymentID string) (*GetPaymentResponse, error) {
	if paymentID == "" {
		return nil, ErrMissingPaymentID
	}

	var resp GetPaymentResponse
	err := c.sendRequest(ctx, "GET", "/payments/"+paymentID, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) SubmitPayment(ctx context.Context, paymentID string) (*SubmitPaymentResponse, error) {
	if paymentID == "" {
		return nil, ErrMissingPaymentID
	}

	var resp SubmitPaymentResponse
	err := c.sendRequest(ctx, "POST", "/payments/"+paymentID+"/submit", nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
