// Package financexlsx — адаптер порта FinanceImport для книг XLSX (EC-01, EC-07).
// Значения читаются по шаблону: лист, строка заголовков, колонки измерений и колонки полей.
// Ошибки отдельных строк не прерывают чтение — они попадают в отчёт (EC-07).
package financexlsx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// Reader читает книги XLSX.
type Reader struct{}

// New создаёт адаптер.
func New() *Reader { return &Reader{} }

var _ ports.FinanceImport = (*Reader)(nil)

// ErrFileTooLarge — файл превышает лимит размера.
var ErrFileTooLarge = errors.New("financexlsx: файл превышает лимит размера")

// maxRows ограничивает размер книги, чтобы одна загрузка не съела память (NF-P05).
const maxRows = 200_000

// Read разбирает книгу по шаблону.
//
// Книга — недоверенный ввод. В excelize 2.11.0 есть незакрытая уязвимость GO-2026-6452:
// специально собранный файл роняет разбор паникой. Пока исправления нет, паника ловится
// здесь и превращается в ошибку валидации — процесс не падает (docs/questions.md, вопрос 35).
func (r *Reader) Read(_ context.Context, src io.Reader, m ports.FinanceMapping) (result ports.FinanceReadResult, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			result = ports.FinanceReadResult{}
			err = fmt.Errorf("%w: книга повреждена или имеет неподдерживаемую структуру: %v", kernel.ErrValidation, rec)
		}
	}()
	f, err := excelize.OpenReader(src)
	if err != nil {
		return ports.FinanceReadResult{}, fmt.Errorf("%w: чтение книги: %w", kernel.ErrValidation, err)
	}
	defer func() { _ = f.Close() }()

	out := ports.FinanceReadResult{}
	for _, sheet := range m.Sheets {
		rows, err := f.GetRows(sheet.Sheet)
		if err != nil {
			out.Errors = append(out.Errors, ports.FinanceImportRowError{Sheet: sheet.Sheet, Message: "лист не найден"})
			continue
		}
		if len(rows) > maxRows {
			return ports.FinanceReadResult{}, fmt.Errorf("%w: в листе %q больше %d строк", kernel.ErrValidation, sheet.Sheet, maxRows)
		}
		header := sheet.HeaderRow
		if header <= 0 {
			header = 1
		}
		if len(rows) < header {
			out.Errors = append(out.Errors, ports.FinanceImportRowError{Sheet: sheet.Sheet, Row: header, Message: "нет строки заголовков"})
			continue
		}
		index := headerIndex(rows[header-1])
		colOf := func(name string) (int, bool) {
			if name == "" {
				return 0, false
			}
			if i, ok := index[strings.ToLower(strings.TrimSpace(name))]; ok {
				return i, true
			}
			if i, err := excelize.ColumnNameToNumber(name); err == nil {
				return i - 1, true
			}
			return 0, false
		}
		readSheet(rows, header, sheet, colOf, &out)
	}
	return out, nil
}

func readSheet(rows [][]string, header int, sheet ports.FinanceSheetMapping,
	colOf func(string) (int, bool), out *ports.FinanceReadResult) {
	cell := func(row []string, idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}
	for i := header; i < len(rows); i++ {
		row := rows[i]
		rowNo := i + 1
		if isEmpty(row) {
			continue
		}
		dims := map[string]string{}
		for name, col := range map[string]string{
			"product": sheet.ProductColumn, "team": sheet.TeamColumn,
			"period": sheet.PeriodColumn, "item": sheet.ItemColumn,
		} {
			idx, ok := colOf(col)
			if !ok {
				continue
			}
			dims[name] = cell(row, idx)
		}
		for _, c := range sheet.Columns {
			idx, ok := colOf(c.Column)
			if !ok {
				out.Errors = append(out.Errors, ports.FinanceImportRowError{Sheet: sheet.Sheet, Row: rowNo,
					Column: c.Column, Message: "колонка не найдена"})
				continue
			}
			raw := cell(row, idx)
			if raw == "" {
				continue
			}
			v, err := parseNumber(raw)
			if err != nil {
				out.Errors = append(out.Errors, ports.FinanceImportRowError{Sheet: sheet.Sheet, Row: rowNo,
					Column: c.Column, Message: err.Error()})
				continue
			}
			out.Cells = append(out.Cells, ports.FinanceCell{
				Sheet: sheet.Sheet, Row: rowNo, FieldKey: c.FieldKey,
				ProductKey: dims["product"], TeamKey: dims["team"],
				Period: dims["period"], Item: dims["item"], Value: v,
			})
		}
	}
}

func headerIndex(row []string) map[string]int {
	out := make(map[string]int, len(row))
	for i, v := range row {
		key := strings.ToLower(strings.TrimSpace(v))
		if key == "" {
			continue
		}
		if _, ok := out[key]; !ok {
			out[key] = i
		}
	}
	return out
}

func isEmpty(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// parseNumber разбирает число в форматах «1234.56», «1 234,56», «(1 234)» — со скобками как минусом.
func parseNumber(raw string) (decimal.Decimal, error) {
	s := strings.TrimSpace(raw)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, strings.TrimSuffix(strings.TrimPrefix(s, "("), ")")
	}
	s = strings.NewReplacer(" ", "", " ", "", " ", "", "₽", "", "%", "").Replace(s)
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return decimal.Zero, fmt.Errorf("пустое значение")
	}
	v, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("значение %q не число", raw)
	}
	if neg {
		v = v.Neg()
	}
	return v, nil
}
