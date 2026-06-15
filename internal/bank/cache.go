package bank

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type AccountBalance struct {
	Name   string `json:"name"`   // e.g., "Available Balance", "Booked Balance"
	Amount string `json:"amount"` // e.g., "120.45"
}

// Currency is an ISO 4217 currency code (e.g. "EUR").
type Currency string

type Account struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	BankName         string           `json:"bank_name"`
	ConnectionName   string           `json:"connection_name"`
	Country          string           `json:"country,omitempty"`
	Currency         Currency         `json:"currency"`
	IBAN             string           `json:"iban"`
	Balances         []AccountBalance `json:"balances"`
	AvailableBalance string           `json:"available_balance"`
	BookedBalance    string           `json:"booked_balance"`
}

type Transaction struct {
	ID               string   `json:"id"`
	Date             string   `json:"date"`        // "2026-06-14"
	Description      string   `json:"description"` // "To: Amazon", "From: Oliver Virtanen"
	Amount           string   `json:"amount"`      // "-15.50", "+120.00"
	Currency         Currency `json:"currency"`
	IsIncoming       bool     `json:"is_incoming"`
	Status           string   `json:"status"` // "Completed", "Pending"
	CounterpartyIban string   `json:"counterparty_iban"`
}

type AccountDetail struct {
	Account      Account          `json:"account"`
	Balances     []AccountBalance `json:"balances"`
	Transactions []Transaction    `json:"transactions"`
	LastFetched  time.Time        `json:"last_fetched"`
}

const (
	keyAccounts   = "accounts:all"
	keyDetailPfx  = "detail:"
	defaultCacheT = 5 * time.Minute
)

// Cache is the provider-agnostic cache port used by the MCP server. Backends:
// none (disabled), memory (per-process), valkey (shared/external, optionally
// encrypted).
type Cache interface {
	GetAccounts(ctx context.Context) ([]Account, bool)
	SetAccounts(ctx context.Context, accounts []Account)
	GetDetail(ctx context.Context, accountID string) (AccountDetail, bool)
	SetDetail(ctx context.Context, accountID string, detail AccountDetail)
	Clear(ctx context.Context)
	Close() error
}

// ValkeyOptions configures the external Valkey/Redis backend.
type ValkeyOptions struct {
	Address  string
	Username string
	Password string
	DB       int
	TLS      bool
}

// CacheOptions selects and configures the cache backend.
type CacheOptions struct {
	Type          string // "none" | "memory" | "valkey" (default "memory")
	TTL           time.Duration
	Valkey        ValkeyOptions
	Encrypted     bool   // encrypt values at rest (valkey only)
	EncryptionKey string // base64-encoded 32-byte AES-256 key (required when Encrypted)
}

// NewCache builds the configured cache backend, wrapped with OpenTelemetry
// instrumentation. It returns an error for misconfiguration (e.g. valkey
// unreachable, or encryption requested without a valid key).
func NewCache(opts CacheOptions) (Cache, error) {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultCacheT
	}

	var (
		backend Cache
		err     error
	)
	switch opts.Type {
	case "", string(typeMemory):
		backend = newMemoryCache(ttl)
	case string(typeNone):
		backend = noopCache{}
	case string(typeValkey):
		backend, err = newValkeyCache(opts, ttl)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown cache type %q (valid: none, memory, valkey)", opts.Type)
	}

	kind := opts.Type
	if kind == "" {
		kind = string(typeMemory)
	}
	return newInstrumentedCache(backend, kind), nil
}

// Internal backend-type tags (kept separate from config to keep bank decoupled).
type backendType string

const (
	typeNone   backendType = "none"
	typeMemory backendType = "memory"
	typeValkey backendType = "valkey"
)

func detailKey(accountID string) string { return keyDetailPfx + accountID }

func marshal(v any) ([]byte, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return b, true
}

// noopCache disables caching: every read misses, every write is dropped.
type noopCache struct{}

func (noopCache) GetAccounts(context.Context) ([]Account, bool) { return nil, false }
func (noopCache) SetAccounts(context.Context, []Account)        {}
func (noopCache) GetDetail(context.Context, string) (AccountDetail, bool) {
	return AccountDetail{}, false
}
func (noopCache) SetDetail(context.Context, string, AccountDetail) {}
func (noopCache) Clear(context.Context)                            {}
func (noopCache) Close() error                                     { return nil }
