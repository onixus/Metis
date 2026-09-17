package portfoliograph_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

type memPub struct{ events []kernel.Event }

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

type fixture struct {
	t     *testing.T
	ctx   context.Context
	svc   *pg.Service
	store *pg.MemStore
	pub   *memPub
	cpo   authz.Scope
	// портфель ИБ
	deception, vm, edr, soar kernel.ID
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(subject string, private map[kernel.ID]authz.Access) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM}, Products: private, Audience: authz.AudienceInternal})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := pg.NewMemStore()
	pub := &memPub{}
	svc := pg.NewService(st, pub, kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)})
	f := &fixture{t: t, ctx: context.Background(), svc: svc, store: st, pub: pub, cpo: cpoScope()}
	f.deception = f.product("deception", "Deception")
	f.vm = f.product("vm", "VM")
	f.edr = f.product("edr", "EDR")
	f.soar = f.product("soar", "SOAR")
	return f
}

func (f *fixture) product(key, name string) kernel.ID {
	f.t.Helper()
	p, err := f.svc.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: key, Name: name, Type: pg.ProductTypeSecurity, Owner: "pm-" + key})
	if err != nil {
		f.t.Fatalf("create product %s: %v", key, err)
	}
	return p.ID
}

func (f *fixture) feature(product kernel.ID, name string, date kernel.Date) kernel.ID {
	f.t.Helper()
	ft, err := f.svc.CreateFeature(f.ctx, f.cpo, product, pg.FeatureInput{Name: name, Status: pg.FeaturePlanned, PlannedDate: date})
	if err != nil {
		f.t.Fatalf("create feature %s: %v", name, err)
	}
	return ft.ID
}

func (f *fixture) dep(from, to kernel.ID, crit pg.Criticality) (pg.Link, error) {
	return f.svc.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: pg.LinkIntegration, FromFeatureID: from, ToFeatureID: to, Criticality: crit})
}

func d(y int, m time.Month, day int) kernel.Date { return kernel.DateOf(y, m, day) }

func TestPG01_CreateProductWithTypeOwnerLifecycleAndSSDLCFlag(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: "mgmt", Name: "Платформа управления", Type: pg.ProductTypeInfrastructure, Owner: "pm-mgmt", Lifecycle: pg.LifecycleActive, SSDLCCertified: true})
	if err != nil {
		t.Fatal(err)
	}
	if !p.SSDLCCertified || p.Lifecycle != pg.LifecycleActive || p.Type != pg.ProductTypeInfrastructure {
		t.Fatalf("атрибуты: %+v", p)
	}
	if _, err := f.svc.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: "mgmt", Name: "Дубль", Type: pg.ProductTypeOther}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("дубль ключа должен давать ErrConflict, получено %v", err)
	}
	if _, err := f.svc.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: "x", Name: "X", Type: "weird"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный тип: %v", err)
	}
	pm := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if _, err := f.svc.CreateProduct(f.ctx, pm, pg.ProductInput{Key: "y", Name: "Y", Type: pg.ProductTypeOther}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM не создаёт продукты: %v", err)
	}
}

func TestPG02_CapabilityFeatureRequirementHierarchy(t *testing.T) {
	f := newFixture(t)
	cap, err := f.svc.CreateCapability(f.ctx, f.cpo, f.edr, "Реагирование")
	if err != nil {
		t.Fatal(err)
	}
	ft, err := f.svc.CreateFeature(f.ctx, f.cpo, f.edr, pg.FeatureInput{Name: "Response API v2", CapabilityID: cap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ft.ProductID != f.edr || ft.CapabilityID != cap.ID || ft.Status != pg.FeatureIdea {
		t.Fatalf("фича: %+v", ft)
	}
	req, err := f.svc.CreateRequirement(f.ctx, f.cpo, ft.ID, "Изоляция хоста за 5 с")
	if err != nil {
		t.Fatal(err)
	}
	if req.ProductID != f.edr || req.FeatureID != ft.ID {
		t.Fatalf("требование несёт product_id и feature_id: %+v", req)
	}
	// Возможность чужого продукта нельзя использовать.
	if _, err := f.svc.CreateFeature(f.ctx, f.cpo, f.vm, pg.FeatureInput{Name: "X", CapabilityID: cap.ID}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
}

func TestPG03_FourLinkTypesWithCriticality(t *testing.T) {
	f := newFixture(t)
	for _, lt := range []pg.LinkType{pg.LinkIntegration, pg.LinkSharedComponent, pg.LinkCommercial, pg.LinkBundled} {
		l, err := f.svc.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: lt, FromProductID: f.edr, ToProductID: f.soar, Criticality: pg.CritAccelerates})
		if err != nil {
			t.Fatalf("%s: %v", lt, err)
		}
		if l.Type != lt || l.Criticality != pg.CritAccelerates || l.IsFeatureLevel() {
			t.Fatalf("связь: %+v", l)
		}
	}
	if _, err := f.svc.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: "magic", FromProductID: f.edr, ToProductID: f.soar, Criticality: pg.CritBlocks}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный тип: %v", err)
	}
	links, err := f.svc.Links(f.ctx, f.cpo)
	if err != nil || len(links) != 4 {
		t.Fatalf("links: %d %v", len(links), err)
	}
}

