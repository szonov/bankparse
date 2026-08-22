package paymentpdf

import (
	"testing"

	"github.com/szonov/bankparse/payment"
	"github.com/szonov/pdf"
)

func TestParsePaymentForm(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(60, 730, "ПЛАТЕЖНОЕ ПОРУЧЕНИЕ № 21"),
		block(83, 760, "30.01.2026"),
		block(320, 740, "29.01.2026"),
		block(330, 720, "Дата"),
		block(281, 645, "Сумма"), block(332, 644, "400000="),
		block(60, 643, "ИНН5000000001"), block(180, 643, "КПП500000002"),
		block(61, 628, "ООО ТЕСТОВЫЙ ПЛАТЕЛЬЩИК"), block(332, 605, "40000000000000000002"),
		block(60, 584, "Плательщик"),
		block(61, 569, "АО БАНК ПЛАТЕЛЬЩИКА"), block(332, 569, "040000002"), block(332, 555, "30100000000000000002"),
		block(60, 539, "Банк Плательщика"),
		block(61, 524, "АО ТЕСТОВЫЙ БАНК"), block(332, 524, "040000001"), block(332, 510, "30100000000000000001"),
		block(60, 494, "Банк Получателя"),
		block(60, 479, "ИНН5000000000"), block(180, 479, "КПП500000001"), block(332, 480, "40000000000000000001"),
		block(61, 464, "ООО ТЕСТОВЫЙ ПОЛУЧАТЕЛЬ"),
		block(281, 444, "Вид оп."), block(332, 444, "01"),
		block(60, 414, "Получатель"),
		block(61, 384, "Оплата по договору"), block(61, 373, "НДС не облагается"),
		block(60, 344, "Назначение платежа"),
	}

	document, err := parseDocument(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if document.Type != payment.PaymentOrder || document.Number != "21" {
		t.Fatalf("unexpected identity: type=%q number=%q", document.Type, document.Number)
	}
	if got := document.Date.Format("02.01.2006"); got != "29.01.2026" {
		t.Fatalf("unexpected document date: %s", got)
	}
	if document.Amount.Kopecks != 40_000_000 || document.Amount.Currency != "RUB" {
		t.Fatalf("unexpected amount: %+v", document.Amount)
	}
	if document.Payer.INN != "5000000001" || document.Payer.Account != "40000000000000000002" {
		t.Fatalf("unexpected payer: %+v", document.Payer)
	}
	if document.Recipient.INN != "5000000000" || document.Recipient.Bank.BIK != "040000001" {
		t.Fatalf("unexpected recipient: %+v", document.Recipient)
	}
	if document.OperationType != "01" || document.Purpose != "Оплата по договору НДС не облагается" {
		t.Fatalf("unexpected payment details: operation=%q purpose=%q", document.OperationType, document.Purpose)
	}
}

func TestParsePaymentRequestPurposeBelowLabel(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 700, "ПЛАТЕЖНОЕ ТРЕБОВАНИЕ № 972"), block(320, 700, "31.12.2014"),
		block(302, 625, "Сумма"), block(350, 625, "252-82"),
		block(54, 624, "ИНН 5000000000"), block(350, 588, "40000000000000000001"),
		block(54, 566, "Плательщик"),
		block(54, 550, "Банк плательщика один"), block(350, 550, "040000001"), block(350, 538, "30100000000000000001"),
		block(54, 526, "Банк Плательщика"),
		block(54, 514, "Банк получателя один"), block(350, 514, "040000001"), block(350, 502, "30100000000000000001"),
		block(54, 489, "Банк Получателя"),
		block(54, 477, "ИНН 5000000001"), block(350, 477, "40000000000000000002"), block(54, 460, "АО Тестовый банк"),
		block(302, 440, "Вид оп."), block(350, 440, "02"), block(54, 416, "Получатель"),
		block(54, 405, "Назначение платежа"), block(54, 391, "Оплата комиссии"), block(54, 380, "НДС не облагается"),
		block(54, 350, "Дата отсылки документов"),
	}
	document, err := parseDocument(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if document.Type != payment.PaymentRequest || document.Purpose != "Оплата комиссии НДС не облагается" {
		t.Fatalf("unexpected document: %+v", document)
	}
}

func TestParseCollectionOrder(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 700, "ИНКАССОВОЕ ПОРУЧЕНИЕ № 731"), block(320, 700, "31.01.2026"),
		block(302, 625, "Сумма"), block(350, 625, "987-65"),
		block(54, 624, "ИНН 5000000000"), block(350, 588, "40000000000000000001"), block(54, 566, "Плательщик"),
		block(54, 550, "АО Банк плательщика"), block(350, 550, "040000001"), block(350, 538, "30100000000000000001"), block(54, 526, "Банк Плательщика"),
		block(54, 514, "АО Банк получателя"), block(350, 514, "040000002"), block(350, 502, "30100000000000000002"), block(54, 489, "Банк Получателя"),
		block(54, 477, "ИНН 5000000001"), block(350, 477, "40000000000000000002"), block(54, 460, "ООО Тестовый получатель"),
		block(302, 440, "Вид оп."), block(350, 440, "06"), block(54, 416, "Получатель"),
		block(54, 391, "Взыскание по синтетическому решению"), block(54, 380, "НДС не облагается"), block(54, 350, "Назначение платежа"),
	}
	document, err := parseDocument(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if document.Type != payment.CollectionOrder || document.Number != "731" || document.Amount.Kopecks != 98_765 {
		t.Fatalf("unexpected collection order: %+v", document)
	}
}

