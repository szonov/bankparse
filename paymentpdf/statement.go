package paymentpdf

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/szonov/pdf"
)

var (
	ErrStatementInfoNotDetected = errors.New("PDF statement info not detected")
	ErrAmbiguousStatementInfo   = errors.New("ambiguous PDF statement info")

	statementTitleRE   = regexp.MustCompile(`(?i)(выписка\s+операций\s+по\s+лицевому\s+счету|выписка\s+по\s+счету\s+клиента|account\s+statement)`)
	headerAccountRE    = regexp.MustCompile(`(?i)(?:выписка\s+операций\s+по\s+лицевому\s+счету|(?:номер\s+сч[её]та|account\s+number)(?:\s*/\s*(?:номер\s+сч[её]та|account\s+number))?\s*:?)[^0-9]{0,20}([0-9][0-9\s]{18,24}[0-9])`)
	accountLabelRE     = regexp.MustCompile(`(?i)^(?:номер\s+сч[её]та|account\s+number)(?:\s*/\s*(?:номер\s+сч[её]та|account\s+number))?\s*:?\s*$`)
	bankFieldRE        = regexp.MustCompile(`(?i)^(?:банк|bank)(?:\s*/\s*(?:банк|bank))?\s*(?::\s*(.*))?$`)
	bankOrganizationRE = regexp.MustCompile(`(?i)(?:\bbank\b|банк|кредитн\S*\s+организац)`)
	organizationRE     = regexp.MustCompile(`(?i)(?:\bbank\b|банк|кредитн\S*\s+организац|(?:^|\s)(?:АО|ПАО|ООО)(?:\s|$)|филиал)`)
	quotedBankRE       = regexp.MustCompile(`(?i)(?:АО|ПАО)\s*[«"][^»"]*банк[^»"]*[»"]`)
	statementTableRE   = regexp.MustCompile(`(?i)(?:сумма\s+по\s+дебету|реквизиты\s+корреспондента|counter\s+party\s+details)`)
)

type StatementInfo struct {
	AccountNumber string
	BankName      string
}

// DetectStatementInfo examines only the first PDF page, where a statement-wide
// header must occur. It never derives metadata from transaction rows or forms.
func DetectStatementInfo(reader *pdf.Reader) (StatementInfo, error) {
	if reader == nil {
		return StatementInfo{}, errors.New("nil PDF reader")
	}
	if reader.NumPage() < 1 {
		return StatementInfo{}, ErrStatementInfoNotDetected
	}
	blocks := make([]pdf.TextBlock, 0, 80)
	foundTitle := false
	blocksAfterTitle := 0
	if err := reader.Page(1).WalkTextBlocks(func(block pdf.TextBlock) error {
		if text := cleanStatementText(block.Text); text != "" {
			block.Text = text
			blocks = append(blocks, block)
			if statementTitleRE.MatchString(text) {
				foundTitle = true
			}
			if foundTitle {
				blocksAfterTitle++
				// A statement header is complete before the transaction table.
				// Stopping here is important for old, very large PDF content streams.
				if statementTableRE.MatchString(text) || blocksAfterTitle >= 40 {
					return pdf.ErrStopTextBlocks
				}
			}
		}
		return nil
	}); err != nil && !errors.Is(err, pdf.ErrStopTextBlocks) {
		return StatementInfo{}, fmt.Errorf("read PDF statement header: %w", err)
	}
	return detectStatementInfoBlocks(blocks)
}