func TestPG04_IntegrationContractWithBothSidesAndCompatibility(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2", d(2026, 11, 1))
	conn := f.feature(f.soar, "Коннектор EDR v2", d(2026, 12, 1))
	c, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{
		Name: "EDR ↔ SOAR", ProviderProductID: f.edr, ConsumerProductID: f.soar,
		ProviderFeatureIDs: []kernel.ID{api}, ConsumerFeatureIDs: []kernel.ID{conn},
		InterfaceVersion: "2.0", Owner: "pm-soar", Status: pg.ContractActive, Criticality: pg.CritBlocks,
		Compatibility: []pg.VersionPair{{ProviderVersion: "5.1", ConsumerVersion: "3.0", Compatible: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ready, err := f.svc.Contract(f.ctx, f.cpo, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InterfaceVersion != "2.0" || len(got.Compatibility) != 1 || got.Status != pg.ContractActive {
		t.Fatalf("контракт: %+v", got)
	}
	if ready != d(2026, 12, 1) {
		t.Fatalf("срок готовности = максимум сроков фич, получено %s", ready)
	}
	links, _ := f.svc.Links(f.ctx, f.cpo)
	var featureLinks, productLinks int
	for _, l := range links {
		if l.ContractID != c.ID {
			continue
		}
		if l.IsFeatureLevel() {
			featureLinks++
			if l.FromFeatureID != conn || l.ToFeatureID != api {
				t.Fatalf("направление: потребитель → поставщик, получено %+v", l)
			}
		} else {
			productLinks++
		}
	}
	if featureLinks != 1 || productLinks != 1 {
		t.Fatalf("рёбра контракта: фич %d, продуктов %d", featureLinks, productLinks)
	}
	// Фича чужого продукта в контракте — ошибка валидации.
	if _, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{Name: "bad", ProviderProductID: f.vm, ConsumerProductID: f.soar,
		ProviderFeatureIDs: []kernel.ID{api}, Criticality: pg.CritBlocks}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации: %v", err)
	}
}

func TestPG05_RejectsFeatureCycle(t *testing.T) {
	f := newFixture(t)
	a := f.feature(f.edr, "A", kernel.Date{})
	b := f.feature(f.soar, "B", kernel.Date{})
	c := f.feature(f.vm, "C", kernel.Date{})
	if _, err := f.dep(a, b, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dep(b, c, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	_, err := f.dep(c, a, pg.CritBlocks)
	var ce *pg.CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("ожидался CycleError, получено %v", err)
	}
	if !errors.Is(err, kernel.ErrConflict) {
		t.Fatal("CycleError должен быть ErrConflict")
	}
	want := []kernel.ID{c, a, b, c}
	if len(ce.Path) != len(want) {
		t.Fatalf("путь цикла: %v", ce.Path)
	}
	for i := range want {
		if ce.Path[i] != want[i] {
			t.Fatalf("путь цикла: %v, ожидался %v", ce.Path, want)
		}
	}
	// Самозависимость тоже цикл.
	if _, err := f.dep(a, a, pg.CritBlocks); !errors.As(err, &ce) {
		t.Fatalf("самозависимость: %v", err)
	}
	// Граф не изменился: связей ровно две.
	links, _ := f.svc.Links(f.ctx, f.cpo)
	if len(links) != 2 {
		t.Fatalf("после отказа связей должно быть 2, есть %d", len(links))
	}
}

func TestPG05_ContractCreatingCycleRejected(t *testing.T) {
	f := newFixture(t)
	a := f.feature(f.edr, "A", kernel.Date{})
	b := f.feature(f.soar, "B", kernel.Date{})
	if _, err := f.dep(a, b, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	// Контракт: SOAR-фича B зависит от EDR-фичи A → b→a, но уже есть a→b: цикл.
	_, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{Name: "c", ProviderProductID: f.edr, ConsumerProductID: f.soar,
		ProviderFeatureIDs: []kernel.ID{a}, ConsumerFeatureIDs: []kernel.ID{b}, Criticality: pg.CritBlocks})
	var ce *pg.CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("ожидался CycleError, получено %v", err)
	}
	contracts, _ := f.svc.Contracts(f.ctx, f.cpo)
	if len(contracts) != 0 {
		t.Fatal("контракт не должен сохраниться")
	}
}

func TestPG06_HubByIncomingConnectivityAndManual(t *testing.T) {
	f := newFixture(t)
	for _, p := range []kernel.ID{f.deception, f.vm, f.edr} {
		if _, err := f.svc.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: pg.LinkIntegration, FromProductID: p, ToProductID: f.soar, Criticality: pg.CritBlocks}); err != nil {
			t.Fatal(err)
		}
	}
	hubs, err := f.svc.Hubs(f.ctx, f.cpo)
	if err != nil {
		t.Fatal(err)
	}
	if hubs[0].ProductID != f.soar || hubs[0].InDegree != 3 || !hubs[0].Computed {
		t.Fatalf("хаб по связности: %+v", hubs[0])
	}
	for _, h := range hubs[1:] {
		if h.Computed {
			t.Fatalf("только максимум связности — вычисленный хаб: %+v", h)
		}
	}
	// Ручное назначение.
	p, _ := f.svc.Product(f.ctx, f.cpo, f.vm)
	if _, err := f.svc.UpdateProduct(f.ctx, f.cpo, f.vm, pg.ProductInput{Key: p.Key, Name: p.Name, Type: p.Type, Owner: p.Owner, HubManual: true}); err != nil {
		t.Fatal(err)
	}
	hubs, _ = f.svc.Hubs(f.ctx, f.cpo)
	var manual bool
	for _, h := range hubs {
		if h.ProductID == f.vm && h.Manual {
			manual = true
		}
	}
	if !manual {
		t.Fatal("ручное назначение хаба не отражено")
	}
	pm := pmScope("pm-edr", map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate})
	e, _ := f.svc.Product(f.ctx, f.cpo, f.edr)
	if _, err := f.svc.UpdateProduct(f.ctx, pm, f.edr, pg.ProductInput{Key: e.Key, Name: e.Name, Type: e.Type, Owner: e.Owner, HubManual: true}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM не назначает хаб: %v", err)
	}
}

