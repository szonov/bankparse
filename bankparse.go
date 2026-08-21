// Package bankparse detects bank file formats and creates streaming payment parsers.
package bankparse

import (
	"bytes"
	"errors"
	"fmt"
	"io"

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

var ErrUnknownFormat = errors.New("unknown bank file format")

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
