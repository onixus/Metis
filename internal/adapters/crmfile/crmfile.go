// Package crmfile — файловый адаптер порта CRM: читает CSV-выгрузки аккаунтов и сделок
// (ТЗ 4.1, question-03). Содержимое файлов — недоверенный ввод: значения читаются как строки,
// ничего не интерпретируется, размер файла ограничен.
package crmfile

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// DefaultMaxFileSize — лимит размера одного файла выгрузки.
const DefaultMaxFileSize int64 = 16 << 20

// ErrFileTooLarge — файл превышает лимит.
var ErrFileTooLarge = errors.New("crmfile: файл превышает лимит размера")

// Adapter читает выгрузки из каталога: accounts.csv и deals.csv.
type Adapter struct {
	dir     string
	maxSize int64
}

// Option настраивает адаптер.
type Option func(*Adapter)

// WithMaxFileSize задаёт лимит размера файла в байтах.
func WithMaxFileSize(n int64) Option { return func(a *Adapter) { a.maxSize = n } }

// New создаёт адаптер над каталогом выгрузок.
func New(dir string, opts ...Option) *Adapter {
	a := &Adapter{dir: dir, maxSize: DefaultMaxFileSize}
	for _, o := range opts {
		o(a)
	}
	return a
}

var _ ports.CRM = (*Adapter)(nil)

// Accounts читает accounts.csv. Колонки: id, name, segment, arr, currency.
func (a *Adapter) Accounts(ctx context.Context) ([]ports.Account, error) {
	rows, err := a.read(ctx, "accounts.csv", []string{"id", "name"})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Account, 0, len(rows))
	for i, r := range rows {
		arr, err := parseMoney(r["arr"], r["currency"])
		if err != nil {
			return nil, fmt.Errorf("accounts.csv строка %d: %w", i+2, err)
		}
		out = append(out, ports.Account{
			ExternalID: r["id"],
			Name:       r["name"],
			Segment:    r["segment"],
			ARR:        arr,
		})
	}
	return out, nil
}

// Deals читает deals.csv. Колонки: id, account_id, name, stage, amount, currency, product_key,
// regulatory, version, expected_date, requested_features (через «;»), blocks_on_features, notes,
// outcome (won|lost), reason, closed_date, products (через «;»), features (через «;»).
func (a *Adapter) Deals(ctx context.Context) ([]ports.Deal, error) {
	rows, err := a.read(ctx, "deals.csv", []string{"id", "account_id", "product_key"})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Deal, 0, len(rows))
	for i, r := range rows {
		line := i + 2
		amount, err := parseMoney(r["amount"], r["currency"])
		if err != nil {
			return nil, fmt.Errorf("deals.csv строка %d: %w", line, err)
		}
		var date kernel.Date
		if s := strings.TrimSpace(r["expected_date"]); s != "" {
			if date, err = kernel.ParseDate(s); err != nil {
				return nil, fmt.Errorf("deals.csv строка %d: %w", line, err)
			}
		}
		var closed kernel.Date
		if s := strings.TrimSpace(r["closed_date"]); s != "" {
			if closed, err = kernel.ParseDate(s); err != nil {
				return nil, fmt.Errorf("deals.csv строка %d: %w", line, err)
			}
		}
		outcome, err := parseOutcome(r["outcome"])
		if err != nil {
			return nil, fmt.Errorf("deals.csv строка %d: %w", line, err)
		}
		products := splitList(r["products"])
		if len(products) == 0 && strings.TrimSpace(r["product_key"]) != "" {
			products = []string{r["product_key"]}
		}
		out = append(out, ports.Deal{
			ExternalID:        r["id"],
			AccountID:         r["account_id"],
			Name:              r["name"],
			Stage:             ports.DealStage(r["stage"]),
			Amount:            amount,
			ProductKey:        r["product_key"],
			Regulatory:        r["regulatory"],
			Version:           r["version"],
			ExpectedDate:      date,
			RequestedFeatures: splitList(r["requested_features"]),
			BlocksOnFeatures:  parseBool(r["blocks_on_features"]),
			Notes:             r["notes"],
			Outcome:           outcome,
			Reason:            r["reason"],
			ClosedDate:        closed,
			Products:          products,
			Features:          splitList(r["features"]),
		})
	}
	return out, nil
}

// read открывает файл, проверяет лимит размера и возвращает строки как словари по заголовку.
func (a *Adapter) read(ctx context.Context, name string, required []string) ([]map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("crmfile: %w", err)
	}
	path := filepath.Join(a.dir, filepath.Base(name))
	f, err := os.Open(path) // #nosec G304 -- каталог задаётся конфигурацией, имя файла фиксировано
	if err != nil {
		return nil, fmt.Errorf("%w: crmfile %s: %w", kernel.ErrUnavailable, name, err)
	}
	defer func() { _ = f.Close() }() // только чтение
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: crmfile %s: %w", kernel.ErrUnavailable, name, err)
	}
	if st.Size() > a.maxSize {
		return nil, fmt.Errorf("%w: %s (%d байт, лимит %d)", ErrFileTooLarge, name, st.Size(), a.maxSize)
	}
	r := csv.NewReader(io.LimitReader(f, a.maxSize+1))
	r.FieldsPerRecord = -1
	r.LazyQuotes = false
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: заголовок: %w", kernel.ErrValidation, name, err)
	}
	for i := range header {
		header[i] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header[i], "\uFEFF")))
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	for _, col := range required {
		if _, ok := idx[col]; !ok {
			return nil, fmt.Errorf("%w: %s: нет колонки %q", kernel.ErrValidation, name, col)
		}
	}
	var rows []map[string]string
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %s строка %d: %w", kernel.ErrValidation, name, line, err)
		}
		row := make(map[string]string, len(header))
		for col, i := range idx {
			if i < len(rec) {
				row[col] = strings.TrimSpace(rec[i])
			}
		}
		for _, col := range required {
			if row[col] == "" {
				return nil, fmt.Errorf("%w: %s строка %d: пустое поле %q", kernel.ErrValidation, name, line, col)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// parseMoney переводит десятичную сумму в минорные единицы (инвариант 6). Пустая сумма — ноль.
func parseMoney(amount, currency string) (kernel.Money, error) {
	amount = strings.TrimSpace(amount)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if amount == "" {
		return kernel.Money{}, nil
	}
	if currency == "" {
		currency = "RUB"
	}
	d, err := decimal.NewFromString(strings.ReplaceAll(amount, " ", ""))
	if err != nil {
		return kernel.Money{}, fmt.Errorf("%w: сумма %q: %w", kernel.ErrValidation, amount, err)
	}
	minor := d.Mul(decimal.NewFromInt(100)).RoundBank(0)
	if !minor.IsInteger() || !minor.BigInt().IsInt64() {
		return kernel.Money{}, fmt.Errorf("%w: сумма %q вне диапазона", kernel.ErrValidation, amount)
	}
	return kernel.Money{Amount: minor.IntPart(), Currency: currency}, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "да", "y":
		return true
	}
	return false
}

// parseOutcome разбирает исход сделки; пустое значение — открытая сделка.
func parseOutcome(raw string) (ports.DealOutcome, error) {
	switch ports.DealOutcome(strings.ToLower(strings.TrimSpace(raw))) {
	case ports.DealOpen:
		return ports.DealOpen, nil
	case ports.DealWon:
		return ports.DealWon, nil
	case ports.DealLost:
		return ports.DealLost, nil
	}
	return ports.DealOpen, fmt.Errorf("%w: исход сделки %q: допустимы won, lost или пусто", kernel.ErrValidation, raw)
}