// Сценарий приёмки 7.7 п.2–3: контракт EDR ↔ SOAR, сигнал со сделкой на 12 млн ₽,
// производная ценность обеих фич выросла на 12 млн × 1,0.
func TestPG07_RollupDerivedDemandFromContractSignal(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2", d(2026, 11, 1))
	conn := f.feature(f.soar, "Коннектор EDR v2", d(2026, 12, 1))
	c, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{Name: "EDR ↔ SOAR", ProviderProductID: f.edr, ConsumerProductID: f.soar,
		ProviderFeatureIDs: []kernel.ID{api}, ConsumerFeatureIDs: []kernel.ID{conn}, Criticality: pg.CritBlocks})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.svc.FeatureValue(f.ctx, f.cpo, api)
	if err := f.svc.SetContractSignalValue(f.ctx, f.cpo, c.ID, kernel.RUB(12_000_000_00)); err != nil {
		t.Fatal(err)
	}
	apiV, _ := f.svc.FeatureValue(f.ctx, f.cpo, api)
	connV, _ := f.svc.FeatureValue(f.ctx, f.cpo, conn)
	if connV.TotalValue.Amount-0 != 12_000_000_00 {
		t.Fatalf("фича потребителя: %v", connV)
	}
	// Поставщик: своя доля контракта 12 млн + производный спрос от потребителя 12 млн × 1,0.
	if apiV.TotalValue.Amount-before.TotalValue.Amount < 12_000_000_00 || apiV.DerivedValue.Amount != 12_000_000_00 {
		t.Fatalf("фича поставщика: %+v", apiV)
	}
}

