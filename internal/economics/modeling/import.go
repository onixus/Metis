package economics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// ProductDirectory — порт: ключ продукта → идентификатор; реализуется portfoliograph.
type ProductDirectory interface {
	ProductIDByKey(ctx context.Context, key string) (kernel.ID, error)
}

// WithImport подключает адаптер импорта и справочник продуктов (EC-01).
func (s *Service) WithImport(r ports.FinanceImport, dir ProductDirectory) *Service {
	s.reader, s.products = r, dir
	return s
}

// TemplateInput — шаблон импорта.
type TemplateInput struct {
	ID     kernel.ID
	Name   string
	Sheets []SheetMap
}

// SaveTemplate создаёт или изменяет шаблон импорта (EC-07).
func (s *Service) SaveTemplate(ctx context.Context, sc authz.Scope, in TemplateInput) (Template, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return Template{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return Template{}, kernel.Invalid("name", "название обязательно")
	}
	if len(in.Sheets) == 0 {
		return Template{}, kernel.Invalid("sheets", "нужен хотя бы один лист")
	}
	for _, sh := range in.Sheets {
		if strings.TrimSpace(sh.Sheet) == "" {
			return Template{}, kernel.Invalid("sheet", "имя листа обязательно")
		}
		if len(sh.Columns) == 0 {
			return Template{}, kernel.Invalid("columns", "нужна хотя бы одна колонка")
		}
		for _, c := range sh.Columns {
			f, err := s.store.Field(ctx, c.FieldKey)
			if err != nil {
				return Template{}, fmt.Errorf("колонка %q: %w", c.Column, err)
			}
			if f.Latest().Source != SourceImport {
				return Template{}, kernel.Invalid("field_key", fmt.Sprintf("поле %q не заполняется импортом", c.FieldKey))
			}
		}
	}
	now := s.clock.Now()
	t := Template{ID: in.ID, Name: in.Name, Sheets: in.Sheets, UpdatedAt: now}
	if t.ID == kernel.NilID {
		t.ID, t.CreatedAt = kernel.NewID(), now
	} else {
		prev, err := s.store.Template(ctx, t.ID)
		if err != nil {
			return Template{}, err
		}
		t.CreatedAt = prev.CreatedAt
	}
	if err := s.store.SaveTemplate(ctx, t); err != nil {
		return Template{}, fmt.Errorf("save template: %w", err)
	}
	return t, nil
}

// Templates возвращает шаблоны импорта.
func (s *Service) Templates(ctx context.Context, sc authz.Scope) ([]Template, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Templates(ctx)
}

// ImportInput — параметры загрузки финансовых данных (EC-01, EC-07).
type ImportInput struct {
	TemplateID kernel.ID
	// Period — период загрузки; если в файле есть колонка периода, она имеет приоритет.
	Period   Period
	FileName string
	Data     []byte
	// Scheduled — загрузка по расписанию, а не вручную.
	Scheduled bool
	// Force разрешает загрузку в закрытый период (EC-11).
	Force bool
}

// ImportResult — результат предпросмотра или применения загрузки.
type ImportResult struct {
	Batch ImportBatch `json:"batch"`
	Rows  []FactRow   `json:"rows"`
}

// PreviewImport разбирает файл и показывает, что будет загружено, ничего не сохраняя (EC-07).
func (s *Service) PreviewImport(ctx context.Context, sc authz.Scope, in ImportInput) (ImportResult, error) {
	return s.runImport(ctx, sc, in, false)
}

// ApplyImport загружает финансовые данные. Повторная загрузка периода создаёт новую
// версию данных; история загрузок сохраняется (EC-07).
func (s *Service) ApplyImport(ctx context.Context, sc authz.Scope, in ImportInput) (ImportResult, error) {
	return s.runImport(ctx, sc, in, true)
}

