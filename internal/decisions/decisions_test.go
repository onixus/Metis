package decisions_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type memPub struct {
	mu     sync.Mutex
	events []kernel.Event
}

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evs...)
	return nil
}

func (m *memPub) count(typ string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func (m *memPub) last(typ string) (kernel.Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.events[i].Type == typ {
			return m.events[i], true
		}
	}
	return kernel.Event{}, false
}

// fakeKB — порт базы знаний в памяти; считает вызовы записи.
type fakeKB struct {
	mu      sync.Mutex
	created []ports.CreatePageInput
	fail    error
}

func (f *fakeKB) Page(context.Context, string) (ports.Page, error) {
	return ports.Page{}, kernel.ErrNotFound
}
func (f *fakeKB) Search(context.Context, string, string) ([]ports.Page, error) {
	return nil, nil
}
func (f *fakeKB) SetProperties(context.Context, string, map[string]string) error { return nil }
func (f *fakeKB) AddLabels(context.Context, string, []string) error              { return nil }
func (f *fakeKB) CreatePage(_ context.Context, in ports.CreatePageInput) (ports.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return ports.Page{}, f.fail
	}
	f.created = append(f.created, in)
	return ports.Page{ID: "2001", Title: in.Title, SpaceKey: in.SpaceKey, URL: "https://kb.example.test/pages/2001", Labels: in.Labels, Properties: in.Properties, Version: 1}, nil
}

var (
	vm  = kernel.NewID()
	edr = kernel.NewID()
)

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(subject string, private map[kernel.ID]authz.Access) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM}, Products: private, Audience: authz.AudienceInternal})
}

func presaleScope() authz.Scope {
	return authz.New(authz.Params{Subject: "presale", Roles: []authz.Role{authz.RolePresale}, AllProducts: authz.AccessStrategic})
}

