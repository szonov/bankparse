// Package payment defines transport-neutral DTOs for Russian payment documents.
//
// The types in this package do not depend on a particular source format. The
// same Document can be produced from a PDF form or a 1CClientBankExchange file.
package payment

import (
	"errors"
	"time"
)

// ErrStop can be returned by DocumentFunc to stop a document walk successfully.
var ErrStop = errors.New("stop walking payment documents")

// DocumentFunc receives documents as they are parsed. Returning ErrStop stops
// the walk successfully; any other error stops the walk and is returned.
type DocumentFunc func(Document) error

// Walker provides sequential, early-terminating access to payment documents.
type Walker interface {
	WalkDocuments(DocumentFunc) error
}

// Type identifies the kind of a bank document.
type Type string

const (
	PaymentOrder    Type = "payment_order"
	PaymentWarrant  Type = "payment_warrant"
	PaymentRequest  Type = "payment_request"
	CollectionOrder Type = "collection_order"
	BankOrder       Type = "bank_order"
)

// Amount is a non-negative monetary amount. Kopecks keeps the DTO independent
// of floating-point and decimal implementations used by an application.
type Amount struct {
	Kopecks  int64  `json:"kopecks"`
	Currency string `json:"currency"`
}

// Bank contains the bank details attached to one side of a document.
type Bank struct {
	Name    string `json:"name"`
	BIK     string `json:"bik"`
	Account string `json:"account"`
}

// Party is a payer or recipient together with its account and bank details.
type Party struct {
	Name    string `json:"name"`
	INN     string `json:"inn"`
	KPP     string `json:"kpp"`
	Account string `json:"account"`
	Bank    Bank   `json:"bank"`
}

// BudgetDetails contains fields 101 and 104-110 of a budget payment order.
// Values are kept as strings because zero and an absent value have different
// meanings in bank exchange formats.
type BudgetDetails struct {
	PayerStatus    string `json:"payer_status"`
	KBK            string `json:"kbk"`
	OKTMO          string `json:"oktmo"`
	Basis          string `json:"basis"`
	TaxPeriod      string `json:"tax_period"`
	DocumentNumber string `json:"document_number"`
	DocumentDate   string `json:"document_date"`
	PaymentType    string `json:"payment_type"`
}

// Document describes a payment independently of its source. Amount is always
// non-negative; direction is determined by comparing Payer and Recipient
// accounts with the account for which a statement is being imported.
type Document struct {
	Type          Type           `json:"type"`
	Number        string         `json:"number"`
	Date          time.Time      `json:"date"`
	OperationType string         `json:"operation_type"`
	Amount        Amount         `json:"amount"`
	Payer         Party          `json:"payer"`
	Recipient     Party          `json:"recipient"`
	Purpose       string         `json:"purpose"`
	Budget        *BudgetDetails `json:"budget,omitempty"`
}
