package economics

import (
	"fmt"
	"math"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/prioritization"
)

func add(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, kernel.Invalid("amount", "int64 monetary overflow")
	}
	return a + b, nil
}

func integer(v decimal.Decimal) (int64, error) {
	if !v.Equal(v.Truncate(0)) || v.LessThan(decimal.NewFromInt(math.MinInt64)) || v.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, kernel.Invalid("amount", "expected int64 minor units")
	}
	return v.IntPart(), nil
}

type portion struct {
	product   kernel.ID
	amount    int64
	share     decimal.Decimal
	remainder decimal.Decimal
}

// portions uses largest remainder allocation and UUID ordering as a stable tie breaker.
func portions(r Row) ([]portion, error) {
	if len(r.Allocations) == 0 {
		return []portion{{product: r.ProductID, amount: r.Amount.Amount, share: decimal.NewFromInt(1)}}, nil
	}
	out, assigned := make([]portion, 0, len(r.Allocations)), decimal.Zero
	for _, allocation := range r.Allocations {
		raw := decimal.NewFromInt(r.Amount.Amount).Mul(allocation.Share)
		amount, err := integer(raw.Floor())
		if err != nil {
			return nil, err
		}
		assigned = assigned.Add(decimal.NewFromInt(amount))
		out = append(out, portion{product: allocation.ProductID, amount: amount, share: allocation.Share, remainder: raw.Sub(raw.Floor())})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].remainder.Equal(out[j].remainder) {
			return out[i].product.String() < out[j].product.String()
		}
		return out[i].remainder.GreaterThan(out[j].remainder)
	})
	left, err := integer(decimal.NewFromInt(r.Amount.Amount).Sub(assigned))
	if err != nil {
		return nil, err
	}
	if left < 0 || left > int64(len(out)) {
		return nil, kernel.Invalid("allocations", "allocation conservation failed")
	}
	for i := int64(0); i < left; i++ {
		out[i].amount++
	}
	return out, nil
}

type selectedRow struct {
	row     Row
	amount  int64
	share   decimal.Decimal
	product kernel.ID
}