func serviceScope() authz.Scope {
	return authz.New(authz.Params{Subject: "service:worker", Roles: []authz.Role{authz.RoleService}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

type fixture struct {
	ctx    context.Context
	svc    *decisions.Service
	pub    *memPub
	clock  kernel.FixedClock
	f1, h1 kernel.ID
}

func newFixture() *fixture {
	pub := &memPub{}
	clock := kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	return &fixture{ctx: context.Background(), svc: decisions.NewService(decisions.NewMemStore(), pub, clock), pub: pub, clock: clock, f1: kernel.NewID(), h1: kernel.NewID()}
}

func (f *fixture) input(product kernel.ID) decisions.Input {
	return decisions.Input{
		ProductID: product,
		Title:     "Коннектор EDR v2",
		Context:   "Сделки блокируются без коннектора",
		Snapshot:  map[string]any{"feature_value": kernel.Money{Amount: 120000000, Currency: "RUB"}, "planned_date": "2026-12-01", "blocked_deals": 3},
		Options:   []decisions.Option{{Key: "A", Title: "Свой коннектор"}, {Key: "B", Title: "Партнёрский", Description: "лицензия"}},
		ChosenKey: "A", Rationale: "Контроль сроков", ExpectedEffect: "+3 сделки в Q1",
		ReviewDate: kernel.DateOf(2027, 3, 1),
		Links:      []decisions.Link{{Kind: decisions.LinkFeature, ID: f.f1}, {Kind: decisions.LinkHypothesis, ID: f.h1}},
	}
}

func TestDA01_CreateRecordWithContextSnapshotOptionsAndReviewDate(t *testing.T) {
	f := newFixture()
	rec, err := f.svc.Create(f.ctx, cpoScope(), f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID == kernel.NilID || rec.ProductID != edr || rec.Status != decisions.StatusProposed || rec.Author != "cpo" {
		t.Fatalf("запись: %+v", rec)
	}
	if rec.ReviewDate != kernel.DateOf(2027, 3, 1) || rec.ChosenKey != "A" || len(rec.Options) != 2 || len(rec.Links) != 2 {
		t.Fatalf("поля: %+v", rec)
	}
	if rec.Snapshot["blocked_deals"] != 3 || rec.CreatedAt != f.clock.T {
		t.Fatalf("снимок/время: %+v", rec)
	}
	if f.pub.count(decisions.EventRecordSaved) != 1 {
		t.Fatal("событие saved не опубликовано")
	}
	got, err := f.svc.Get(f.ctx, cpoScope(), rec.ID)
	if err != nil || got.Title != rec.Title {
		t.Fatalf("get: %+v %v", got, err)
	}
	// Валидация: заголовок, контекст, выбранный вариант вне списка, повтор ключей, вид связи.
	bad := []decisions.Input{
		{ProductID: edr, Context: "x"},
		{ProductID: edr, Title: "x"},
		{ProductID: edr, Title: "x", Context: "x", Options: []decisions.Option{{Key: "A", Title: "a"}}, ChosenKey: "Z"},
		{ProductID: edr, Title: "x", Context: "x", Options: []decisions.Option{{Key: "A", Title: "a"}, {Key: "A", Title: "b"}}},
		{ProductID: edr, Title: "x", Context: "x", Links: []decisions.Link{{Kind: "deal", ID: kernel.NewID()}}},
		{ProductID: edr, Title: "x", Context: "x", Links: []decisions.Link{{Kind: decisions.LinkFeature}}},
	}
	for i, in := range bad {
		if _, err := f.svc.Create(f.ctx, cpoScope(), in); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("вариант %d: ожидалась ошибка валидации, получено %v", i, err)
		}
	}
}

func TestDA01_UpdateOnlyProposedAcceptSupersede(t *testing.T) {
	f := newFixture()
	cpo := cpoScope()
	rec, err := f.svc.Create(f.ctx, cpo, f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	in := f.input(edr)
	in.Title = "Коннектор EDR v2 (уточнено)"
	in.ProductID = vm // продукт решения не меняется
	upd, err := f.svc.Update(f.ctx, cpo, rec.ID, in)
	if err != nil || upd.Title != in.Title || upd.ProductID != edr {
		t.Fatalf("update: %+v %v", upd, err)
	}
	// Принятие требует выбранного варианта.
	noChoice := f.input(edr)
	noChoice.ChosenKey = ""
	open, err := f.svc.Create(f.ctx, cpo, noChoice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Accept(f.ctx, cpo, open.ID); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("принятие без выбора: %v", err)
	}
	acc, err := f.svc.Accept(f.ctx, cpo, rec.ID)
	if err != nil || acc.Status != decisions.StatusAccepted {
		t.Fatalf("accept: %+v %v", acc, err)
	}
	if f.pub.count(decisions.EventRecordAccepted) != 1 {
		t.Fatal("событие accepted не опубликовано")
	}
	if _, err := f.svc.Update(f.ctx, cpo, rec.ID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("принятое решение не редактируется: %v", err)
	}
	if _, err := f.svc.Accept(f.ctx, cpo, rec.ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("повторное принятие: %v", err)
	}
	// Замена: заменяющее решение должно быть принято и относиться к тому же продукту.
	next, err := f.svc.Create(f.ctx, cpo, f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Supersede(f.ctx, cpo, rec.ID, next.ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("замена непринятым: %v", err)
	}
	if _, err := f.svc.Accept(f.ctx, cpo, next.ID); err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.Create(f.ctx, cpo, f.input(vm))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Supersede(f.ctx, cpo, rec.ID, other.ID); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("замена решением другого продукта: %v", err)
	}
	if _, err := f.svc.Supersede(f.ctx, cpo, rec.ID, rec.ID); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("замена самим собой: %v", err)
	}
	sup, err := f.svc.Supersede(f.ctx, cpo, rec.ID, next.ID)
	if err != nil || sup.Status != decisions.StatusSuperseded || sup.SupersededBy != next.ID {
		t.Fatalf("supersede: %+v %v", sup, err)
	}
	rej, err := f.svc.Reject(f.ctx, cpo, open.ID)
	if err != nil || rej.Status != decisions.StatusRejected {
		t.Fatalf("reject: %+v %v", rej, err)
	}
	// Списки по продукту и статусу.
	all, err := f.svc.List(f.ctx, cpo, edr, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("list edr: %d %v", len(all), err)
	}
	accepted, err := f.svc.List(f.ctx, cpo, edr, decisions.StatusAccepted)
	if err != nil || len(accepted) != 1 || accepted[0].ID != next.ID {
		t.Fatalf("list accepted: %+v %v", accepted, err)
	}
	if _, err := f.svc.List(f.ctx, cpo, edr, "weird"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный статус: %v", err)
	}
}

func TestDA01_DecisionsForLinkedEntity(t *testing.T) {
	f := newFixture()
	cpo := cpoScope()
	rec, err := f.svc.Create(f.ctx, cpo, f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(f.ctx, cpo, f.input(vm)); err != nil {
		t.Fatal(err)
	}
	refs, err := f.svc.DecisionsFor(f.ctx, cpo, "feature", f.f1)
	if err != nil || len(refs) != 2 || refs[0].ID != rec.ID || refs[0].Title != rec.Title {
		t.Fatalf("decisions for feature: %+v %v", refs, err)
	}
	// PM продукта VM видит только решение VM.
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{vm: authz.AccessPrivate})
	refs, err = f.svc.DecisionsFor(f.ctx, pmVM, "hypothesis", f.h1)
	if err != nil || len(refs) != 1 || refs[0].ID == rec.ID {
		t.Fatalf("фильтр по доступу: %+v %v", refs, err)
	}
	if refs, err := f.svc.DecisionsFor(f.ctx, cpo, "feature", kernel.NewID()); err != nil || len(refs) != 0 {
		t.Fatalf("без связей: %+v %v", refs, err)
	}
	if _, err := f.svc.DecisionsFor(f.ctx, cpo, "deal", f.f1); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный вид: %v", err)
	}
	if _, err := f.svc.DecisionsFor(f.ctx, authz.Scope{}, "feature", f.f1); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой scope: %v", err)
	}
}

func TestDA01_ABAC_PMOfVMCannotWriteEDRPresaleReads(t *testing.T) {
	f := newFixture()
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{vm: authz.AccessPrivate})
	pmEDR := pmScope("pm-edr", map[kernel.ID]authz.Access{edr: authz.AccessPrivate})
	// PM продукта VM не создаёт решение по EDR.
	if _, err := f.svc.Create(f.ctx, pmVM, f.input(edr)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("pm-vm пишет в EDR: %v", err)
	}
	rec, err := f.svc.Create(f.ctx, pmEDR, f.input(edr))
	if err != nil {
		t.Fatalf("pm-edr создаёт решение по EDR: %v", err)
	}
	// Presale читает стратегический срез, но не пишет.
	presale := presaleScope()
	if got, err := f.svc.Get(f.ctx, presale, rec.ID); err != nil || got.ID != rec.ID {
		t.Fatalf("presale читает: %v", err)
	}
	if list, err := f.svc.List(f.ctx, presale, edr, ""); err != nil || len(list) != 1 {
		t.Fatalf("presale список: %d %v", len(list), err)
	}
	if _, err := f.svc.Accept(f.ctx, presale, rec.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("presale принимает: %v", err)
	}
	if _, err := f.svc.Update(f.ctx, pmVM, rec.ID, f.input(edr)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("pm-vm редактирует EDR: %v", err)
	}
	if err := f.svc.RequestPage(f.ctx, pmVM, rec.ID, "METIS"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("pm-vm запрашивает страницу EDR: %v", err)
	}
	// PM без доступа к EDR не читает решение.
	if _, err := f.svc.Get(f.ctx, pmVM, rec.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("pm-vm читает EDR: %v", err)
	}
	// Нулевой Scope запрещает всё.
	if _, err := f.svc.Get(f.ctx, authz.Scope{}, rec.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой scope: %v", err)
	}
	// Портфельное решение (NilID): пишет и читает cpo/admin; PM и presale — нет.
	port, err := f.svc.Create(f.ctx, cpoScope(), f.input(kernel.NilID))
	if err != nil {
		t.Fatalf("cpo создаёт портфельное решение: %v", err)
	}
	if _, err := f.svc.Create(f.ctx, pmEDR, f.input(kernel.NilID)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("pm создаёт портфельное решение: %v", err)
	}
	if _, err := f.svc.Get(f.ctx, presale, port.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("presale читает портфельное решение: %v", err)
	}
	if _, err := f.svc.List(f.ctx, pmEDR, kernel.NilID, ""); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("pm список портфельных: %v", err)
	}
	admin := authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}})
	if _, err := f.svc.Get(f.ctx, admin, port.ID); err != nil {
		t.Fatalf("admin читает портфельное решение: %v", err)
	}
}

