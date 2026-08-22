package bankexchange

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/szonov/bankparse/payment"
	"golang.org/x/text/encoding/charmap"
)

type smallChunkReader struct {
	reader *bytes.Reader
	size   int
}

func (r *smallChunkReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.size {
		buffer = buffer[:r.size]
	}
	return r.reader.Read(buffer)
}

func TestParseUTF8(t *testing.T) {
	e, err := Parse([]byte("1CClientBankExchange\nВерсияФормата=1.03\nКодировка=UTF-8\nРасчСчет=40700000000000000001\nКонецФайла\n"))
	if err != nil || e.Account != "40700000000000000001" {
		t.Fatalf("parse: %#v %v", e, err)
	}
}

func TestParseDocumentToBankDocument(t *testing.T) {
	source := `1CClientBankExchange
ВерсияФормата=1.03
Кодировка=UTF-8
РасчСчет=40000000000000000001
СекцияДокумент=Платежное поручение
Номер=3
Дата=04.02.2026
Сумма=27957.05
Плательщик=ООО "Тестовый клиент"
ПлательщикИНН=5000000000
ПлательщикКПП=500000001
ПлательщикРасчСчет=40000000000000000001
ПлательщикБанк1=АО ТЕСТОВЫЙ БАНК
ПлательщикБИК=040000001
ПлательщикКорсчет=30100000000000000001
Получатель=ТЕСТОВЫЙ ПОЛУЧАТЕЛЬ
ПолучательИНН=5000000002
ПолучательКПП=500000002
ПолучательРасчСчет=40000000000000000002
ПолучательБанк1=АО БАНК ПОЛУЧАТЕЛЯ
ПолучательБИК=040000002
ПолучательКорсчет=30100000000000000002
ВидОплаты=01
СтатусСоставителя=01
ПоказательКБК=10000000000000000001
ОКАТО=50000001
ПоказательОснования=0
ПоказательПериода=МС.01.2026
ПоказательНомера=0
ПоказательДаты=0
ПоказательТипа=
НазначениеПлатежа=Единый налоговый платеж. НДС не облагается.
КонецДокумента
КонецФайла
`
	exchange, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(exchange.Documents) != 1 {
		t.Fatalf("documents=%d", len(exchange.Documents))
	}
	document := exchange.Documents[0]
	if document.Type != payment.PaymentOrder || document.Number != "3" || document.OperationType != "01" {
		t.Fatalf("unexpected identity: %+v", document)
	}
	if document.Amount != (payment.Amount{Kopecks: 2_795_705, Currency: "RUB"}) {
		t.Fatalf("unexpected amount: %+v", document.Amount)
	}
	if document.Payer.INN != "5000000000" || document.Payer.Bank.BIK != "040000001" {
		t.Fatalf("unexpected payer: %+v", document.Payer)
	}
	if document.Recipient.Account != "40000000000000000002" || document.Recipient.Bank.Account != "30100000000000000002" {
		t.Fatalf("unexpected recipient: %+v", document.Recipient)
	}
	if document.Budget == nil || document.Budget.KBK != "10000000000000000001" || document.Budget.Basis != "0" {
		t.Fatalf("unexpected budget details: %+v", document.Budget)
	}
}

func TestParseDocumentWithoutBudget(t *testing.T) {
	source := "1CClientBankExchange\nРасчСчет=40700000000000000001\n" +
		"СекцияДокумент=Банковский ордер\nНомер=1\nДата=01.02.2026\nСумма=1,5\n" +
		"ПлательщикКПП=0\nКонецДокумента\nКонецФайла\n"
	exchange, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	document := exchange.Documents[0]
	if document.Type != payment.BankOrder || document.Amount.Kopecks != 150 || document.Budget != nil {
		t.Fatalf("unexpected document: %+v", document)
	}
	if document.Payer.KPP != "" {
		t.Fatalf("KPP=%q", document.Payer.KPP)
	}
}