func calculate(snapshot Snapshot, in ReportInput) (Report, error) {
	_, _, err := compileFields(snapshot.Fields)
	if err != nil {
		return Report{}, err
	}
	if len(in.Overrides) > MaxFields+7 {
		return Report{}, kernel.Invalid("overrides", "too many scenario inputs")
	}
	cost := 0
	for _, row := range snapshot.Rows {
		count := len(row.Allocations)
		if count == 0 {
			count = 1
		}
		cost += count * (8 + len(snapshot.Fields))
		if cost > MaxCalculationCost {
			return Report{}, kernel.Invalid("calculation", "financial calculation cost limit exceeded")
		}
	}
	byProduct, all := map[kernel.ID][]selectedRow{}, []selectedRow{}
	teams, investments := map[string]*TeamCost{}, map[string]*Investment{}
	for _, row := range snapshot.Rows {
		if in.Team != "" && row.TeamID != in.Team {
			continue
		}
		parts, err := portions(row)
		if err != nil {
			return Report{}, err
		}
		for _, part := range parts {
			if in.FilterProductID != kernel.NilID && part.product != in.FilterProductID {
				continue
			}
			selected := selectedRow{row: row, amount: part.amount, share: part.share, product: part.product}
			byProduct[part.product] = append(byProduct[part.product], selected)
			all = append(all, selected)
			if row.TeamID != "" && row.Category != Revenue {
				key := part.product.String() + "/" + row.TeamID
				if teams[key] == nil {
					teams[key] = &TeamCost{ProductID: part.product, TeamID: row.TeamID, Headcount: row.Headcount}
				}
				team := teams[key]
				if row.Headcount > team.Headcount {
					team.Headcount = row.Headcount
				}
				team.Cost, err = add(team.Cost, part.amount)
				if err != nil {
					return Report{}, err
				}
			}
			refs := map[string]string{"branch": row.Branch}
			if row.FeatureID != kernel.NilID {
				refs["feature"] = row.FeatureID.String()
			}
			if row.CertificationTrackID != kernel.NilID {
				refs["certification"] = row.CertificationTrackID.String()
			}
			for kind, key := range refs {
				if key == "" {
					continue
				}
				identity := part.product.String() + "/" + kind + "/" + key
				if investments[identity] == nil {
					investments[identity] = &Investment{ProductID: part.product, Kind: kind, Key: key}
				}
				investment := investments[identity]
				if row.Category == Revenue {
					investment.Revenue, err = add(investment.Revenue, part.amount)
				} else {
					investment.Cost, err = add(investment.Cost, part.amount)
				}
				if err != nil {
					return Report{}, err
				}
				investment.Balance, err = add(investment.Revenue, -investment.Cost)
				if err != nil {
					return Report{}, err
				}
			}
		}
	}
	if len(in.Overrides) > 0 && len(byProduct) > 1 {
		return Report{}, kernel.Invalid("overrides", "select one product for a scenario")
	}
	for _, field := range snapshot.Fields {
		cost += len(field.Formula) * (len(byProduct) + 1)
		if cost > MaxCalculationCost {
			return Report{}, kernel.Invalid("calculation", "financial calculation cost limit exceeded")
		}
	}
	report := Report{ProductID: snapshot.ProductID, Period: snapshot.Period, Currency: snapshot.Currency, Version: snapshot.Version, Closed: snapshot.Closed,
		Scenario: len(in.Overrides) > 0, Products: []ProductReport{}, Teams: []TeamCost{}, Investments: []Investment{}}
	for product, rows := range byProduct {
		pr, err := productReport(product, rows, snapshot.Fields, in.Overrides)
		if err != nil {
			return Report{}, err
		}
		report.Products = append(report.Products, pr)
	}
	sort.Slice(report.Products, func(i, j int) bool {
		return report.Products[i].ProductID.String() < report.Products[j].ProductID.String()
	})
	report.Total, err = productReport(kernel.NilID, all, snapshot.Fields, in.Overrides)
	if err != nil {
		return Report{}, err
	}
	// Aggregate input scenarios do not pretend to reallocate team or feature facts.
	if !report.Scenario {
		for _, team := range teams {
			report.Teams = append(report.Teams, *team)
		}
		for _, investment := range investments {
			report.Investments = append(report.Investments, *investment)
		}
		sort.Slice(report.Teams, func(i, j int) bool {
			a, b := report.Teams[i], report.Teams[j]
			return a.ProductID.String()+a.TeamID < b.ProductID.String()+b.TeamID
		})
		sort.Slice(report.Investments, func(i, j int) bool {
			a, b := report.Investments[i], report.Investments[j]
			return a.ProductID.String()+a.Kind+a.Key < b.ProductID.String()+b.Kind+b.Key
		})
	}
	return report, nil
}

