package economics

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// sumField суммирует значения поля в срезе продукта и периода.
// overrides — подмена значений сценария (EC-13); nil для фактических данных.
func (s *Service) sumField(ctx context.Context, key string, product kernel.ID, p Period, overrides []Override) (decimal.Decimal, error) {
	src := s.source(ctx, overrides)
	sl := Slice{ProductID: product, Period: p}.formulaSlice()
	vals, err := src.Values(key, formula.Filter{Slice: sl})
	if err != nil {
		return decimal.Zero, err
	}
	out := decimal.Zero
	for _, v := range vals {
		out = out.Add(v)
	}
	return out, nil
}

// productsWithData возвращает продукты, по которым есть данные за период.
func (s *Service) productsWithData(ctx context.Context, p Period) ([]kernel.ID, error) {
	rows, err := s.store.Facts(ctx, FactFilter{Period: &p})
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	seen := map[kernel.ID]bool{}
	out := make([]kernel.ID, 0)
	for _, r := range rows {
		if r.ProductID == kernel.NilID || seen[r.ProductID] {
			continue
		}
		seen[r.ProductID] = true
		out = append(out, r.ProductID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// directCosts — прямые затраты продукта за период: ФОТ, прямые затраты, маркетинг.
func (s *Service) directCosts(ctx context.Context, product kernel.ID, p Period, overrides []Override) (decimal.Decimal, error) {
	out := decimal.Zero
	for _, key := range []string{s.cfg.PayrollField, s.cfg.DirectCostField, s.cfg.MarketingField} {
		v, err := s.sumField(ctx, key, product, p, overrides)
		if err != nil {
			return decimal.Zero, err
		}
		out = out.Add(v)
	}
	return out, nil
}

// bundleRevenue — доля продукта в выручке бандлов по правилам атрибуции (EC-04).
func (s *Service) bundleRevenue(ctx context.Context, product kernel.ID, p Period, overrides []Override) (decimal.Decimal, error) {
	rules, err := s.store.BundleRules(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("bundle rules: %w", err)
	}
	effective := map[string]BundleRule{}
	for _, r := range rules {
		if r.EffectiveFrom.After(p.End()) {
			continue
		}
		if cur, ok := effective[r.BundleKey]; !ok || cur.Version < r.Version {
			effective[r.BundleKey] = r
		}
	}
	out := decimal.Zero
	nilProduct := kernel.NilID
	for key, rule := range effective {
		share, ok := rule.Shares[product]
		if !ok || share.IsZero() {
			continue
		}
		item := key
		rows, err := s.store.Facts(ctx, FactFilter{FieldKey: s.cfg.BundleRevenueField, Period: &p, Item: &item, Product: &nilProduct})
		if err != nil {
			return decimal.Zero, fmt.Errorf("facts: %w", err)
		}
		total := decimal.Zero
		for _, r := range rows {
			total = total.Add(r.Value)
		}
		for _, o := range overrides {
			if o.FieldKey == s.cfg.BundleRevenueField && (o.Period.IsZero() || o.Period == p) {
				total = o.Value
			}
		}
		out = out.Add(total.Mul(share))
	}
	return out, nil
}

// hubLoad — нагрузка хабов на продукт по правилам аллокации, действующим в периоде (EC-02).
func (s *Service) hubLoad(ctx context.Context, product kernel.ID, p Period, overrides []Override) (decimal.Decimal, error) {
	rules, err := s.store.AllocationRules(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("allocation rules: %w", err)
	}
	effective := map[kernel.ID]AllocationRule{}
	for _, r := range rules {
		if r.EffectiveFrom.After(p.End()) {
			continue
		}
		if cur, ok := effective[r.HubProductID]; !ok || cur.Version < r.Version {
			effective[r.HubProductID] = r
		}
	}
	out := decimal.Zero
	for hub, rule := range effective {
		if hub == product {
			continue
		}
		hubCosts, err := s.directCosts(ctx, hub, p, overrides)
		if err != nil {
			return decimal.Zero, err
		}
		if hubCosts.IsZero() {
			continue
		}
		share, err := s.allocationShare(ctx, rule, product, p, overrides)
		if err != nil {
			return decimal.Zero, err
		}
		out = out.Add(hubCosts.Mul(share))
	}
	return out, nil
}

// allocationShare — доля продукта в затратах хаба по базе распределения правила (EC-02).
func (s *Service) allocationShare(ctx context.Context, rule AllocationRule, product kernel.ID, p Period, overrides []Override) (decimal.Decimal, error) {
	switch rule.Basis {
	case BasisManual:
		return rule.Shares[product], nil
	case BasisRevenue, BasisWorklogs:
		consumers := rule.Consumers
		if len(consumers) == 0 {
			all, err := s.productsWithData(ctx, p)
			if err != nil {
				return decimal.Zero, err
			}
			for _, id := range all {
				if id != rule.HubProductID {
					consumers = append(consumers, id)
				}
			}
		}
		weight := func(id kernel.ID) (decimal.Decimal, error) {
			if rule.Basis == BasisRevenue {
				return s.sumField(ctx, s.cfg.RevenueField, id, p, overrides)
			}
			return s.worklogWeight(ctx, id, p)
		}
		total := decimal.Zero
		own := decimal.Zero
		found := false
		for _, id := range consumers {
			w, err := weight(id)
			if err != nil {
				return decimal.Zero, err
			}
			total = total.Add(w)
			if id == product {
				own, found = w, true
			}
		}
		if !found || total.IsZero() {
			return decimal.Zero, nil
		}
		return own.DivRound(total, 10), nil
	}
	return decimal.Zero, kernel.Invalid("basis", "неизвестная база распределения")
}

// worklogWeight — вес продукта по долям команд из worklogs (DL-05, EC-12).
func (s *Service) worklogWeight(ctx context.Context, product kernel.ID, p Period) (decimal.Decimal, error) {
	shares, err := s.store.TeamShares(ctx, p)
	if err != nil {
		return decimal.Zero, fmt.Errorf("team shares: %w", err)
	}
	out := decimal.Zero
	for _, sh := range shares {
		if sh.ProductID == product {
			out = out.Add(sh.Share)
		}
	}
	return out, nil
}

// pnl считает P&L продукта за период; overrides — сценарий (nil для фактических данных).
func (s *Service) pnl(ctx context.Context, product kernel.ID, p Period, overrides []Override) (PnL, error) {
	revenue, err := s.sumField(ctx, s.cfg.RevenueField, product, p, overrides)
	if err != nil {
		return PnL{}, err
	}
	bundle, err := s.bundleRevenue(ctx, product, p, overrides)
	if err != nil {
		return PnL{}, err
	}
	costs, err := s.directCosts(ctx, product, p, overrides)
	if err != nil {
		return PnL{}, err
	}
	load, err := s.hubLoad(ctx, product, p, overrides)
	if err != nil {
		return PnL{}, err
	}
	total := revenue.Add(bundle)
	return PnL{
		ProductID: product, Period: p,
		Revenue:       s.money(revenue),
		BundleRevenue: s.money(bundle),
		DirectCosts:   s.money(costs),
		HubLoad:       s.money(load),
		DirectProfit:  s.money(total.Sub(costs)),
		LoadedProfit:  s.money(total.Sub(costs).Sub(load)),
	}, nil
}

// ProductPnL — P&L продукта в двух видах: прямой и с нагрузкой хаба (EC-03).
func (s *Service) ProductPnL(ctx context.Context, sc authz.Scope, product kernel.ID, p Period) (PnL, error) {
	if err := s.requireRead(sc, product, authz.FinanceAggregates); err != nil {
		return PnL{}, err
	}
	if p.IsZero() {
		return PnL{}, kernel.Invalid("period", "период обязателен")
	}
	out, err := s.pnl(ctx, product, p, nil)
	if err != nil {
		return PnL{}, err
	}
	s.logAccess(ctx, sc, "read", "economics.pnl", product, map[string]any{"period": p.String()})
	return out, nil
}

// PortfolioPnL — свод P&L по портфелю (EC-03).
func (s *Service) PortfolioPnL(ctx context.Context, sc authz.Scope, p Period) (PortfolioPnL, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return PortfolioPnL{}, err
	}
	if p.IsZero() {
		return PortfolioPnL{}, kernel.Invalid("period", "период обязателен")
	}
	products, err := s.productsWithData(ctx, p)
	if err != nil {
		return PortfolioPnL{}, err
	}
	out := PortfolioPnL{Period: p, Products: make([]PnL, 0, len(products))}
	revenue, costs := decimal.Zero, decimal.Zero
	for _, id := range products {
		pnl, err := s.pnl(ctx, id, p, nil)
		if err != nil {
			return PortfolioPnL{}, err
		}
		out.Products = append(out.Products, pnl)
		revenue = revenue.Add(decimal.NewFromInt(pnl.Revenue.Amount + pnl.BundleRevenue.Amount))
		costs = costs.Add(decimal.NewFromInt(pnl.DirectCosts.Amount))
	}
	out.Revenue, out.Costs = s.money(revenue), s.money(costs)
	out.Profit = s.money(revenue.Sub(costs))
	s.logAccess(ctx, sc, "read", "economics.pnl.portfolio", kernel.NilID, map[string]any{"period": p.String()})
	return out, nil
}

// TeamCosts — затраты команды за период с разбивкой по продуктам (EC-12).
// Доли берутся из worklogs или заданы вручную.
func (s *Service) TeamCosts(ctx context.Context, sc authz.Scope, teamID kernel.ID, p Period) (TeamCost, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return TeamCost{}, err
	}
	rows, err := s.store.Facts(ctx, FactFilter{FieldKey: s.cfg.PayrollField, Period: &p, Team: &teamID})
	if err != nil {
		return TeamCost{}, fmt.Errorf("facts: %w", err)
	}
	total := decimal.Zero
	for _, r := range rows {
		total = total.Add(r.Value)
	}
	shares, err := s.store.TeamShares(ctx, p)
	if err != nil {
		return TeamCost{}, fmt.Errorf("team shares: %w", err)
	}
	out := TeamCost{TeamID: teamID, Period: p, Total: s.money(total), ByProduct: map[kernel.ID]kernel.Money{}}
	for _, sh := range shares {
		if sh.TeamID != teamID {
			continue
		}
		out.ByProduct[sh.ProductID] = s.money(total.Mul(sh.Share))
	}
	return out, nil
}

// Matrix — матрица «команда × продукт» за период (EC-12).
func (s *Service) Matrix(ctx context.Context, sc authz.Scope, p Period) (Matrix, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return Matrix{}, err
	}
	teams, err := s.store.Teams(ctx)
	if err != nil {
		return Matrix{}, fmt.Errorf("teams: %w", err)
	}
	out := Matrix{Period: p, Cells: map[kernel.ID]map[kernel.ID]kernel.Money{}}
	productSeen := map[kernel.ID]bool{}
	for _, t := range teams {
		cost, err := s.TeamCosts(ctx, sc, t.ID, p)
		if err != nil {
			return Matrix{}, err
		}
		out.Teams = append(out.Teams, t.ID)
		out.Cells[t.ID] = cost.ByProduct
		for pid := range cost.ByProduct {
			if !productSeen[pid] {
				productSeen[pid] = true
				out.Products = append(out.Products, pid)
			}
		}
	}
	sort.Slice(out.Products, func(i, j int) bool { return out.Products[i].String() < out.Products[j].String() })
	s.logAccess(ctx, sc, "read", "economics.matrix", kernel.NilID, map[string]any{"period": p.String()})
	return out, nil
}

