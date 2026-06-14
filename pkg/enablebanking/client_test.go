package enablebanking_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"
)

// Helper to generate a temporary RSA private key for testing JWT signatures
func generateTestRSAKey(t *testing.T) string {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048) // 2048 is faster for tests
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}

	privPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_private.key")
	if err := os.WriteFile(keyPath, privPEMBytes, 0600); err != nil {
		t.Fatalf("failed to write test key: %v", err)
	}

	return keyPath
}

func TestClient_RequiredFields(t *testing.T) {
	keyPath := generateTestRSAKey(t)
	client := enablebanking.NewClient("app-id-123", keyPath, "", "SANDBOX")

	ctx := context.Background()

	// Test StartAuthorization missing required fields
	_, err := client.StartAuthorization(ctx, "", "DE", "state", "http://localhost", 90)
	if err != enablebanking.ErrMissingAspspName {
		t.Errorf("expected ErrMissingAspspName, got: %v", err)
	}

	_, err = client.StartAuthorization(ctx, "BankName", "", "state", "http://localhost", 90)
	if err != enablebanking.ErrMissingAspspCountry {
		t.Errorf("expected ErrMissingAspspCountry, got: %v", err)
	}

	_, err = client.StartAuthorization(ctx, "BankName", "DE", "", "http://localhost", 90)
	if err != enablebanking.ErrMissingState {
		t.Errorf("expected ErrMissingState, got: %v", err)
	}

	_, err = client.StartAuthorization(ctx, "BankName", "DE", "state", "", 90)
	if err != enablebanking.ErrMissingRedirectURL {
		t.Errorf("expected ErrMissingRedirectURL, got: %v", err)
	}

	// Test AuthorizeSession missing required fields
	_, err = client.AuthorizeSession(ctx, "")
	if err != enablebanking.ErrMissingCode {
		t.Errorf("expected ErrMissingCode, got: %v", err)
	}

	// Test GetSession missing required fields
	_, err = client.GetSession(ctx, "")
	if err != enablebanking.ErrMissingSessionID {
		t.Errorf("expected ErrMissingSessionID, got: %v", err)
	}

	// Test GetBalances missing required fields
	_, err = client.GetBalances(ctx, "")
	if err != enablebanking.ErrMissingAccountID {
		t.Errorf("expected ErrMissingAccountID, got: %v", err)
	}

	// Test CreatePayment missing required fields
	_, err = client.CreatePayment(ctx, "debtor", "", "CreditorName", "10.00", "EUR", "SEPA", "state", "http://localhost")
	if err != enablebanking.ErrMissingCreditorIban {
		t.Errorf("expected ErrMissingCreditorIban, got: %v", err)
	}

	_, err = client.CreatePayment(ctx, "debtor", "creditor-iban", "", "10.00", "EUR", "SEPA", "state", "http://localhost")
	if err != enablebanking.ErrMissingCreditorName {
		t.Errorf("expected ErrMissingCreditorName, got: %v", err)
	}

	_, err = client.CreatePayment(ctx, "debtor", "creditor-iban", "CreditorName", "", "EUR", "SEPA", "state", "http://localhost")
	if err != enablebanking.ErrMissingAmount {
		t.Errorf("expected ErrMissingAmount, got: %v", err)
	}
}

func TestClient_GetASPSPs_Mocked(t *testing.T) {
	keyPath := generateTestRSAKey(t)
	
	// Create mock HTTP server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/aspsps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		// Verify authorization header has JWT
		auth := r.Header.Get("Authorization")
		if auth == "" || !jwtHelper.ContainsJWT(auth) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		response := enablebanking.GetAspspsResponse{
			ASPSPs: []enablebanking.ASPSP{
				{Name: "C24 Bank", Country: "DE", Bic: "CBANKDEFF"},
				{Name: "Nordea", Country: "FI", Bic: "NDEAFIHH"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	client := enablebanking.NewClient("app-id-123", keyPath, "", "SANDBOX")
	client.SetBaseURL(mockServer.URL)

	ctx := context.Background()
	aspsps, err := client.GetASPSPs(ctx)
	if err != nil {
		t.Fatalf("failed to call GetASPSPs: %v", err)
	}

	if len(aspsps) != 2 {
		t.Errorf("expected 2 ASPSPs, got: %d", len(aspsps))
	}
	if aspsps[0].Name != "C24 Bank" {
		t.Errorf("expected 'C24 Bank', got: %s", aspsps[0].Name)
	}
}

// Minimal JWT check helper
func TestClient_GetSession_Mocked(t *testing.T) {
	keyPath := generateTestRSAKey(t)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sessions/session-id-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		response := enablebanking.GetSessionResponse{
			SessionID: "session-id-123",
			Status:    "AUTHORIZED",
			Accounts:  []string{"acc-1", "acc-2"},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	client := enablebanking.NewClient("app-id-123", keyPath, "", "SANDBOX")
	client.SetBaseURL(mockServer.URL)

	ctx := context.Background()
	sess, err := client.GetSession(ctx, "session-id-123")
	if err != nil {
		t.Fatalf("failed to call GetSession: %v", err)
	}

	if sess.Status != "AUTHORIZED" {
		t.Errorf("expected 'AUTHORIZED', got: %s", sess.Status)
	}
	if len(sess.Accounts) != 2 {
		t.Errorf("expected 2 accounts, got: %d", len(sess.Accounts))
	}
}

type containsHelper struct{}

func (containsHelper) ContainsJWT(s string) bool {
	return len(s) > 20 // basic check
}

var jwtHelper containsHelper
