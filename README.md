# bankparse

`bankparse` — библиотека на Go для последовательного извлечения платёжных
документов из банковских файлов.

Поддерживаемые источники:

- PDF с платёжными поручениями, платёжными требованиями и банковскими ордерами;
- текстовый формат `1CClientBankExchange` в UTF-8, Windows-1251 и CP866.

Оба парсера возвращают одну модель `payment.Document` и предоставляют одинаковый
потоковый метод `WalkDocuments`. Документы обрабатываются по одному, поэтому для
импорта не требуется хранить весь результат в памяти.

## Установка

```bash
go get github.com/szonov/bankparse
```

## Автоматическое определение формата

Корневой пакет определяет формат по содержимому файла, а не по его имени или
MIME-типу. Входной файл должен реализовывать `io.ReaderAt`; отдельно передаётся
его размер. Этим требованиям соответствуют, например, `*os.File` и файлы,
полученные через `multipart.File`.

```go
package main

import (
	"fmt"
	"os"

	"github.com/szonov/bankparse"
	"github.com/szonov/bankparse/payment"
)

func main() {
	file, err := os.Open("bank-file")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		panic(err)
	}

	parser, err := bankparse.Open(file, info.Size())
	if err != nil {
		panic(err)
	}

	err = parser.WalkDocuments(func(document payment.Document) error {
		fmt.Printf("%s: %d коп.\n", document.Number, document.Amount.Kopecks)
		return nil
	})
	if err != nil {
		panic(err)
	}
}
```

`bankparse.DetectFormat` позволяет только определить формат, не создавая парсер.
Чтение сигнатуры выполняется через `io.ReaderAt` и не изменяет текущую позицию
файла.

## Информация о выписке

До обхода документов можно извлечь общий номер счёта и, если он присутствует в
шапке, текстовое название банка:

```go
statement, err := bankparse.DetectStatementInfo(file, info.Size())
if errors.Is(err, bankparse.ErrStatementInfoNotDetected) {
	// В файле нет общей шапки выписки.
} else if err != nil {
	return err
}
fmt.Println(statement.AccountNumber, statement.BankName)
```

Для PDF анализируется только первая страница и принимается только счёт,
семантически связанный с общей шапкой выписки. PDF, которые начинаются сразу с
отдельных платёжных документов, намеренно возвращают
`ErrStatementInfoNotDetected`. Для `1CClientBankExchange` используются общие
поля `РасчСчет` и `Отправитель`; при отсутствии общего счёта допускается один и
тот же счёт из секций `СекцияРасчСчет`.

`BankName` необязателен и содержит извлечённый, лишь минимально нормализованный
текст, а не канонический идентификатор. Сопоставление со справочником банков,
БИК или именем файла выполняет вызывающее приложение.

## Ранняя остановка

Чтобы штатно завершить обход, callback может вернуть `payment.ErrStop`:

```go
err := parser.WalkDocuments(func(document payment.Document) error {
	if document.Number == "42" {
		return payment.ErrStop
	}
	return nil
})
```

Оба парсера прекращают чтение сразу. `WalkDocuments` считает такую остановку
успешной и возвращает `nil`. Любая другая ошибка callback также немедленно
останавливает обход и возвращается вызывающему коду.

## Прямое использование парсеров

### 1CClientBankExchange

`bankexchange.New` принимает обычный `io.Reader`. Файл декодируется и разбирается
последовательно, без предварительного чтения всего содержимого в `[]byte`.

```go
reader, err := bankexchange.New(source)
if err != nil {
	return err
}

err = reader.WalkDocuments(handleDocument)
```

После полного обхода `reader.Info()` содержит метаданные обмена: версию формата,
кодировку, отправителя, период и расчётные счета.

Для совместимости доступна функция `bankexchange.Parse([]byte)`, которая собирает
метаданные и все документы в `bankexchange.Exchange`.

### PDF

Низкоуровневое чтение выполняет
[`github.com/szonov/pdf`](https://github.com/szonov/pdf). Парсер `paymentpdf`
последовательно обходит дерево страниц и позволяет остановить обработку, не
интерпретируя оставшиеся страницы.

```go
pdfReader, err := pdf.NewReader(source, size)
if err != nil {
	return err
}

reader := paymentpdf.New(pdfReader)
err = reader.WalkDocuments(handleDocument)
```

Если вызывающему коду нужен номер страницы, можно использовать специализированный
метод `WalkPageDocuments`.

## Пакеты

- `payment` — общая модель платёжного документа и потоковый контракт;
- `bankexchange` — парсер `1CClientBankExchange`;
- `paymentpdf` — парсер платёжных документов из PDF;
- `bankparse` — определение формата и создание подходящего парсера;
- `cmd/bankparse` — небольшая CLI-утилита, выводящая документы в JSON.

## CLI

```bash
go run ./cmd/bankparse /path/to/bank-file
```

Формат файла определяется автоматически. Каждый найденный документ выводится в
JSON сразу после разбора.

## Проверка

```bash
go test ./...
```
