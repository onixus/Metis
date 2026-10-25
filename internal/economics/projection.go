package economics

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

type ProjectedFact struct {
	Item      string          `json:"item,omitempty"`
	RowID     kernel.ID       `json:"row_id"`
	ProductID kernel.ID       `json:"product_id"`
	TeamID    string          `json:"team_id"`
	FieldKey  string          `json:"field_key"`
	Type      string          `json:"type"`
	Value     decimal.Decimal `json:"value"`
	BundleID  string          `json:"bundle_id,omitempty"`
	Source    Source          `json:"source"`
}

type Projection struct {
	Snapshot SnapshotInfo    `json:"snapshot"`
	Facts    []ProjectedFact `json:"facts"`
}

// Project exposes only authorized immutable book facts, using the P&L allocation algorithm.
func (s *Service) Project(ctx context.Context, sc authz.Scope, product kernel.ID, period string, version int) (Projection, error) {
	snapshot, err := s.Snapshot(ctx, sc, product, period, version)
	if err != nil {
		return Projection{}, err
	}
	// Bound the projection with exactly the report's allocation/field cost check.
	if _, err := s.calculateSnapshot(ctx, sc, snapshot, ReportInput{}); err != nil {
		return Projection{}, err
	}
	out := Projection{Snapshot: snapshot.SnapshotInfo, Facts: []ProjectedFact{}}
	inputs := []Field{}
	for _, field := range snapshot.Fields {
		if field.Source != "calculated" {
			inputs = append(inputs, field)
		}
	}
	for _, row := range snapshot.Rows {
		if err := ctx.Err(); err != nil {
			return Projection{}, err
		}
		parts, err := portions(row)
		if err != nil {
			return Projection{}, err
		}
		for _, part := range parts {
			// productReport applies the identical money/non-money allocation to custom inputs.
			report, err := productReport(part.product, []selectedRow{{row: row, amount: part.amount, share: part.share, product: part.product}}, inputs, nil)
			if err != nil {
				return Projection{}, err
			}
			appendFact := func(key, fieldType string, value decimal.Decimal) {
				out.Facts = append(out.Facts, ProjectedFact{Item: row.ModelItem, RowID: row.ID, ProductID: part.product, TeamID: row.TeamID, FieldKey: key, Type: fieldType, Value: value, Source: row.Source, BundleID: row.BundleID})
			}
			appendFact(row.Category, "money", decimal.NewFromInt(part.amount))
			for _, field := range snapshot.Fields {
				if field.Source == "calculated" || field.Type == "date" || field.Type == "catalog" {
					continue
				}
				raw, ok := report.Metrics[field.Key]
				if !ok {
					continue
				}
				v, err := decimal.NewFromString(raw)
				if err != nil {
					return Projection{}, kernel.Invalid("projection", "invalid numeric input")
				}
				appendFact(field.Key, field.Type, v)
			}
		}
	}
	sort.SliceStable(out.Facts, func(i, j int) bool {
		a, b := out.Facts[i], out.Facts[j]
		if a.ProductID != b.ProductID {
			return a.ProductID.String() < b.ProductID.String()
		}
		if a.RowID != b.RowID {
			return a.RowID.String() < b.RowID.String()
		}
		return a.FieldKey < b.FieldKey
	})
	return out, nil
}
