package signals_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/adapters/crmfile"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/signals"
)

type memPub struct{ events []kernel.Event }

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

func (m *memPub) count(typ string) int {
	n := 0
	for _, e := range m.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

type fixture struct {
	t     *testing.T
	ctx   context.Context
	graph *pg.Service
	svc   *signals.Service
	pub   *memPub
	cpo   authz.Scope
	// портфель ИБ
	vm, edr, soar kernel.ID
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(subject string, private map[kernel.ID]authz.Access) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM}, Products: private, Audience: authz.AudienceInternal})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub := &memPub{}
	clock := kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	graph := pg.NewService(pg.NewMemStore(), pub, clock)
	f := &fixture{t: t, ctx: context.Background(), graph: graph, pub: pub, cpo: cpoScope()}
	f.svc = signals.NewService(signals.NewMemStore(), graph, pub, clock)
	f.vm = f.product("vm", "VM")
	f.edr = f.product("edr", "EDR")
	f.soar = f.product("soar", "SOAR")
	return f
}

func (f *fixture) product(key, name string) kernel.ID {
	f.t.Helper()
	p, err := f.graph.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: key, Name: name, Type: pg.ProductTypeSecurity, Owner: "pm-" + key})
	if err != nil {
		f.t.Fatalf("create product %s: %v", key, err)
	}
	return p.ID
}

func (f *fixture) feature(product kernel.ID, name string) kernel.ID {
	f.t.Helper()
	ft, err := f.graph.CreateFeature(f.ctx, f.cpo, product, pg.FeatureInput{Name: name, Status: pg.FeaturePlanned, PlannedDate: kernel.DateOf(2026, 12, 1)})
	if err != nil {
		f.t.Fatalf("create feature %s: %v", name, err)
	}
	return ft.ID
}

func (f *fixture) ingest(sc authz.Scope, in signals.IngestInput) signals.Signal {
	f.t.Helper()
	if in.Source == "" {
		in.Source = signals.SourceManual
	}
	sig, err := f.svc.Ingest(f.ctx, sc, in)
	if err != nil {
		f.t.Fatalf("ingest %q: %v", in.Text, err)
	}
	return sig
}

func TestSG01_IngestFromManualServiceDeskAndImport(t *testing.T) {
	f := newFixture(t)
	for _, src := range []signals.Source{signals.SourceManual, signals.SourceServiceDesk, signals.SourceImport} {
		sig := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Source: src, Text: "Нужен экспорт в SIEM"})
		if sig.Source != src || sig.Status != signals.StatusNew || sig.ProductID != f.edr || sig.CreatedBy != "cpo" {
			t.Fatalf("сигнал %s: %+v", src, sig)
		}
	}
	if _, err := f.svc.Ingest(f.ctx, f.cpo, signals.IngestInput{ProductID: f.edr, Source: "telegram", Text: "x"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный источник: %v", err)
	}
	if _, err := f.svc.Ingest(f.ctx, f.cpo, signals.IngestInput{ProductID: f.edr, Source: signals.SourceManual, Text: "  "}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустой текст: %v", err)
	}
	if got := f.pub.count(signals.EventSignalIngested); got != 3 {
		t.Fatalf("событий ingested: %d", got)
	}
}

func TestSG01_ImportFromCRMIsIdempotentPerRequestedFeature(t *testing.T) {
	f := newFixture(t)
	crm := crmfile.New("../../fixtures/crm")
	res, err := f.svc.ImportFromCRM(f.ctx, f.cpo, crm)
	if err != nil {
		t.Fatal(err)
	}
	// deal-001: 1 фича; deal-002: 2 фичи; deal-003: без фич, есть notes; deal-004: 1 фича.
	if len(res.Imported) != 5 || len(res.Skipped) != 0 {
		t.Fatalf("импорт: %d сигналов, пропущено %v", len(res.Imported), res.Skipped)
	}
	first := res.Imported[0]
	if first.Source != signals.SourceCRM || first.ProductID != f.edr || first.DealID != "deal-001" || first.AccountID != "acc-001" ||
		first.Weight != kernel.RUB(12_000_000_00) || first.AccountARR != kernel.RUB(24_000_000_00) || !first.BlocksDeal ||
		first.Version != "3.0" || first.Segment != "enterprise" || first.Text != "Response API v2" {
		t.Fatalf("сигнал сделки: %+v", first)
	}
	// Сделка без суммы: вес равен ARR аккаунта (нулевому у acc-004).
	if last := res.Imported[4]; last.ProductID != f.soar || !last.Weight.IsZero() {
		t.Fatalf("сделка без суммы: %+v", last)
	}
	// Повторный импорт не создаёт дублей (ТЗ 4.2).
	if _, err := f.svc.ImportFromCRM(f.ctx, f.cpo, crm); err != nil {
		t.Fatal(err)
	}
	all, err := f.svc.Signals(f.ctx, f.cpo, f.vm, signals.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("после повторного импорта у VM %d сигналов, ожидалось 2", len(all))
	}
	// PM VM импортирует только сделки своего продукта; остальные пропускаются с ErrForbidden.
	pm := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	res, err = f.svc.ImportFromCRM(f.ctx, pm, crm)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 2 || !errors.Is(res.Skipped["deal-001"], kernel.ErrForbidden) {
		t.Fatalf("импорт PM: %d, пропущено %v", len(res.Imported), res.Skipped)
	}
	if _, err := f.svc.ImportFromCRM(f.ctx, authz.Scope{}, crm); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: %v", err)
	}
}

