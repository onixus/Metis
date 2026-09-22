// Package ports — интерфейсы портов ядра к внешним системам (ТЗ 4.1).
package ports

import (
	"context"
	"io"

	"github.com/shopspring/decimal"
)

// FinanceColumn — колонка листа, из которой берётся значение поля платформы (EC-07).
type FinanceColumn struct {
	// Column — буква колонки («C») или текст заголовка в строке заголовков.
	Column string
	// FieldKey — поле платформы, куда попадает значение.
	FieldKey string
}

// FinanceSheetMapping — привязка листа книги к полям и измерениям (EC-07).
type FinanceSheetMapping struct {
	Sheet         string
	HeaderRow     int
	ProductColumn string
	TeamColumn    string
	PeriodColumn  string
	ItemColumn    string
	Columns       []FinanceColumn
}

// FinanceMapping — шаблон импорта: какие листы и колонки читать.
type FinanceMapping struct {
	Sheets []FinanceSheetMapping
}

// FinanceCell — разобранное значение одной ячейки с координатами исходной строки.
type FinanceCell struct {
	Sheet      string
	Row        int
	FieldKey   string
	ProductKey string
	TeamKey    string
	Period     string
	Item       string
	Value      decimal.Decimal
}

// FinanceImportRowError — ошибка разбора строки файла (EC-07).
type FinanceImportRowError struct {
	Sheet   string
	Row     int
	Column  string
	Message string
}

// FinanceReadResult — результат чтения книги: значения и ошибки по строкам.
type FinanceReadResult struct {
	Cells  []FinanceCell
	Errors []FinanceImportRowError
}

// FinanceImport — порт импорта финансовых данных (ТЗ 4.1, EC-01).
// Платформа в источник ничего не пишет.
type FinanceImport interface {
	// Read разбирает книгу по шаблону; ошибки отдельных строк возвращаются в результате,
	// а не прерывают чтение.
	Read(ctx context.Context, r io.Reader, m FinanceMapping) (FinanceReadResult, error)
}