func TestDA01_ADRRenderedFromTemplateWithLinks(t *testing.T) {
	f := newFixture()
	rec, err := f.svc.Create(f.ctx, cpoScope(), f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	body := decisions.RenderADR(rec)
	for _, want := range []string{
		"<h1>Коннектор EDR v2</h1>", rec.ID.String(), "<h2>Контекст</h2>", "Сделки блокируются без коннектора",
		"<strong>A</strong>: Свой коннектор <strong>(выбран)</strong>", "<h2>Ожидаемый эффект</h2>", "+3 сделки в Q1",
		"2027-03-01", "<th>blocked_deals</th><td>3</td>", "<th>planned_date</th><td>2026-12-01</td>",
		"<td>feature</td><td>" + f.f1.String() + "</td>", "<td>hypothesis</td><td>" + f.h1.String() + "</td>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("нет фрагмента %q в:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "<th>feature_value</th><td>") {
		t.Fatalf("снимок с деньгами не отрендерен:\n%s", body)
	}
	props := decisions.PageProperties(rec)
	if props[decisions.PropDecisionID] != rec.ID.String() || props[decisions.PropProductID] != edr.String() || props[decisions.PropDecisionStatus] != "proposed" {
		t.Fatalf("свойства: %v", props)
	}
	if props["metis_link_feature"] != f.f1.String() || props["metis_link_hypothesis"] != f.h1.String() {
		t.Fatalf("связи в свойствах: %v", props)
	}
}

func TestDA01_PublishPageHandlerIdempotent(t *testing.T) {
	f := newFixture()
	cpo := cpoScope()
	rec, err := f.svc.Create(f.ctx, cpo, f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RequestPage(f.ctx, cpo, rec.ID, ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустое пространство: %v", err)
	}
	if err := f.svc.RequestPage(f.ctx, cpo, rec.ID, "METIS"); err != nil {
		t.Fatal(err)
	}
	ev, ok := f.pub.last(decisions.EventPageRequested)
	if !ok || ev.AggregateID != rec.ID || ev.ProductID != edr {
		t.Fatalf("событие page.requested: %+v", ev)
	}
	kb := &fakeKB{fail: kernel.ErrUnavailable}
	h := decisions.NewPublishPageHandler(f.svc, kb, serviceScope())
	// Сбой базы знаний: ошибка наружу, событие не отмечено обработанным — повтор из outbox сработает.
	if err := h.Handle(f.ctx, ev); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("сбой базы знаний: %v", err)
	}
	kb.mu.Lock()
	kb.fail = nil
	kb.mu.Unlock()
	if err := h.Handle(f.ctx, ev); err != nil {
		t.Fatal(err)
	}
	// Повторная доставка того же события и событие другого типа — без второй страницы.
	if err := h.Handle(f.ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(f.ctx, kernel.Event{ID: kernel.NewID(), Type: decisions.EventRecordSaved}); err != nil {
		t.Fatal(err)
	}
	if len(kb.created) != 1 {
		t.Fatalf("страниц создано: %d", len(kb.created))
	}
	in := kb.created[0]
	if in.SpaceKey != "METIS" || in.Title != "ADR: Коннектор EDR v2" || !strings.Contains(in.Body, "<h2>Связи</h2>") {
		t.Fatalf("вход создания страницы: %+v", in)
	}
	if len(in.Labels) != 2 || in.Labels[0] != "metis" || in.Labels[1] != "adr" {
		t.Fatalf("метки: %v", in.Labels)
	}
	if in.Properties[decisions.PropDecisionID] != rec.ID.String() || in.Properties["metis_link_feature"] != f.f1.String() {
		t.Fatalf("свойства: %v", in.Properties)
	}
	got, err := f.svc.Get(f.ctx, cpo, rec.ID)
	if err != nil || got.PageID != "2001" {
		t.Fatalf("PageID не сохранён: %+v %v", got, err)
	}
	if f.pub.count(decisions.EventPageCreated) != 1 {
		t.Fatalf("событий page.created: %d", f.pub.count(decisions.EventPageCreated))
	}
	// Повторный запрос страницы для решения со страницей — конфликт; повторное событие с новым ID —
	// идемпотентно по PageID.
	if err := f.svc.RequestPage(f.ctx, cpo, rec.ID, "METIS"); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("повторный запрос: %v", err)
	}
	dup := ev
	dup.ID = kernel.NewID()
	if err := h.Handle(f.ctx, dup); err != nil || len(kb.created) != 1 {
		t.Fatalf("повтор с новым ID события: %v, страниц %d", err, len(kb.created))
	}
	// Обработчик без валидного Scope отказывает.
	if err := decisions.NewPublishPageHandler(f.svc, kb, authz.Scope{}).Handle(f.ctx, ev); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой scope обработчика: %v", err)
	}
}