func TestSG02_ProductRequiredAndOptionalBindings(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Ingest(f.ctx, f.cpo, signals.IngestInput{Source: signals.SourceManual, Text: "без продукта"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("product_id обязателен: %v", err)
	}
	if _, err := f.svc.Ingest(f.ctx, f.cpo, signals.IngestInput{ProductID: f.edr, Source: signals.SourceManual, Text: "x", DealID: "d"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("сделка без аккаунта: %v", err)
	}
	bare := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "только продукт"})
	if bare.AccountID != "" || bare.DealID != "" || bare.Version != "" || bare.Segment != "" || !bare.Weight.IsZero() {
		t.Fatalf("привязки опциональны: %+v", bare)
	}
	full := f.ingest(f.cpo, signals.IngestInput{
		ProductID: f.edr, Text: "полный", AccountID: "acc-1", DealID: "deal-1", Version: "3.1", Segment: "enterprise",
		DealAmount: kernel.RUB(5_000_00), AccountARR: kernel.RUB(1_000_00),
	})
	if full.AccountID != "acc-1" || full.DealID != "deal-1" || full.Version != "3.1" || full.Segment != "enterprise" {
		t.Fatalf("привязки: %+v", full)
	}
	if full.Weight != kernel.RUB(5_000_00) {
		t.Fatalf("вес = сумма сделки: %v", full.Weight)
	}
	arrOnly := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "аккаунт", AccountID: "acc-2", AccountARR: kernel.RUB(700_00)})
	if arrOnly.Weight != kernel.RUB(700_00) {
		t.Fatalf("вес без сделки = ARR: %v", arrOnly.Weight)
	}
}

func TestSG03_TriageQueueStatusesAndDueDate(t *testing.T) {
	f := newFixture(t)
	late := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "поздний", DueDate: kernel.DateOf(2026, 10, 20)})
	early := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "ранний", DueDate: kernel.DateOf(2026, 9, 20)})
	def := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "по умолчанию"})
	other := f.ingest(f.cpo, signals.IngestInput{ProductID: f.vm, Text: "другой продукт"})
	if def.DueDate.String() != "2026-10-01" {
		t.Fatalf("срок по умолчанию 14 дней: %s", def.DueDate)
	}
	rej := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "отклонён"})
	if _, err := f.svc.Triage(f.ctx, f.cpo, rej.ID, signals.TriageInput{Status: signals.StatusRejected}); err != nil {
		t.Fatal(err)
	}
	rev, err := f.svc.Triage(f.ctx, f.cpo, late.ID, signals.TriageInput{Status: signals.StatusInReview, DueDate: kernel.DateOf(2026, 9, 18)})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Status != signals.StatusInReview || rev.DueDate.String() != "2026-09-18" {
		t.Fatalf("triage: %+v", rev)
	}
	q, err := f.svc.TriageQueue(f.ctx, f.cpo, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 3 || q[0].ID != late.ID || q[1].ID != early.ID || q[2].ID != def.ID {
		t.Fatalf("очередь не по сроку: %+v", q)
	}
	for _, s := range q {
		if s.ID == other.ID || s.ID == rej.ID {
			t.Fatalf("в очереди чужой продукт или отклонённый: %+v", s)
		}
	}
	for _, bad := range []signals.Status{signals.StatusLinked, signals.StatusMerged, "weird"} {
		if _, err := f.svc.Triage(f.ctx, f.cpo, early.ID, signals.TriageInput{Status: bad}); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("статус %s через Triage: %v", bad, err)
		}
	}
	if _, err := f.svc.Triage(f.ctx, f.cpo, kernel.NewID(), signals.TriageInput{Status: signals.StatusNew}); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("несуществующий: %v", err)
	}
	if got := f.pub.count(signals.EventSignalTriaged); got != 2 {
		t.Fatalf("событий triaged: %d", got)
	}
}

