package prioritization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pr "github.com/onixus/metis/internal/prioritization"
)

// fakeImpact — фейк порта ImpactCost (PR-05, реализует compliance).
type fakeImpact struct{ costs map[kernel.ID]kernel.Money }

func (f *fakeImpact) ConfirmationCost(_ context.Context, sc authz.Scope, id kernel.ID) (kernel.Money, error) {
	if !sc.Valid() {
		return kernel.Money{}, kernel.ErrForbidden
	}
	c, ok := f.costs[id]
	if !ok {
		return kernel.Money{}, kernel.NotFound("impact class", id)
	}
	return c, nil
}

func TestPR04_RegulatoryMandatoryExcludedFromRanking(t *testing.T) {
	svc, pub := newSvc(t)
	c := cpo()
	m, err := svc.CreateModel(ctx, c, pr.ModelInput{ProductID: edr, Name: "RICE", Type: pr.ModelRICE})
	if err != nil {
		t.Fatal(err)
	}
	top, mid, reg := kernel.NewID(), kernel.NewID(), kernel.NewID()
	for id, reach := range map[kernel.ID]string{top: "300", mid: "200", reg: "100"} {
		if _, err := svc.SetFeatureInputs(ctx, c, m.ID, edr, id, vals("reach", reach, "impact", "1", "confidence", "1", "effort", "1")); err != nil {
			t.Fatal(err)
		}
	}
	// флаг без причины отклоняется
	if _, err := svc.SetRegulatoryMandatory(ctx, c, edr, reg, true, " "); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("флаг без причины: %v", err)
	}
	fl, err := svc.SetRegulatoryMandatory(ctx, c, edr, reg, true, "требование сертификации ФСТЭК")
	if err != nil || !fl.RegulatoryMandatory || fl.SetBy != "cpo" || !fl.SetAt.Equal(clock.T) || fl.ProductID != edr {
		t.Fatalf("флаг: %v %+v", err, fl)
	}
	res, err := svc.Rank(ctx, c, m.ID, edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Ranked) != 2 || res.Ranked[0].FeatureID != top || res.Ranked[1].FeatureID != mid {
		t.Fatalf("общий список без обязательной фичи: %+v", res.Ranked)
	}
	if len(res.Mandatory) != 1 || res.Mandatory[0].FeatureID != reg || !res.Mandatory[0].Score.Equal(dec("100")) {
		t.Fatalf("обязательные отдельным списком со скором: %+v", res.Mandatory)
	}
	// Ranking совместим: только Ranked
	rank, err := svc.Ranking(ctx, c, m.ID, edr)
	if err != nil || len(rank) != 2 {
		t.Fatalf("Ranking: %v %d", err, len(rank))
	}
	got, err := svc.Flags(ctx, c, edr, reg)
	if err != nil || !got.RegulatoryMandatory || got.Reason != "требование сертификации ФСТЭК" {
		t.Fatalf("Flags: %v %+v", err, got)
	}
	if got, err := svc.Flags(ctx, c, edr, top); err != nil || got.RegulatoryMandatory {
		t.Fatalf("Flags незаданной фичи — нулевые: %v %+v", err, got)
	}
	// снятие флага возвращает фичу в общий список
	if _, err := svc.SetRegulatoryMandatory(ctx, c, edr, reg, false, ""); err != nil {
		t.Fatal(err)
	}
	if res, err = svc.Rank(ctx, c, m.ID, edr); err != nil || len(res.Ranked) != 3 || len(res.Mandatory) != 0 {
		t.Fatalf("после снятия флага: %v %+v", err, res)
	}
	flags := 0
	for _, ev := range pub.events {
		if ev.Type == pr.EventFlagsSet {
			flags++
		}
	}
	if flags != 2 {
		t.Fatalf("событий flags.set: %d", flags)
	}
	// ABAC: PM другого продукта не ставит флаг; фича EDR не переносится в VM
	if _, err := svc.SetRegulatoryMandatory(ctx, pm("pm-vm", vm), edr, reg, true, "x"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чужой PM поставил флаг: %v", err)
	}
	if _, err := svc.SetRegulatoryMandatory(ctx, c, vm, reg, true, "x"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("флаг фичи EDR от имени VM: %v", err)
	}
	var zero authz.Scope
	if _, err := svc.Rank(ctx, zero, m.ID, edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope Rank: %v", err)
	}
}

