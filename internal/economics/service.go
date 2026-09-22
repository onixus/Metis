package economics

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/prioritization"
)

type Auditor interface {
	Append(context.Context, audit.Entry) (audit.Record, error)
}

type ReferenceReader interface {
	ValidateFinanceRow(context.Context, authz.Scope, Row) error
	ValidateFinanceProduct(context.Context, authz.Scope, kernel.ID) error
}

type Service struct {
	store      Store
	audit      Auditor
	clock      kernel.Clock
	references ReferenceReader
}

func (s *Service) WithReferences(references ReferenceReader) *Service {
	s.references = references
	return s
}

func NewService(store Store, auditor Auditor, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{store: store, audit: auditor, clock: clock}
}

func (s *Service) record(ctx context.Context, sc authz.Scope, product kernel.ID, period string, version int, action audit.Action) error {
	if s.audit == nil {
		return fmt.Errorf("%w: financial audit is required", kernel.ErrUnavailable)
	}
	_, err := s.audit.Append(ctx, audit.Entry{Actor: sc.Subject(), Action: action, ObjectType: "economics.period", ObjectID: period,
		ProductID: product, Details: map[string]any{"version": version}})
	if err != nil {
		return fmt.Errorf("financial audit: %w", err)
	}
	return nil
}

func validatePeriod(period string) error {
	if len(period) != 7 {
		return kernel.Invalid("period", "expected YYYY-MM")
	}
	if _, err := time.Parse("2006-01", period); err != nil {
		return kernel.Invalid("period", "expected YYYY-MM")
	}
	return nil
}

func numericValue(raw string) (decimal.Decimal, error) {
	// Restrict exponent and precision before decimal parsing; user input cannot create enormous powers.
	if len(raw) > 48 || !regexp.MustCompile(`^-?[0-9]+(\.[0-9]{1,12})?$`).MatchString(raw) {
		return decimal.Zero, kernel.Invalid("values", "expected bounded decimal without exponent")
	}
	v, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, kernel.Invalid("values", "invalid decimal")
	}
	return v, nil
}

func isCategory(c string) bool {
	switch c {
	case Revenue, Payroll, DirectCost, Marketing, HubCost, CertificationCost, MaintenanceCost:
		return true
	}
	return false
}

func validateInput(sc authz.Scope, in SaveInput) error {
	if err := sc.Require(authz.ActionWriteFinance, in.ProductID); err != nil {
		return err
	}
	if err := validatePeriod(in.Period); err != nil {
		return err
	}
	if len(in.Currency) != 3 || in.Currency != strings.ToUpper(in.Currency) || !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(in.Currency) {
		return kernel.Invalid("currency", "expected three uppercase currency letters")
	}
	if in.ExpectedVersion < 0 || len(in.Rows) > MaxRows || len(in.Fields) > MaxFields {
		return kernel.Invalid("snapshot", "version or size limit")
	}
	if len(in.Source) > 256 || len(in.SourceHash) > 128 {
		return kernel.Invalid("source", "source metadata too long")
	}
	fields, _, err := compileFields(in.Fields)
	if err != nil {
		return err
	}
	ids := map[kernel.ID]bool{}
	for i := range in.Rows {
		r := in.Rows[i]
		if err := sc.Require(authz.ActionWriteFinance, r.ProductID); err != nil {
			return err
		}
		if !isCategory(r.Category) || r.Amount.Currency != in.Currency || r.Amount.Amount < 0 {
			return kernel.Invalid("rows", "invalid category, currency or negative amount")
		}
		if r.ID != kernel.NilID && ids[r.ID] {
			return kernel.Invalid("rows", "duplicate row id")
		}
		ids[r.ID] = true
		if len(r.Description) > 2000 || len(r.TeamID) > 128 || len(r.Branch) > 128 || len(r.BundleID) > 128 || r.Headcount < 0 || r.Headcount > 1000000 || len(r.Source.File) > 512 || len(r.Source.Sheet) > 256 || len(r.Source.Hash) > 128 || r.Source.Row < 0 || r.Source.Row > 1048576 {
			return kernel.Invalid("rows", "row metadata limit")
		}
		if r.Category == Payroll && (r.TeamID == "" || r.Headcount < 1) {
			return kernel.Invalid("payroll", "team and aggregate headcount required")
		}
		if len(r.Allocations) > 100 || (r.AllocationSource != "" && r.AllocationSource != "manual" && r.AllocationSource != "worklogs") {
			return kernel.Invalid("allocations", "invalid allocation source or limit")
		}
		total, targets := decimal.Zero, map[kernel.ID]bool{}
		for _, a := range r.Allocations {
			if err := sc.Require(authz.ActionWriteFinance, a.ProductID); err != nil {
				return err
			}
			if a.Share.Exponent() < -12 || a.Share.Exponent() > 0 || targets[a.ProductID] || !a.Share.GreaterThan(decimal.Zero) || a.Share.GreaterThan(decimal.NewFromInt(1)) {
				return kernel.Invalid("allocations", "shares must be unique positive decimals up to 12 places")
			}
			targets[a.ProductID] = true
			total = total.Add(a.Share)
		}
		if len(r.Allocations) > 0 && !total.Equal(decimal.NewFromInt(1)) {
			return kernel.Invalid("allocations", "shares must sum to one")
		}
		for key, raw := range r.Values {
			f, ok := fields[key]
			if !ok || f.Source == "calculated" {
				return kernel.Invalid("values", "unknown or calculated field input")
			}
			switch f.Type {
			case "date":
				if _, err := kernel.ParseDate(raw); err != nil {
					return err
				}
			case "catalog":
				if len(raw) > 256 {
					return kernel.Invalid("values", "catalog value too long")
				}
			default:
				v, err := numericValue(raw)
				if err != nil {
					return err
				}
				if f.Type == "money" && (!v.Equal(v.Truncate(0)) || v.LessThan(decimal.NewFromInt(-9223372036854775807-1)) || v.GreaterThan(decimal.NewFromInt(9223372036854775807))) {
					return kernel.Invalid("values", "money field must be int64 minor units")
				}
			}
		}
	}
	return nil
}