func TestSG05_LinkToFeatureKeepsMoneyWeight(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2")
	otherFeature := f.feature(f.edr, "Изоляция хоста")
	a := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "A", AccountID: "acc-1", DealID: "deal-1", DealAmount: kernel.RUB(3_000_000_00), AccountARR: kernel.RUB(10_000_000_00), BlocksDeal: true})
	b := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "B", AccountID: "acc-2", AccountARR: kernel.RUB(2_000_000_00)})

	linked, err := f.svc.LinkToFeature(f.ctx, f.cpo, a.ID, api)
	if err != nil {
		t.Fatal(err)
	}
	if linked.Status != signals.StatusLinked || linked.FeatureID != api {
		t.Fatalf("привязка: %+v", linked)
	}
	if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, b.ID, api); err != nil {
		t.Fatal(err)
	}
	ft, err := f.graph.Feature(f.ctx, f.cpo, api)
	if err != nil {
		t.Fatal(err)
	}
	if ft.OwnValue != kernel.RUB(5_000_000_00) {
		t.Fatalf("own value = сумма весов: %v", ft.OwnValue)
	}
	// Перепривязка: старая фича теряет вес, новая получает.
	if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, b.ID, otherFeature); err != nil {
		t.Fatal(err)
	}
	ft, _ = f.graph.Feature(f.ctx, f.cpo, api)
	of, _ := f.graph.Feature(f.ctx, f.cpo, otherFeature)
	if ft.OwnValue != kernel.RUB(3_000_000_00) || of.OwnValue != kernel.RUB(2_000_000_00) {
		t.Fatalf("перепривязка: %v / %v", ft.OwnValue, of.OwnValue)
	}
	// Отклонение привязанного сигнала снимает его вес.
	if _, err := f.svc.Triage(f.ctx, f.cpo, a.ID, signals.TriageInput{Status: signals.StatusRejected}); err != nil {
		t.Fatal(err)
	}
	ft, _ = f.graph.Feature(f.ctx, f.cpo, api)
	if !ft.OwnValue.IsZero() {
		t.Fatalf("после отклонения: %v", ft.OwnValue)
	}
	// Фича чужого продукта.
	vmFeature := f.feature(f.vm, "Отчёт для ЦБ")
	if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, b.ID, vmFeature); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("фича другого продукта: %v", err)
	}
	if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, b.ID, kernel.NewID()); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("несуществующая фича: %v", err)
	}
	if got := f.pub.count(signals.EventSignalLinked); got != 3 {
		t.Fatalf("событий linked: %d", got)
	}
}

