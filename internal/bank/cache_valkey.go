package bank

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/valkey-io/valkey-go"
)

// valkeyCache is a shared cache backed by an external Valkey/Redis server.
// Values are JSON-encoded and, when a codec is set, AES-256-GCM encrypted at
// rest. TTL is enforced server-side via SET ... EX.
type valkeyCache struct {
	client valkey.Client
	ttl    time.Duration
	codec  *cipherCodec // nil => values stored unencrypted
}

func newValkeyCache(opts CacheOptions, ttl time.Duration) (Cache, error) {
	clientOpt := valkey.ClientOption{
		InitAddress: []string{opts.Valkey.Address},
		Username:    opts.Valkey.Username,
		Password:    opts.Valkey.Password,
		SelectDB:    opts.Valkey.DB,
	}
	if opts.Valkey.TLS {
		clientOpt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	client, err := valkey.NewClient(clientOpt)
	if err != nil {
		return nil, fmt.Errorf("connect to valkey at %q: %w", opts.Valkey.Address, err)
	}

	var codec *cipherCodec
	if opts.Encrypted {
		codec, err = newCipherCodec(opts.EncryptionKey)
		if err != nil {
			client.Close()
			return nil, err
		}
	}
	return &valkeyCache{client: client, ttl: ttl, codec: codec}, nil
}

func (c *valkeyCache) encode(v any) (string, bool) {
	b, ok := marshal(v)
	if !ok {
		return "", false
	}
	if c.codec != nil {
		sealed, err := c.codec.seal(b)
		if err != nil {
			return "", false
		}
		b = sealed
	}
	return string(b), true
}

func (c *valkeyCache) decode(s string, v any) bool {
	b := []byte(s)
	if c.codec != nil {
		opened, err := c.codec.open(b)
		if err != nil {
			return false
		}
		b = opened
	}
	return json.Unmarshal(b, v) == nil
}

func (c *valkeyCache) set(ctx context.Context, key string, v any) {
	val, ok := c.encode(v)
	if !ok {
		return
	}
	seconds := int64(c.ttl.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	cmd := c.client.B().Set().Key(key).Value(val).ExSeconds(seconds).Build()
	if err := c.client.Do(ctx, cmd).Error(); err != nil {
		slog.WarnContext(ctx, "valkey cache set failed", "key", key, "error", err)
	}
}

func (c *valkeyCache) get(ctx context.Context, key string, v any) bool {
	resp := c.client.Do(ctx, c.client.B().Get().Key(key).Build())
	s, err := resp.ToString()
	if err != nil {
		if !valkey.IsValkeyNil(err) {
			slog.WarnContext(ctx, "valkey cache get failed", "key", key, "error", err)
		}
		return false
	}
	return c.decode(s, v)
}

func (c *valkeyCache) GetAccounts(ctx context.Context) ([]Account, bool) {
	var a []Account
	if !c.get(ctx, keyAccounts, &a) {
		return nil, false
	}
	return a, true
}

func (c *valkeyCache) SetAccounts(ctx context.Context, accounts []Account) {
	c.set(ctx, keyAccounts, accounts)
}

func (c *valkeyCache) GetDetail(ctx context.Context, accountID string) (AccountDetail, bool) {
	var d AccountDetail
	if !c.get(ctx, detailKey(accountID), &d) {
		return AccountDetail{}, false
	}
	return d, true
}

func (c *valkeyCache) SetDetail(ctx context.Context, accountID string, detail AccountDetail) {
	c.set(ctx, detailKey(accountID), detail)
}

func (c *valkeyCache) Clear(ctx context.Context) {
	// Best-effort: drop the accounts index; per-account detail keys expire by TTL.
	// (Avoids FLUSHDB, which would be destructive on a shared database.)
	_ = c.client.Do(ctx, c.client.B().Del().Key(keyAccounts).Build()).Error()
}

func (c *valkeyCache) Close() error {
	c.client.Close()
	return nil
}
