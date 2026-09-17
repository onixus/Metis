package financexlsx_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/onixus/metis/internal/adapters/financexlsx"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// TestEC07_BrokenWorkbookIsValidationError: повреждённый файл — ошибка валидации, а не паника.
// Прикрывает незакрытую уязвимость excelize GO-2026-6452 (вопрос 35).
func TestEC07_BrokenWorkbookIsValidationError(t *testing.T) {
	r := financexlsx.New()
	_, err := r.Read(context.Background(), bytes.NewReader([]byte("это не книга XLSX")),
		ports.FinanceMapping{Sheets: []ports.FinanceSheetMapping{{Sheet: "Лист1"}}})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
}

// TestEC07_MissingSheetReportedAsRowError: отсутствующий лист попадает в отчёт об ошибках.
func TestEC07_MissingSheetReportedAsRowError(t *testing.T) {
	book := validBook(t)
	r := financexlsx.New()
	res, err := r.Read(context.Background(), bytes.NewReader(book),
		ports.FinanceMapping{Sheets: []ports.FinanceSheetMapping{{Sheet: "Нет такого", HeaderRow: 1}}})
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Sheet != "Нет такого" {
		t.Fatalf("ошибки: %+v", res.Errors)
	}
}

// validBook собирает минимальную корректную книгу.
func validBook(t *testing.T) []byte {
	t.Helper()
	x := excelize.NewFile()
	if _, err := x.NewSheet("Финансы"); err != nil {
		t.Fatalf("лист: %v", err)
	}
	if err := x.SetCellValue("Финансы", "A1", "Продукт"); err != nil {
		t.Fatalf("ячейка: %v", err)
	}
	var buf bytes.Buffer
	if err := x.Write(&buf); err != nil {
		t.Fatalf("книга: %v", err)
	}
	return buf.Bytes()
}