func TestParseDocumentDatePriority(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{
			name: "debited date",
			fields: map[string]string{
				"Дата": "01.02.2026", "ДатаПоступило": "02.02.2026", "ДатаСписано": "03.02.2026",
			},
			want: "03.02.2026",
		},
		{
			name: "received date",
			fields: map[string]string{
				"Дата": "01.02.2026", "ДатаПоступило": "02.02.2026", "ДатаСписано": " ",
			},
			want: "02.02.2026",
		},
		{
			name: "document date",
			fields: map[string]string{
				"Дата": "01.02.2026",
			},
			want: "01.02.2026",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]string{
				"СекцияДокумент": "Банковский ордер",
				"Номер":          "7",
				"Сумма":          "10.50",
			}
			for name, value := range test.fields {
				fields[name] = value
			}
			document, err := parseDocument(fields)
			if err != nil {
				t.Fatal(err)
			}
			if got := document.Date.Format("02.01.2006"); got != test.want {
				t.Fatalf("date=%s, want %s", got, test.want)
			}
		})
	}
}

func TestParseNonBankOrderUsesDocumentDate(t *testing.T) {
	fields := map[string]string{
		"СекцияДокумент": "Платежное поручение",
		"Номер":          "8",
		"Дата":           "01.02.2026",
		"ДатаПоступило":  "02.02.2026",
		"ДатаСписано":    "03.02.2026",
		"Сумма":          "10.50",
	}
	document, err := parseDocument(fields)
	if err != nil {
		t.Fatal(err)
	}
	if got := document.Date.Format("02.01.2006"); got != "01.02.2026" {
		t.Fatalf("date=%s, want 01.02.2026", got)
	}
}

func TestParseAmount(t *testing.T) {
	for input, want := range map[string]int64{
		"27957": 2_795_700, "1674.45": 167_445, "1674,4": 167_440, "0.01": 1,
	} {
		amount, err := parseAmount(input)
		if err != nil || amount.Kopecks != want || amount.Currency != "RUB" {
			t.Errorf("parseAmount(%q) = %+v, %v; want %d kopecks", input, amount, err, want)
		}
	}
	for _, input := range []string{"", "-1", "1.234", "1,2.3", "abc"} {
		if _, err := parseAmount(input); err == nil {
			t.Errorf("parseAmount(%q) unexpectedly succeeded", input)
		}
	}
}

func TestParsePaymentWarrantType(t *testing.T) {
	got, err := parseDocumentType("Платежный ордер")
	if err != nil || got != payment.PaymentWarrant {
		t.Fatalf("unexpected document type: %q, %v", got, err)
	}
}