func detectStatementInfoBlocks(blocks []pdf.TextBlock) (StatementInfo, error) {
	titleIndex := -1
	for index, block := range blocks {
		if statementTitleRE.MatchString(block.Text) {
			titleIndex = index
			break
		}
	}
	if titleIndex < 0 {
		return StatementInfo{}, ErrStatementInfoNotDetected
	}
	accounts := make(map[string]struct{})
	for index, block := range blocks {
		if match := headerAccountRE.FindStringSubmatch(block.Text); len(match) > 1 {
			if account := normalizeAccount(match[1]); account != "" {
				accounts[account] = struct{}{}
			}
		}
		if accountLabelRE.MatchString(block.Text) {
			if value := accountNearLabel(blocks, index); value != "" {
				accounts[value] = struct{}{}
			}
		}
	}
	// Some banks emit the title and its account number as adjacent blocks.
	// Joining a small window preserves that semantic relationship without
	// considering account numbers from transaction rows.
	for end := titleIndex + 2; end <= titleIndex+4 && end <= len(blocks); end++ {
		text := joinBlockText(blocks[titleIndex:end])
		if match := headerAccountRE.FindStringSubmatch(text); len(match) > 1 {
			if account := normalizeAccount(match[1]); account != "" {
				accounts[account] = struct{}{}
			}
		}
	}
	// Rotated PDFs can emit a visually adjacent account before the title even
	// when the account is drawn to its right. Accept only a standalone account
	// in a narrow window so transaction rows cannot become statement metadata.
	start := max(titleIndex-2, 0)
	end := min(titleIndex+2, len(blocks)-1)
	for index := start; index <= end; index++ {
		if account := normalizeAccount(blocks[index].Text); account != "" {
			accounts[account] = struct{}{}
		}
	}
	if len(accounts) == 0 {
		return StatementInfo{}, ErrStatementInfoNotDetected
	}
	if len(accounts) > 1 {
		return StatementInfo{}, ErrAmbiguousStatementInfo
	}
	info := StatementInfo{}
	for account := range accounts {
		info.AccountNumber = account
	}
	banks := make(map[string]struct{})
	for index, block := range blocks {
		match := bankFieldRE.FindStringSubmatch(block.Text)
		if len(match) == 0 {
			continue
		}
		name := cleanStatementText(match[1])
		if name == "" {
			name = textFollowingLabel(blocks, index)
		}
		if name != "" {
			banks[name] = struct{}{}
		}
	}
	if len(banks) > 1 {
		return StatementInfo{}, ErrAmbiguousStatementInfo
	}
	for bank := range banks {
		info.BankName = bank
	}
	if info.BankName == "" {
		info.BankName = organizationBeforeTitle(blocks, titleIndex)
	}
	return info, nil
}

func accountNearLabel(blocks []pdf.TextBlock, labelIndex int) string {
	start := max(labelIndex-3, 0)
	end := min(labelIndex+3, len(blocks)-1)
	for index := start; index <= end; index++ {
		if account := normalizeAccount(blocks[index].Text); account != "" {
			return account
		}
	}
	return ""
}

func textFollowingLabel(blocks []pdf.TextBlock, labelIndex int) string {
	for index := labelIndex + 1; index < len(blocks) && index <= labelIndex+3; index++ {
		if text := cleanStatementText(blocks[index].Text); text != "" && !isStatementHeaderLabel(text) {
			return text
		}
	}
	return ""
}

func isStatementHeaderLabel(text string) bool {
	return accountLabelRE.MatchString(text) || bankFieldRE.MatchString(text) || statementTitleRE.MatchString(text)
}

func organizationBeforeTitle(blocks []pdf.TextBlock, titleIndex int) string {
	// Prefer text which identifies a bank. A client organization can be the
	// closest block before the title in rotated statement headers.
	if name := organizationBeforeTitleMatching(blocks, titleIndex, bankOrganizationRE); name != "" {
		return name
	}
	return organizationBeforeTitleMatching(blocks, titleIndex, organizationRE)
}

func organizationBeforeTitleMatching(blocks []pdf.TextBlock, titleIndex int, pattern *regexp.Regexp) string {
	for index := titleIndex - 1; index >= 0; index-- {
		text := cleanStatementText(blocks[index].Text)
		if pattern.MatchString(text) && !bankFieldRE.MatchString(text) {
			if location := quotedBankRE.FindStringIndex(text); location != nil && strings.TrimSpace(text[location[1]:]) != "" {
				return cleanStatementText(text[location[0]:location[1]])
			}
			return text
		}
	}
	return ""
}

func joinBlockText(blocks []pdf.TextBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, cleanStatementText(block.Text))
	}
	return strings.Join(parts, " ")
}

func normalizeAccount(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	if len(value) != 20 {
		return ""
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return value
}

func cleanStatementText(value string) string { return strings.Join(strings.Fields(value), " ") }
