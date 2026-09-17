package prioritization_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	pr "github.com/onixus/metis/internal/prioritization"
)

type memPub struct{ events []kernel.Event }

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

// fakeMoney — фейк порта signals (PR-02).
type fakeMoney struct {
	arr, blocked map[kernel.ID]kernel.Money
}

func (f *fakeMoney) ARRByFeature(_ context.Context, sc authz.Scope, id kernel.ID) (kernel.Money, error) {
	if !sc.Valid() {
		return kernel.Money{}, kernel.ErrForbidden
	}
	return f.arr[id], nil
}

func (f *fakeMoney) BlockedDealsByFeature(_ context.Context, sc authz.Scope, id kernel.ID) (kernel.Money, error) {
	if !sc.Valid() {
		return kernel.Money{}, kernel.ErrForbidden
	}
	return f.blocked[id], nil
}

var (
	ctx   = context.Background()
	clock = kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	edr   = kernel.NewID()
	vm    = kernel.NewID()
)

func cpo() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pm(subject string, product kernel.ID) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM},
		Products: map[kernel.ID]authz.Access{product: authz.AccessPrivate}, Audience: authz.AudienceInternal})
}

func newSvc(t *testing.T) (*pr.Service, *memPub) {
	t.Helper()
	pub := &memPub{}
	return pr.NewService(pr.NewMemStore(), pub, clock), pub
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func vals(kv ...string) map[string]decimal.Decimal {
	m := map[string]decimal.Decimal{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = dec(kv[i+1])
	}
	return m
}

func TestPR01_RICEModelScoresFeature(t *testing.T) {
	svc, pub := newSvc(t)
	m, err := svc.CreateModel(ctx, cpo(), pr.ModelInput{ProductID: edr, Name: "RICE", Type: pr.ModelRICE})
	if err != nil {
		t.Fatal(err)
	}
	if m.Formula != pr.FormulaRICE || len(m.Inputs) != 4 {
		t.Fatalf("встроенная модель: %+v", m)
	}
	f := kernel.NewID()
	if _, err := svc.SetFeatureInputs(ctx, pm("pm-edr", edr), m.ID, edr, f, vals("reach", "1000", "impact", "2", "confidence", "0.8", "effort", "4")); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Score(ctx, pm("pm-edr", edr), m.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Score.Equal(dec("400")) {
		t.Fatalf("RICE: ожидалось 400, получено %s", r.Score)
	}
	if len(r.Components) != 4 || !strings.Contains(r.Explanation, "reach=1000") {
		t.Fatalf("пояснение: %+v", r)
	}
	var types []string
	for _, e := range pub.events {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != pr.EventModelSaved+","+pr.EventInputsSet {
		t.Fatalf("события: %v", types)
	}
}

func TestPR01_WSJFModelRanksFeatures(t *testing.T) {
	svc, _ := newSvc(t)
	m, err := svc.CreateModel(ctx, cpo(), pr.ModelInput{Name: "WSJF", Type: pr.ModelWSJF}) // портфельная
	if err != nil {
		t.Fatal(err)
	}
	small, big := kernel.NewID(), kernel.NewID()
	scope := pm("pm-vm", vm)
	if _, err := svc.SetFeatureInputs(ctx, scope, m.ID, vm, big, vals("user_business_value", "8", "time_criticality", "8", "risk_reduction", "4", "job_size", "10")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetFeatureInputs(ctx, scope, m.ID, vm, small, vals("user_business_value", "5", "time_criticality", "3", "risk_reduction", "2", "job_size", "2")); err != nil {
		t.Fatal(err)
	}
	rank, err := svc.Ranking(ctx, scope, m.ID, vm)
	if err != nil {
		t.Fatal(err)
	}
	if len(rank) != 2 || rank[0].FeatureID != small || !rank[0].Score.Equal(dec("5")) || !rank[1].Score.Equal(dec("2")) {
		t.Fatalf("ранжирование WSJF: %+v", rank)
	}
}

func TestPR01_CustomFormulaWithMinMaxAndUnaryMinus(t *testing.T) {
	svc, _ := newSvc(t)
	m, err := svc.CreateModel(ctx, cpo(), pr.ModelInput{ProductID: edr, Name: "own", Type: pr.ModelCustom,
		Formula: "max(value, 1) * (1 - -risk) / min(size, 10)"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Inputs, ",") != "value,risk,size" {
		t.Fatalf("входы выведены из формулы: %v", m.Inputs)
	}
	f := kernel.NewID()
	if _, err := svc.SetFeatureInputs(ctx, cpo(), m.ID, edr, f, vals("value", "0.5", "risk", "0.5", "size", "20")); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Score(ctx, cpo(), m.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Score.Equal(dec("0.15")) { // 1 * 1.5 / 10
		t.Fatalf("ожидалось 0.15, получено %s", r.Score)
	}
	// UpdateModel: переменная, не объявленная во входах, отклоняется.
	_, err = svc.UpdateModel(ctx, cpo(), m.ID, pr.ModelInput{Name: "own", Type: pr.ModelCustom, Formula: "a + b", Inputs: []string{"a"}})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидался ErrValidation, получено %v", err)
	}
	// Задание переменной вне модели — ошибка.
	_, err = svc.SetFeatureInputs(ctx, cpo(), m.ID, edr, f, vals("effort", "1"))
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидался ErrValidation, получено %v", err)
	}
}

func TestPR01_FormulaRejectsUnsafeExpressions(t *testing.T) {
	bad := map[string]string{
		"unknown function": "sqrt(x)",
		"call syntax":      "x(1)",
		"assignment":       "x = 1",
		"string":           `"a"`,
		"empty":            "   ",
		"unbalanced":       "(1 + 2",
		"trailing":         "1 2",
		"double dot":       "1.2.3",
		"too long":         strings.Repeat("1+", pr.MaxFormulaLength/2) + "1",
		"too deep":         strings.Repeat("(", pr.MaxFormulaDepth+1) + "1" + strings.Repeat(")", pr.MaxFormulaDepth+1),
		"deep unary":       strings.Repeat("-", pr.MaxFormulaDepth+2) + "1",
	}
	for name, src := range bad {
		if _, err := pr.ParseFormula(src); !errors.Is(err, kernel.ErrValidation) {
			t.Errorf("%s: ожидался ErrValidation, получено %v", name, err)
		}
	}
	f, err := pr.ParseFormula("a / b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Eval(vals("a", "1", "b", "0")); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("деление на ноль: ожидался ErrValidation, получено %v", err)
	}
	if _, err := f.Eval(vals("a", "1")); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестная переменная: ожидался ErrValidation, получено %v", err)
	}
	// Модель с системным именем во входах отклоняется.
	svc, _ := newSvc(t)
	_, err = svc.CreateModel(ctx, cpo(), pr.ModelInput{Name: "x", Type: pr.ModelCustom, Formula: "arr", Inputs: []string{"arr"}})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("системная переменная во входах: %v", err)
	}
}

func TestPR02_MoneyMetricsAvailableInFormula(t *testing.T) {
	f := kernel.NewID()
	money := &fakeMoney{arr: map[kernel.ID]kernel.Money{f: kernel.RUB(1_500_000_00)},
		blocked: map[kernel.ID]kernel.Money{f: kernel.RUB(250_000_50)}}
	svc, _ := newSvc(t)
	svc.WithMoneyMetrics(money)
	m, err := svc.CreateModel(ctx, cpo(), pr.ModelInput{ProductID: edr, Name: "money", Type: pr.ModelCustom, Formula: "(arr + blocked_deals) / effort"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Inputs, ",") != "effort" {
		t.Fatalf("денежные переменные не являются входами PM: %v", m.Inputs)
	}
	if _, err := svc.SetFeatureInputs(ctx, cpo(), m.ID, edr, f, vals("effort", "2")); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Score(ctx, cpo(), m.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Score.Equal(dec("875000.25")) {
		t.Fatalf("ожидалось 875000.25 руб, получено %s", r.Score)
	}
	sys := 0
	for _, c := range r.Components {
		if c.System {
			sys++
		}
	}
	if sys != 2 {
		t.Fatalf("в пояснении должны быть arr и blocked_deals: %+v", r.Components)
	}
}

func TestPR03_DerivedDemandRaisesHubFeatureScore(t *testing.T) {
	graph := pg.NewService(pg.NewMemStore(), nil, clock)
	c := cpo()
	mk := func(key string) kernel.ID {
		p, err := graph.CreateProduct(ctx, c, pg.ProductInput{Key: key, Name: key, Type: pg.ProductTypeSecurity, Owner: "pm-" + key})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	soar, edrP := mk("soar"), mk("edr")
	feat := func(p kernel.ID, name string) kernel.ID {
		f, err := graph.CreateFeature(ctx, c, p, pg.FeatureInput{Name: name, Status: pg.FeaturePlanned})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	hub, plain, consumer := feat(soar, "Hub API"), feat(soar, "Plain"), feat(edrP, "Consumer")
	for _, id := range []kernel.ID{hub, plain} {
		if err := graph.SetFeatureOwnValue(ctx, c, id, kernel.RUB(100_00)); err != nil {
			t.Fatal(err)
		}
	}
	if err := graph.SetFeatureOwnValue(ctx, c, consumer, kernel.RUB(1_000_00)); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.CreateLink(ctx, c, pg.LinkInput{Type: pg.LinkIntegration, FromFeatureID: consumer, ToFeatureID: hub, Criticality: pg.CritBlocks}); err != nil {
		t.Fatal(err)
	}

	svc, _ := newSvc(t)
	svc.WithDerivedDemand(graph) // *portfoliograph.Service реализует порт напрямую
	m, err := svc.CreateModel(ctx, c, pr.ModelInput{ProductID: soar, Name: "value", Type: pr.ModelCustom, Formula: "total_value / effort"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []kernel.ID{hub, plain} {
		if _, err := svc.SetFeatureInputs(ctx, c, m.ID, soar, id, vals("effort", "1")); err != nil {
			t.Fatal(err)
		}
	}
	rank, err := svc.Ranking(ctx, c, m.ID, soar)
	if err != nil {
		t.Fatal(err)
	}
	if len(rank) != 2 || rank[0].FeatureID != hub {
		t.Fatalf("хаб должен быть выше: %+v", rank)
	}
	if !rank[0].Score.Equal(dec("1100")) || !rank[1].Score.Equal(dec("100")) {
		t.Fatalf("own 100 + derived 1000×1,0: %s, %s", rank[0].Score, rank[1].Score)
	}
	var derived decimal.Decimal
	for _, comp := range rank[0].Components {
		if comp.Name == pr.VarTotalValue {
			derived = comp.Value
		}
	}
	if !derived.Equal(dec("1100")) {
		t.Fatalf("компонент total_value: %+v", rank[0].Components)
	}
}

func TestPR01_ABACForeignPMCannotSetInputsOrCreatePortfolioModel(t *testing.T) {
	svc, _ := newSvc(t)
	m, err := svc.CreateModel(ctx, cpo(), pr.ModelInput{ProductID: edr, Name: "RICE", Type: pr.ModelRICE})
	if err != nil {
		t.Fatal(err)
	}
	f := kernel.NewID()
	stranger := pm("pm-vm", vm)
	if _, err := svc.SetFeatureInputs(ctx, stranger, m.ID, edr, f, vals("reach", "1")); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM чужого продукта задал входы: %v", err)
	}
	if _, err := svc.CreateModel(ctx, stranger, pr.ModelInput{Name: "portfolio", Type: pr.ModelRICE}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM создал портфельную модель: %v", err)
	}
	if _, err := svc.UpdateModel(ctx, stranger, m.ID, pr.ModelInput{Name: "x", Type: pr.ModelRICE}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM изменил чужую модель: %v", err)
	}
	if _, err := svc.SetFeatureInputs(ctx, pm("pm-edr", edr), m.ID, edr, f, vals("reach", "1", "impact", "1", "confidence", "1", "effort", "1")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Ranking(ctx, stranger, m.ID, edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM чужого продукта видит ранжирование: %v", err)
	}
	if _, err := svc.Score(ctx, stranger, m.ID, f); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM чужого продукта видит скор: %v", err)
	}
	models, err := svc.Models(ctx, stranger)
	if err != nil || len(models) != 0 {
		t.Fatalf("PM чужого продукта видит модель EDR: %v %v", models, err)
	}
	// Модель EDR нельзя применить к VM.
	if _, err := svc.SetFeatureInputs(ctx, cpo(), m.ID, vm, f, vals("reach", "1")); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("модель другого продукта: %v", err)
	}
}

func TestNFS01_ZeroScopeDeniesAllPrioritization(t *testing.T) {
	svc, _ := newSvc(t)
	var zero authz.Scope
	m, _ := svc.CreateModel(ctx, cpo(), pr.ModelInput{Name: "p", Type: pr.ModelRICE})
	checks := map[string]error{}
	_, checks["create"] = svc.CreateModel(ctx, zero, pr.ModelInput{Name: "p", Type: pr.ModelRICE})
	_, checks["update"] = svc.UpdateModel(ctx, zero, m.ID, pr.ModelInput{Name: "p", Type: pr.ModelRICE})
	_, checks["models"] = svc.Models(ctx, zero)
	_, checks["inputs"] = svc.SetFeatureInputs(ctx, zero, m.ID, edr, kernel.NewID(), nil)
	_, checks["score"] = svc.Score(ctx, zero, m.ID, kernel.NewID())
	_, checks["ranking"] = svc.Ranking(ctx, zero, m.ID, edr)
	for name, err := range checks {
		if !errors.Is(err, kernel.ErrForbidden) {
			t.Errorf("%s: нулевой Scope должен давать ErrForbidden, получено %v", name, err)
		}
	}
}