func TestParsePartyWithSeparateTaxIDBlocks(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 624, "ИНН"), block(82, 624, "5000000000"),
		block(180, 624, "КПП"), block(210, 624, "500000001"),
		block(54, 610, "ООО Тестовый клиент"), block(350, 590, "40000000000000000001"),
	}
	party := parseParty(blocks, 570, 640)
	if party.INN != "5000000000" || party.KPP != "500000001" || party.Name != "ООО Тестовый клиент" {
		t.Fatalf("unexpected party: %+v", party)
	}
}

func TestParsePartyPrefersFormAccountColumn(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 624, "ИНН 5000000000"),
		block(32, 610, "Дополнительные сведения 40000000000000000009"),
		block(385, 590, "40000000000000000001"),
	}
	party := parseParty(blocks, 570, 640)
	if party.Account != "40000000000000000001" {
		t.Fatalf("account=%q", party.Account)
	}
}

func TestParsePartyRemovesZeroKPPFromEntrepreneurName(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 624, "ИНН"), block(82, 624, "500000000001"),
		block(180, 610, `КПП0 ИП "Тестовый Предприниматель"`),
		block(350, 590, "40000000000000000003"),
	}
	party := parseParty(blocks, 570, 640)
	if party.INN != "500000000001" || party.KPP != "" || party.Name != `ИП "Тестовый Предприниматель"` {
		t.Fatalf("unexpected party: %+v", party)
	}
}

func TestParseBankOrder(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(60, 820, "02.03.2026"), block(30, 806, "БАНКОВСКИЙ ОРДЕР № 460857"),
		block(277, 806, "01.10.2015"), block(290, 790, "Дата"),
		block(451, 698, "Сумма"), block(430, 680, "3000="), block(407, 750, "Вид оп."), block(491, 750, "17"),
		block(407, 716, "Очер. плат."), block(491, 716, "5"),
		block(111, 698, "Плательщик"), block(32, 680, "ООО Тестовый клиент"), block(247, 680, "40000000000000000001"),
		block(112, 640, "Получатель"), block(32, 622, "АО ТЕСТОВЫЙ ПОЛУЧАТЕЛЬ"), block(247, 622, "40000000000000000004"),
		block(154, 598, "Назначение платежа"), block(32, 582, "Комиссия за обслуживание"), block(32, 570, "НДС не взимается"),
	}
	document, err := parseDocument(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if document.Type != payment.BankOrder || document.Amount.Kopecks != 300_000 || document.OperationType != "17" {
		t.Fatalf("unexpected bank order: %+v", document)
	}
}

func TestParseCombinedTwelveDigitINN(t *testing.T) {
	party := parseParty([]pdf.TextBlock{
		block(54, 620, "ИНН 500000000001"), block(54, 605, "ИП Тестовый"),
		block(350, 590, "40000000000000000003"),
	}, 570, 640)
	if party.INN != "500000000001" {
		t.Fatalf("unexpected INN: %q", party.INN)
	}
}

func TestStatementMarkers(t *testing.T) {
	for _, text := range []string{"ВЫПИСКА ОПЕРАЦИЙ ПО ЛИЦЕВОМУ СЧЕТУ", "Сумма по дебету", "Сумма по кредиту"} {
		if !isStatementBlock(text) {
			t.Errorf("statement marker was not recognized: %q", text)
		}
	}
	if isStatementBlock("ПЛАТЕЖНОЕ ПОРУЧЕНИЕ") {
		t.Fatal("document title recognized as a statement")
	}
}

func TestDocumentFormMarkersBeforeDelayedTitle(t *testing.T) {
	for _, text := range []string{
		"0401071",
		"0401066",
		"Плательщик",
		"Банк плательщика",
		"Банк получателя",
		"Получатель",
		"Назначение платежа",
	} {
		if !isDocumentFormBlock(text) {
			t.Errorf("document form marker was not recognized: %q", text)
		}
	}
	if isDocumentFormBlock("Сумма по дебету") {
		t.Fatal("statement marker recognized as a document form")
	}
}

func TestParsePaymentWarrant(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 700, "ПЛАТЕЖНЫЙ ОРДЕР № 107"), block(320, 700, "31.01.2026"),
		block(302, 625, "Сумма"), block(350, 625, "12345-67"),
		block(54, 624, "ИНН 5000000000"), block(350, 588, "40000000000000000001"), block(54, 566, "Плательщик"),
		block(54, 550, "АО Банк плательщика"), block(350, 550, "040000001"), block(350, 538, "30100000000000000001"), block(54, 526, "Банк Плательщика"),
		block(54, 514, "АО Банк получателя"), block(350, 514, "040000002"), block(350, 502, "30100000000000000002"), block(54, 489, "Банк Получателя"),
		block(54, 477, "ИНН 5000000001"), block(350, 477, "40000000000000000002"), block(54, 460, "УФК по Тестовой области"),
		block(302, 440, "Вид оп."), block(350, 440, "16"), block(54, 416, "Получатель"),
		block(54, 405, "Назначение платежа"), block(54, 391, "Синтетический налоговый платеж"), block(54, 380, "НДС не облагается"),
	}
	document, err := parseDocument(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if document.Type != payment.PaymentWarrant || document.Purpose != "Синтетический налоговый платеж НДС не облагается" {
		t.Fatalf("unexpected document: %+v", document)
	}
}