// decoratingKB — база знаний, которая создаёт страницу и падает на оформлении (метки, свойства),
// возвращая созданную страницу вместе с ошибкой, как адаптер Confluence.
type decoratingKB struct {
	mu                 sync.Mutex
	created            []ports.CreatePageInput
	failing            bool
	labels, properties int
}

func (f *decoratingKB) Page(context.Context, string) (ports.Page, error) {
	return ports.Page{}, kernel.ErrNotFound
}
func (f *decoratingKB) Search(context.Context, string, string) ([]ports.Page, error) { return nil, nil }
func (f *decoratingKB) SetProperties(context.Context, string, map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.properties++
	return nil
}
func (f *decoratingKB) AddLabels(context.Context, string, []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels++
	if f.failing {
		return kernel.ErrUnavailable
	}
	return nil
}

func (f *decoratingKB) CreatePage(_ context.Context, in ports.CreatePageInput) (ports.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, in)
	id := "300" + strconv.Itoa(len(f.created))
	page := ports.Page{ID: id, Title: in.Title, SpaceKey: in.SpaceKey, URL: "https://kb.example.test/pages/" + id, Version: 1}
	if f.failing {
		// Страница создана, метки не добавлены: идентификатор возвращается вместе с ошибкой.
		return page, fmt.Errorf("метки страницы %s: %w", id, kernel.ErrUnavailable)
	}
	page.Labels, page.Properties = in.Labels, in.Properties
	return page, nil
}

