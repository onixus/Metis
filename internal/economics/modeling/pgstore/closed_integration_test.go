//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func TestEC11_PostgresClosedDefinitionGuardSurvivesRestart(t *testing.T) {
	isolateDatabase(t)
	d := database(t)
	ctx, sc := context.Background(), identityaccess.FinanceServiceScope("synthetic-closed-model")
	svc := service(t, d, nil)
	date := kernel.DateOf(2026, 1, 1)
	p := economics.PeriodOf(2026, 1)
	product, hub := kernel.NewID(), kernel.NewID()
	if _, err := svc.SaveField(ctx, sc, economics.FieldInput{Key: "revenue", Name: "Synthetic revenue", Type: economics.FieldMoney, Source: economics.SourceManual, Currency: "RUB", EffectiveFrom: date}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMetric(ctx, sc, economics.MetricInput{Key: "closed_metric", Name: "Synthetic metric", Expression: `field("revenue")`, EffectiveFrom: date}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetManualValue(ctx, sc, "revenue", economics.Slice{ProductID: product, Period: p}, decimal.NewFromInt(100)); err != nil {
		t.Fatal(err)
	}
	team, err := svc.SaveTeam(ctx, sc, "team", "Synthetic team")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetTeamShares(ctx, sc, team.ID, p, map[kernel.ID]decimal.Decimal{product: decimal.NewFromInt(1)}); err != nil {
		t.Fatal(err)
	}
	baseline, err := svc.ProductPnL(ctx, sc, product, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ClosePeriod(ctx, sc, p); err != nil {
		t.Fatal(err)
	}
	restarted := service(t, database(t), nil)
	_, metricErr := restarted.SaveMetric(ctx, sc, economics.MetricInput{Key: "closed_metric", Name: "Changed", Expression: `field("revenue") * d("2")`, EffectiveFrom: p.End()})
	_, fieldErr := restarted.SaveField(ctx, sc, economics.FieldInput{Key: "revenue", Name: "Changed", Type: economics.FieldMoney, Source: economics.SourceManual, Currency: "USD", EffectiveFrom: p.End()})
	_, allocationErr := restarted.SaveAllocationRule(ctx, sc, economics.AllocationInput{HubProductID: hub, Basis: economics.BasisManual, Shares: map[kernel.ID]decimal.Decimal{product: decimal.NewFromInt(1)}})
	_, bundleErr := restarted.SaveBundleRule(ctx, sc, economics.BundleInput{BundleKey: "new_bundle", Shares: map[kernel.ID]decimal.Decimal{product: decimal.NewFromInt(1)}})
	shareErr := restarted.SetWorklogShares(ctx, sc, team.ID, p, map[kernel.ID]decimal.Decimal{hub: decimal.NewFromInt(1)})
	for name, err := range map[string]error{"field": fieldErr, "metric": metricErr, "allocation": allocationErr, "bundle": bundleErr, "shares": shareErr} {
		if !errors.Is(err, kernel.ErrConflict) {
			t.Fatalf("%s guard: %v", name, err)
		}
	}
	if _, err := restarted.SaveMetric(ctx, sc, economics.MetricInput{Key: "closed_metric", Name: "Future", Expression: `field("revenue") * d("2")`, EffectiveFrom: kernel.DateOf(2026, 2, 1)}); err != nil {
		t.Fatal(err)
	}
	value, err := restarted.Value(ctx, sc, "closed_metric", economics.Slice{ProductID: product, Period: p})
	if err != nil || !value.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("closed value drift: %s %v", value, err)
	}
	pnl, err := restarted.ProductPnL(ctx, sc, product, p)
	if err != nil || pnl != baseline {
		t.Fatalf("closed pnl drift: %+v %+v %v", baseline, pnl, err)
	}
}
