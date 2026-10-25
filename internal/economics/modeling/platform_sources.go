package economics

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// PlatformSourceKind names a typed read-only source, never an arbitrary URL or table.
type PlatformSourceKind string

const (
	SourceWorklogs   PlatformSourceKind = "worklogs"
	SourceCRMRevenue PlatformSourceKind = "crm_revenue"
	SourceHeadcount  PlatformSourceKind = "headcount"
)

// PlatformProvenance identifies aggregate snapshots; individual authors and salaries must not be included.
type PlatformProvenance struct {
	Source   string `json:"source"`
	RecordID string `json:"record_id"`
	Version  string `json:"version"`
}

// PlatformValue is a complete historical aggregate. Worklogs are seconds, CRM uses minor
// currency units, and headcount is people. Zero is meaningful only for a captured complete source.
type PlatformValue struct {
	Value      decimal.Decimal      `json:"value"`
	Currency   string               `json:"currency,omitempty"`
	SnapshotID string               `json:"snapshot_id"`
	AsOf       time.Time            `json:"as_of"`
	Provenance []PlatformProvenance `json:"provenance"`
}

type PlatformSources interface {
	ReadFormulaSource(ctx context.Context, sc authz.Scope, kind PlatformSourceKind, sl Slice) (PlatformValue, error)
}

// SourceObservation accompanies only executed source reads in Explain.
type SourceObservation struct {
	Kind          PlatformSourceKind   `json:"kind"`
	ProductID     kernel.ID            `json:"product_id,omitempty"`
	TeamID        kernel.ID            `json:"team_id,omitempty"`
	Period        string               `json:"period"`
	Item          string               `json:"item,omitempty"`
	Currency      string               `json:"currency,omitempty"`
	SnapshotID    string               `json:"snapshot_id"`
	AsOf          time.Time            `json:"as_of"`
	Provenance    []PlatformProvenance `json:"provenance"`
	CapturedValue string               `json:"captured_value"`
	Overridden    bool                 `json:"overridden"`
}

func (s *Service) WithPlatformSources(port PlatformSources) *Service {
	s.platformSources = port
	s.platformIdentity = new(byte)
	return s
}

func (f *factSource) Platform(kind string, flt formula.Filter) (decimal.Decimal, error) {
	value, err := f.platform(kind, flt)
	if err != nil {
		f.sourceErr = err
	}
	return value, err
}