func TestParseRotatedStatementSummary(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(70, 33, "Количество операций"), block(110, 33, "Итого оборотов"),
		block(40, 170, "Дебет"), block(40, 390, "Кредит"),
		block(20, 610, "Всего"),
		block(70, 170, "7"), block(70, 390, "3"), block(70, 610, "10"),
		block(110, 170, "12 345,67"), block(110, 390, "76 543,21"),
	}
	want := statementSummary{Count: 10, Debit: 1_234_567, Credit: 7_654_321}
	if got := parseStatementSummary(blocks); got != want {
		t.Fatalf("unexpected statement summary: %+v", got)
	}
}

func TestDocumentTitleWithLatinN(t *testing.T) {
	match := documentTitleRE.FindStringSubmatch("ПЛАТЕЖНОЕ ПОРУЧЕНИЕ N 21")
	if len(match) != 3 || match[2] != "21" {
		t.Fatalf("unexpected title match: %#v", match)
	}
}

func TestParseMoney(t *testing.T) {
	for input, want := range map[string]int64{"400000=": 40_000_000, "252-82": 25_282, "5 863,24": 586_324} {
		got, err := parseMoney(input)
		if err != nil || got != want {
			t.Errorf("parseMoney(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
}

func TestPurposeRegionTextSkipsBudgetDetailsRow(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 400, "10000000000000000001"), block(200, 400, "50000001"),
		block(280, 400, "ТП"), block(320, 400, "МС.09.2014"), block(450, 400, "15.10.2014"),
		block(54, 385, "НДС не облагается"),
	}
	got := purposeRegionText(blocks, 370, 410, func(pdf.TextBlock) bool { return true })
	if got != "НДС не облагается" {
		t.Fatalf("unexpected purpose: %q", got)
	}
}

func TestPurposeRegionTextSkipsUINBeforeBudgetDetailsRow(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(450, 410, "1234567890"),
		block(54, 400, "10000000000000000001"), block(200, 400, "50000001"),
		block(280, 400, "0"), block(320, 400, "0"), block(410, 400, "0"), block(450, 400, "0"),
		block(54, 385, "Тестовое назначение платежа"),
	}
	got := purposeRegionText(blocks, 370, 420, func(pdf.TextBlock) bool { return true })
	if got != "Тестовое назначение платежа" {
		t.Fatalf("unexpected purpose: %q", got)
	}
}

func TestParseBudgetDetails(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(30, 400, "01"), block(54, 400, "10000000000000000001"), block(200, 400, "50000001"),
		block(280, 400, "ТП"), block(320, 400, "МС.09.2014"), block(410, 400, "0"),
		block(450, 400, "15.10.2014"), block(530, 400, "0"),
	}
	got := parseBudgetDetails(blocks, 390, 410, func(pdf.TextBlock) bool { return true })
	if got == nil {
		t.Fatal("budget details were not parsed")
	}
	if got.PayerStatus != "01" || got.KBK != "10000000000000000001" || got.OKTMO != "50000001" ||
		got.Basis != "ТП" || got.TaxPeriod != "МС.09.2014" || got.DocumentNumber != "0" ||
		got.DocumentDate != "15.10.2014" || got.PaymentType != "0" {
		t.Fatalf("unexpected budget details: %+v", got)
	}
}

func TestParseMergedOKTMOAndBasis(t *testing.T) {
	blocks := []pdf.TextBlock{
		block(54, 400, "10000000000000000002"), block(200, 400, "500000000"),
		block(320, 400, "МС.01.2026"), block(410, 400, "0"), block(450, 400, "0"),
	}
	got := parseBudgetDetails(blocks, 390, 410, func(pdf.TextBlock) bool { return true })
	if got == nil || got.OKTMO != "50000000" || got.Basis != "0" || got.TaxPeriod != "МС.01.2026" {
		t.Fatalf("unexpected budget details: %+v", got)
	}
}

func TestFindPayerStatusNextToDocumentTitle(t *testing.T) {
	blocks := []pdf.TextBlock{
		{X: 60, Y: 731, Width: 160, Text: "ПЛАТЕЖНОЕ ПОРУЧЕНИЕ № 3"},
		block(540, 735, "01"), block(332, 443, "01"),
	}
	if got := findPayerStatus(blocks); got != "01" {
		t.Fatalf("unexpected payer status: %q", got)
	}
}

func block(x, y float64, text string) pdf.TextBlock {
	return pdf.TextBlock{X: x, Y: y, Text: text}
}