// TestDA01_PublishPageDoesNotDuplicateOnDecorationFailure — если база знаний создала страницу и
// упала на оформлении, PageID сохраняется до возврата ошибки: повторная доставка события идёт по
// ветке «страница есть» и второй страницы ADR не создаёт (дефект 8, TODO(question-29)).
func TestDA01_PublishPageDoesNotDuplicateOnDecorationFailure(t *testing.T) {
	f := newFixture()
	cpo := cpoScope()
	rec, err := f.svc.Create(f.ctx, cpo, f.input(edr))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RequestPage(f.ctx, cpo, rec.ID, "METIS"); err != nil {
		t.Fatal(err)
	}
	ev, ok := f.pub.last(decisions.EventPageRequested)
	if !ok {
		t.Fatal("нет события page.requested")
	}

	kb := &decoratingKB{failing: true}
	h := decisions.NewPublishPageHandler(f.svc, kb, serviceScope())
	if err := h.Handle(f.ctx, ev); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("сбой оформления: ожидался ErrUnavailable, получено %v", err)
	}
	got, err := f.svc.Get(f.ctx, cpo, rec.ID)
	if err != nil || got.PageID != "3001" {
		t.Fatalf("PageID не сохранён после сбоя оформления: %q %v", got.PageID, err)
	}

	// Failed decoration must not acknowledge the event even when the page ID survived.
	if err := h.Handle(f.ctx, ev); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("premature ACK: %v", err)
	}
	kb.mu.Lock()
	kb.failing = false
	kb.mu.Unlock()
	// Повтор события: страница уже есть, второй вызов CreatePage не делается.
	if err := h.Handle(f.ctx, ev); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	kb.mu.Lock()
	n := len(kb.created)
	kb.mu.Unlock()
	if n != 1 {
		t.Fatalf("создано страниц: %d, ожидалась 1", n)
	}
	if got, err := f.svc.Get(f.ctx, cpo, rec.ID); err != nil || got.PageID != "3001" {
		t.Fatalf("PageID после повтора: %q %v", got.PageID, err)
	}
	// Decoration is retried before one completion event is published.
	if kb.labels != 2 || kb.properties != 1 {
		t.Fatalf("decoration calls: labels=%d properties=%d", kb.labels, kb.properties)
	}
	if f.pub.count(decisions.EventPageCreated) != 1 {
		t.Fatalf("событий page.created: %d", f.pub.count(decisions.EventPageCreated))
	}
}