func TestParseLegacyEncodings(t *testing.T) {
	for name, encoding := range map[string]*charmap.Charmap{"windows-1251": charmap.Windows1251, "DOS": charmap.CodePage866} {
		t.Run(name, func(t *testing.T) {
			source := "1CClientBankExchange\nВерсияФормата=1.03\nКодировка=" + name + "\nРасчСчет=40700000000000000001\nКонецФайла\n"
			data, err := encoding.NewEncoder().Bytes([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			exchange, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if exchange.Account != "40700000000000000001" {
				t.Fatalf("account=%q", exchange.Account)
			}
		})
	}
}

func TestReaderWalkDocumentsStopsEarly(t *testing.T) {
	source := `1CClientBankExchange
РасчСчет=40700000000000000001
СекцияДокумент=Банковский ордер
Номер=1
Дата=01.02.2026
Сумма=1
КонецДокумента
СекцияДокумент=Неизвестный документ
КонецДокумента
КонецФайла
`
	reader, err := New(&smallChunkReader{reader: bytes.NewReader([]byte(source)), size: 3})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := reader.WalkDocuments(func(document payment.Document) error {
		count++
		return payment.ErrStop
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("callbacks=%d", count)
	}
}

func TestReaderWalkDocumentsReturnsCallbackError(t *testing.T) {
	source := "1CClientBankExchange\nРасчСчет=40700000000000000001\n" +
		"СекцияДокумент=Банковский ордер\nНомер=1\nДата=01.02.2026\nСумма=1\nКонецДокумента\n"
	reader, err := New(bytes.NewBufferString(source))
	if err != nil {
		t.Fatal(err)
	}
	want := io.ErrClosedPipe
	err = reader.WalkDocuments(func(document payment.Document) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("WalkDocuments() error = %v", err)
	}
}

func TestReaderValidatesAccountTurnovers(t *testing.T) {
	source := `1CClientBankExchange
РасчСчет=40000000000000000001
СекцияРасчСчет
РасчСчет=40000000000000000001
ВсегоПоступило=25.50
ВсегоСписано=10.25
КонецРасчСчет
СекцияДокумент=Платежное поручение
Номер=1
Дата=01.02.2026
Сумма=10.25
ПлательщикРасчСчет=40000000000000000001
ПолучательРасчСчет=40000000000000000002
КонецДокумента
СекцияДокумент=Платежное поручение
Номер=2
Дата=02.02.2026
Сумма=25.50
ПлательщикРасчСчет=40000000000000000003
ПолучательРасчСчет=40000000000000000001
КонецДокумента
КонецФайла
`
	if _, err := Parse([]byte(source)); err != nil {
		t.Fatal(err)
	}
}

func TestReaderSumsRepeatedAccountSectionTurnovers(t *testing.T) {
	source := `1CClientBankExchange
РасчСчет=40000000000000000001
СекцияРасчСчет
РасчСчет=40000000000000000001
ВсегоПоступило=20.00
ВсегоСписано=10.25
КонецРасчСчет
СекцияРасчСчет
РасчСчет=40000000000000000001
ВсегоПоступило=5.50
ВсегоСписано=0
КонецРасчСчет
СекцияДокумент=Платежное поручение
Номер=1
Дата=01.02.2026
Сумма=10.25
ПлательщикРасчСчет=40000000000000000001
ПолучательРасчСчет=40000000000000000002
КонецДокумента
СекцияДокумент=Платежное поручение
Номер=2
Дата=02.02.2026
Сумма=20.00
ПлательщикРасчСчет=40000000000000000003
ПолучательРасчСчет=40000000000000000001
КонецДокумента
СекцияДокумент=Платежное поручение
Номер=3
Дата=03.02.2026
Сумма=5.50
ПлательщикРасчСчет=40000000000000000004
ПолучательРасчСчет=40000000000000000001
КонецДокумента
КонецФайла
`
	if _, err := Parse([]byte(source)); err != nil {
		t.Fatal(err)
	}
}

func TestReaderRejectsAccountTurnoverMismatch(t *testing.T) {
	source := `1CClientBankExchange
РасчСчет=40000000000000000001
СекцияРасчСчет
РасчСчет=40000000000000000001
ВсегоСписано=10.26
КонецРасчСчет
СекцияДокумент=Платежное поручение
Номер=1
Дата=01.02.2026
Сумма=10.25
ПлательщикРасчСчет=40000000000000000001
ПолучательРасчСчет=40000000000000000002
КонецДокумента
КонецФайла
`
	_, err := Parse([]byte(source))
	if !errors.Is(err, ErrDocumentTotalsMismatch) {
		t.Fatalf("Parse() error = %v; want %v", err, ErrDocumentTotalsMismatch)
	}
}

func TestReaderIgnoresMissingAccountTurnovers(t *testing.T) {
	source := `1CClientBankExchange
СекцияРасчСчет
РасчСчет=40000000000000000001
КонецРасчСчет
СекцияДокумент=Банковский ордер
Номер=1
Дата=01.02.2026
Сумма=1
КонецДокумента
КонецФайла
`
	if _, err := Parse([]byte(source)); err != nil {
		t.Fatal(err)
	}
}
