package bankparse

import (
	"bytes"
	"errors"
	"io"
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

func TestDetectStatementInfoClientBankExchange(t *testing.T) {
	data := []byte("1CClientBankExchange\nОтправитель=  АО   Тестовый Банк  \nРасчСчет=40000000000000000001\nКонецФайла\n")
	reader := bytes.NewReader(data)
	if _, err := reader.Seek(7, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := DetectStatementInfo(reader, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	want := StatementInfo{AccountNumber: "40000000000000000001", BankName: "АО Тестовый Банк"}
	if got != want {
		t.Fatalf("DetectStatementInfo() = %#v; want %#v", got, want)
	}
	position, _ := reader.Seek(0, io.SeekCurrent)
	if position != 7 {
		t.Fatalf("position = %d; want 7", position)
	}
	parser, err := Open(reader, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := parser.WalkDocuments(func(payment.Document) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestDetectStatementInfoClientBankExchangeSections(t *testing.T) {
	for name, body := range map[string]string{
		"one":  "СекцияРасчСчет\nРасчСчет=40000000000000000001\nКонецРасчСчет\n",
		"same": "СекцияРасчСчет\nРасчСчет=40000000000000000001\nКонецРасчСчет\nСекцияРасчСчет\nРасчСчет=40000000000000000001\nКонецРасчСчет\n",
	} {
		t.Run(name, func(t *testing.T) {
			data := []byte("1CClientBankExchange\n" + body + "КонецФайла\n")
			got, err := DetectStatementInfo(bytes.NewReader(data), int64(len(data)))
			if err != nil || got.AccountNumber != "40000000000000000001" || got.BankName != "" {
				t.Fatalf("DetectStatementInfo() = %#v, %v", got, err)
			}
		})
	}
}

func TestDetectStatementInfoErrors(t *testing.T) {
	if _, err := DetectStatementInfo(nil, 0); err == nil {
		t.Fatal("nil reader unexpectedly succeeded")
	}
	if _, err := DetectStatementInfo(bytes.NewReader(nil), -1); err == nil {
		t.Fatal("negative size unexpectedly succeeded")
	}
	unknown := []byte("unknown")
	if _, err := DetectStatementInfo(bytes.NewReader(unknown), int64(len(unknown))); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("unknown format error = %v", err)
	}
	ambiguous := []byte("1CClientBankExchange\nСекцияРасчСчет\nРасчСчет=40000000000000000001\nКонецРасчСчет\nСекцияРасчСчет\nРасчСчет=40000000000000000002\nКонецРасчСчет\nКонецФайла\n")
	if _, err := DetectStatementInfo(bytes.NewReader(ambiguous), int64(len(ambiguous))); !errors.Is(err, ErrStatementInfoAmbiguous) {
		t.Fatalf("ambiguous error = %v", err)
	}
	invalid := []byte("1CClientBankExchange\nРасчСчет=123\nКонецФайла\n")
	if _, err := DetectStatementInfo(bytes.NewReader(invalid), int64(len(invalid))); err == nil {
		t.Fatal("invalid account unexpectedly succeeded")
	}
	missing := []byte("1CClientBankExchange\nОтправитель=Не банк\nКонецФайла\n")
	if _, err := DetectStatementInfo(bytes.NewReader(missing), int64(len(missing))); !errors.Is(err, ErrStatementInfoNotDetected) {
		t.Fatalf("missing info error = %v", err)
	}
	damagedPDF := []byte("%PDF-1.7\nnot a PDF")
	if _, err := DetectStatementInfo(bytes.NewReader(damagedPDF), int64(len(damagedPDF))); err == nil || errors.Is(err, ErrStatementInfoNotDetected) {
		t.Fatalf("damaged PDF error = %v", err)
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
