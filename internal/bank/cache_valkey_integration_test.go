//go:build valkeyintegration

package bank

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

// TestValkeyIntegration exercises the valkey backend against a real server.
// Run with: VALKEY_ADDR=127.0.0.1:6379 go test -tags valkeyintegration ./internal/bank/
func TestValkeyIntegration(t *testing.T) {
	addr := os.Getenv("VALKEY_ADDR")
	if addr == "" {
		t.Skip("set VALKEY_ADDR to run the valkey integration test")
	}
	ctx := context.Background()

	for _, encrypted := range []bool{false, true} {
		key := ""
		if encrypted {
			key, _ = NewEncryptionKey()
		}
		c, err := NewCache(CacheOptions{
			Type:          "valkey",
			TTL:           time.Minute,
			Valkey:        ValkeyOptions{Address: addr},
			Encrypted:     encrypted,
			EncryptionKey: key,
		})
		if err != nil {
			t.Fatalf("encrypted=%v: NewCache: %v", encrypted, err)
		}

		accounts := []Account{{ID: "v1", Name: "Valkey", IBAN: "DE89370400440532013000", Currency: "EUR"}}
		c.SetAccounts(ctx, accounts)
		got, ok := c.GetAccounts(ctx)
		if !ok || len(got) != 1 || got[0].ID != "v1" {
			t.Fatalf("encrypted=%v: accounts miss: ok=%v %+v", encrypted, ok, got)
		}

		c.SetDetail(ctx, "v1", AccountDetail{Account: Account{ID: "v1"}})
		if _, ok := c.GetDetail(ctx, "v1"); !ok {
			t.Fatalf("encrypted=%v: detail miss", encrypted)
		}

		// Prove encryption at rest: the raw stored value must not contain the IBAN.
		if encrypted {
			raw, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}})
			if err != nil {
				t.Fatal(err)
			}
			s, _ := raw.Do(ctx, raw.B().Get().Key(keyAccounts).Build()).ToString()
			if strings.Contains(s, "DE89370400440532013000") {
				t.Error("encrypted backend leaked plaintext into valkey")
			}
			raw.Close()
		}

		c.Clear(ctx)
		_ = c.Close()
	}
}
