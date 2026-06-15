package bank

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type memoryEntry struct {
	data   []byte
	expiry time.Time
}

// memoryCache is a per-process, TTL'd in-memory cache. It is NOT shared across
// processes — the TUI and the MCP server keep independent caches when this
// backend is used. Use the valkey backend for a shared cache.
type memoryCache struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]memoryEntry
}

func newMemoryCache(ttl time.Duration) *memoryCache {
	return &memoryCache{ttl: ttl, m: make(map[string]memoryEntry)}
}

func (c *memoryCache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiry) {
		return nil, false
	}
	return e.data, true
}

func (c *memoryCache) set(key string, data []byte) {
	c.mu.Lock()
	c.m[key] = memoryEntry{data: data, expiry: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *memoryCache) GetAccounts(context.Context) ([]Account, bool) {
	b, ok := c.get(keyAccounts)
	if !ok {
		return nil, false
	}
	var a []Account
	if json.Unmarshal(b, &a) != nil {
		return nil, false
	}
	return a, true
}

func (c *memoryCache) SetAccounts(_ context.Context, accounts []Account) {
	if b, ok := marshal(accounts); ok {
		c.set(keyAccounts, b)
	}
}

func (c *memoryCache) GetDetail(_ context.Context, accountID string) (AccountDetail, bool) {
	b, ok := c.get(detailKey(accountID))
	if !ok {
		return AccountDetail{}, false
	}
	var d AccountDetail
	if json.Unmarshal(b, &d) != nil {
		return AccountDetail{}, false
	}
	return d, true
}

func (c *memoryCache) SetDetail(_ context.Context, accountID string, detail AccountDetail) {
	if b, ok := marshal(detail); ok {
		c.set(detailKey(accountID), b)
	}
}

func (c *memoryCache) Clear(context.Context) {
	c.mu.Lock()
	c.m = make(map[string]memoryEntry)
	c.mu.Unlock()
}

func (c *memoryCache) Close() error { return nil }