func (f *factSource) platform(kind string, flt formula.Filter) (decimal.Decimal, error) {
	source := PlatformSourceKind(kind)
	if source != SourceWorklogs && source != SourceCRMRevenue && source != SourceHeadcount {
		return decimal.Zero, kernel.Invalid("source", "неизвестный источник формулы")
	}
	if source == SourceCRMRevenue {
		if err := f.markCRMRead(); err != nil {
			return decimal.Zero, err
		}
	}
	filter, err := factFilter("", flt)
	if err != nil {
		return decimal.Zero, err
	}
	if filter.Period == nil || filter.Period.IsZero() {
		return decimal.Zero, kernel.Invalid("period", "для прямого источника нужен конкретный месяц")
	}
	sl := Slice{Period: *filter.Period}
	if filter.Product != nil {
		sl.ProductID = *filter.Product
	}
	if filter.Team != nil {
		sl.TeamID = *filter.Team
	}
	if filter.Item != nil {
		sl.Item = *filter.Item
	}
	if err := f.svc.requireRead(f.caller, sl.ProductID, authz.FinanceFull); err != nil {
		return decimal.Zero, err
	}
	if f.svc.platformSources == nil {
		return decimal.Zero, fmt.Errorf("%w: источник %s не подключён", kernel.ErrUnavailable, source)
	}
	if err := f.ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	f.platformLock.Lock()
	defer f.platformLock.Unlock()
	if err := f.ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	cacheKey := platformCacheKey{f.svc.platformIdentity, f.cacheScope, kind, metricSliceKey("", sl.formulaSlice())}
	v, exists := f.platformMemo[cacheKey]
	if !exists {
		if len(f.platformMemo) >= 128 {
			return decimal.Zero, kernel.Invalid("sources", "не более 128 снимков источников за расчёт")
		}
		if err := f.ctx.Err(); err != nil {
			return decimal.Zero, err
		}
		v, err = f.svc.platformSources.ReadFormulaSource(f.ctx, f.caller, source, sl)
		if err != nil {
			return decimal.Zero, fmt.Errorf("источник %s: %w", source, err)
		}
		if err := validatePlatformValue(source, v, f.svc.cfg.Currency); err != nil {
			return decimal.Zero, err
		}
		v = kernel.CloneValue(v)
		f.platformMemo[cacheKey] = v
	}
	value, overridden, err := f.platformOverride(source, sl, v.Value)
	if err != nil {
		return decimal.Zero, err
	}
	if f.active != "" {
		*f.platformCost += 1 + len(v.Provenance)
		if *f.platformCost > 16384 {
			return decimal.Zero, kernel.Invalid("sources", "превышен бюджет происхождения источников")
		}
		observation := SourceObservation{Kind: source, ProductID: sl.ProductID, TeamID: sl.TeamID, Period: sl.Period.String(), Item: sl.Item, Currency: v.Currency, SnapshotID: v.SnapshotID, AsOf: v.AsOf, Provenance: kernel.CloneValue(v.Provenance), CapturedValue: v.Value.String(), Overridden: overridden}
		ex := Explanation{Key: kind, Kind: formula.RefSource, Value: value, Sources: []SourceObservation{observation}}
		f.traces[f.active] = append(f.traces[f.active], evaluatedInput{field: &ex})
	}
	return value, nil
}

func validatePlatformValue(kind PlatformSourceKind, v PlatformValue, currency string) error {
	if err := formula.ValidateDecimal(v.Value); err != nil {
		return kernel.Invalid("source_value", err.Error())
	}
	if v.Value.IsNegative() || !v.Value.Equal(v.Value.Truncate(0)) || v.Value.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return kernel.Invalid("source_value", "источник должен вернуть неотрицательное целое int64")
	}
	if v.SnapshotID == "" || len(v.SnapshotID) > 256 || v.AsOf.IsZero() || len(v.Provenance) == 0 || len(v.Provenance) > 256 {
		return fmt.Errorf("%w: неполный снимок источника", kernel.ErrUnavailable)
	}
	if kind == SourceCRMRevenue {
		if v.Currency == "" || v.Currency != currency {
			return kernel.Invalid("currency", "валюта выручки CRM должна совпадать с валютой модели")
		}
	} else if v.Currency != "" {
		return kernel.Invalid("currency", "worklogs и численность не являются денежными источниками")
	}
	for _, record := range v.Provenance {
		if record.Source == "" || len(record.Source) > 128 || record.RecordID == "" || len(record.RecordID) > 256 || len(record.Version) > 256 {
			return fmt.Errorf("%w: неполное происхождение источника", kernel.ErrUnavailable)
		}
	}
	return nil
}

// Source overrides replace one complete product/month aggregate; captured provenance remains visible.
func (f *factSource) platformOverride(kind PlatformSourceKind, sl Slice, base decimal.Decimal) (decimal.Decimal, bool, error) {
	for _, override := range f.overrides {
		if override.Source != kind || (!override.Period.IsZero() && override.Period != sl.Period) {
			continue
		}
		if override.ProductID != kernel.NilID && sl.ProductID != kernel.NilID && override.ProductID != sl.ProductID {
			continue
		}
		if override.TeamID != kernel.NilID && sl.TeamID != kernel.NilID && override.TeamID != sl.TeamID {
			continue
		}
		if override.ProductID != sl.ProductID || override.TeamID != sl.TeamID || sl.Item != "" {
			return decimal.Zero, false, kernel.Invalid("overrides", "подмена источника и вычисляемый продукт/команда должны совпадать точно; частичная подмена агрегата неоднозначна")
		}
		return override.Value, true, nil
	}
	return base, false, nil
}
