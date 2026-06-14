package bank

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	badger "github.com/dgraph-io/badger/v4"
)

type AccountBalance struct {
	Name   string `json:"name"`   // e.g., "Available Balance", "Booked Balance"
	Amount string `json:"amount"` // e.g., "120.45"
}

type Account struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	BankName         string           `json:"bank_name"`
	Currency         string           `json:"currency"`
	IBAN             string           `json:"iban"`
	Balances         []AccountBalance `json:"balances"`
	AvailableBalance string           `json:"available_balance"`
	BookedBalance    string           `json:"booked_balance"`
}

type Transaction struct {
	ID               string `json:"id"`
	Date             string `json:"date"`             // "2026-06-14"
	Description      string `json:"description"`      // "To: Amazon", "From: Oliver Virtanen"
	Amount           string `json:"amount"`           // "-15.50", "+120.00"
	Currency         string `json:"currency"`
	IsIncoming       bool   `json:"is_incoming"`
	Status           string `json:"status"`           // "Completed", "Pending"
	CounterpartyIban string `json:"counterparty_iban"`
}

type AccountDetail struct {
	Account      Account          `json:"account"`
	Balances     []AccountBalance `json:"balances"`
	Transactions []Transaction    `json:"transactions"`
	LastFetched  time.Time        `json:"last_fetched"`
}

type Cache struct {
	dbPath string
	db     *badger.DB
	ttl    time.Duration
}

func NewCache(dbPath string, defaultTTL time.Duration) *Cache {
	if dbPath == "" {
		dbPath = ".bank.db"
	}

	opts := badger.DefaultOptions(dbPath).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		panic(fmt.Sprintf("failed to open badger cache database: %v", err))
	}

	if defaultTTL <= 0 {
		defaultTTL = 5 * time.Minute
	}

	return &Cache{
		dbPath: dbPath,
		db:     db,
		ttl:    defaultTTL,
	}
}

func (c *Cache) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

func (c *Cache) SetAccounts(ctx context.Context, accounts []Account) {
	bytes, err := json.Marshal(accounts)
	if err != nil {
		return
	}

	_ = c.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte("accounts:all"), bytes).WithTTL(c.ttl)
		return txn.SetEntry(e)
	})
}

func (c *Cache) GetAccounts(ctx context.Context) ([]Account, bool) {
	var accounts []Account
	found := false

	_ = c.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("accounts:all"))
		if err != nil {
			return err // returns ErrKeyNotFound if expired or missing
		}

		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(val, &accounts); err != nil {
			return err
		}

		found = true
		return nil
	})

	return accounts, found
}

func (c *Cache) SetDetail(ctx context.Context, accountID string, detail AccountDetail) {
	bytes, err := json.Marshal(detail)
	if err != nil {
		return
	}

	_ = c.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte("detail:"+accountID), bytes).WithTTL(c.ttl)
		return txn.SetEntry(e)
	})
}

func (c *Cache) GetDetail(ctx context.Context, accountID string) (AccountDetail, bool) {
	var detail AccountDetail
	found := false

	_ = c.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("detail:" + accountID))
		if err != nil {
			return err
		}

		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}

		if err := json.Unmarshal(val, &detail); err != nil {
			return err
		}

		found = true
		return nil
	})

	return detail, found
}

func (c *Cache) Clear(ctx context.Context) {
	_ = c.db.DropAll()
}
