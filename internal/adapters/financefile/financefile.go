// Package financefile implements bounded, read-only CSV/XLSX imports. Documents
// are untrusted data; formulas, links, macros and XML entities are never executed.
package financefile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const (
	MaxFileSize         = 8 << 20
	maxEntries          = 128
	maxEntrySize        = 16 << 20
	maxExpandedSize     = 32 << 20
	maxCompressionRatio = 100
	maxRows             = 10000
	maxColumns          = 128
	maxCellSize         = 4096
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ ports.Finance = (*Adapter)(nil)

type cell struct {
	value string
	err   string
}

type sourceRow struct {
	number int
	cells  []cell
}

func invalid(message string) error {
	return fmt.Errorf("%w: financefile: %s", kernel.ErrValidation, message)
}

func (a *Adapter) Parse(ctx context.Context, filename string, data []byte, template ports.FinanceTemplate) (ports.FinancePreview, error) {
	if err := ctx.Err(); err != nil {
		return ports.FinancePreview{}, fmt.Errorf("financefile: %w", err)
	}
	if len(data) == 0 || len(data) > MaxFileSize {
		return ports.FinancePreview{}, invalid("file size must be between 1 byte and 8 MiB")
	}
	if len(filename) > 256 {
		return ports.FinancePreview{}, invalid("filename is too long")
	}
	if template.HeaderRow == 0 {
		template.HeaderRow = 1
	}
	if template.HeaderRow < 1 || template.HeaderRow > maxRows {
		return ports.FinancePreview{}, invalid("header row is outside the supported range")
	}
	var rows []sourceRow
	var sheet string
	var err error
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".csv":
		rows, err = readCSV(ctx, data, template)
	case ".xlsx":
		rows, sheet, err = readXLSX(ctx, data, template)
	default:
		return ports.FinancePreview{}, invalid("only CSV and XLSX are supported")
	}
	if err != nil {
		return ports.FinancePreview{}, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	preview, err := normalize(ctx, filepath.Base(filename), sheet, hash, rows, template)
	if err != nil {
		return preview, err
	}
	metadata, err := json.Marshal(struct {
		RawHash  string                `json:"raw_hash"`
		Template ports.FinanceTemplate `json:"template"`
	}{RawHash: hash, Template: template})
	if err != nil {
		return ports.FinancePreview{}, fmt.Errorf("financefile fingerprint: %w", err)
	}
	fingerprint := sha256.Sum256(metadata)
	preview.SourceHash = hex.EncodeToString(fingerprint[:])
	preview.SourceHashes = []string{hash}
	return preview, nil
}

func readCSV(ctx context.Context, data []byte, template ports.FinanceTemplate) ([]sourceRow, error) {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, invalid("CSV must be UTF-8 without NUL bytes")
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	if template.Delimiter != "" {
		switch template.Delimiter {
		case ",", ";", "\t":
			r.Comma = rune(template.Delimiter[0])
		default:
			return nil, invalid("CSV delimiter must be comma, semicolon, or tab")
		}
	}
	var out []sourceRow
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("financefile: %w", err)
		}
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalid("malformed CSV record")
		}
		if len(out) >= maxRows+template.HeaderRow || len(record) > maxColumns {
			return nil, invalid("CSV row or column limit exceeded")
		}
		line, _ := r.FieldPos(0)
		row := sourceRow{number: line, cells: make([]cell, len(record))}
		for i, value := range record {
			if len(value) > maxCellSize {
				return nil, invalid("cell exceeds 4096 bytes")
			}
			row.cells[i].value = value
		}
		out = append(out, row)
	}
	return out, nil
}

func knownFields() []string {
	return []string{"product_id", "period", "category", "amount", "amount_minor", "currency", "team_id", "headcount", "feature_id", "certification_track_id", "branch", "bundle_id", "description"}
}

func normalize(ctx context.Context, filename, sheet, hash string, rows []sourceRow, template ports.FinanceTemplate) (ports.FinancePreview, error) {
	preview := ports.FinancePreview{Rows: []ports.FinanceRow{}, Errors: []ports.FinanceRowError{}, SourceHash: hash, Sheet: sheet}
	headerIndex := -1
	for i, row := range rows {
		if row.number == template.HeaderRow {
			headerIndex = i
			break
		}
	}
	if headerIndex < 0 {
		return preview, invalid("header row is missing")
	}
	indexes := make(map[string]int)
	for i, c := range rows[headerIndex].cells {
		h := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(c.value, "\uFEFF")))
		if c.err != "" {
			return preview, invalid("invalid header cell")
		}
		if h == "" {
			continue
		}
		if _, exists := indexes[h]; exists {
			return preview, invalid("duplicate column header")
		}
		indexes[h] = i
	}
	columns := template.Columns
	if len(columns) == 0 {
		columns = make(map[string]string)
		for _, name := range knownFields() {
			if _, ok := indexes[name]; ok {
				columns[name] = name
			}
		}
	}
	mapping := make(map[string]int)
	for field, header := range columns {
		known := false
		for _, name := range knownFields() {
			if name == field {
				known = true
				break
			}
		}
		if !known {
			return preview, invalid("unknown mapped field")
		}
		index, ok := indexes[strings.ToLower(strings.TrimSpace(header))]
		if !ok {
			return preview, invalid("mapped column is missing")
		}
		mapping[field] = index
	}
	for _, name := range []string{"product_id", "period", "category", "currency"} {
		if _, ok := mapping[name]; !ok {
			return preview, invalid("required column is missing: " + name)
		}
	}
	_, major := mapping["amount"]
	_, minor := mapping["amount_minor"]
	if major == minor {
		return preview, invalid("map exactly one of amount and amount_minor")
	}
	for _, source := range rows[headerIndex+1:] {
		if err := ctx.Err(); err != nil {
			return preview, fmt.Errorf("financefile: %w", err)
		}
		if len(preview.Rows)+len(preview.Errors) > maxRows*16 {
			return preview, invalid("row error limit exceeded")
		}
		values := make(map[string]string)
		errorsBefore := len(preview.Errors)
		blank := true
		for _, c := range source.cells {
			if strings.TrimSpace(c.value) != "" || c.err != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		for _, field := range knownFields() {
			index, mapped := mapping[field]
			if !mapped || index >= len(source.cells) {
				continue
			}
			c := source.cells[index]
			if c.err != "" {
				preview.Errors = append(preview.Errors, ports.FinanceRowError{Row: source.number, Field: field, Message: c.err})
			}
			values[field] = strings.TrimSpace(c.value)
		}
		row, rowErrors := normalizeRow(source.number, values, minor)
		preview.Errors = append(preview.Errors, rowErrors...)
		if len(preview.Errors) != errorsBefore {
			continue
		}
		row.Source = ports.FinanceSource{File: filename, Sheet: sheet, Row: source.number, Hash: hash}
		preview.Rows = append(preview.Rows, row)
	}
	if len(preview.Rows) == 0 && len(preview.Errors) == 0 {
		return preview, invalid("no financial rows")
	}
	return preview, nil
}

