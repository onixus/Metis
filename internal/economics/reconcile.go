package economics

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// ImportCategoryTotal uses integer decimal strings in minor units to avoid JSON precision loss.
type ImportCategoryTotal struct {
	Category string `json:"category"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
	Delta    string `json:"delta"`
}

type ImportReconciliation struct {
	Matched int                   `json:"matched"`
	Added   int                   `json:"added"`
	Changed int                   `json:"changed"`
	Removed int                   `json:"removed"`
	Totals  []ImportCategoryTotal `json:"totals"`
}

// PreviewReconciliation validates a complete trusted preview without writing a financial version.
// A closed period can be previewed; committing it still requires explicit recalculation.
func (s *Service) PreviewReconciliation(ctx context.Context, sc authz.Scope, product kernel.ID, period string, preview ports.FinancePreview, expectedVersion int) (ImportReconciliation, error) {
	in, previous, err := s.prepareImport(ctx, sc, product, period, preview, expectedVersion, false)
	if err != nil {
		return ImportReconciliation{}, err
	}
	result, err := reconcileImport(previous, &in)
	if err != nil {
		return ImportReconciliation{}, err
	}
	if err := validateInput(sc, in); err != nil {
		return ImportReconciliation{}, err
	}
	if err := s.validateReferences(ctx, sc, in); err != nil {
		return ImportReconciliation{}, err
	}
	if previous.Version > 0 && previous.Currency != in.Currency {
		return ImportReconciliation{}, kernel.Invalid("currency", "period currency cannot change")
	}
	candidate := Snapshot{SnapshotInfo: SnapshotInfo{ID: kernel.NewID(), ProductID: product, Period: period, Currency: in.Currency, Version: previous.Version + 1}, Rows: in.Rows, Fields: in.Fields}
	if _, err := s.calculateSnapshot(context.WithValue(ctx, candidateSnapshotKey{}, candidate), sc, candidate, ReportInput{}); err != nil {
		return ImportReconciliation{}, err
	}
	if err := s.record(ctx, sc, product, period, previous.Version, audit.ActionViewFinance); err != nil {
		return ImportReconciliation{}, err
	}
	return result, nil
}

// A struct key avoids delimiter collisions in user-controlled descriptions.
// TODO(question-50): replace this conservative key only with a trusted source row ID.
type importRowKey struct {
	Product, Feature, Track                                        kernel.ID
	Category, Team, Item, Branch, Bundle, Description, File, Sheet string
}

func rowImportKey(r Row) importRowKey {
	return importRowKey{r.ProductID, r.FeatureID, r.CertificationTrackID, r.Category, r.TeamID, r.ModelItem, r.Branch, r.BundleID, r.Description, r.Source.File, r.Source.Sheet}
}

func reconcileImport(previous Snapshot, in *SaveInput) (ImportReconciliation, error) {
	manual := map[string]bool{}
	for _, field := range in.Fields {
		manual[field.Key] = field.Source == "manual"
	}
	for _, row := range previous.Rows {
		if canonicalManualRow(row, manual) {
			in.Rows = append(in.Rows, kernel.CloneValue(row))
		}
	}

	old := make(map[importRowKey]Row, len(previous.Rows))
	for _, row := range previous.Rows {
		key := rowImportKey(row)
		if _, exists := old[key]; exists {
			return ImportReconciliation{}, kernel.Invalid("reconciliation", "previous rows have ambiguous business keys; import without preserving rules or disambiguate descriptions")
		}
		old[key] = row
	}
	seen := make(map[importRowKey]bool, len(in.Rows))
	result := ImportReconciliation{Totals: []ImportCategoryTotal{}}
	before, after := map[string]decimal.Decimal{}, map[string]decimal.Decimal{}
	for _, row := range previous.Rows {
		before[row.Category] = before[row.Category].Add(decimal.NewFromInt(row.Amount.Amount))
	}
	for i := range in.Rows {
		row := &in.Rows[i]
		key := rowImportKey(*row)
		if seen[key] {
			return ImportReconciliation{}, kernel.Invalid("reconciliation", "import rows have ambiguous business keys; disambiguate descriptions")
		}
		seen[key] = true
		after[row.Category] = after[row.Category].Add(decimal.NewFromInt(row.Amount.Amount))
		prev, exists := old[key]
		if !exists {
			result.Added++
			continue
		}
		result.Matched++
		if row.Amount != prev.Amount || row.Headcount != prev.Headcount {
			result.Changed++
		}
		row.ID, row.AllocationSource = prev.ID, prev.AllocationSource
		row.Allocations = append([]Allocation{}, prev.Allocations...)
		row.Values = make(map[string]string, len(prev.Values))
		for k, v := range prev.Values {
			if manual[k] {
				row.Values[k] = v
			}
		}
	}
	for key, row := range old {
		if seen[key] {
			continue
		}
		configured := len(row.Allocations) > 0
		for key := range row.Values {
			configured = configured || manual[key]
		}
		if configured {
			return ImportReconciliation{}, kernel.Invalid("reconciliation", "import removes a row with financial rules or manual values; explicit review required")
		}
		result.Removed++
	}
	categories := map[string]bool{}
	for k := range before {
		categories[k] = true
	}
	for k := range after {
		categories[k] = true
	}
	keys := make([]string, 0, len(categories))
	for k := range categories {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		result.Totals = append(result.Totals, ImportCategoryTotal{Category: k, Previous: before[k].String(), Current: after[k].String(), Delta: after[k].Sub(before[k]).String()})
	}
	return result, nil
}

func canonicalManualRow(row Row, manual map[string]bool) bool {
	if row.Source.File != CanonicalManualSource || row.Category != DirectCost || row.Amount.Amount != 0 || len(row.Allocations) > 0 || row.FeatureID != kernel.NilID || row.CertificationTrackID != kernel.NilID || row.Branch != "" || row.BundleID != "" || len(row.Values) == 0 || !manual[row.Source.Sheet] {
		return false
	}
	for key := range row.Values {
		if !manual[key] {
			return false
		}
	}
	return true
}