func TestPR05_CostCombinesDevAndConfirmationAndFeedsFormula(t *testing.T) {
	f := kernel.NewID()
	svc, pub := newSvc(t)
	c := cpo()
	// без порта — подтверждение 0
	fc, err := svc.SetDevCost(ctx, c, edr, f, kernel.RUB(1_000_000_00))
	if err != nil || fc.ConfirmationCost.Amount != 0 || fc.Total.Amount != 1_000_000_00 {
		t.Fatalf("без порта: %v %+v", err, fc)
	}
	svc.WithImpactCost(&fakeImpact{costs: map[kernel.ID]kernel.Money{f: kernel.RUB(250_000_00)}})
	fc, err = svc.Cost(ctx, c, edr, f)
	if err != nil || fc.DevCost.Amount != 1_000_000_00 || fc.ConfirmationCost.Amount != 250_000_00 || fc.Total != kernel.RUB(1_250_000_00) {
		t.Fatalf("стоимость: %v %+v", err, fc)
	}
	// фича без класса влияния (ErrNotFound порта) и без dev cost — нули
	if other, err := svc.Cost(ctx, c, edr, kernel.NewID()); err != nil || !other.Total.IsZero() {
		t.Fatalf("фича без данных: %v %+v", err, other)
	}
	// переменные cost, dev_cost, confirmation_cost в формуле (в основных единицах)
	m, err := svc.CreateModel(ctx, c, pr.ModelInput{ProductID: edr, Name: "value/cost", Type: pr.ModelCustom,
		Formula: "value / cost + dev_cost - confirmation_cost"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Inputs) != 1 || m.Inputs[0] != "value" {
		t.Fatalf("переменные стоимости системные, а не входы PM: %v", m.Inputs)
	}
	if _, err := svc.SetFeatureInputs(ctx, c, m.ID, edr, f, vals("value", "2500000")); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Score(ctx, c, m.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Score.Equal(dec("750002")) { // 2500000/1250000 + 1000000 - 250000
		t.Fatalf("скор с учётом стоимости: %s", r.Score)
	}
	var costVar decimal.Decimal
	for _, comp := range r.Components {
		if comp.Name == pr.VarCost && comp.System {
			costVar = comp.Value
		}
	}
	if !costVar.Equal(dec("1250000")) {
		t.Fatalf("компонент cost: %+v", r.Components)
	}
	// валидация и ABAC
	if _, err := svc.SetDevCost(ctx, c, edr, f, kernel.RUB(-1)); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("отрицательная стоимость: %v", err)
	}
	if _, err := svc.SetDevCost(ctx, c, edr, f, kernel.Money{Amount: 5}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("без валюты: %v", err)
	}
	if _, err := svc.SetDevCost(ctx, pm("pm-vm", vm), edr, f, kernel.RUB(1)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чужой PM задал стоимость: %v", err)
	}
	if _, err := svc.Cost(ctx, pm("pm-vm", vm), edr, f); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чужой PM видит стоимость: %v", err)
	}
	// системное имя во входах отклоняется
	if _, err := svc.CreateModel(ctx, c, pr.ModelInput{Name: "x", Type: pr.ModelCustom, Formula: "cost", Inputs: []string{"cost"}}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("cost как вход PM: %v", err)
	}
	costEvents := 0
	for _, ev := range pub.events {
		if ev.Type == pr.EventDevCostSet {
			costEvents++
		}
	}
	if costEvents != 1 {
		t.Fatalf("событий dev_cost.set: %d", costEvents)
	}
}