func TestPG07_RollupFormulaWithCoefficients(t *testing.T) {
	f := newFixture(t)
	hub := f.feature(f.soar, "Hub", kernel.Date{})
	a := f.feature(f.edr, "A", kernel.Date{})
	b := f.feature(f.vm, "B", kernel.Date{})
	c := f.feature(f.deception, "C", kernel.Date{})
	for _, x := range []struct {
		id   kernel.ID
		crit pg.Criticality
		val  int64
	}{{a, pg.CritBlocks, 100_00}, {b, pg.CritAccelerates, 100_00}, {c, pg.CritDesirable, 100_00}} {
		if _, err := f.dep(x.id, hub, x.crit); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, x.id, kernel.RUB(x.val)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, hub, kernel.RUB(10_00)); err != nil {
		t.Fatal(err)
	}
	v, _ := f.svc.FeatureValue(f.ctx, f.cpo, hub)
	// 10 + 100×1,0 + 100×0,5 + 100×0,2 = 180
	if v.TotalValue.Amount != 180_00 {
		t.Fatalf("value(hub) = %v, ожидалось 180.00", v.TotalValue)
	}
	// Настраиваемые коэффициенты.
	adm := authz.New(authz.Params{Subject: "adm", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	st := pg.DefaultSettings()
	st.Coefficients[pg.CritDesirable] = decimal.RequireFromString("0.4")
	if err := f.svc.UpdateSettings(f.ctx, adm, st); err != nil {
		t.Fatal(err)
	}
	v, _ = f.svc.FeatureValue(f.ctx, f.cpo, hub)
	if v.TotalValue.Amount != 200_00 {
		t.Fatalf("после смены коэффициента: %v", v.TotalValue)
	}
	// Транзитивность: D → A → hub: ценность D доходит до hub через A.
	dd := f.feature(f.vm, "D", kernel.Date{})
	if _, err := f.dep(dd, a, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, dd, kernel.RUB(50_00)); err != nil {
		t.Fatal(err)
	}
	v, _ = f.svc.FeatureValue(f.ctx, f.cpo, hub)
	if v.TotalValue.Amount != 250_00 {
		t.Fatalf("транзитивный rollup: %v", v.TotalValue)
	}
}

// Сценарий приёмки 7.7 п.5: сдвиг фичи коннектора SOAR на 21 день: срок контракта сдвинут,
// зависимая фича EDR помечена как затронутая.
func TestPG08_ShiftPropagatesToDependentsAndContract(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2", d(2026, 11, 1))
	conn := f.feature(f.soar, "Коннектор EDR v2", d(2026, 12, 1))
	c, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{Name: "EDR ↔ SOAR", ProviderProductID: f.soar, ConsumerProductID: f.edr,
		ProviderFeatureIDs: []kernel.ID{conn}, ConsumerFeatureIDs: []kernel.ID{api}, Criticality: pg.CritBlocks})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.ShiftFeatureDate(f.ctx, f.cpo, conn, d(2026, 12, 22), "эпик коннектора сдвинут в Jira")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Affected) != 1 || res.Affected[0].FeatureID != api || res.Affected[0].ImpliedDate != d(2026, 12, 22) {
		t.Fatalf("затронутые: %+v", res.Affected)
	}
	if len(res.Contracts) != 1 || res.Contracts[0] != c.ID {
		t.Fatalf("контракты: %v", res.Contracts)
	}
	_, ready, _ := f.svc.Contract(f.ctx, f.cpo, c.ID)
	if ready != d(2026, 12, 22) {
		t.Fatalf("срок контракта: %s", ready)
	}
	apiF, _ := f.svc.Feature(f.ctx, f.cpo, api)
	if !apiF.Affected || apiF.AffectedBy != conn || apiF.ImpliedDate != d(2026, 12, 22) {
		t.Fatalf("фича EDR должна быть помечена: %+v", apiF)
	}
	// Причина ушла в событие.
	var found bool
	for _, ev := range f.pub.events {
		if ev.Type == pg.EventDateShifted && ev.AggregateID == conn {
			found = true
		}
	}
	if !found {
		t.Fatal("событие сдвига не опубликовано")
	}
	if _, err := f.svc.ShiftFeatureDate(f.ctx, f.cpo, conn, d(2027, 1, 1), ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("без причины — ошибка: %v", err)
	}
	// Сдвиг раньше не помечает зависимые.
	res, _ = f.svc.ShiftFeatureDate(f.ctx, f.cpo, conn, d(2026, 10, 1), "ускорили")
	if len(res.Affected) != 0 {
		t.Fatal("сдвиг раньше не затрагивает зависимые")
	}
}

