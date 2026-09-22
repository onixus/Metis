package financefile

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const productID = "11111111-1111-4111-8111-111111111111"
const csvHeader = "product_id,period,category,amount,currency,team_id,headcount,description\n"

func TestEC01EC07_CSVMappingPreviewLineage(t *testing.T) {
	input := "Report\nПродукт;Месяц;Статья;Сумма;Валюта;Команда;Людей;Комментарий\n" + productID + ";2026-09;payroll;1200.25;RUB;team-a;5;Тестовый ФОТ\n" + productID + ";2026-09;revenue;bad;RUB;;;do-not-echo-this\n"
	template := ports.FinanceTemplate{HeaderRow: 2, Delimiter: ";", Columns: map[string]string{"product_id": "Продукт", "period": "Месяц", "category": "Статья", "amount": "Сумма", "currency": "Валюта", "team_id": "Команда", "headcount": "Людей", "description": "Комментарий"}}
	p, err := New().Parse(context.Background(), "synthetic.csv", []byte(input), template)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || len(p.Errors) != 1 {
		t.Fatalf("unexpected preview: %+v", p)
	}
	row := p.Rows[0]
	if row.Amount != kernel.RUB(120025) || row.Headcount != 5 || row.TeamID != "team-a" || row.Source.Row != 3 || row.Source.File != "synthetic.csv" || len(p.SourceHashes) != 1 || row.Source.Hash != p.SourceHashes[0] || len(p.SourceHash) != 64 {
		t.Fatalf("row: %+v", row)
	}
	if p.Errors[0].Row != 4 || p.Errors[0].Field != "amount" || strings.Contains(p.Errors[0].Message, "bad") {
		t.Fatalf("row error: %+v", p.Errors)
	}
}

func TestEC01_ExactMoneyAndAggregatePayroll(t *testing.T) {
	for _, tc := range []struct {
		amount string
		valid  bool
		want   int64
	}{
		{"0", true, 0}, {"92233720368547758.07", true, 9223372036854775807}, {"1.23e2", true, 12300},
		{"0.001", false, 0}, {"-1", false, 0}, {"92233720368547758.08", false, 0}, {"1e2147483647", false, 0}, {"1,23", false, 0},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			value, err := parseAmount(tc.amount, false)
			if (err == nil) != tc.valid || (err == nil && value != tc.want) {
				t.Fatalf("got %d, %v", value, err)
			}
		})
	}
	p, err := New().Parse(context.Background(), "payroll.csv", []byte(csvHeader+productID+",2026-09,payroll,100,RUB,,,\n"), ports.FinanceTemplate{})
	if err != nil || len(p.Rows) != 0 || len(p.Errors) != 2 {
		t.Fatalf("individual payroll accepted: %+v %v", p, err)
	}
}

func TestEC01EC07_FinanceFixturesHaveEqualFinancialFacts(t *testing.T) {
	var previews []ports.FinancePreview
	for _, name := range []string{"example.csv", "example.xlsx"} {
		data, err := os.ReadFile("../../../fixtures/finance/" + name)
		if err != nil {
			t.Fatal(err)
		}
		p, err := New().Parse(context.Background(), name, data, ports.FinanceTemplate{})
		if err != nil || len(p.Rows) != 7 || len(p.Errors) != 0 {
			t.Fatalf("fixture %s: %+v %v", name, p, err)
		}
		previews = append(previews, p)
	}
	for i, csvRow := range previews[0].Rows {
		xlsxRow := previews[1].Rows[i]
		if csvRow.ProductID != xlsxRow.ProductID || csvRow.Amount != xlsxRow.Amount || csvRow.Period != xlsxRow.Period || csvRow.Category != xlsxRow.Category || csvRow.TeamID != xlsxRow.TeamID || csvRow.Headcount != xlsxRow.Headcount {
			t.Fatalf("fixture mismatch row %d", i)
		}
	}
}

