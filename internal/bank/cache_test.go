package bank

import (
	"context"
	"encoding/base64"
	"testing"
	"time"
)

func TestMemoryCache_HitMissExpiry(t *testing.T) {
	ctx := context.Background()
	c := newMemoryCache(50 * time.Millisecond)

	if _, ok := c.GetAccounts(ctx); ok {
		t.Fatal("expected miss on empty cache")
	}

	accounts := []Account{{ID: "a1", Name: "Main", Currency: "EUR"}}
	c.SetAccounts(ctx, accounts)

	got, ok := c.GetAccounts(ctx)
	if !ok || len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("expected hit with 1 account, got ok=%v %+v", ok, got)
	}

	time.Sleep(70 * time.Millisecond)
	if _, ok := c.GetAccounts(ctx); ok {
		t.Error("expected miss after TTL expiry")
	}
}

func TestNoopCache_AlwaysMisses(t *testing.T) {
	ctx := context.Background()
	c := noopCache{}
	c.SetAccounts(ctx, []Account{{ID: "x"}})
	if _, ok := c.GetAccounts(ctx); ok {
		t.Error("noop cache must never return a hit")
	}
	c.SetDetail(ctx, "x", AccountDetail{})
	if _, ok := c.GetDetail(ctx, "x"); ok {
		t.Error("noop cache must never return a hit")
	}
}

func TestCipherCodec_RoundTrip(t *testing.T) {
	key, err := NewEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	codec, err := newCipherCodec(key)
	if err != nil {
		t.Fatal(err)
	}

	plain := []byte(`{"hello":"world"}`)
	sealed, err := codec.seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(sealed) == string(plain) {
		t.Fatal("sealed output must not equal plaintext")
	}
	opened, err := codec.open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != string(plain) {
		t.Fatalf("round trip mismatch: %q", opened)
	}

	// Tampered ciphertext must fail authentication.
	sealed[len(sealed)-1] ^= 0xff
	if _, err := codec.open(sealed); err == nil {
		t.Error("expected authentication failure on tampered ciphertext")
	}
}

func TestNewCipherCodec_BadKey(t *testing.T) {
	if _, err := newCipherCodec(""); err == nil {
		t.Error("empty key should error")
	}
	if _, err := newCipherCodec("not-base64!!"); err == nil {
		t.Error("invalid base64 should error")
	}
	if _, err := newCipherCodec(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("non-32-byte key should error")
	}
}

func TestNewEncryptionKey_Is32Bytes(t *testing.T) {
	k, err := NewEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 32 {
		t.Errorf("key = %d bytes, want 32", len(raw))
	}
}

func TestNewCache_Factory(t *testing.T) {
	for _, typ := range []string{"", "memory", "none"} {
		c, err := NewCache(CacheOptions{Type: typ, TTL: time.Minute})
		if err != nil {
			t.Fatalf("type %q: %v", typ, err)
		}
		_ = c.Close()
	}
	if _, err := NewCache(CacheOptions{Type: "bogus"}); err == nil {
		t.Error("unknown cache type should error")
	}
}
