package economics

import (
	"context"
	"math"
	"sort"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func canonicalProductPnL(r book.ProductReport, p Period, currency string) PnL {
	m := func(v int64) kernel.Money { return kernel.Money{Amount: v, Currency: currency} }
	return PnL{ProductID: r.ProductID, Period: p, Revenue: m(r.Revenue - r.BundleRevenue), BundleRevenue: m(r.BundleRevenue), DirectCosts: m(r.DirectCost), HubLoad: m(r.HubCost), DirectProfit: m(r.DirectProfit), LoadedProfit: m(r.LoadedProfit)}
}
func (s *Service) canonicalReport(ctx context.Context, sc authz.Scope, p Period, product kernel.ID, overrides []Override) (book.Report, bool, error) {
	c, err := s.canonicalStore()
	if err != nil {
		return book.Report{}, false, nil
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil || !bound {
		return book.Report{}, bound, err
	}
	snapshot, err := c.books.Snapshot(ctx, sc, a.BookProductID, p.String(), 0)
	if err != nil {
		return book.Report{}, true, err
	}
	known := map[string]bool{book.Revenue: true, book.Payroll: true, book.DirectCost: true, book.Marketing: true, book.HubCost: true, book.CertificationCost: true, book.MaintenanceCost: true}
	for _, field := range snapshot.Fields {
		if field.Source != "calculated" {
			known[field.Key] = true
		}
	}
	in := book.ReportInput{ProductID: a.BookProductID, Period: p.String(), FilterProductID: product, Overrides: map[string]string{}}
	aliases := map[string]string{s.cfg.RevenueField: book.Revenue, s.cfg.PayrollField: book.Payroll, s.cfg.DirectCostField: book.DirectCost, s.cfg.MarketingField: book.Marketing}
	for _, o := range overrides {
		if o.Source != "" {
			continue
		}
		if !o.Period.IsZero() && o.Period != p {
			continue
		}
		if o.ProductID != kernel.NilID && o.ProductID != product {
			continue
		}
		if o.FieldKey == s.cfg.BundleRevenueField || o.FieldKey == "direct_total" || o.FieldKey == "direct_profit" || o.FieldKey == "loaded_profit" {
			return book.Report{}, true, kernel.Invalid("overrides", "canonical derived totals cannot be replaced; override revenue or source cost inputs")
		}
		key := o.FieldKey
		if raw := aliases[key]; raw != "" {
			key = raw
		}
		for raw, mapped := range a.FieldKeys {
			if key == mapped {
				key = raw
				break
			}
		}
		if known[key] {
			in.Overrides[key] = o.Value.String()
		} // Model-only virtual inputs affect formulas, not fixed book categories.
	}
	report, err := c.books.Report(ctx, sc, in)
	return report, true, err
}
func (s *Service) canonicalPnL(ctx context.Context, sc authz.Scope, product kernel.ID, p Period, overrides []Override) (PnL, bool, error) {
	r, bound, err := s.canonicalReport(ctx, sc, p, product, overrides)
	if err != nil || !bound {
		return PnL{}, bound, err
	}
	if len(r.Products) == 0 {
		return canonicalProductPnL(book.ProductReport{ProductID: product}, p, r.Currency), true, nil
	}
	return canonicalProductPnL(r.Products[0], p, r.Currency), true, nil
}
func (s *Service) canonicalPortfolio(ctx context.Context, sc authz.Scope, p Period) (PortfolioPnL, bool, error) {
	r, bound, err := s.canonicalReport(ctx, sc, p, kernel.NilID, nil)
	if err != nil || !bound {
		return PortfolioPnL{}, bound, err
	}
	costs, err := checkedHistoryMoney(r.Total.DirectCost, r.Total.HubCost)
	if err != nil {
		return PortfolioPnL{}, true, err
	}
	out := PortfolioPnL{Period: p, Products: []PnL{}, Revenue: kernel.Money{Amount: r.Total.Revenue, Currency: r.Currency}, Costs: kernel.Money{Amount: costs, Currency: r.Currency}, Profit: kernel.Money{Amount: r.Total.LoadedProfit, Currency: r.Currency}}
	for _, row := range r.Products {
		out.Products = append(out.Products, canonicalProductPnL(row, p, r.Currency))
	}
	return out, true, nil
}
func (s *Service) canonicalTeamCosts(ctx context.Context, sc authz.Scope, p Period, team kernel.ID) (TeamCost, bool, error) {
	r, bound, err := s.canonicalReport(ctx, sc, p, kernel.NilID, nil)
	if err != nil || !bound {
		return TeamCost{}, bound, err
	}
	c, _ := s.canonicalStore()
	known, err := c.canonicalTeamMap(ctx, sc)
	if err != nil {
		return TeamCost{}, true, err
	}
	out := TeamCost{TeamID: team, Period: p, Total: kernel.Money{Currency: r.Currency}, ByProduct: map[kernel.ID]kernel.Money{}}
	for _, row := range r.Teams {
		id, err := resolveCanonicalTeam(row.TeamID, known)
		if err != nil {
			return out, true, err
		}
		if id != team {
			continue
		}
		var sumErr error
		out.Total.Amount, sumErr = checkedHistoryMoney(out.Total.Amount, row.Cost)
		if sumErr != nil {
			return out, true, sumErr
		}
		out.ByProduct[row.ProductID] = kernel.Money{Amount: row.Cost, Currency: r.Currency}
	}
	return out, true, nil
}
func (s *Service) canonicalMatrix(ctx context.Context, sc authz.Scope, p Period) (Matrix, bool, error) {
	r, bound, err := s.canonicalReport(ctx, sc, p, kernel.NilID, nil)
	if err != nil || !bound {
		return Matrix{}, bound, err
	}
	c, _ := s.canonicalStore()
	known, err := c.canonicalTeamMap(ctx, sc)
	if err != nil {
		return Matrix{}, true, err
	}
	out := Matrix{Period: p, Teams: []kernel.ID{}, Products: []kernel.ID{}, Cells: map[kernel.ID]map[kernel.ID]kernel.Money{}}
	seen := map[kernel.ID]bool{}
	for _, row := range r.Teams {
		id, err := resolveCanonicalTeam(row.TeamID, known)
		if err != nil {
			return out, true, err
		}
		if out.Cells[id] == nil {
			out.Cells[id] = map[kernel.ID]kernel.Money{}
			out.Teams = append(out.Teams, id)
		}
		out.Cells[id][row.ProductID] = kernel.Money{Amount: row.Cost, Currency: r.Currency}
		if !seen[row.ProductID] {
			seen[row.ProductID] = true
			out.Products = append(out.Products, row.ProductID)
		}
	}
	sort.Slice(out.Teams, func(i, j int) bool { return out.Teams[i].String() < out.Teams[j].String() })
	sort.Slice(out.Products, func(i, j int) bool { return out.Products[i].String() < out.Products[j].String() })
	return out, true, nil
}

func checkedHistoryMoney(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, kernel.Invalid("amount", "int64 monetary overflow")
	}
	return a + b, nil
}
