package enablebanking

import (
	"fmt"

	"github.com/ngoldack/fin-mcp/internal/bank"
	"github.com/ngoldack/fin-mcp/internal/config"
	eb "github.com/ngoldack/fin-mcp/pkg/enablebanking"
)

func mapAccount(acc eb.AccountResource, conn config.Connection) bank.Account {
	iban := acc.AccountID.Iban
	if iban == "" {
		iban = acc.AccountID.BBan
	}
	name := acc.Name
	if name == "" {
		name = "Standard Account"
	}
	return bank.Account{
		ID:             acc.Uid,
		Name:           name,
		BankName:       conn.Bank,
		ConnectionName: conn.Name,
		Country:        string(conn.Country),
		Currency:       bank.Currency(acc.Currency),
		IBAN:           iban,
	}
}

func mapBalances(balances []eb.BalanceResource) ([]bank.AccountBalance, string, string) {
	var out []bank.AccountBalance
	var available, booked string

	for _, b := range balances {
		name := b.Name
		if name == "" {
			switch b.BalanceType {
			case "CLBD":
				name = "Booked Balance"
			case "ITBD":
				name = "Interim Booked Balance"
			case "XPBD":
				name = "Expected Balance"
			case "OPBD":
				name = "Opening Balance"
			case "CLAV":
				name = "Available Balance"
			case "ITAV":
				name = "Interim Available Balance"
			default:
				name = "Account Balance"
			}
		}
		out = append(out, bank.AccountBalance{Name: name, Amount: b.BalanceAmount.Amount})

		switch b.BalanceType {
		case "CLAV", "ITAV":
			available = b.BalanceAmount.Amount
		case "CLBD", "ITBD":
			booked = b.BalanceAmount.Amount
		}
	}

	if available == "" {
		if booked != "" {
			available = booked
		} else if len(out) > 0 {
			available = out[0].Amount
		}
	}
	if booked == "" {
		if available != "" {
			booked = available
		} else if len(out) > 0 {
			booked = out[0].Amount
		}
	}
	return out, available, booked
}

func mapTransactions(txs []eb.Transaction) []bank.Transaction {
	var out []bank.Transaction
	for _, tx := range txs {
		date := tx.BookingDate
		if date == "" {
			date = tx.TransactionDate
		}

		desc := "Transfer"
		var counterpartyIban string
		if tx.CreditDebitIndicator == "CRDT" {
			if tx.Debtor != nil && tx.Debtor.Name != "" {
				desc = "From: " + tx.Debtor.Name
			}
			if tx.DebtorAccount != nil {
				counterpartyIban = tx.DebtorAccount.Iban
				if counterpartyIban == "" {
					counterpartyIban = tx.DebtorAccount.BBan
				}
			}
		} else {
			if tx.Creditor != nil && tx.Creditor.Name != "" {
				desc = "To: " + tx.Creditor.Name
			}
			if tx.CreditorAccount != nil {
				counterpartyIban = tx.CreditorAccount.Iban
				if counterpartyIban == "" {
					counterpartyIban = tx.CreditorAccount.BBan
				}
			}
		}
		if len(tx.RemittanceInformation) > 0 && tx.RemittanceInformation[0] != "" {
			desc = fmt.Sprintf("%s (%s)", desc, tx.RemittanceInformation[0])
		}

		isIncoming := tx.CreditDebitIndicator == "CRDT"
		amount := tx.TransactionAmount.Amount
		if isIncoming {
			amount = "+" + amount
		} else {
			amount = "-" + amount
		}

		status := "Completed"
		if tx.Status == "PDNG" {
			status = "Pending"
		}

		out = append(out, bank.Transaction{
			ID:               tx.TransactionID,
			Date:             date,
			Description:      desc,
			Amount:           amount,
			Currency:         bank.Currency(tx.TransactionAmount.Currency),
			IsIncoming:       isIncoming,
			Status:           status,
			CounterpartyIban: counterpartyIban,
		})
	}
	return out
}
