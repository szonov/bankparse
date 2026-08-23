// Package bankparse detects bank file formats and creates streaming payment parsers.
package bankparse

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/szonov/bankparse/bankexchange"
	"github.com/szonov/bankparse/payment"
	"github.com/szonov/bankparse/paymentpdf"
	"github.com/szonov/pdf"
)

type Format string

const (
	FormatPDF                Format = "pdf"
	FormatClientBankExchange Format = "1c_client_bank_exchange"
)

var (
	ErrUnknownFormat            = errors.New("unknown bank file format")
	ErrStatementInfoNotDetected = errors.New("statement info not detected")
	ErrStatementInfoAmbiguous   = errors.New("ambiguous statement info")
	ErrStatementAccountNotFound = errors.New("statement account not found")
	ErrDocumentCountMismatch    = paymentpdf.ErrDocumentCountMismatch
	ErrDocumentTotalsMismatch   = payment.ErrDocumentTotalsMismatch
)

type StatementInfo struct {
	AccountNumber string `json:"account_number"`
	BankName      string `json:"bank_name,omitempty"`
}

// DetectStatementAccounts returns every unique account declared by a statement.
// PDF and single-account 1C statements return a one-element slice.
func DetectStatementAccounts(reader io.ReaderAt, size int64) ([]StatementInfo, error) {
	format, err := DetectFormat(reader, size)
	if err != nil {
		return nil, err
	}
	if format == FormatPDF {
		info, err := DetectStatementInfo(reader, size)
		if err != nil {
			return nil, err
		}
		return []StatementInfo{info}, nil
	}
	info, err := bankexchange.DetectAccounts(io.NewSectionReader(reader, 0, size))
	if errors.Is(err, bankexchange.ErrStatementInfoNotDetected) {
		return nil, ErrStatementInfoNotDetected
	}
	if err != nil {
		return nil, fmt.Errorf("read 1CClientBankExchange accounts: %w", err)
	}
	bankName := strings.Join(strings.Fields(info.Sender), " ")
	accounts := make([]StatementInfo, 0, len(info.AccountNumbers))
	for _, account := range info.AccountNumbers {
		if normalizeStatementAccount(account) == "" {
			return nil, errors.New("invalid statement account")
		}
		accounts = append(accounts, StatementInfo{AccountNumber: account, BankName: bankName})
	}
	return accounts, nil
}

const detectionProbeSize = 4096

// DetectFormat identifies a bank file by its contents without changing any
// sequential read position maintained by reader.
func DetectFormat(reader io.ReaderAt, size int64) (Format, error) {
	probe, err := readPrefix(reader, size)
	if err != nil {
		return "", err
	}
	if offset := bytes.Index(probe, []byte("%PDF-")); offset >= 0 && offset < 1024 {
		return FormatPDF, nil
	}
	probe = bytes.TrimPrefix(probe, []byte{0xef, 0xbb, 0xbf})
	probe = bytes.TrimLeft(probe, " \t\r\n")
	if bytes.HasPrefix(probe, []byte("1CClientBankExchange")) {
		return FormatClientBankExchange, nil
	}
	return "", ErrUnknownFormat
}

// Open detects the source format and returns a streaming payment parser.
func Open(reader io.ReaderAt, size int64) (payment.Walker, error) {
	format, err := DetectFormat(reader, size)
	if err != nil {
		return nil, err
	}
	return OpenFormat(format, reader, size)
}