// FeatureItem возвращает ключ измерения «статья» для фичи.
func FeatureItem(featureID kernel.ID) string { return "feature:" + featureID.String() }

// BranchItem возвращает ключ измерения «статья» для ветки версии.
func BranchItem(branch string) string { return "branch:" + branch }

// FeatureEconomics — инвестиции в фичу против привязанной выручки (EC-05).
func (s *Service) FeatureEconomics(ctx context.Context, sc authz.Scope, product, feature kernel.ID, p Period) (FeatureEconomics, error) {
	if err := s.requireRead(sc, product, authz.FinanceAggregates); err != nil {
		return FeatureEconomics{}, err
	}
	item := FeatureItem(feature)
	sum := func(key string) (decimal.Decimal, error) {
		filter := FactFilter{FieldKey: key, Product: &product, Item: &item}
		if !p.IsZero() {
			filter.Period = &p
		}
		rows, err := s.store.Facts(ctx, filter)
		if err != nil {
			return decimal.Zero, fmt.Errorf("facts: %w", err)
		}
		out := decimal.Zero
		for _, r := range rows {
			out = out.Add(r.Value)
		}
		return out, nil
	}
	investment, err := sum(s.cfg.FeatureCostField)
	if err != nil {
		return FeatureEconomics{}, err
	}
	revenue, err := sum(s.cfg.FeatureRevenueField)
	if err != nil {
		return FeatureEconomics{}, err
	}
	return FeatureEconomics{FeatureID: feature, Investment: s.money(investment),
		Revenue: s.money(revenue), Balance: s.money(revenue.Sub(investment))}, nil
}