func compileFields(fields []Field) (map[string]Field, []string, error) {
	if len(fields) > MaxFields {
		return nil, nil, kernel.Invalid("fields", "too many fields")
	}
	byKey, dependencies := map[string]Field{}, map[string][]string{}
	for _, f := range fields {
		if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`).MatchString(f.Key) || isCategory(f.Key) || f.Key == "direct_profit" || f.Key == "loaded_profit" || f.Key == "direct_total" || len(f.Name) > 256 {
			return nil, nil, kernel.Invalid("fields", "invalid or reserved field key")
		}
		if _, exists := byKey[f.Key]; exists {
			return nil, nil, kernel.Invalid("fields", "duplicate field key")
		}
		switch f.Type {
		case "money", "number", "percent", "date", "catalog":
		default:
			return nil, nil, kernel.Invalid("fields", "unknown field type")
		}
		switch f.Source {
		case "import", "manual", "calculated":
		default:
			return nil, nil, kernel.Invalid("fields", "unknown field source")
		}
		if f.Source == "calculated" {
			if f.Type == "date" || f.Type == "catalog" {
				return nil, nil, kernel.Invalid("fields", "calculated fields must be numeric")
			}
			formula, err := prioritization.ParseFormula(f.Formula)
			if err != nil {
				return nil, nil, err
			}
			dependencies[f.Key] = formula.Variables()
		} else if f.Formula != "" {
			return nil, nil, kernel.Invalid("fields", "formula requires calculated source")
		}
		byKey[f.Key] = f
	}
	state, order := map[string]int{}, []string{}
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return kernel.Invalid("formula", "cyclic financial indicators")
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		for _, dep := range dependencies[key] {
			if isCategory(dep) || dep == "direct_profit" || dep == "loaded_profit" || dep == "direct_total" {
				continue
			}
			f, exists := byKey[dep]
			if !exists || f.Type == "date" || f.Type == "catalog" {
				return kernel.Invalid("formula", "unknown or nonnumeric field reference")
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[key] = 2
		order = append(order, key)
		return nil
	}
	for _, f := range fields {
		if err := visit(f.Key); err != nil {
			return nil, nil, err
		}
	}
	return byKey, order, nil
}

func (s *Service) Save(ctx context.Context, sc authz.Scope, in SaveInput) (Snapshot, error) {
	if err := validateInput(sc, in); err != nil {
		return Snapshot{}, err
	}
	if s.references == nil {
		return Snapshot{}, fmt.Errorf("%w: financial reference validation is required", kernel.ErrUnavailable)
	}
	if err := s.references.ValidateFinanceProduct(ctx, sc, in.ProductID); err != nil {
		return Snapshot{}, err
	}
	for _, row := range in.Rows {
		if err := s.references.ValidateFinanceProduct(ctx, sc, row.ProductID); err != nil {
			return Snapshot{}, err
		}
		if err := s.references.ValidateFinanceRow(ctx, sc, row); err != nil {
			return Snapshot{}, err
		}
		for _, allocation := range row.Allocations {
			if err := s.references.ValidateFinanceProduct(ctx, sc, allocation.ProductID); err != nil {
				return Snapshot{}, err
			}
		}
	}
	previous, err := s.store.Snapshot(ctx, sc, in.ProductID, in.Period, 0)
	if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return Snapshot{}, err
	}
	if previous.Version != in.ExpectedVersion {
		return Snapshot{}, kernel.ErrConflict
	}
	if previous.Closed && !in.Recalculate {
		return Snapshot{}, fmt.Errorf("%w: closed period requires explicit recalculation", kernel.ErrConflict)
	}
	if previous.Version > 0 && previous.Currency != in.Currency {
		return Snapshot{}, kernel.Invalid("currency", "period currency cannot change")
	}
	snapshot := Snapshot{SnapshotInfo: SnapshotInfo{ID: kernel.NewID(), ProductID: in.ProductID, Period: in.Period, Currency: in.Currency,
		Version: in.ExpectedVersion + 1, Closed: previous.Closed, Source: in.Source, SourceHash: in.SourceHash,
		CreatedAt: s.clock.Now().UTC(), CreatedBy: sc.Subject(), RowCount: len(in.Rows)}, Rows: in.Rows, Fields: in.Fields}
	snapshot, err = cloneSnapshot(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range snapshot.Rows {
		if snapshot.Rows[i].ID == kernel.NilID {
			snapshot.Rows[i].ID = kernel.NewID()
		}
	}
	if _, err := calculate(snapshot, ReportInput{}); err != nil {
		return Snapshot{}, err
	}
	if err := s.record(ctx, sc, in.ProductID, in.Period, snapshot.Version, audit.ActionRuleChange); err != nil {
		return Snapshot{}, err
	}
	if err := s.store.Append(ctx, sc, snapshot, in.ExpectedVersion); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) Snapshot(ctx context.Context, sc authz.Scope, product kernel.ID, period string, version int) (Snapshot, error) {
	if err := validatePeriod(period); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := s.store.Snapshot(ctx, sc, product, period, version)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.record(ctx, sc, product, period, snapshot.Version, audit.ActionViewFinance); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) History(ctx context.Context, sc authz.Scope, product kernel.ID, period string) ([]SnapshotInfo, error) {
	if err := validatePeriod(period); err != nil {
		return nil, err
	}
	history, err := s.store.History(ctx, sc, product, period)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, sc, product, period, 0, audit.ActionViewFinance); err != nil {
		return nil, err
	}
	return history, nil
}

func (s *Service) Close(ctx context.Context, sc authz.Scope, product kernel.ID, period string, expectedVersion int) (Snapshot, error) {
	if err := sc.Require(authz.ActionWriteFinance, product); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := s.store.Snapshot(ctx, sc, product, period, 0)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.Version != expectedVersion || snapshot.Closed {
		return Snapshot{}, kernel.ErrConflict
	}
	snapshot.ID, snapshot.Version, snapshot.Closed = kernel.NewID(), expectedVersion+1, true
	snapshot.CreatedAt, snapshot.CreatedBy = s.clock.Now().UTC(), sc.Subject()
	if err := s.record(ctx, sc, product, period, snapshot.Version, audit.ActionRuleChange); err != nil {
		return Snapshot{}, err
	}
	if err := s.store.Append(ctx, sc, snapshot, expectedVersion); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) Report(ctx context.Context, sc authz.Scope, in ReportInput) (Report, error) {
	if in.FilterProductID != kernel.NilID {
		if err := sc.Require(authz.ActionReadFinance, in.FilterProductID); err != nil {
			return Report{}, err
		}
	}
	snapshot, err := s.store.Snapshot(ctx, sc, in.ProductID, in.Period, in.Version)
	if err != nil {
		return Report{}, err
	}
	report, err := calculate(snapshot, in)
	if err != nil {
		return Report{}, err
	}
	action := audit.ActionViewFinance
	if in.Export {
		action = audit.ActionExport
	}
	if err := s.record(ctx, sc, in.ProductID, in.Period, snapshot.Version, action); err != nil {
		return Report{}, err
	}
	return report, nil
}
