// Package paymentpdf extracts payment documents from positioned PDF text blocks.
package paymentpdf

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/szonov/bankparse/payment"
	"github.com/szonov/pdf"
)

var (
	documentTitleRE = regexp.MustCompile(`(?i)(ПЛАТЕЖНОЕ\s+ПОРУЧЕНИЕ|ПЛАТЕЖНЫЙ\s+ОРДЕР|ПЛАТЕЖНОЕ\s+ТРЕБОВАНИЕ|ИНКАССОВОЕ\s+ПОРУЧЕНИЕ|БАНКОВСКИЙ\s+ОРДЕР)\s*(?:№|N)\s*([^\s]+)`)
	dateRE          = regexp.MustCompile(`\b(\d{2}\.\d{2}\.\d{4})\b`)
	accountRE       = regexp.MustCompile(`\b\d{20}\b`)
	bikRE           = regexp.MustCompile(`\b\d{9}\b`)
	innRE           = regexp.MustCompile(`(?i)ИНН\s*(\d{12}|\d{10})`)
	kppRE           = regexp.MustCompile(`(?i)КПП\s*(\d{9})`)
	innValueRE      = regexp.MustCompile(`^(\d{10}|\d{12})$`)
	kppValueRE      = regexp.MustCompile(`^\d{9}$`)
	taxIDValueRE    = regexp.MustCompile(`^(?:\d{9}|\d{10}|\d{12})$`)
	kppZeroPrefixRE = regexp.MustCompile(`(?i)^КПП\s*0\s*`)
	budgetCodeRE    = regexp.MustCompile(`^\d{20}$`)
	oktmoRE         = regexp.MustCompile(`^\d{8}$`)
	payerStatusRE   = regexp.MustCompile(`^\d{2}$`)
	nineDigitsRE    = regexp.MustCompile(`^\d{9}$`)
	moneyRE         = regexp.MustCompile(`^\s*([0-9][0-9 ]*)(?:[,.\-]([0-9]{2})|=)?\s*$`)
)

// ErrDocumentCountMismatch reports that a statement summary and the following
// complete sequence of document forms contain different operation counts.
var ErrDocumentCountMismatch = errors.New("PDF document count does not match statement summary")

// ErrDocumentTotalsMismatch reports that parsed document amounts do not add up
// to the debit and credit turnovers printed in the statement summary.
var ErrDocumentTotalsMismatch = errors.New("PDF document totals do not match statement summary")

type statementSummary struct {
	Count  int
	Debit  int64
	Credit int64
}

// DocumentFunc receives a document as soon as its page has been interpreted.
// Returning an error stops both page interpretation and page-tree traversal.
type PageDocumentFunc func(pageNumber int, document payment.Document) error

// Reader walks payment documents stored as individual PDF forms.
type Reader struct {
	reader *pdf.Reader
}

func New(reader *pdf.Reader) *Reader {
	return &Reader{reader: reader}
}