func TestEC01_RequiresMinorUnitsForUnsupportedMajorCurrency(t *testing.T) {
	for _, currency := range []string{"JPY", "KWD"} {
		p, err := New().Parse(context.Background(), "test.csv", []byte(csvHeader+productID+",2026-09,revenue,10,"+currency+",,,\n"), ports.FinanceTemplate{})
		if err != nil || len(p.Rows) != 0 || len(p.Errors) != 1 || p.Errors[0].Field != "currency" {
			t.Fatalf("ambiguous currency accepted: %+v %v", p, err)
		}
		p, err = New().Parse(context.Background(), "test.csv", []byte(strings.Replace(csvHeader, "amount,", "amount_minor,", 1)+productID+",2026-09,revenue,10,"+currency+",,,\n"), ports.FinanceTemplate{})
		if err != nil || len(p.Rows) != 1 || len(p.Errors) != 0 || p.Rows[0].Amount.Amount != 10 {
			t.Fatalf("minor amount changed: %+v %v", p, err)
		}
	}
}

func TestEC07_RejectsAmbiguousHeadersAndMappings(t *testing.T) {
	for _, input := range []string{
		"product_id,period,category,amount,currency,currency\n",
		"product_id,period,category,amount,amount_minor,currency\n",
		"product_id,period,category,currency\n",
	} {
		if _, err := New().Parse(context.Background(), "test.csv", []byte(input), ports.FinanceTemplate{}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("invalid header accepted: %v", err)
		}
	}
}

func TestEC07_ImportFingerprintChangesWithMappingAndKeepsRawLineage(t *testing.T) {
	data := []byte(csvHeader + productID + ",2026-09,revenue,10,RUB,,,synthetic-description\n")
	p1, err := New().Parse(context.Background(), "synthetic.csv", data, ports.FinanceTemplate{})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := New().Parse(context.Background(), "synthetic.csv", data, ports.FinanceTemplate{Columns: map[string]string{"product_id": "product_id", "period": "period", "category": "category", "amount": "amount", "currency": "currency"}})
	if err != nil {
		t.Fatal(err)
	}
	if p1.SourceHash == p2.SourceHash || p1.Rows[0].Source.Hash != p2.Rows[0].Source.Hash || p1.SourceHashes[0] != p2.SourceHashes[0] {
		t.Fatal("mapping change lost or raw lineage changed")
	}
}

func FuzzNFS14_UntrustedFinancialFile(f *testing.F) {
	f.Add([]byte(csvHeader+productID+",2026-09,revenue,10,RUB,,,synthetic\n"), false)
	f.Add([]byte("PK\x03\x04invalid"), true)
	f.Fuzz(func(t *testing.T, data []byte, xlsx bool) {
		if len(data) > 65536 {
			return
		}
		name := "synthetic.csv"
		if xlsx {
			name = "synthetic.xlsx"
		}
		_, _ = New().Parse(context.Background(), name, data, ports.FinanceTemplate{})
	})
}

func TestNFS14_XLSXCachedValuesAndRichStrings(t *testing.T) {
	xlsx := makeWorkbook(t, `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="inlineStr"><is><t>period</t></is></c><c r="C1" t="inlineStr"><is><t>category</t></is></c><c r="D1" t="inlineStr"><is><t>amount</t></is></c><c r="E1" t="inlineStr"><is><t>currency</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>`+productID+`</t></is></c><c r="B2" t="str"><v>2026-09</v></c><c r="C2" t="inlineStr"><is><r><t>rev</t></r><r><t>enue</t></r></is></c><c r="D2"><f>WEBSERVICE("https://invalid.example/never")</f><v>123.45</v></c><c r="E2" t="str"><v>RUB</v></c></row>`, nil)
	p, err := New().Parse(context.Background(), "synthetic.xlsx", xlsx, ports.FinanceTemplate{Sheet: "Finance"})
	if err != nil || len(p.Rows) != 1 || len(p.Errors) != 0 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	if p.Rows[0].Amount != kernel.RUB(12345) || p.Rows[0].Source.Sheet != "Finance" {
		t.Fatalf("row: %+v", p.Rows[0])
	}
}

