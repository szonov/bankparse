// Package bankexchange parses files in the 1CClientBankExchange format.
package bankexchange

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/szonov/bankparse/payment"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

var (
	ErrInvalidFormat            = errors.New("invalid 1CClientBankExchange format")
	ErrStatementInfoNotDetected = errors.New("statement info not detected")
	ErrAmbiguousAccounts        = errors.New("ambiguous statement accounts")
)

type Info struct {
	Version, Encoding, Sender, Account string
	DateFrom, DateTo                   time.Time
	Accounts                           []Account
}

// Exchange is the materialized result returned by Parse.
type Exchange struct {
	Info
	Documents []payment.Document
}

type Account struct{ Fields map[string]string }

// Reader incrementally parses a single 1CClientBankExchange stream.
type Reader struct {
	lines  *bufio.Reader
	info   Info
	walked bool
}

func New(source io.Reader) (*Reader, error) {
	if source == nil {
		return nil, errors.New("nil 1CClientBankExchange reader")
	}
	decoded, encoding, err := decodedReader(source)
	if err != nil {
		return nil, err
	}
	r := &Reader{lines: bufio.NewReader(decoded), info: Info{Encoding: encoding}}
	line, err := r.readLine()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrInvalidFormat
		}
		return nil, err
	}
	if line != "1CClientBankExchange" {
		return nil, ErrInvalidFormat
	}
	return r, nil
}

// DetectInfo reads the exchange header and account sections without parsing or
// materializing payment documents. A top-level РасчСчет is authoritative; when
// it is absent all account sections must identify the same account.
func DetectInfo(source io.Reader) (Info, error) {
	r, err := New(source)
	if err != nil {
		return Info{}, err
	}
	accounts := make(map[string]struct{})
	inAccount := false
	for {
		line, readErr := r.readLine()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Info{}, readErr
		}
		atEOF := errors.Is(readErr, io.EOF)
		switch {
		case line == "СекцияРасчСчет":
			inAccount = true
		case line == "КонецРасчСчет":
			inAccount = false
		case strings.HasPrefix(line, "СекцияДокумент=") || line == "КонецФайла":
			return detectedInfo(r.info, accounts)
		default:
			key, value, ok := strings.Cut(line, "=")
			if ok {
				key, value = strings.TrimSpace(key), strings.TrimSpace(value)
				if inAccount && key == "РасчСчет" && value != "" {
					accounts[value] = struct{}{}
				} else if !inAccount {
					switch key {
					case "ВерсияФормата":
						r.info.Version = value
					case "Отправитель":
						r.info.Sender = value
					case "РасчСчет":
						r.info.Account = value
					case "ДатаНачала":
						r.info.DateFrom, _ = parseDate(value)
					case "ДатаКонца":
						r.info.DateTo, _ = parseDate(value)
					}
				}
			}
		}
		if atEOF {
			return detectedInfo(r.info, accounts)
		}
	}
}

func detectedInfo(info Info, accounts map[string]struct{}) (Info, error) {
	if info.Account != "" {
		return info, nil
	}
	if len(accounts) == 0 {
		return Info{}, ErrStatementInfoNotDetected
	}
	if len(accounts) > 1 {
		return Info{}, ErrAmbiguousAccounts
	}
	for account := range accounts {
		info.Account = account
	}
	return info, nil
}

// Info returns metadata parsed so far. It is complete after WalkDocuments.
func (r *Reader) Info() Info {
	if r == nil {
		return Info{}
	}
	return r.info
}