func productReport(product kernel.ID, rows []selectedRow, fields []Field, overrides map[string]string) (ProductReport, error) {
	definitions, order, err := compileFields(fields)
	if err != nil {
		return ProductReport{}, err
	}
	pr := ProductReport{ProductID: product, Amounts: map[string]int64{}, Metrics: map[string]string{}, Lineage: map[string]Lineage{}}
	values := map[string]decimal.Decimal{}
	for _, category := range []string{Revenue, Payroll, DirectCost, Marketing, HubCost, CertificationCost, MaintenanceCost} {
		pr.Amounts[category] = 0
		values[category] = decimal.Zero
	}
	for _, field := range fields {
		if field.Type != "date" && field.Type != "catalog" {
			values[field.Key] = decimal.Zero
		}
	}
	for _, selected := range rows {
		row := selected.row
		pr.Amounts[row.Category], err = add(pr.Amounts[row.Category], selected.amount)
		if err != nil {
			return ProductReport{}, err
		}
		line := pr.Lineage[row.Category]
		line.RowIDs = append(line.RowIDs, row.ID)
		line.Sources = append(line.Sources, row.Source)
		pr.Lineage[row.Category] = line
		for key, raw := range row.Values {
			field := definitions[key]
			if field.Type == "date" || field.Type == "catalog" {
				continue
			}
			v, err := numericValue(raw)
			if err != nil {
				return ProductReport{}, err
			}
			if field.Type == "money" {
				amount, err := integer(v)
				if err != nil {
					return ProductReport{}, err
				}
				allocatedRow := row
				allocatedRow.Amount.Amount = amount
				allocated, err := portions(allocatedRow)
				if err != nil {
					return ProductReport{}, err
				}
				for _, part := range allocated {
					if part.product == selected.product {
						v = decimal.NewFromInt(part.amount)
						break
					}
				}
			} else {
				v = v.Mul(selected.share)
			}
			values[key] = values[key].Add(v)
			line := pr.Lineage[key]
			line.RowIDs = append(line.RowIDs, row.ID)
			line.Sources = append(line.Sources, row.Source)
			pr.Lineage[key] = line
		}
	}
	for key, raw := range overrides {
		value, err := numericValue(raw)
		if err != nil {
			return ProductReport{}, err
		}
		if isCategory(key) {
			amount, err := integer(value)
			if err != nil {
				return ProductReport{}, err
			}
			if amount < 0 {
				return ProductReport{}, kernel.Invalid("overrides", "negative category amount")
			}
			pr.Amounts[key] = amount
		} else {
			field, exists := definitions[key]
			if !exists || field.Source == "calculated" || field.Type == "date" || field.Type == "catalog" {
				return ProductReport{}, kernel.Invalid("overrides", "only numeric input fields can be overridden")
			}
			if field.Type == "money" {
				if _, err := integer(value); err != nil {
					return ProductReport{}, err
				}
			}
			values[key] = value
		}
		pr.Lineage[key] = Lineage{Formula: "scenario override", Dependencies: []string{}, RowIDs: []kernel.ID{}, Sources: []Source{}}
	}
	pr.Revenue, pr.HubCost = pr.Amounts[Revenue], pr.Amounts[HubCost]
	for _, category := range []string{Payroll, DirectCost, Marketing, CertificationCost, MaintenanceCost} {
		pr.DirectCost, err = add(pr.DirectCost, pr.Amounts[category])
		if err != nil {
			return ProductReport{}, err
		}
	}
	pr.DirectProfit, err = add(pr.Revenue, -pr.DirectCost)
	if err != nil {
		return ProductReport{}, err
	}
	pr.LoadedProfit, err = add(pr.DirectProfit, -pr.HubCost)
	if err != nil {
		return ProductReport{}, err
	}
	for key, amount := range pr.Amounts {
		values[key] = decimal.NewFromInt(amount)
	}
	values["direct_total"] = decimal.NewFromInt(pr.DirectCost)
	values["direct_profit"] = decimal.NewFromInt(pr.DirectProfit)
	values["loaded_profit"] = decimal.NewFromInt(pr.LoadedProfit)
	pr.Lineage["direct_total"] = mergeLineage("payroll + direct_cost + marketing + certification_cost + maintenance_cost", []string{Payroll, DirectCost, Marketing, CertificationCost, MaintenanceCost}, pr.Lineage)
	pr.Lineage["direct_profit"] = mergeLineage("revenue - direct_total", []string{Revenue, "direct_total"}, pr.Lineage)
	pr.Lineage["loaded_profit"] = mergeLineage("direct_profit - hub_cost", []string{"direct_profit", HubCost}, pr.Lineage)
	for _, key := range order {
		field := definitions[key]
		if field.Type == "date" || field.Type == "catalog" {
			continue
		}
		if field.Source == "calculated" {
			formula, err := prioritization.ParseFormula(field.Formula)
			if err != nil {
				return ProductReport{}, err
			}
			value, err := formula.Eval(values)
			if err != nil {
				return ProductReport{}, fmt.Errorf("financial indicator %s: %w", key, err)
			}
			if value.NumDigits() > 128 || value.Exponent() < -64 || value.Exponent() > 64 {
				return ProductReport{}, kernel.Invalid("formula", "financial indicator numeric precision limit exceeded")
			}
			if field.Type == "money" {
				value = value.RoundBank(0)
				if _, err := integer(value); err != nil {
					return ProductReport{}, err
				}
			}
			values[key] = value
			pr.Lineage[key] = mergeLineage(field.Formula, formula.Variables(), pr.Lineage)
		}
		if field.Type == "money" {
			if _, err := integer(values[key]); err != nil {
				return ProductReport{}, err
			}
		}
		pr.Metrics[key] = values[key].String()
	}
	return pr, nil
}

func mergeLineage(formula string, dependencies []string, lines map[string]Lineage) Lineage {
	line := Lineage{Formula: formula, Dependencies: dependencies, RowIDs: []kernel.ID{}, Sources: []Source{}}
	seen := map[kernel.ID]bool{}
	for _, dep := range dependencies {
		source := lines[dep]
		for i, id := range source.RowIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			line.RowIDs = append(line.RowIDs, id)
			if i < len(source.Sources) {
				line.Sources = append(line.Sources, source.Sources[i])
			}
		}
	}
	return line
}