func normalizeRow(number int, values map[string]string, minor bool) (ports.FinanceRow, []ports.FinanceRowError) {
	r := ports.FinanceRow{Period: values["period"], Category: values["category"], TeamID: values["team_id"], Branch: values["branch"], BundleID: values["bundle_id"], Description: values["description"]}
	var out []ports.FinanceRowError
	add := func(field, message string) {
		out = append(out, ports.FinanceRowError{Row: number, Field: field, Message: message})
	}
	for _, field := range []string{"product_id", "feature_id", "certification_track_id"} {
		v := values[field]
		if v == "" && field != "product_id" {
			continue
		}
		id, err := kernel.ParseID(v)
		if err != nil || id == kernel.NilID {
			add(field, "expected a nonzero UUID")
			continue
		}
		switch field {
		case "product_id":
			r.ProductID = id
		case "feature_id":
			r.FeatureID = id
		case "certification_track_id":
			r.CertificationTrackID = id
		}
	}
	period, err := time.Parse("2006-01", r.Period)
	if err != nil || period.Format("2006-01") != r.Period || period.Year() < 1 {
		add("period", "expected YYYY-MM")
	}
	switch r.Category {
	case "revenue", "payroll", "direct_cost", "marketing", "hub_cost", "certification_cost", "maintenance_cost":
	default:
		add("category", "unknown financial category")
	}
	r.Amount.Currency = values["currency"]
	if len(r.Amount.Currency) != 3 || strings.IndexFunc(r.Amount.Currency, func(c rune) bool { return c < 'A' || c > 'Z' }) >= 0 {
		add("currency", "expected a three-letter uppercase currency code")
	}
	amountField := "amount"
	if minor {
		amountField = "amount_minor"
	} else {
		switch r.Amount.Currency {
		case "RUB", "USD", "EUR", "GBP", "CNY":
		default:
			add("currency", "major-unit amounts support RUB/USD/EUR/GBP/CNY; use amount_minor for other currencies")
		}
	}
	amount, err := parseAmount(values[amountField], minor)
	if err != nil {
		add(amountField, "expected a nonnegative exact amount within int64 range")
	} else {
		r.Amount.Amount = amount
	}
	if values["headcount"] != "" {
		n, err := strconv.Atoi(values["headcount"])
		if err != nil || n < 0 || n > 1000000 {
			add("headcount", "expected an integer from 0 to 1000000")
		} else {
			r.Headcount = n
		}
	}
	if r.Category == "payroll" {
		if r.TeamID == "" {
			add("team_id", "payroll requires an aggregate team identifier")
		}
		if r.Headcount < 1 {
			add("headcount", "payroll requires team headcount")
		}
	}
	return r, out
}

func parseAmount(s string, minor bool) (int64, error) {
	if s == "" || len(s) > 48 {
		return 0, invalid("invalid amount")
	}
	// Bound exponent before invoking decimal, preventing pathological allocation.
	mantissa := s
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp, err := strconv.Atoi(s[i+1:])
		if minor || err != nil || exp < -18 || exp > 18 {
			return 0, invalid("invalid exponent")
		}
		mantissa = s[:i]
	}
	if strings.IndexFunc(mantissa, func(c rune) bool { return (c < '0' || c > '9') && c != '.' && c != '+' }) >= 0 {
		return 0, invalid("invalid amount")
	}
	if minor {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v < 0 {
			return 0, invalid("invalid minor amount")
		}
		return v, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil || d.IsNegative() {
		return 0, invalid("invalid amount")
	}
	d = d.Mul(decimal.NewFromInt(100))
	if !d.IsInteger() || !d.BigInt().IsInt64() {
		return 0, invalid("amount loses precision or overflows")
	}
	return d.IntPart(), nil
}

// EscapeCSVCell prevents spreadsheet formula injection, including formulas
// preceded by whitespace/control bytes. Apply to every untrusted exported cell.
func EscapeCSVCell(s string) string {
	trimmed := strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\uFEFF' })
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}