// BranchCosts — стоимость поддержки веток версий продукта (EC-05).
func (s *Service) BranchCosts(ctx context.Context, sc authz.Scope, product kernel.ID, p Period) ([]BranchCost, error) {
	if err := s.requireRead(sc, product, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	filter := FactFilter{FieldKey: s.cfg.BranchCostField, Product: &product}
	if !p.IsZero() {
		filter.Period = &p
	}
	rows, err := s.store.Facts(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	totals := map[string]decimal.Decimal{}
	for _, r := range rows {
		branch := r.Item
		if len(branch) > len("branch:") && branch[:len("branch:")] == "branch:" {
			branch = branch[len("branch:"):]
		}
		totals[branch] = totals[branch].Add(r.Value)
	}
	out := make([]BranchCost, 0, len(totals))
	for branch, v := range totals {
		out = append(out, BranchCost{ProductID: product, Branch: branch, Cost: s.money(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Branch < out[j].Branch })
	return out, nil
}

// CertificationEconomics — затраты трека сертификации против выручки,
// доступной только с сертификатом (EC-06).
func (s *Service) CertificationEconomics(ctx context.Context, sc authz.Scope, product kernel.ID, p Period) (CertificationEconomics, error) {
	if err := s.requireRead(sc, product, authz.FinanceAggregates); err != nil {
		return CertificationEconomics{}, err
	}
	cost := decimal.Zero
	if s.tracks != nil {
		m, err := s.tracks.TrackCost(ctx, sc, product)
		if err != nil {
			return CertificationEconomics{}, fmt.Errorf("затраты трека: %w", err)
		}
		cost = decimal.NewFromInt(m.Amount)
	}
	fromFacts, err := s.sumField(ctx, s.cfg.TrackCostField, product, p, nil)
	if err != nil {
		return CertificationEconomics{}, err
	}
	cost = cost.Add(fromFacts)
	revenue, err := s.sumField(ctx, s.cfg.CertifiedRevenueField, product, p, nil)
	if err != nil {
		return CertificationEconomics{}, err
	}
	s.logAccess(ctx, sc, "read", "economics.certification", product, map[string]any{"period": p.String()})
	return CertificationEconomics{ProductID: product, Period: p, TrackCost: s.money(cost),
		CertifiedRevenue: s.money(revenue), Balance: s.money(revenue.Sub(cost))}, nil
}