func TestPG08_ShiftPropagatesTransitively(t *testing.T) {
	f := newFixture(t)
	a := f.feature(f.soar, "A", d(2026, 10, 1))
	b := f.feature(f.edr, "B", d(2026, 10, 15))
	c := f.feature(f.vm, "C", d(2027, 3, 1))
	if _, err := f.dep(b, a, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dep(c, b, pg.CritDesirable); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.ShiftFeatureDate(f.ctx, f.cpo, a, d(2026, 11, 1), "перенос")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Affected) != 2 || res.Affected[0].FeatureID != b || res.Affected[1].FeatureID != c {
		t.Fatalf("транзитивно: %+v", res.Affected)
	}
	if res.Affected[0].ImpliedDate != d(2026, 11, 1) || res.Affected[1].ImpliedDate != d(2027, 3, 1) {
		t.Fatalf("implied: %+v", res.Affected)
	}
}

// Сценарий приёмки 7.7 п.6: PM VM не видит приватный контур EDR; PM SOAR видит стратегический срез EDR.
func TestPG10_StrategicSliceVisibleByGraphEdgeOnly(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2", d(2026, 11, 1))
	if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, api, kernel.RUB(5_00)); err != nil {
		t.Fatal(err)
	}
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if _, err := f.svc.Features(f.ctx, pmVM, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM VM не видит приватный контур EDR: %v", err)
	}
	if _, err := f.svc.StrategicSlice(f.ctx, pmVM, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM VM не видит и стратегический срез EDR без ребра: %v", err)
	}
	pmSOAR := pmScope("pm-soar", map[kernel.ID]authz.Access{f.soar: authz.AccessPrivate, f.edr: authz.AccessStrategic})
	slice, err := f.svc.StrategicSlice(f.ctx, pmSOAR, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(slice.Features) != 1 || slice.Features[0].TotalValue.Amount != 5_00 || slice.Features[0].PlannedDate != d(2026, 11, 1) {
		t.Fatalf("срез: %+v", slice)
	}
	if _, err := f.svc.Features(f.ctx, pmSOAR, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM SOAR не видит сырой бэклог EDR: %v", err)
	}
	products, _ := f.svc.Products(f.ctx, pmVM)
	if len(products) != 1 || products[0].ID != f.vm {
		t.Fatalf("PM VM видит только VM: %+v", products)
	}
}

func TestPG10_LinkedProductsPort(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: pg.LinkIntegration, FromProductID: f.edr, ToProductID: f.soar, Criticality: pg.CritBlocks}); err != nil {
		t.Fatal(err)
	}
	n, _ := f.svc.LinkedProducts(f.ctx, f.soar)
	if len(n) != 1 || n[0] != f.edr {
		t.Fatalf("соседи: %v", n)
	}
	id, err := f.svc.ProductIDByKey(f.ctx, "edr")
	if err != nil || id != f.edr {
		t.Fatal("ProductIDByKey")
	}
}

func TestNFS01_ZeroScopeDeniedEverywhere(t *testing.T) {
	f := newFixture(t)
	var zero authz.Scope
	if _, err := f.svc.Products(f.ctx, zero); err != nil {
		t.Fatal(err)
	}
	if ps, _ := f.svc.Products(f.ctx, zero); len(ps) != 0 {
		t.Fatal("нулевой Scope не видит продукты")
	}
	if _, err := f.svc.CreateFeature(f.ctx, zero, f.edr, pg.FeatureInput{Name: "x"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal("нулевой Scope не пишет")
	}
	if err := f.svc.Recompute(f.ctx, zero); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal("нулевой Scope не пересчитывает")
	}
}

func TestService_LoadRestoresGraphFromStore(t *testing.T) {
	f := newFixture(t)
	a := f.feature(f.edr, "A", d(2026, 10, 1))
	b := f.feature(f.soar, "B", d(2026, 10, 1))
	if _, err := f.dep(a, b, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, a, kernel.RUB(7_00)); err != nil {
		t.Fatal(err)
	}
	svc2 := pg.NewService(f.store, nil, kernel.SystemClock{})
	if err := svc2.Load(f.ctx); err != nil {
		t.Fatal(err)
	}
	v, err := svc2.FeatureValue(f.ctx, f.cpo, b)
	if err != nil || v.TotalValue.Amount != 7_00 {
		t.Fatalf("после перезагрузки rollup: %+v %v", v, err)
	}
	if _, err := svc2.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: pg.LinkIntegration, FromFeatureID: b, ToFeatureID: a, Criticality: pg.CritBlocks}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("цикл после перезагрузки: %v", err)
	}
}
