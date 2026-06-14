package bank

import (
	"fmt"

	"github.com/ngoldack/enable-banking-go/pkg/enablebanking"
)

func MapAccountToDomain(acc enablebanking.AccountResource, bankName string) Account {
	iban := acc.AccountID.Iban
	if iban == "" {
		iban = acc.AccountID.BBan
	}

	name := acc.Name
	if name == "" {
		name = "Standard Account"
	}

	return Account{
		ID:       acc.Uid,
		Name:     name,
		BankName: bankName,
		Currency: acc.Currency,
		IBAN:     iban,
	}
}

func MapBalancesToDomain(balances []enablebanking.BalanceResource) ([]AccountBalance, string, string) {
	var domainBalances []AccountBalance
	var available, booked string

	for _, bal := range balances {
		name := bal.Name
		if name == "" {
			switch bal.BalanceType {
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

		domainBalances = append(domainBalances, AccountBalance{
			Name:   name,
			Amount: bal.BalanceAmount.Amount,
		})

		// Track primary available and booked balances
		switch bal.BalanceType {
		case "CLAV", "ITAV":
			available = bal.BalanceAmount.Amount
		case "CLBD", "ITBD":
			booked = bal.BalanceAmount.Amount
		}
	}

	// Fallbacks if specific types are missing
	if available == "" {
		if booked != "" {
			available = booked
		} else if len(domainBalances) > 0 {
			available = domainBalances[0].Amount
		}
	}
	if booked == "" {
		if available != "" {
			booked = available
		} else if len(domainBalances) > 0 {
			booked = domainBalances[0].Amount
		}
	}

	return domainBalances, available, booked
}

func MapTransactionsToDomain(txs []enablebanking.Transaction) []Transaction {
	var domainTxs []Transaction
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

		// Include remittance info in description if available
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

		domainTxs = append(domainTxs, Transaction{
			ID:               tx.TransactionID,
			Date:             date,
			Description:      desc,
			Amount:           amount,
			Currency:         tx.TransactionAmount.Currency,
			IsIncoming:       isIncoming,
			Status:           status,
			CounterpartyIban: counterpartyIban,
		})
	}
	return domainTxs
}