// OpenAccount opens a statement for one explicitly selected account. For 1C
// exchanges it emits only documents where the account is payer or recipient
// and validates that account's turnovers after all callbacks.
func OpenAccount(reader io.ReaderAt, size int64, account string) (payment.Walker, error) {
	accounts, err := DetectStatementAccounts(reader, size)
	if err != nil {
		return nil, err
	}
	found := false
	for _, info := range accounts {
		if info.AccountNumber == account {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrStatementAccountNotFound
	}
	format, err := DetectFormat(reader, size)
	if err != nil {
		return nil, err
	}
	if format == FormatPDF {
		return OpenFormat(format, reader, size)
	}
	selected, err := bankexchange.NewForAccount(io.NewSectionReader(reader, 0, size), account)
	if err != nil {
		return nil, fmt.Errorf("open 1CClientBankExchange account: %w", err)
	}
	return selected, nil
}

// DetectStatementInfo extracts statement-wide metadata without changing any
// sequential position maintained by reader.
func DetectStatementInfo(reader io.ReaderAt, size int64) (StatementInfo, error) {
	format, err := DetectFormat(reader, size)
	if err != nil {
		return StatementInfo{}, err
	}
	switch format {
	case FormatPDF:
		pdfReader, err := pdf.NewReader(reader, size)
		if err != nil {
			return StatementInfo{}, fmt.Errorf("open PDF: %w", err)
		}
		info, err := paymentpdf.DetectStatementInfo(pdfReader)
		if errors.Is(err, paymentpdf.ErrStatementInfoNotDetected) {
			return StatementInfo{}, ErrStatementInfoNotDetected
		}
		if errors.Is(err, paymentpdf.ErrAmbiguousStatementInfo) {
			return StatementInfo{}, ErrStatementInfoAmbiguous
		}
		if err != nil {
			return StatementInfo{}, err
		}
		return StatementInfo{AccountNumber: info.AccountNumber, BankName: info.BankName}, nil
	case FormatClientBankExchange:
		info, err := bankexchange.DetectInfo(io.NewSectionReader(reader, 0, size))
		if errors.Is(err, bankexchange.ErrStatementInfoNotDetected) {
			return StatementInfo{}, ErrStatementInfoNotDetected
		}
		if errors.Is(err, bankexchange.ErrAmbiguousAccounts) {
			return StatementInfo{}, ErrStatementInfoAmbiguous
		}
		if err != nil {
			return StatementInfo{}, fmt.Errorf("read 1CClientBankExchange info: %w", err)
		}
		if normalizeStatementAccount(info.Account) == "" {
			return StatementInfo{}, errors.New("invalid statement account")
		}
		return StatementInfo{AccountNumber: info.Account, BankName: strings.Join(strings.Fields(info.Sender), " ")}, nil
	default:
		return StatementInfo{}, ErrUnknownFormat
	}
}

func normalizeStatementAccount(account string) string {
	if len(account) != 20 {
		return ""
	}
	for _, r := range account {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return account
}

// OpenFormat creates a parser for an explicitly selected format.
func OpenFormat(format Format, source io.ReaderAt, size int64) (payment.Walker, error) {
	if source == nil {
		return nil, errors.New("nil bank file reader")
	}
	if size < 0 {
		return nil, errors.New("negative bank file size")
	}
	switch format {
	case FormatPDF:
		reader, err := pdf.NewReader(source, size)
		if err != nil {
			return nil, fmt.Errorf("open PDF: %w", err)
		}
		return paymentpdf.New(reader), nil
	case FormatClientBankExchange:
		_, err := bankexchange.DetectInfo(io.NewSectionReader(source, 0, size))
		if errors.Is(err, bankexchange.ErrAmbiguousAccounts) {
			return nil, ErrStatementInfoAmbiguous
		}
		if err != nil {
			return nil, fmt.Errorf("read 1CClientBankExchange info: %w", err)
		}
		reader, err := bankexchange.New(io.NewSectionReader(source, 0, size))
		if err != nil {
			return nil, fmt.Errorf("open 1CClientBankExchange: %w", err)
		}
		return reader, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownFormat, format)
	}
}

func readPrefix(reader io.ReaderAt, size int64) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("nil bank file reader")
	}
	if size < 0 {
		return nil, errors.New("negative bank file size")
	}
	length := int64(detectionProbeSize)
	if size < length {
		length = size
	}
	probe := make([]byte, length)
	n, err := reader.ReadAt(probe, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read bank file signature: %w", err)
	}
	return probe[:n], nil
}

var (
	_ payment.Walker = (*bankexchange.Reader)(nil)
	_ payment.Walker = (*paymentpdf.Reader)(nil)
)