// WalkDocuments walks the PDF page tree once and extracts individual bank
// document forms. Statement table pages are identified from their initial text
// blocks and skipped without interpreting the rest of their content stream.
func (r *Reader) WalkPageDocuments(executor PageDocumentFunc) error {
	if r == nil || r.reader == nil {
		return errors.New("nil PDF reader")
	}
	if executor == nil {
		return errors.New("nil document executor")
	}
	info, infoErr := DetectStatementInfo(r.reader)
	if infoErr != nil && !errors.Is(infoErr, ErrStatementInfoNotDetected) && !errors.Is(infoErr, ErrAmbiguousStatementInfo) {
		return infoErr
	}
	var summaryPage, documentCount int
	var expected statementSummary
	var debit, credit int64
	err := r.reader.WalkPages(func(pageNumber int, page pdf.Page) error {
		document, found, summary, err := readDocumentPage(page)
		if err != nil {
			return fmt.Errorf("parse bank document on page %d: %w", pageNumber, err)
		}
		if summary.Count > 0 {
			summaryPage, expected = pageNumber, summary
			return nil
		}
		if !found {
			return nil
		}
		if documentCount == 0 && summaryPage != pageNumber-1 {
			expected = statementSummary{}
		}
		documentCount++
		if info.AccountNumber != "" {
			switch {
			case document.Payer.Account == info.AccountNumber:
				debit += document.Amount.Kopecks
			case document.Recipient.Account == info.AccountNumber:
				credit += document.Amount.Kopecks
			}
		}
		if err := executor(pageNumber, document); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if expected.Count > 0 && documentCount != expected.Count {
		return fmt.Errorf("%w: summary has %d, parsed %d", ErrDocumentCountMismatch, expected.Count, documentCount)
	}
	if expected.Count > 0 && info.AccountNumber != "" && (debit != expected.Debit || credit != expected.Credit) {
		return fmt.Errorf("%w: debit summary=%d parsed=%d, credit summary=%d parsed=%d",
			ErrDocumentTotalsMismatch, expected.Debit, debit, expected.Credit, credit)
	}
	return nil
}

// WalkDocuments implements payment.Walker. Returning payment.ErrStop from the
// callback stops both content interpretation and page-tree traversal successfully.
func (r *Reader) WalkDocuments(executor payment.DocumentFunc) error {
	if executor == nil {
		return errors.New("nil document executor")
	}
	err := r.WalkPageDocuments(func(_ int, document payment.Document) error {
		return executor(document)
	})
	if errors.Is(err, payment.ErrStop) {
		return nil
	}
	return err
}

const classificationBlockLimit = 32

func readDocumentPage(page pdf.Page) (payment.Document, bool, statementSummary, error) {
	blocks := make([]pdf.TextBlock, 0, 80)
	found := false
	candidate := false
	err := page.WalkTextBlocks(func(block pdf.TextBlock) error {
		blocks = append(blocks, block)
		text := clean(block.Text)
		if documentTitleRE.MatchString(text) {
			found = true
			return nil
		}
		if isDocumentFormBlock(text) {
			candidate = true
		}
		if !found && (isStatementBlock(text) || (!candidate && len(blocks) >= classificationBlockLimit)) {
			return pdf.ErrStopTextBlocks
		}
		return nil
	})
	if errors.Is(err, pdf.ErrStopTextBlocks) {
		return payment.Document{}, false, statementSummary{}, nil
	}
	if err != nil {
		return payment.Document{}, false, statementSummary{}, err
	}
	if !found {
		return payment.Document{}, false, parseStatementSummary(blocks), nil
	}
	document, err := parseDocument(blocks)
	return document, true, statementSummary{}, err
}

func isDocumentFormBlock(text string) bool {
	text = clean(text)
	if text == "0401060" || text == "0401061" || text == "0401066" || text == "0401067" || text == "0401071" || strings.Contains(text, "Поступ. в банк плат.") {
		return true
	}
	return equalFold(text, "Плательщик") ||
		equalFold(text, "Банк плательщика") ||
		equalFold(text, "Банк получателя") ||
		equalFold(text, "Получатель") ||
		equalFold(text, "Назначение платежа")
}

func isStatementBlock(text string) bool {
	text = strings.ToUpper(clean(text))
	return strings.Contains(text, "ВЫПИСКА ОПЕРАЦИЙ") ||
		strings.Contains(text, "СУММА ПО ДЕБЕТУ") ||
		strings.Contains(text, "СУММА ПО КРЕДИТУ")
}

func parseDocument(blocks []pdf.TextBlock) (payment.Document, error) {
	title, ok := findBlock(blocks, func(text string) bool { return documentTitleRE.MatchString(text) })
	if !ok {
		return payment.Document{}, errors.New("document title is missing")
	}
	match := documentTitleRE.FindStringSubmatch(clean(title.Text))
	document := payment.Document{
		Type:   documentType(match[1]),
		Number: strings.TrimSpace(match[2]),
		Amount: payment.Amount{Currency: "RUB"},
	}

	dateOrigin := title
	if label, found := findBlock(blocks, func(text string) bool { return equalFold(text, "Дата") }); found {
		dateOrigin = label
	}
	dateText := nearestText(blocks, dateOrigin, func(text string) bool { return dateRE.MatchString(text) })
	if dateText == "" {
		return payment.Document{}, errors.New("document date is missing")
	}
	parsedDate, err := time.Parse("02.01.2006", dateRE.FindString(dateText))
	if err != nil {
		return payment.Document{}, fmt.Errorf("document date: %w", err)
	}
	document.Date = parsedDate

	if document.Type == payment.BankOrder {
		err = parseBankOrder(blocks, &document)
	} else {
		err = parsePaymentForm(blocks, &document)
	}
	if err != nil {
		return payment.Document{}, err
	}
	return document, nil
}

func parsePaymentForm(blocks []pdf.TextBlock, document *payment.Document) error {
	payerY, ok := labelY(blocks, "Плательщик")
	if !ok {
		return errors.New("payer section is missing")
	}
	payerBankY, ok := labelY(blocks, "Банк Плательщика")
	if !ok {
		return errors.New("payer bank section is missing")
	}
	recipientBankY, ok := labelY(blocks, "Банк Получателя")
	if !ok {
		return errors.New("recipient bank section is missing")
	}
	recipientY, ok := labelY(blocks, "Получатель")
	if !ok {
		return errors.New("recipient section is missing")
	}
	purposeY, ok := labelY(blocks, "Назначение платежа")
	if !ok {
		return errors.New("payment purpose section is missing")
	}

	payerTop := maxY(blocks) + 1
	for _, block := range blocks {
		if equalFold(block.Text, "Сумма") && block.X > 250 && block.Y > payerY && block.Y < payerTop {
			// Tax identifiers can share the amount label's baseline, so keep
			// the complete line rather than using the label origin as a bound.
			payerTop = block.Y + math.Max(block.Height, 1) + 1
		}
	}
	document.Payer = parseParty(blocks, payerY, payerTop)
	document.Payer.Bank = parseBank(blocks, payerBankY, payerY)
	document.Recipient.Bank = parseBank(blocks, recipientBankY, payerBankY)
	document.Recipient = mergeParty(document.Recipient, parseParty(blocks, recipientY, recipientBankY))
	purposeFilter := func(block pdf.TextBlock) bool {
		return block.X < 570 && !isFormLabel(block.Text)
	}
	// Most forms put the purpose above its caption, while payment warrants and
	// older payment requests put it below.
	if document.Type == payment.PaymentWarrant {
		document.Purpose = contiguousRegionText(blocks, purposeY-100, purposeY, purposeFilter)
		if document.Purpose == "" {
			document.Purpose = purposeRegionText(blocks, purposeY, recipientY, purposeFilter)
		}
	} else {
		document.Purpose = purposeRegionText(blocks, purposeY, recipientY, purposeFilter)
		if document.Purpose == "" {
			document.Purpose = contiguousRegionText(blocks, purposeY-100, purposeY, purposeFilter)
		}
	}
	document.Budget = parseBudgetDetails(blocks, purposeY, recipientY, purposeFilter)
	if document.Budget != nil && document.Budget.PayerStatus == "" {
		document.Budget.PayerStatus = findPayerStatus(blocks)
	}
	document.OperationType = valueRightOfLabel(blocks, "Вид оп.", `^\d{2}$`)

	amount, err := findAmount(blocks)
	if err != nil {
		return err
	}
	document.Amount.Kopecks = amount
	return validateDocument(*document)
}

func parseBankOrder(blocks []pdf.TextBlock, document *payment.Document) error {
	payerLabel, ok := findBlock(blocks, func(text string) bool { return equalFold(text, "Плательщик") })
	if !ok {
		return errors.New("payer section is missing")
	}
	recipientLabel, ok := findBlock(blocks, func(text string) bool { return equalFold(text, "Получатель") })
	if !ok {
		return errors.New("recipient section is missing")
	}
	purposeLabel, ok := findBlock(blocks, func(text string) bool { return equalFold(text, "Назначение платежа") })
	if !ok {
		return errors.New("payment purpose section is missing")
	}

	// Bank orders put section values directly below their labels, unlike the
	// standard 0401060/0401061 form where values precede the section caption.
	document.Payer.Name = leftRegionText(blocks, recipientLabel.Y, payerLabel.Y)
	document.Payer.Account = firstPatternInRegion(blocks, recipientLabel.Y, payerLabel.Y, accountRE)
	document.Recipient.Name = leftRegionText(blocks, purposeLabel.Y, recipientLabel.Y)
	document.Recipient.Account = firstPatternInRegion(blocks, purposeLabel.Y, recipientLabel.Y, accountRE)
	document.Purpose = regionText(blocks, purposeLabel.Y-90, purposeLabel.Y, func(block pdf.TextBlock) bool {
		return block.X < 350 && !isFormLabel(block.Text)
	})
	document.OperationType = valueRightOfLabel(blocks, "Вид оп.", `^\d{2}$`)
	amount, err := findAmount(blocks)
	if err != nil {
		return err
	}
	document.Amount.Kopecks = amount
	return validateDocument(*document)
}

func parseParty(blocks []pdf.TextBlock, bottom, top float64) payment.Party {
	var party payment.Party
	party.Account = firstPatternInRegion(blocks, bottom, top, accountRE)
	region := blocksInRegion(blocks, bottom, top)
	for _, block := range region {
		text := clean(block.Text)
		if match := innRE.FindStringSubmatch(text); len(match) > 0 {
			party.INN = match[1]
		}
		if match := kppRE.FindStringSubmatch(text); len(match) > 0 {
			party.KPP = match[1]
		}
	}
	if party.INN == "" {
		party.INN = valueForLabel(region, "ИНН", innValueRE)
	}
	if party.KPP == "" {
		party.KPP = valueForLabel(region, "КПП", kppValueRE)
	}
	party.Name = regionText(blocks, bottom, top, func(block pdf.TextBlock) bool {
		text := clean(block.Text)
		return block.X < 275 && !innRE.MatchString(text) && !kppRE.MatchString(text) &&
			!taxIDValueRE.MatchString(text) && !isFormLabel(text) && !accountRE.MatchString(text)
	})
	// Individual entrepreneurs have no KPP. Some forms render the empty value
	// as "КПП 0" in the same text block as the party name.
	party.Name = clean(kppZeroPrefixRE.ReplaceAllString(party.Name, ""))
	return party
}

func valueForLabel(blocks []pdf.TextBlock, label string, valuePattern *regexp.Regexp) string {
	for _, block := range blocks {
		if !equalFold(block.Text, label) {
			continue
		}
		candidate := nearestBlock(blocks, block, func(candidate pdf.TextBlock) bool {
			return candidate.X > block.X && valuePattern.MatchString(clean(candidate.Text))
		})
		if candidate.Text != "" && math.Abs(candidate.Y-block.Y) <= 5 {
			return clean(candidate.Text)
		}
	}
	return ""
}

func parseBank(blocks []pdf.TextBlock, bottom, top float64) payment.Bank {
	var bank payment.Bank
	bank.Account = firstPatternInRegion(blocks, bottom, top, accountRE)
	bank.BIK = firstPatternInRegion(blocks, bottom, top, bikRE)
	bank.Name = regionText(blocks, bottom, top, func(block pdf.TextBlock) bool {
		text := clean(block.Text)
		return block.X < 275 && !isFormLabel(text) && !accountRE.MatchString(text) && !bikRE.MatchString(text)
	})
	return bank
}

func mergeParty(base, values payment.Party) payment.Party {
	values.Bank = base.Bank
	return values
}

func validateDocument(document payment.Document) error {
	switch {
	case document.Number == "":
		return errors.New("document number is missing")
	case document.Amount.Kopecks <= 0:
		return errors.New("document amount is missing")
	case document.Payer.Account == "":
		return errors.New("payer account is missing")
	case document.Purpose == "":
		return errors.New("payment purpose is missing")
	}
	return nil
}

func findAmount(blocks []pdf.TextBlock) (int64, error) {
	for _, label := range blocks {
		if !equalFold(label.Text, "Сумма") || label.X < 250 {
			continue
		}
		candidate := nearestBlock(blocks, label, func(block pdf.TextBlock) bool {
			// The payment priority is printed just above the amount column on
			// bank orders. Its value (usually "5") can be geometrically closer
			// to the amount label than the actual amount below the label.
			return block != label && block.Y <= label.Y+2 && moneyRE.MatchString(clean(block.Text))
		})
		if candidate.Text != "" {
			return parseMoney(candidate.Text)
		}
	}
	return 0, errors.New("document amount is missing")
}

func parseMoney(text string) (int64, error) {
	match := moneyRE.FindStringSubmatch(clean(text))
	if match == nil {
		return 0, fmt.Errorf("invalid amount %q", text)
	}
	rubles, err := strconv.ParseInt(strings.ReplaceAll(match[1], " ", ""), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q: %w", text, err)
	}
	kopecks := int64(0)
	if match[2] != "" {
		kopecks, err = strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid amount %q: %w", text, err)
		}
	}
	return rubles*100 + kopecks, nil
}

func documentType(title string) payment.Type {
	title = strings.ToUpper(title)
	switch {
	case strings.Contains(title, "ИНКАССОВОЕ"):
		return payment.CollectionOrder
	case strings.Contains(title, "ПЛАТЕЖНЫЙ") && strings.Contains(title, "ОРДЕР"):
		return payment.PaymentWarrant
	case strings.Contains(title, "ПОРУЧЕНИЕ"):
		return payment.PaymentOrder
	case strings.Contains(title, "ТРЕБОВАНИЕ"):
		return payment.PaymentRequest
	default:
		return payment.BankOrder
	}
}

func parseStatementSummary(blocks []pdf.TextBlock) statementSummary {
	countText := summaryCell(blocks, "Количество операций", "Всего")
	debitText := summaryCell(blocks, "Итого оборотов", "Дебет")
	creditText := summaryCell(blocks, "Итого оборотов", "Кредит")
	count, countErr := strconv.Atoi(countText)
	debit, debitErr := parseMoney(debitText)
	credit, creditErr := parseMoney(creditText)
	if countErr != nil || debitErr != nil || creditErr != nil || count <= 0 {
		return statementSummary{}
	}
	return statementSummary{Count: count, Debit: debit, Credit: credit}
}

func summaryCell(blocks []pdf.TextBlock, rowText, columnText string) string {
	row, rowOK := findBlock(blocks, func(text string) bool { return equalFold(text, rowText) })
	column, columnOK := findBlock(blocks, func(text string) bool { return equalFold(text, columnText) })
	debit, debitOK := findBlock(blocks, func(text string) bool { return equalFold(text, "Дебет") })
	credit, creditOK := findBlock(blocks, func(text string) bool { return equalFold(text, "Кредит") })
	if !rowOK || !columnOK || !debitOK || !creditOK {
		return ""
	}
	rotated := math.Abs(debit.X-credit.X) <= 2
	for _, block := range blocks {
		if rotated && math.Abs(block.X-row.X) <= 2 && math.Abs(block.Y-column.Y) <= 2 {
			return clean(block.Text)
		}
		if !rotated && math.Abs(block.X-column.X) <= 2 && math.Abs(block.Y-row.Y) <= 2 {
			return clean(block.Text)
		}
	}
	return ""
}

func valueRightOfLabel(blocks []pdf.TextBlock, label, pattern string) string {
	re := regexp.MustCompile(pattern)
	for _, block := range blocks {
		if !equalFold(block.Text, label) {
			continue
		}
		nearest := nearestBlock(blocks, block, func(candidate pdf.TextBlock) bool {
			return candidate.X > block.X && re.MatchString(clean(candidate.Text))
		})
		return clean(nearest.Text)
	}
	return ""
}

func nearestText(blocks []pdf.TextBlock, origin pdf.TextBlock, accept func(string) bool) string {
	return clean(nearestBlock(blocks, origin, func(block pdf.TextBlock) bool { return accept(clean(block.Text)) }).Text)
}

func nearestBlock(blocks []pdf.TextBlock, origin pdf.TextBlock, accept func(pdf.TextBlock) bool) pdf.TextBlock {
	var result pdf.TextBlock
	best := 1e18
	for _, block := range blocks {
		if !accept(block) {
			continue
		}
		dx, dy := block.X-origin.X, block.Y-origin.Y
		distance := dx*dx + dy*dy
		if distance < best {
			best, result = distance, block
		}
	}
	return result
}

func labelY(blocks []pdf.TextBlock, label string) (float64, bool) {
	block, ok := findBlock(blocks, func(text string) bool { return equalFold(text, label) })
	return block.Y, ok
}

func findBlock(blocks []pdf.TextBlock, accept func(string) bool) (pdf.TextBlock, bool) {
	for _, block := range blocks {
		if accept(clean(block.Text)) {
			return block, true
		}
	}
	return pdf.TextBlock{}, false
}

func firstPatternInRegion(blocks []pdf.TextBlock, bottom, top float64, pattern *regexp.Regexp) string {
	for _, block := range blocksInRegion(blocks, bottom, top) {
		if match := pattern.FindString(clean(block.Text)); match != "" {
			return match
		}
	}
	return ""
}

func leftRegionText(blocks []pdf.TextBlock, bottom, top float64) string {
	return regionText(blocks, bottom, top, func(block pdf.TextBlock) bool {
		text := clean(block.Text)
		return block.X < 220 && !accountRE.MatchString(text) && !isFormLabel(text)
	})
}

func regionText(blocks []pdf.TextBlock, bottom, top float64, accept func(pdf.TextBlock) bool) string {
	region := blocksInRegion(blocks, bottom, top)
	var values []string
	for _, block := range region {
		if accept(block) {
			values = append(values, clean(block.Text))
		}
	}
	return clean(strings.Join(values, " "))
}

// purposeRegionText removes the separate budget-payment details row (KBK,
// OKTMO, basis, tax period, document date and type) rendered immediately above
// the actual purpose on payment orders. Those fields are not purpose text.
func purposeRegionText(blocks []pdf.TextBlock, bottom, top float64, accept func(pdf.TextBlock) bool) string {
	region := blocksInRegion(blocks, bottom, top)
	filtered := make([]pdf.TextBlock, 0, len(region))
	for _, block := range region {
		if accept(block) {
			filtered = append(filtered, block)
		}
	}
	if len(filtered) == 0 {
		return ""
	}

	// A UIN can be rendered on a separate line immediately before the other
	// budget fields. Find the budget row and remove the whole service prefix,
	// not just the first visual line in the purpose region.
	for start := 0; start < len(filtered); {
		lineY := filtered[start].Y
		end := start
		values := make([]string, 0, 8)
		for end < len(filtered) && math.Abs(filtered[end].Y-lineY) <= 2 {
			values = append(values, clean(filtered[end].Text))
			end++
		}
		if budgetRowStart(splitMergedBudgetValues(values)) >= 0 {
			filtered = filtered[end:]
			break
		}
		start = end
	}

	values := make([]string, 0, len(filtered))
	for _, block := range filtered {
		values = append(values, clean(block.Text))
	}
	return clean(strings.Join(values, " "))
}

func parseBudgetDetails(blocks []pdf.TextBlock, bottom, top float64, accept func(pdf.TextBlock) bool) *payment.BudgetDetails {
	region := blocksInRegion(blocks, bottom, top)
	for start := 0; start < len(region); {
		lineY := region[start].Y
		end := start
		values := make([]string, 0, 8)
		for end < len(region) && math.Abs(region[end].Y-lineY) <= 2 {
			if accept(region[end]) {
				if value := clean(region[end].Text); value != "" {
					values = append(values, value)
				}
			}
			end++
		}

		values = splitMergedBudgetValues(values)
		kbkIndex := budgetRowStart(values)
		if kbkIndex >= 0 {
			details := &payment.BudgetDetails{}
			if kbkIndex > 0 && payerStatusRE.MatchString(values[kbkIndex-1]) {
				details.PayerStatus = values[kbkIndex-1]
			}
			details.KBK = valueAt(values, kbkIndex)
			details.OKTMO = valueAt(values, kbkIndex+1)
			details.Basis = valueAt(values, kbkIndex+2)
			details.TaxPeriod = valueAt(values, kbkIndex+3)
			details.DocumentNumber = valueAt(values, kbkIndex+4)
			details.DocumentDate = valueAt(values, kbkIndex+5)
			details.PaymentType = valueAt(values, kbkIndex+6)
			return details
		}
		start = end
	}
	return nil
}

func budgetRowStart(values []string) int {
	for i, value := range values {
		if budgetCodeRE.MatchString(value) && i+1 < len(values) && (oktmoRE.MatchString(values[i+1]) || values[i+1] == "0") {
			return i
		}
	}
	return -1
}

func splitMergedBudgetValues(values []string) []string {
	for i := 1; i < len(values); i++ {
		// New SberBusiness forms place an empty-space-free one-character
		// basis immediately after the eight-digit OKTMO. The text interpreter
		// correctly coalesces them into one block, so split by field widths.
		if nineDigitsRE.MatchString(values[i]) && budgetCodeRE.MatchString(values[i-1]) {
			result := make([]string, 0, len(values)+1)
			result = append(result, values[:i]...)
			result = append(result, values[i][:8], values[i][8:])
			result = append(result, values[i+1:]...)
			return result
		}
	}
	return values
}

func findPayerStatus(blocks []pdf.TextBlock) string {
	title, found := findBlock(blocks, func(text string) bool { return documentTitleRE.MatchString(text) })
	if !found {
		return ""
	}
	for _, block := range blocks {
		if block.X > title.X+title.Width && math.Abs(block.Y-title.Y) <= 6 && payerStatusRE.MatchString(clean(block.Text)) {
			return clean(block.Text)
		}
	}
	return ""
}

func valueAt(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}

// contiguousRegionText reads the first visual paragraph in a region. It stops
// at a larger vertical gap so footer fields below a purpose do not become part
// of the purpose merely because they are in the same broad coordinate range.
func contiguousRegionText(blocks []pdf.TextBlock, bottom, top float64, accept func(pdf.TextBlock) bool) string {
	region := blocksInRegion(blocks, bottom, top)
	values := make([]string, 0, len(region))
	var previous *pdf.TextBlock
	for i := range region {
		block := &region[i]
		if !accept(*block) {
			continue
		}
		if previous != nil {
			maximumGap := math.Max(15, previous.Height*2)
			if previous.Y-block.Y > maximumGap {
				break
			}
		}
		values = append(values, clean(block.Text))
		previous = block
	}
	return clean(strings.Join(values, " "))
}

func blocksInRegion(blocks []pdf.TextBlock, bottom, top float64) []pdf.TextBlock {
	region := make([]pdf.TextBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Y > bottom+0.5 && block.Y < top-0.5 {
			region = append(region, block)
		}
	}
	sort.SliceStable(region, func(i, j int) bool {
		if difference := region[i].Y - region[j].Y; difference > 2 || difference < -2 {
			return region[i].Y > region[j].Y
		}
		return region[i].X < region[j].X
	})
	return region
}

func maxY(blocks []pdf.TextBlock) float64 {
	maximum := 0.0
	for _, block := range blocks {
		if block.Y > maximum {
			maximum = block.Y
		}
	}
	return maximum
}

func isFormLabel(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(clean(text)))
	for _, label := range []string{
		"плательщик", "получатель", "банк плательщика", "банк получателя",
		"сч. №", "сч.№", "бик", "сумма", "инн", "кпп", "вид оп.",
		"наз. пл.", "назначение платежа", "код", "срок плат.", "очер. плат.", "рез. поле",
	} {
		if normalized == label {
			return true
		}
	}
	return false
}

func equalFold(left, right string) bool { return strings.EqualFold(clean(left), clean(right)) }

func clean(value string) string {
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) }), " ")
}
