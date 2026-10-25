package app

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

const canonicalManualSource = "metis:canonical-manual"

type canonicalManualDefinitionKey struct{}
type canonicalManualGrant struct {
	Owner  kernel.ID
	Period string
	Field  economics.Field
}
type canonicalManualWriter struct{ app *App }

func allowedManualProjection(ctx context.Context, owner kernel.ID, period string, before, after []economics.Field) bool {
	grant, ok := ctx.Value(canonicalManualDefinitionKey{}).(canonicalManualGrant)
	if !ok || grant.Owner != owner || grant.Period != period || grant.Field.Source != "manual" {
		return false
	}
	oldOthers := []economics.Field{}
	newOthers := []economics.Field{}
	found := false
	for _, f := range before {
		if f.Key != grant.Field.Key {
			oldOthers = append(oldOthers, f)
		}
	}
	for _, f := range after {
		if f.Key == grant.Field.Key {
			if found || !reflect.DeepEqual(f, grant.Field) {
				return false
			}
			found = true
		} else {
			newOthers = append(newOthers, f)
		}
	}
	return found && reflect.DeepEqual(oldOthers, newOthers)
}

type manualGroup struct {
	Product    kernel.ID
	Team, Item string
}

func canonicalManualDecimal(value decimal.Decimal) (string, error) {
	raw := value.String()
	if len(raw) > 48 || !regexp.MustCompile(`^-?[0-9]+(\.[0-9]{1,12})?$`).MatchString(raw) {
		return "", kernel.Invalid("value", "canonical book supports up to 48 decimal characters and 12 fractional places; value was not rounded")
	}
	return raw, nil
}
func (w canonicalManualWriter) WriteCanonicalManualValue(ctx context.Context, sc authz.Scope, in modeling.CanonicalManualInput) (modeling.FactRow, error) {
	if in.Field.Source != modeling.SourceManual || in.Slice.ProductID == kernel.NilID || in.Slice.Period.IsZero() {
		return modeling.FactRow{}, kernel.Invalid("field", "validated manual field and concrete slice required")
	}
	if in.Field.Type != modeling.FieldMoney && in.Field.Type != modeling.FieldNumber && in.Field.Type != modeling.FieldPercent {
		return modeling.FactRow{}, kernel.Invalid("field", "numeric manual input required")
	}
	if len(in.Slice.Item) > 256 || !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`).MatchString(in.BookFieldKey) {
		return modeling.FactRow{}, kernel.Invalid("field", "canonical field key must fit 64 characters and item 256")
	}
	if _, err := canonicalManualDecimal(in.Value); err != nil {
		return modeling.FactRow{}, err
	}
	current, err := w.app.Economics.Snapshot(ctx, sc, in.BookProductID, in.Slice.Period.String(), 0)
	if err != nil {
		return modeling.FactRow{}, err
	}
	if current.Closed {
		return modeling.FactRow{}, fmt.Errorf("%w: canonical period is closed", kernel.ErrConflict)
	}
	if in.Field.Type == modeling.FieldMoney && in.Field.Currency != current.Currency {
		return modeling.FactRow{}, kernel.Invalid("currency", "manual field and canonical book currencies must match")
	}
	projection, err := w.app.Economics.Project(ctx, sc, in.BookProductID, in.Slice.Period.String(), current.Version)
	if err != nil {
		return modeling.FactRow{}, err
	}
	fields := append([]economics.Field{}, current.Fields...)
	projected := economics.Field{Key: in.BookFieldKey, Name: in.Field.Name, Type: string(in.Field.Type), Source: "manual"}
	found := false
	for i, f := range fields {
		if f.Key == in.BookFieldKey {
			if f.Source != "manual" || f.Type != projected.Type {
				return modeling.FactRow{}, kernel.Invalid("field", "canonical input is not a compatible manual field")
			}
			fields[i] = projected
			found = true
		}
	}
	if !found {
		fields = append(fields, projected)
	}
	team := ""
	if in.Slice.TeamID != kernel.NilID {
		teams, err := w.app.Modeling.Teams(ctx, sc)
		if err != nil {
			return modeling.FactRow{}, err
		}
		for _, candidate := range teams {
			if candidate.ID == in.Slice.TeamID {
				team = candidate.Key
				break
			}
		}
		if team == "" || len(team) > 128 {
			return modeling.FactRow{}, kernel.Invalid("team", "canonical manual input requires a declared team")
		}
	}
	hasManualValue := map[kernel.ID]bool{}
	for _, row := range current.Rows {
		if _, ok := row.Values[in.BookFieldKey]; ok {
			hasManualValue[row.ID] = true
		}
	}
	groupValues := map[manualGroup]decimal.Decimal{}
	for _, f := range projection.Facts {
		if f.FieldKey == in.BookFieldKey && hasManualValue[f.RowID] {
			g := manualGroup{f.ProductID, f.TeamID, f.Item}
			groupValues[g] = groupValues[g].Add(f.Value)
		}
	}
	target := manualGroup{in.Slice.ProductID, team, in.Slice.Item}
	groupValues[target] = in.Value
	rows := make([]economics.Row, 0, len(current.Rows)+len(groupValues))
	oldIDs := map[manualGroup]kernel.ID{}
	for _, r := range current.Rows {
		if r.Values != nil {
			r.Values = kernel.CloneValue(r.Values)
			delete(r.Values, in.BookFieldKey)
		}
		owned := r.Source.File == canonicalManualSource && r.Source.Sheet == in.BookFieldKey && r.Category == economics.DirectCost && r.Amount.Amount == 0 && len(r.Allocations) == 0 && r.FeatureID == kernel.NilID && r.CertificationTrackID == kernel.NilID && r.Branch == "" && r.BundleID == ""
		if owned && len(r.Values) == 0 {
			oldIDs[manualGroup{r.ProductID, r.TeamID, r.ModelItem}] = r.ID
			continue
		}
		rows = append(rows, r)
	}
	groups := make([]manualGroup, 0, len(groupValues))
	for g := range groupValues {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Product != b.Product {
			return a.Product.String() < b.Product.String()
		}
		if a.Team != b.Team {
			return a.Team < b.Team
		}
		return a.Item < b.Item
	})
	var responseID kernel.ID
	for index, g := range groups {
		value := groupValues[g]
		raw, err := canonicalManualDecimal(value)
		if err != nil {
			return modeling.FactRow{}, err
		}
		id := oldIDs[g]
		if id == kernel.NilID {
			id = kernel.NewID()
		}
		hash, err := contentHash(struct {
			Key               string
			Product           kernel.ID
			Team, Item, Value string
		}{in.BookFieldKey, g.Product, g.Team, g.Item, raw})
		if err != nil {
			return modeling.FactRow{}, err
		}
		rows = append(rows, economics.Row{ID: id, ProductID: g.Product, Category: economics.DirectCost, Amount: kernel.Money{Currency: current.Currency}, TeamID: g.Team, ModelItem: g.Item, Description: "Ручное значение общего конструктора", Source: economics.Source{File: canonicalManualSource, Sheet: in.BookFieldKey, Row: index + 1, Hash: hash}, Values: map[string]string{in.BookFieldKey: raw}, Allocations: []economics.Allocation{}})
		if g == target {
			responseID = id
		}
	}
	grant := canonicalManualGrant{Owner: in.BookProductID, Period: current.Period, Field: projected}
	writeCtx := context.WithValue(ctx, canonicalManualDefinitionKey{}, grant)
	saved, err := w.app.Economics.Save(writeCtx, sc, economics.SaveInput{ProductID: current.ProductID, Period: current.Period, Currency: current.Currency, ExpectedVersion: current.Version, Source: current.Source, SourceHash: current.SourceHash, Rows: rows, Fields: fields})
	if err != nil {
		return modeling.FactRow{}, err
	}
	return modeling.FactRow{BookProductID: saved.ProductID, ID: responseID, BatchID: saved.ID, ProductID: in.Slice.ProductID, TeamID: in.Slice.TeamID, Period: in.Slice.Period, Item: in.Slice.Item, FieldKey: in.FieldKey, Value: in.Value, DataVersion: saved.Version}, nil
}