// Сценарий приёмки 7.7 п.3: к контракту привязан сигнал со сделкой на 12 млн ₽.
func TestSG05_AcceptanceContractSignal12M(t *testing.T) {
	f := newFixture(t)
	edrAPI := f.feature(f.edr, "Response API v2")
	soarConn := f.feature(f.soar, "Коннектор EDR v2")
	c, err := f.graph.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{
		Name: "EDR ↔ SOAR", ProviderProductID: f.edr, ConsumerProductID: f.soar,
		ProviderFeatureIDs: []kernel.ID{edrAPI}, ConsumerFeatureIDs: []kernel.ID{soarConn},
		Status: pg.ContractActive, Criticality: pg.CritBlocks, Owner: "pm-soar",
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := f.ingest(f.cpo, signals.IngestInput{
		ProductID: f.edr, Source: signals.SourceCRM, Text: "Поставка EDR+SOAR", AccountID: "acc-001", DealID: "deal-001",
		DealAmount: kernel.RUB(12_000_000_00), AccountARR: kernel.RUB(24_000_000_00),
	})
	if _, err := f.svc.LinkToContract(f.ctx, f.cpo, sig.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	got, _, err := f.graph.Contract(f.ctx, f.cpo, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SignalValue != kernel.RUB(12_000_000_00) {
		t.Fatalf("ценность контракта: %v", got.SignalValue)
	}
	// Обе фичи контракта унаследовали 12 млн × 1,0 (ТЗ 2.4).
	for _, id := range []kernel.ID{edrAPI, soarConn} {
		v, err := f.graph.FeatureValue(f.ctx, f.cpo, id)
		if err != nil {
			t.Fatal(err)
		}
		// Контрактный бонус входит в own; поставщик дополнительно получает производную ценность от потребителя.
		if v.OwnValue != kernel.RUB(12_000_000_00) || v.TotalValue.Amount < 12_000_000_00 {
			t.Fatalf("ценность фичи %s не выросла на 12 млн: %+v", id, v)
		}
	}
	// Сигнал продукта вне контракта не привязывается.
	vmSig := f.ingest(f.cpo, signals.IngestInput{ProductID: f.vm, Text: "чужой"})
	if _, err := f.svc.LinkToContract(f.ctx, f.cpo, vmSig.ID, c.ID); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("продукт вне контракта: %v", err)
	}
}

func TestSG05_ARRAndBlockedDealsByFeature(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2")
	inputs := []signals.IngestInput{
		{ProductID: f.edr, Text: "A1", AccountID: "acc-1", DealID: "deal-1", DealAmount: kernel.RUB(12_000_000_00), AccountARR: kernel.RUB(24_000_000_00), BlocksDeal: true},
		{ProductID: f.edr, Text: "A2", AccountID: "acc-1", DealID: "deal-1", DealAmount: kernel.RUB(12_000_000_00), AccountARR: kernel.RUB(24_000_000_00), BlocksDeal: true},
		{ProductID: f.edr, Text: "B", AccountID: "acc-2", DealID: "deal-2", DealAmount: kernel.RUB(3_000_000_00), AccountARR: kernel.RUB(8_000_000_00)},
		{ProductID: f.edr, Text: "C", AccountID: "acc-3", AccountARR: kernel.RUB(1_000_000_00)},
	}
	for _, in := range inputs {
		sig := f.ingest(f.cpo, in)
		if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, sig.ID, api); err != nil {
			t.Fatal(err)
		}
	}
	unlinked := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "не привязан", AccountID: "acc-9", AccountARR: kernel.RUB(99_00)})
	_ = unlinked
	arr, err := f.svc.ARRByFeature(f.ctx, f.cpo, api)
	if err != nil {
		t.Fatal(err)
	}
	if arr != kernel.RUB(33_000_000_00) {
		t.Fatalf("ARR по фиче (аккаунты без дублей): %v", arr)
	}
	blocked, err := f.svc.BlockedDealsByFeature(f.ctx, f.cpo, api)
	if err != nil {
		t.Fatal(err)
	}
	if blocked != kernel.RUB(12_000_000_00) {
		t.Fatalf("блокируемые сделки (без дублей): %v", blocked)
	}
	ft, _ := f.graph.Feature(f.ctx, f.cpo, api)
	if ft.OwnValue != kernel.RUB(28_000_000_00) {
		t.Fatalf("own value = сумма весов всех сигналов: %v", ft.OwnValue)
	}
}

func TestABAC_PMOfVMDoesNotSeeEDRSignals(t *testing.T) {
	f := newFixture(t)
	sig := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "сырой сигнал EDR"})
	edrFeature := f.feature(f.edr, "Response API v2")
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate, f.edr: authz.AccessStrategic})

	if _, err := f.svc.Signal(f.ctx, pmVM, sig.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Signal: %v", err)
	}
	if _, err := f.svc.TriageQueue(f.ctx, pmVM, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("TriageQueue: %v", err)
	}
	if _, err := f.svc.Signals(f.ctx, pmVM, f.edr, signals.Filter{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Signals: %v", err)
	}
	if _, err := f.svc.Ingest(f.ctx, pmVM, signals.IngestInput{ProductID: f.edr, Source: signals.SourceManual, Text: "x"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := f.svc.Triage(f.ctx, pmVM, sig.ID, signals.TriageInput{Status: signals.StatusInReview}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Triage: %v", err)
	}
	if _, err := f.svc.LinkToFeature(f.ctx, pmVM, sig.ID, edrFeature); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("LinkToFeature: %v", err)
	}
	if _, err := f.svc.ARRByFeature(f.ctx, pmVM, edrFeature); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ARRByFeature: %v", err)
	}
	if _, err := f.svc.BlockedDealsByFeature(f.ctx, pmVM, edrFeature); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("BlockedDealsByFeature: %v", err)
	}
	// Свой продукт доступен.
	own := f.ingest(pmVM, signals.IngestInput{ProductID: f.vm, Text: "свой"})
	if _, err := f.svc.Signal(f.ctx, pmVM, own.ID); err != nil {
		t.Fatalf("свой сигнал: %v", err)
	}
	// Нулевой Scope запрещает всё.
	var zero authz.Scope
	if _, err := f.svc.Signal(f.ctx, zero, own.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: %v", err)
	}
	if _, err := f.svc.Ingest(f.ctx, zero, signals.IngestInput{ProductID: f.vm, Source: signals.SourceManual, Text: "x"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope ingest: %v", err)
	}
}