// WalkDocuments parses documents sequentially without retaining them. Returning
// payment.ErrStop from executor stops the walk successfully; other errors are returned.
func (r *Reader) WalkDocuments(executor payment.DocumentFunc) error {
	if r == nil || r.lines == nil {
		return errors.New("nil 1CClientBankExchange reader")
	}
	if executor == nil {
		return errors.New("nil document executor")
	}
	if r.walked {
		return errors.New("1CClientBankExchange reader already walked")
	}
	r.walked = true

	var current map[string]string
	var section string
	for {
		line, err := r.readLine()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		atEOF := errors.Is(err, io.EOF)
		if line == "" {
			if atEOF {
				break
			}
			continue
		}
		if line == "СекцияРасчСчет" {
			current = map[string]string{}
			section = "account"
			continue
		}
		if strings.HasPrefix(line, "СекцияДокумент=") {
			current = map[string]string{}
			section = "document"
			current["СекцияДокумент"] = strings.TrimSpace(strings.TrimPrefix(line, "СекцияДокумент="))
			continue
		}
		if line == "КонецРасчСчет" {
			r.info.Accounts = append(r.info.Accounts, Account{Fields: current})
			current = nil
			section = ""
			continue
		}
		if line == "КонецДокумента" {
			document, err := parseDocument(current)
			if err != nil {
				return err
			}
			if err := executor(document); err != nil {
				if errors.Is(err, payment.ErrStop) {
					return nil
				}
				return err
			}
			current = nil
			section = ""
			continue
		}
		if line == "КонецФайла" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if current != nil {
			current[key] = value
			continue
		}
		switch key {
		case "ВерсияФормата":
			r.info.Version = value
		case "Отправитель":
			r.info.Sender = value
		case "РасчСчет":
			r.info.Account = value
		case "ДатаНачала":
			r.info.DateFrom, _ = parseDate(value)
		case "ДатаКонца":
			r.info.DateTo, _ = parseDate(value)
		}
		_ = section
		if atEOF {
			break
		}
	}
	if r.info.Account == "" && len(r.info.Accounts) > 0 {
		r.info.Account = r.info.Accounts[0].Fields["РасчСчет"]
	}
	if r.info.Account == "" {
		return fmt.Errorf("%w: account is missing", ErrInvalidFormat)
	}
	return nil
}

func (r *Reader) readLine() (string, error) {
	line, err := r.lines.ReadString('\n')
	return strings.TrimSpace(line), err
}

// Parse materializes an exchange for callers that need all documents at once.
func Parse(data []byte) (Exchange, error) {
	r, err := New(bytes.NewReader(data))
	if err != nil {
		return Exchange{}, err
	}
	var documents []payment.Document
	if err := r.WalkDocuments(func(document payment.Document) error {
		documents = append(documents, document)
		return nil
	}); err != nil {
		return Exchange{}, err
	}
	return Exchange{Info: r.Info(), Documents: documents}, nil
}

func parseDocument(fields map[string]string) (payment.Document, error) {
	typeName := value(fields, "СекцияДокумент")
	documentType, err := parseDocumentType(typeName)
	if err != nil {
		return payment.Document{}, err
	}
	number := value(fields, "Номер")
	if number == "" {
		return payment.Document{}, fmt.Errorf("%w: %s number is missing", ErrInvalidFormat, typeName)
	}

	date, err := parseDate(value(fields, "Дата"))
	if err != nil {
		return payment.Document{}, fmt.Errorf("%w: document %q date: %v", ErrInvalidFormat, number, err)
	}
	amount, err := parseAmount(value(fields, "Сумма"))
	if err != nil {
		return payment.Document{}, fmt.Errorf("%w: document %q amount: %v", ErrInvalidFormat, number, err)
	}

	document := payment.Document{
		Type:          documentType,
		Number:        number,
		Date:          date,
		OperationType: firstValue(fields, "ВидОплаты", "Код"),
		Amount:        amount,
		Payer:         parseParty(fields, "Плательщик"),
		Recipient:     parseParty(fields, "Получатель"),
		Purpose:       value(fields, "НазначениеПлатежа"),
		Budget:        parseBudget(fields),
	}
	return document, nil
}

func parseDocumentType(value string) (payment.Type, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "платежное поручение":
		return payment.PaymentOrder, nil
	case "платежный ордер":
		return payment.PaymentWarrant, nil
	case "платежное требование":
		return payment.PaymentRequest, nil
	case "инкассовое поручение":
		return payment.CollectionOrder, nil
	case "банковский ордер":
		return payment.BankOrder, nil
	default:
		return "", fmt.Errorf("%w: unsupported document type %q", ErrInvalidFormat, value)
	}
}