func (s *Service) runImport(ctx context.Context, sc authz.Scope, in ImportInput, apply bool) (ImportResult, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return ImportResult{}, err
	}
	if s.reader == nil {
		return ImportResult{}, fmt.Errorf("%w: адаптер импорта не подключён", kernel.ErrUnavailable)
	}
	tpl, err := s.store.Template(ctx, in.TemplateID)
	if err != nil {
		return ImportResult{}, err
	}
	if len(in.Data) == 0 {
		return ImportResult{}, kernel.Invalid("data", "файл пуст")
	}
	read, err := s.reader.Read(ctx, bytes.NewReader(in.Data), mapping(tpl))
	if err != nil {
		return ImportResult{}, err
	}

	sum := sha256.Sum256(in.Data)
	batch := ImportBatch{
		ID: kernel.NewID(), TemplateID: tpl.ID, Period: in.Period, Status: BatchPreview,
		FileName: in.FileName, SHA256: hex.EncodeToString(sum[:]), Actor: sc.Subject(),
		At: s.clock.Now(), Scheduled: in.Scheduled, Errors: rowErrors(read.Errors),
	}
	rows, periods, errs := s.convert(ctx, batch.ID, in.Period, tpl, read.Cells)
	batch.Errors = append(batch.Errors, errs...)
	batch.Rows = len(rows)
	sortRowErrors(batch.Errors)
	if len(rows) == 0 {
		batch.Status = BatchRejected
		if apply {
			if err := s.store.SaveBatch(ctx, batch); err != nil {
				return ImportResult{}, fmt.Errorf("save batch: %w", err)
			}
		}
		return ImportResult{Batch: batch}, fmt.Errorf("%w: ни одна строка файла не загружена", kernel.ErrValidation)
	}
	if len(periods) != 1 {
		return ImportResult{Batch: batch}, kernel.Invalid("period", "загрузка охватывает несколько периодов: разделите файл")
	}
	batch.Period = periods[0]
	for i := range rows {
		rows[i].Period = batch.Period
	}
	if !apply {
		return ImportResult{Batch: batch, Rows: rows}, nil
	}

	closed, err := s.PeriodClosed(ctx, batch.Period)
	if err != nil {
		return ImportResult{}, err
	}
	if closed && !in.Force {
		return ImportResult{}, fmt.Errorf("%w: период %s закрыт; загрузка — только явным действием", kernel.ErrConflict, batch.Period)
	}
	version, err := s.nextDataVersion(ctx, batch.Period)
	if err != nil {
		return ImportResult{}, err
	}
	batch.DataVersion, batch.Status = version, BatchApplied
	loaded := map[string]bool{}
	for i := range rows {
		rows[i].DataVersion = version
		loaded[rows[i].FieldKey] = true
	}
	// Перенос строк прежней версии считается до записи загрузки: иначе действующей
	// версией периода уже была бы новая, ещё пустая.
	carried, err := s.carryForward(ctx, batch, loaded)
	if err != nil {
		return ImportResult{}, err
	}
	if err := s.store.SaveBatch(ctx, batch); err != nil {
		return ImportResult{}, fmt.Errorf("save batch: %w", err)
	}
	if err := s.store.AppendFacts(ctx, append(rows, carried...)); err != nil {
		return ImportResult{}, fmt.Errorf("append facts: %w", err)
	}
	s.logAccess(ctx, sc, "write", "economics.import:"+batch.ID.String(), kernel.NilID, map[string]any{
		"period": batch.Period.String(), "version": version, "rows": len(rows),
		"errors": len(batch.Errors), "scheduled": in.Scheduled, "sha256": batch.SHA256,
	})
	if err := s.emit(ctx, EventBatchApplied, batch.ID, kernel.NilID, sc.Subject(), batch); err != nil {
		return ImportResult{}, err
	}
	if closed {
		if err := s.emit(ctx, EventPeriodRecalced, kernel.NewID(), kernel.NilID, sc.Subject(),
			map[string]string{"period": batch.Period.String(), "reason": "загрузка в закрытый период"}); err != nil {
			return ImportResult{}, err
		}
	}
	return ImportResult{Batch: batch, Rows: rows}, nil
}

// carryForward переносит в новую версию данных периода строки полей, которых нет в загрузке:
// файлы разных статей грузятся по отдельности и не должны затирать друг друга.
func (s *Service) carryForward(ctx context.Context, batch ImportBatch, loaded map[string]bool) ([]FactRow, error) {
	prev, err := s.store.Facts(ctx, FactFilter{Period: &batch.Period})
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	out := make([]FactRow, 0, len(prev))
	for _, r := range prev {
		if loaded[r.FieldKey] {
			continue
		}
		r.ID, r.BatchID, r.DataVersion = kernel.NewID(), batch.ID, batch.DataVersion
		out = append(out, r)
	}
	return out, nil
}