func TestNFS14_XLSXFormulaWithoutCacheProducesRowError(t *testing.T) {
	rows := `<row r="1">` + inlineCell("A1", "product_id") + inlineCell("B1", "period") + inlineCell("C1", "category") + inlineCell("D1", "amount") + inlineCell("E1", "currency") + `</row><row r="2">` + inlineCell("A2", productID) + inlineCell("B2", "2026-09") + inlineCell("C2", "revenue") + `<c r="D2"><f>1+1</f></c>` + inlineCell("E2", "RUB") + `</row>`
	p, err := New().Parse(context.Background(), "synthetic.xlsx", makeWorkbook(t, rows, nil), ports.FinanceTemplate{})
	if err != nil || len(p.Rows) != 0 || len(p.Errors) == 0 || p.Errors[0].Message != "formula has no cached value" {
		t.Fatalf("preview: %+v %v", p, err)
	}
}

func TestNFS14_RejectsUnsafeXLSX(t *testing.T) {
	cases := map[string]map[string]string{
		"traversal":            {"../escape.xml": "x"},
		"macro":                {"xl/vbaProject.bin": "x"},
		"externalPart":         {"xl/externalLinks/link.xml": "<x/>"},
		"externalRelationship": {"xl/worksheets/_rels/sheet1.xml.rels": `<Relationships><Relationship Id="x" TargetMode="External" Target="https://invalid.example"/></Relationships>`},
		"DTD":                  {"xl/workbook.xml": `<!DOCTYPE x [<!ENTITY x SYSTEM "file:///etc/passwd">]><workbook/>`},
		"deepXML":              {"xl/workbook.xml": strings.Repeat("<x>", 34) + strings.Repeat("</x>", 34)},
		"duplicateCase":        {"XL/WORKBOOK.XML": "<workbook/>"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New().Parse(context.Background(), "test.xlsx", makeWorkbook(t, "", extra), ports.FinanceTemplate{})
			if !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("unsafe XLSX accepted: %v", err)
			}
		})
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.Create("bomb.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(strings.Repeat("A", 1<<20))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Parse(context.Background(), "bomb.xlsx", b.Bytes(), ports.FinanceTemplate{}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatal("compression bomb accepted")
	}
}

func TestNFS14_ResourceLimitsAndCancellation(t *testing.T) {
	for _, input := range [][]byte{bytes.Repeat([]byte("x"), MaxFileSize+1), []byte(csvHeader + strings.Repeat("a", maxCellSize+1)), []byte("a\x00b"), {0xff}} {
		if _, err := New().Parse(context.Background(), "test.csv", input, ports.FinanceTemplate{}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Parse(ctx, "test.csv", []byte(csvHeader), ports.FinanceTemplate{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestNFS14_CSVExportEscapesFormulaPrefixes(t *testing.T) {
	for _, value := range []string{"=1+1", "+value", "-value", "@SUM(1)", " \t=1", "\r\n@data", "\uFEFF+cmd"} {
		if escaped := EscapeCSVCell(value); escaped != "'"+value {
			t.Fatalf("unsafe export %q", escaped)
		}
	}
	if got := EscapeCSVCell("ordinary text"); got != "ordinary text" {
		t.Fatal(got)
	}
}

func inlineCell(ref, value string) string {
	return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, value)
}

func makeWorkbook(t *testing.T, rows string, extra map[string]string) []byte {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml":        `<Types><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`,
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Finance" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>product_id</t></si></sst>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData>` + rows + `</sheetData></worksheet>`,
	}
	for name, value := range extra {
		parts[name] = value
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, value := range parts {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
