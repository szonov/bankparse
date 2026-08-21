package bankparse

import (
	"bytes"
	"errors"
	"testing"

	"github.com/szonov/bankparse/payment"
)

func TestDetectFormat(t *testing.T) {
	for name, test := range map[string]struct {
		data   []byte
		format Format
	}{
		"PDF":               {data: []byte("%PDF-1.7\n"), format: FormatPDF},
		"PDF after comment": {data: []byte("comment\n%PDF-1.7\n"), format: FormatPDF},
		"1C":                {data: []byte("1CClientBankExchange\n"), format: FormatClientBankExchange},
		"1C BOM":            {data: []byte("\xef\xbb\xbf1CClientBankExchange\n"), format: FormatClientBankExchange},
	} {
		t.Run(name, func(t *testing.T) {
			reader := bytes.NewReader(test.data)
			format, err := DetectFormat(reader, int64(len(test.data)))
			if err != nil || format != test.format {
				t.Fatalf("DetectFormat() = %q, %v; want %q", format, err, test.format)
			}
		})
	}
}

func TestDetectFormatRejectsUnknownData(t *testing.T) {
	data := []byte("not a bank file")
	_, err := DetectFormat(bytes.NewReader(data), int64(len(data)))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("DetectFormat() error = %v", err)
	}
}

func TestDetectFormatDoesNotChangeReadPosition(t *testing.T) {
	data := []byte("%PDF-1.7\n")
	reader := bytes.NewReader(data)
	if _, err := reader.Seek(4, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectFormat(reader, int64(len(data))); err != nil {
		t.Fatal(err)
	}
	position, err := reader.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if position != 4 {
		t.Fatalf("position=%d; want 4", position)
	}
}

func TestOpenClientBankExchange(t *testing.T) {
	data := []byte("1CClientBankExchange\nРасчСчет=40700000000000000001\nКонецФайла\n")
	parser, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.WalkDocuments(func(document payment.Document) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
