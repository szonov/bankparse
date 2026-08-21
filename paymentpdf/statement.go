package paymentpdf

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/szonov/pdf"
)

var (
	ErrStatementInfoNotDetected = errors.New("PDF statement info not detected")
	ErrAmbiguousStatementInfo   = errors.New("ambiguous PDF statement info")

	statementTitleRE = regexp.MustCompile(`(?i)(выписка\s+операций\s+по\s+лицевому\s+счету|выписка\s+по\s+счету\s+клиента|account\s+statement)`)
	headerAccountRE  = regexp.MustCompile(`(?i)(?:выписка\s+операций\s+по\s+лицевому\s+счету|(?:номер\s+сч[её]та|account\s+number)(?:\s*/\s*(?:номер\s+сч[её]та|account\s+number))?\s*:?)[^0-9]{0,20}([0-9][0-9\s]{18,24}[0-9])`)
	accountLabelRE   = regexp.MustCompile(`(?i)^(?:номер\s+сч[её]та|account\s+number)(?:\s*/\s*(?:номер\s+сч[её]та|account\s+number))?\s*:?\s*$`)
	bankFieldRE      = regexp.MustCompile(`(?i)^(?:банк|bank)(?:\s*/\s*(?:банк|bank))?\s*(?::\s*(.*))?$`)
	organizationRE   = regexp.MustCompile(`(?i)(?:\bbank\b|банк|кредитн\S*\s+организац|(?:^|\s)(?:АО|ПАО|ООО)(?:\s|$)|филиал)`)
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
	if err := reader.Page(1).WalkTextBlocks(func(block pdf.TextBlock) error {
		if text := cleanStatementText(block.Text); text != "" {
			block.Text = text
			blocks = append(blocks, block)
		}
		return nil
	}); err != nil {
		return StatementInfo{}, fmt.Errorf("read PDF statement header: %w", err)
	}
	return detectStatementInfoBlocks(blocks)
}

func detectStatementInfoBlocks(blocks []pdf.TextBlock) (StatementInfo, error) {
	var title pdf.TextBlock
	foundTitle := false
	for _, block := range blocks {
		if statementTitleRE.MatchString(block.Text) {
			title, foundTitle = block, true
			break
		}
	}
	if !foundTitle {
		return StatementInfo{}, ErrStatementInfoNotDetected
	}
	accounts := make(map[string]struct{})
	for _, block := range blocks {
		if match := headerAccountRE.FindStringSubmatch(block.Text); len(match) > 1 {
			if account := normalizeAccount(match[1]); account != "" {
				accounts[account] = struct{}{}
			}
		}
		if accountLabelRE.MatchString(block.Text) {
			if value := normalizeAccount(textRightOfHeaderLabel(blocks, block)); value != "" {
				accounts[value] = struct{}{}
			}
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
	for _, block := range blocks {
		match := bankFieldRE.FindStringSubmatch(block.Text)
		if len(match) == 0 {
			continue
		}
		name := cleanStatementText(match[1])
		if name == "" {
			name = textRightOfHeaderLabel(blocks, block)
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
		info.BankName = organizationAboveTitle(blocks, title)
	}
	return info, nil
}

func textRightOfHeaderLabel(blocks []pdf.TextBlock, label pdf.TextBlock) string {
	bestDistance := math.MaxFloat64
	value := ""
	for _, candidate := range blocks {
		if candidate.X <= label.X || math.Abs(candidate.Y-label.Y) > 6 {
			continue
		}
		if distance := candidate.X - label.X; distance < bestDistance {
			bestDistance, value = distance, cleanStatementText(candidate.Text)
		}
	}
	return value
}

func organizationAboveTitle(blocks []pdf.TextBlock, title pdf.TextBlock) string {
	candidates := make([]pdf.TextBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Y > title.Y && organizationRE.MatchString(block.Text) && !bankFieldRE.MatchString(block.Text) {
			candidates = append(candidates, block)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		di, dj := candidates[i].Y-title.Y, candidates[j].Y-title.Y
		if math.Abs(di-dj) < 2 {
			return candidates[i].X < candidates[j].X
		}
		return di < dj
	})
	if len(candidates) == 0 {
		return ""
	}
	return cleanStatementText(candidates[0].Text)
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
