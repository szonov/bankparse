package paymentpdf

import (
	"errors"
	"testing"

	"github.com/szonov/pdf"
)

func TestDetectStatementInfoBlocks(t *testing.T) {
	tests := map[string]struct {
		blocks []pdf.TextBlock
		want   StatementInfo
	}{
		"Russian title": {
			blocks: []pdf.TextBlock{{X: 100, Y: 700, Text: "ВЫПИСКА ОПЕРАЦИЙ ПО ЛИЦЕВОМУ СЧЕТУ 40000000000000000001"}},
			want:   StatementInfo{AccountNumber: "40000000000000000001"},
		},
		"bilingual labels": {
			blocks: []pdf.TextBlock{{X: 100, Y: 700, Text: "Account statement"}, {X: 50, Y: 670, Text: "Номер счета / Account number:"}, {X: 260, Y: 670, Text: "40000000000000000002"}, {X: 50, Y: 650, Text: "Банк / Bank:"}, {X: 170, Y: 650, Text: "АО \"Северный Банк\""}},
			want:   StatementInfo{AccountNumber: "40000000000000000002", BankName: "АО \"Северный Банк\""},
		},
		"organization above title": {
			blocks: []pdf.TextBlock{{X: 50, Y: 740, Text: "  Сибирский   филиал АО \"Третий банк\" "}, {X: 100, Y: 700, Text: "Выписка по счету клиента"}, {X: 50, Y: 670, Text: "Номер счета: 40000000000000000002"}},
			want:   StatementInfo{AccountNumber: "40000000000000000002", BankName: "Сибирский филиал АО \"Третий банк\""},
		},
		"ignores operation rows": {
			blocks: []pdf.TextBlock{{X: 100, Y: 700, Text: "Выписка по счету клиента"}, {X: 50, Y: 670, Text: "Номер счета: 40000000000000000002"}, {X: 30, Y: 300, Text: "40000000000000000003 Банк контрагента"}, {X: 30, Y: 280, Text: "Банк получателя: Иной банк"}},
			want:   StatementInfo{AccountNumber: "40000000000000000002"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := detectStatementInfoBlocks(test.blocks)
			if err != nil || got != test.want {
				t.Fatalf("detectStatementInfoBlocks() = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestDetectStatementInfoBlocksRejectsNonStatementAndAmbiguity(t *testing.T) {
	_, err := detectStatementInfoBlocks([]pdf.TextBlock{{Text: "ПЛАТЕЖНОЕ ПОРУЧЕНИЕ № 1"}, {Text: "40000000000000000002"}})
	if !errors.Is(err, ErrStatementInfoNotDetected) {
		t.Fatalf("payment form error = %v", err)
	}
	_, err = detectStatementInfoBlocks([]pdf.TextBlock{{Text: "Account statement"}, {Text: "Account number: 40000000000000000002"}, {Text: "Номер счета: 40000000000000000001"}})
	if !errors.Is(err, ErrAmbiguousStatementInfo) {
		t.Fatalf("ambiguous header error = %v", err)
	}
}

func TestDetectStatementInfoBlocksRequiresValidAccount(t *testing.T) {
	_, err := detectStatementInfoBlocks([]pdf.TextBlock{{Text: "Account statement"}, {Text: "Account number: 123"}})
	if !errors.Is(err, ErrStatementInfoNotDetected) {
		t.Fatalf("invalid account error = %v", err)
	}
}