func parseAmount(value string) (payment.Amount, error) {
	original := value
	value = strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	if value == "" {
		return payment.Amount{}, errors.New("value is missing")
	}
	if strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return payment.Amount{}, fmt.Errorf("invalid value %q", original)
	}
	if strings.Count(value, ",")+strings.Count(value, ".") > 1 {
		return payment.Amount{}, fmt.Errorf("invalid value %q", original)
	}
	value = strings.ReplaceAll(value, ",", ".")
	parts := strings.SplitN(value, ".", 2)
	if len(parts[0]) == 0 {
		return payment.Amount{}, fmt.Errorf("invalid value %q", original)
	}
	rubles, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return payment.Amount{}, fmt.Errorf("invalid value %q", original)
	}
	kopecks := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 2 {
			return payment.Amount{}, fmt.Errorf("invalid value %q", original)
		}
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		kopecks, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return payment.Amount{}, fmt.Errorf("invalid value %q", original)
		}
	}
	if rubles > (math.MaxInt64-kopecks)/100 {
		return payment.Amount{}, fmt.Errorf("value %q overflows kopecks", original)
	}
	return payment.Amount{Kopecks: rubles*100 + kopecks, Currency: "RUB"}, nil
}

func parseParty(fields map[string]string, prefix string) payment.Party {
	return payment.Party{
		Name:    firstValue(fields, prefix+"1", prefix),
		INN:     value(fields, prefix+"ИНН"),
		KPP:     normalizeKPP(value(fields, prefix+"КПП")),
		Account: firstValue(fields, prefix+"РасчСчет", prefix+"Счет"),
		Bank: payment.Bank{
			Name:    firstValue(fields, prefix+"Банк1", prefix+"Банк"),
			BIK:     value(fields, prefix+"БИК"),
			Account: value(fields, prefix+"Корсчет"),
		},
	}
}

func parseBudget(fields map[string]string) *payment.BudgetDetails {
	details := payment.BudgetDetails{
		PayerStatus:    value(fields, "СтатусСоставителя"),
		KBK:            value(fields, "ПоказательКБК"),
		OKTMO:          value(fields, "ОКАТО"),
		Basis:          value(fields, "ПоказательОснования"),
		TaxPeriod:      value(fields, "ПоказательПериода"),
		DocumentNumber: value(fields, "ПоказательНомера"),
		DocumentDate:   value(fields, "ПоказательДаты"),
		PaymentType:    value(fields, "ПоказательТипа"),
	}
	if details == (payment.BudgetDetails{}) {
		return nil
	}
	return &details
}

func normalizeKPP(value string) string {
	if value == "0" {
		return ""
	}
	return value
}

func value(fields map[string]string, name string) string {
	return strings.TrimSpace(fields[name])
}

func firstValue(fields map[string]string, names ...string) string {
	for _, name := range names {
		if result := value(fields, name); result != "" {
			return result
		}
	}
	return ""
}

func parseDate(value string) (time.Time, error) { return time.Parse("02.01.2006", value) }
func decodedReader(source io.Reader) (io.Reader, string, error) {
	raw := bufio.NewReader(source)
	probe, err := raw.Peek(4096)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, "", err
	}
	if bytes.HasPrefix(probe, []byte{0xef, 0xbb, 0xbf}) {
		if _, err := raw.Discard(3); err != nil {
			return nil, "", err
		}
		return raw, "utf-8", nil
	}

	// Restrict the probe to complete lines so a multi-byte UTF-8 rune split at
	// the buffer boundary cannot make a UTF-8 export look like a legacy encoding.
	complete := probe
	if i := bytes.LastIndexByte(complete, '\n'); i >= 0 {
		complete = complete[:i+1]
	}
	if encodingDeclared(complete, charmap.CodePage866, "dos", "cp866") {
		return transform.NewReader(raw, charmap.CodePage866.NewDecoder()), "cp866", nil
	}
	if encodingDeclared(complete, charmap.Windows1251, "windows", "windows-1251") {
		return transform.NewReader(raw, charmap.Windows1251.NewDecoder()), "windows-1251", nil
	}
	if utf8.Valid(complete) {
		return raw, "utf-8", nil
	}
	// Windows-1251 is the de-facto default when a legacy export omits a usable
	// encoding declaration.
	return transform.NewReader(raw, charmap.Windows1251.NewDecoder()), "windows-1251", nil
}

func encodingDeclared(probe []byte, encoding *charmap.Charmap, values ...string) bool {
	decoded, err := encoding.NewDecoder().Bytes(probe)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(decoded))
	if !strings.Contains(lower, "кодировка=") {
		return false
	}
	for _, value := range values {
		if strings.Contains(lower, "кодировка="+value) {
			return true
		}
	}
	return false
}