// convert превращает ячейки файла в строки данных, собирая ошибки по строкам (EC-07).
func (s *Service) convert(ctx context.Context, batchID kernel.ID, fallback Period, tpl Template,
	cells []ports.FinanceCell) ([]FactRow, []Period, []RowError) {
	scale := map[string]bool{} // ключ колонки → значения уже в минорных единицах
	for _, sh := range tpl.Sheets {
		for _, c := range sh.Columns {
			scale[sh.Sheet+"|"+c.FieldKey] = c.MinorUnits
		}
	}
	var (
		rows    []FactRow
		errs    []RowError
		periods = map[string]Period{}
	)
	fields := map[string]Field{}
	for _, c := range cells {
		f, ok := fields[c.FieldKey]
		if !ok {
			loaded, err := s.store.Field(ctx, c.FieldKey)
			if err != nil {
				errs = append(errs, RowError{Sheet: c.Sheet, Row: c.Row, Message: fmt.Sprintf("поле %q не заведено", c.FieldKey)})
				continue
			}
			fields[c.FieldKey] = loaded
			f = loaded
		}
		period := fallback
		if c.Period != "" {
			p, err := ParsePeriod(c.Period)
			if err != nil {
				errs = append(errs, RowError{Sheet: c.Sheet, Row: c.Row, Message: fmt.Sprintf("период %q: YYYY-MM", c.Period)})
				continue
			}
			period = p
		}
		if period.IsZero() {
			errs = append(errs, RowError{Sheet: c.Sheet, Row: c.Row, Message: "период не задан ни в файле, ни в запросе"})
			continue
		}
		product, err := s.productID(ctx, c.ProductKey)
		if err != nil {
			errs = append(errs, RowError{Sheet: c.Sheet, Row: c.Row, Message: fmt.Sprintf("продукт %q: %v", c.ProductKey, err)})
			continue
		}
		team, err := s.teamID(ctx, c.TeamKey)
		if err != nil {
			errs = append(errs, RowError{Sheet: c.Sheet, Row: c.Row, Message: fmt.Sprintf("команда %q: %v", c.TeamKey, err)})
			continue
		}
		value := c.Value
		if f.Latest().Type == FieldMoney && !scale[c.Sheet+"|"+c.FieldKey] {
			value = value.Mul(decimal.NewFromInt(100)).RoundBank(0)
		}
		periods[period.String()] = period
		rows = append(rows, FactRow{
			ID: kernel.NewID(), BatchID: batchID, FieldKey: c.FieldKey, ProductID: product, TeamID: team,
			Period: period, Item: c.Item, Value: value, Sheet: c.Sheet, Row: c.Row,
		})
	}
	out := make([]Period, 0, len(periods))
	for _, p := range periods {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return rows, out, errs
}

func (s *Service) productID(ctx context.Context, key string) (kernel.ID, error) {
	if strings.TrimSpace(key) == "" {
		return kernel.NilID, nil
	}
	if s.products == nil {
		return kernel.NilID, fmt.Errorf("справочник продуктов не подключён")
	}
	id, err := s.products.ProductIDByKey(ctx, key)
	if err != nil {
		return kernel.NilID, fmt.Errorf("не найден")
	}
	return id, nil
}

func (s *Service) teamID(ctx context.Context, key string) (kernel.ID, error) {
	if strings.TrimSpace(key) == "" {
		return kernel.NilID, nil
	}
	teams, err := s.store.Teams(ctx)
	if err != nil {
		return kernel.NilID, fmt.Errorf("список команд: %w", err)
	}
	for _, t := range teams {
		if t.Key == key || t.Name == key {
			return t.ID, nil
		}
	}
	return kernel.NilID, fmt.Errorf("не заведена")
}

func mapping(t Template) ports.FinanceMapping {
	out := ports.FinanceMapping{Sheets: make([]ports.FinanceSheetMapping, 0, len(t.Sheets))}
	for _, sh := range t.Sheets {
		m := ports.FinanceSheetMapping{
			Sheet: sh.Sheet, HeaderRow: sh.HeaderRow, ProductColumn: sh.ProductColumn,
			TeamColumn: sh.TeamColumn, PeriodColumn: sh.PeriodColumn, ItemColumn: sh.ItemColumn,
		}
		for _, c := range sh.Columns {
			m.Columns = append(m.Columns, ports.FinanceColumn{Column: c.Column, FieldKey: c.FieldKey})
		}
		out.Sheets = append(out.Sheets, m)
	}
	return out
}

func rowErrors(in []ports.FinanceImportRowError) []RowError {
	out := make([]RowError, 0, len(in))
	for _, e := range in {
		out = append(out, RowError{Sheet: e.Sheet, Row: e.Row, Column: e.Column, Message: e.Message})
	}
	return out
}

func sortRowErrors(errs []RowError) {
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Sheet != errs[j].Sheet {
			return errs[i].Sheet < errs[j].Sheet
		}
		if errs[i].Row != errs[j].Row {
			return errs[i].Row < errs[j].Row
		}
		return errs[i].Column < errs[j].Column
	})
}

// Batches возвращает историю загрузок (EC-07).
func (s *Service) Batches(ctx context.Context, sc authz.Scope, p Period) ([]ImportBatch, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Batches(ctx, p)
}
