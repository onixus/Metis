package delivery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

// --- заглушки ---

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

func (m *memPub) ofType(typ string) []kernel.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []kernel.Event
	for _, e := range m.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// fakeTracker — трекер в памяти; фиксирует записи, чтобы проверить, что они идут только из обработчика.
type fakeTracker struct {
	mu       sync.Mutex
	epics    map[string]ports.Epic
	issues   map[string][]ports.Issue
	sprints  map[string][]ports.Sprint
	down     bool
	created  []string
	nextKey  int
	worklogs []ports.Worklog
}

func newTracker() *fakeTracker {
	return &fakeTracker{epics: map[string]ports.Epic{}, issues: map[string][]ports.Issue{}, sprints: map[string][]ports.Sprint{}, nextKey: 100}
}

func (f *fakeTracker) fail() error {
	if f.down {
		return fmt.Errorf("%w: трекер выключен", kernel.ErrUnavailable)
	}
	return nil
}

func (f *fakeTracker) Epics(_ context.Context, _ string) ([]ports.Epic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	out := make([]ports.Epic, 0, len(f.epics))
	for _, e := range f.epics {
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeTracker) Epic(_ context.Context, key string) (ports.Epic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return ports.Epic{}, err
	}
	e, ok := f.epics[key]
	if !ok {
		return ports.Epic{}, fmt.Errorf("%w: эпик %s", kernel.ErrNotFound, key)
	}
	return e, nil
}

func (f *fakeTracker) EpicIssues(_ context.Context, key string) ([]ports.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	return append([]ports.Issue(nil), f.issues[key]...), nil
}

func (f *fakeTracker) Sprints(_ context.Context, board string) ([]ports.Sprint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	return append([]ports.Sprint(nil), f.sprints[board]...), nil
}

func (f *fakeTracker) Versions(context.Context, string) ([]ports.Version, error) { return nil, nil }
func (f *fakeTracker) Worklogs(_ context.Context, keys []string, since time.Time) ([]ports.Worklog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := make([]ports.Worklog, 0, len(f.worklogs))
	for _, w := range f.worklogs {
		if !want[w.IssueKey] || (!since.IsZero() && w.Started.Before(since)) {
			continue
		}
		out = append(out, w)
	}
	return out, nil
}

func (f *fakeTracker) CreateEpic(_ context.Context, project, summary, _, featureRef string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return "", err
	}
	f.nextKey++
	key := fmt.Sprintf("%s-%d", project, f.nextKey)
	f.epics[key] = ports.Epic{Key: key, Summary: summary, FeatureRef: featureRef, Status: "To Do"}
	f.created = append(f.created, key)
	return key, nil
}

func (f *fakeTracker) SetEpicPriority(context.Context, string, string) error   { return nil }
func (f *fakeTracker) LinkEpicToFeature(context.Context, string, string) error { return nil }

type fakeDLQ struct{ n int }

func (d fakeDLQ) DLQCount(context.Context) (int, error) { return d.n, nil }

// --- окружение ---

type fixture struct {
	t       *testing.T
	ctx     context.Context
	clock   *kernel.FixedClock
	pub     *memPub
	tracker *fakeTracker
	store   *delivery.MemStore
	graph   *pg.Service
	svc     *delivery.Service
	cpo     authz.Scope
	svcSc   authz.Scope
	edr     kernel.ID
	soar    kernel.ID
	vm      kernel.ID
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func adminScope() authz.Scope {
	return authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

// serviceScope — сервисная учётка воркера. Пока роль service не имеет ActionWriteRoadmap, добавляем admin.
func serviceScope() authz.Scope {
	return authz.New(authz.Params{Subject: "svc-delivery", Roles: []authz.Role{authz.RoleService, authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(subject string, private map[kernel.ID]authz.Access) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM}, Products: private, Audience: authz.AudienceInternal})
}

func d(y int, m time.Month, day int) kernel.Date { return kernel.DateOf(y, m, day) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := &kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	pub := &memPub{}
	graph := pg.NewService(pg.NewMemStore(), pub, clock)
	tracker := newTracker()
	store := delivery.NewMemStore()
	svcSc := serviceScope()
	svc := delivery.NewService(store, tracker, graph, pub, clock, delivery.Config{Name: "jira-mock", StaleAfter: 15 * time.Minute, ServiceScope: svcSc})
	f := &fixture{t: t, ctx: context.Background(), clock: clock, pub: pub, tracker: tracker, store: store, graph: graph, svc: svc, cpo: cpoScope(), svcSc: svcSc}
	f.edr = f.product("edr", "EDR")
	f.soar = f.product("soar", "SOAR")
	f.vm = f.product("vm", "VM")
	return f
}

func (f *fixture) product(key, name string) kernel.ID {
	f.t.Helper()
	p, err := f.graph.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: key, Name: name, Type: pg.ProductTypeSecurity, Owner: "pm-" + key})
	if err != nil {
		f.t.Fatalf("create product: %v", err)
	}
	return p.ID
}

func (f *fixture) feature(product kernel.ID, name string, date kernel.Date) kernel.ID {
	f.t.Helper()
	ft, err := f.graph.CreateFeature(f.ctx, f.cpo, product, pg.FeatureInput{Name: name, Status: pg.FeaturePlanned, PlannedDate: date})
	if err != nil {
		f.t.Fatalf("create feature: %v", err)
	}
	return ft.ID
}

func (f *fixture) mapped(product kernel.ID, name, epicKey string, due kernel.Date, issues ...ports.Issue) kernel.ID {
	f.t.Helper()
	id := f.feature(product, name, due)
	f.tracker.epics[epicKey] = ports.Epic{Key: epicKey, Summary: name, Status: "In Progress", DueDate: due, FixVersions: []string{"2.4"}}
	f.tracker.issues[epicKey] = issues
	if _, err := f.svc.MapFeature(f.ctx, f.cpo, id, epicKey, "SOAR"); err != nil {
		f.t.Fatalf("map: %v", err)
	}
	return id
}

func issue(key string, done bool) ports.Issue {
	st := "To Do"
	if done {
		st = "Done"
	}
	return ports.Issue{Key: key, Summary: "задача " + key, Status: st, Done: done}
}

func (f *fixture) sync() {
	f.t.Helper()
	if err := f.svc.Sync(f.ctx); err != nil {
		f.t.Fatalf("sync: %v", err)
	}
}

// --- DL-01 ---

func TestDL01_MappingAndEpicCreationGoesThroughOutbox(t *testing.T) {
	f := newFixture(t)
	conn := f.feature(f.soar, "Коннектор EDR v2", d(2026, 12, 1))

	// Запрос создания эпика не трогает трекер: только событие-команда в outbox.
	if err := f.svc.CreateEpicForFeature(f.ctx, f.cpo, conn, "SOAR"); err != nil {
		t.Fatal(err)
	}
	if len(f.tracker.created) != 0 {
		t.Fatal("запись в трекер до обработки outbox запрещена (инвариант 5)")
	}
	evs := f.pub.ofType(delivery.EventEpicCreateRequested)
	if len(evs) != 1 || evs[0].AggregateID != conn || evs[0].ProductID != f.soar {
		t.Fatalf("событие: %+v", evs)
	}
	var req delivery.EpicCreateRequest
	if err := json.Unmarshal(evs[0].Payload, &req); err != nil || req.Project != "SOAR" || req.Summary != "Коннектор EDR v2" {
		t.Fatalf("payload: %+v %v", req, err)
	}

	// Обработчик воркера вызывает порт и записывает ключ в фичу.
	h := delivery.NewCreateEpicHandler(f.tracker, f.svc, f.graph, f.svcSc)
	if err := h.Handle(f.ctx, evs[0]); err != nil {
		t.Fatal(err)
	}
	if len(f.tracker.created) != 1 {
		t.Fatalf("создано эпиков: %d", len(f.tracker.created))
	}
	key := f.tracker.created[0]
	if f.tracker.epics[key].FeatureRef != conn.String() {
		t.Fatalf("ссылка на фичу в эпике: %q", f.tracker.epics[key].FeatureRef)
	}
	ft, _ := f.graph.Feature(f.ctx, f.cpo, conn)
	if ft.ExternalKey != key {
		t.Fatalf("ExternalKey фичи: %q, ожидался %s", ft.ExternalKey, key)
	}
	m, err := f.store.MappingByEpic(f.ctx, key)
	if err != nil || m.FeatureID != conn || m.ProductID != f.soar {
		t.Fatalf("маппинг: %+v %v", m, err)
	}
	if got, _ := f.graph.FeatureByExternalKey(f.ctx, f.cpo, key); got.ID != conn {
		t.Fatal("фича не находится по внешнему ключу")
	}
	// Повторная доставка того же события — идемпотентна.
	if err := h.Handle(f.ctx, evs[0]); err != nil || len(f.tracker.created) != 1 {
		t.Fatalf("повтор: %v, создано %d", err, len(f.tracker.created))
	}
	// Повторный запрос создания для привязанной фичи — конфликт.
	if err := f.svc.CreateEpicForFeature(f.ctx, f.cpo, conn, "SOAR"); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("ожидался ErrConflict: %v", err)
	}
	// Маппинг релиза на fix version.
	rel := kernel.NewID()
	rm, err := f.svc.MapRelease(f.ctx, f.cpo, rel, f.soar, "SOAR", "SOAR 2.4")
	if err != nil || rm.FixVersion != "SOAR 2.4" {
		t.Fatalf("release mapping: %+v %v", rm, err)
	}
	// PM чужого продукта не может ни привязать, ни запросить эпик.
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	other := f.feature(f.soar, "Ещё фича", d(2027, 1, 1))
	if err := f.svc.CreateEpicForFeature(f.ctx, pmVM, other, "SOAR"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался ErrForbidden: %v", err)
	}
	if _, err := f.svc.MapFeature(f.ctx, pmVM, other, "SOAR-7", "SOAR"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался ErrForbidden: %v", err)
	}
	if _, err := f.svc.MapFeature(f.ctx, authz.Scope{}, other, "SOAR-7", "SOAR"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: %v", err)
	}
}

// --- DL-02 ---

func TestDL02_SprintStatusGoalProgressAndCarryOver(t *testing.T) {
	f := newFixture(t)
	fm := delivery.DefaultFieldMapping()
	fm.Boards[f.soar] = "7"
	if err := f.svc.SetFieldMapping(f.ctx, adminScope(), fm); err != nil {
		t.Fatal(err)
	}
	f.tracker.sprints["7"] = []ports.Sprint{
		{ID: "102", Name: "Sprint 8", Goal: "Изоляция хоста", State: ports.SprintActive, StartDate: d(2026, 8, 18), EndDate: d(2026, 9, 1),
			Issues: []ports.Issue{issue("SOAR-102", true), issue("SOAR-103", false), issue("SOAR-105", false)}},
		{ID: "101", Name: "Sprint 7", Goal: "Схема и клиент", State: ports.SprintClosed, StartDate: d(2026, 8, 3), EndDate: d(2026, 8, 17),
			Issues: []ports.Issue{issue("SOAR-101", true), issue("SOAR-102", false)}},
	}
	f.sync()
	sprints, st, err := f.svc.SprintStatuses(f.ctx, f.cpo, f.soar)
	if err != nil {
		t.Fatal(err)
	}
	if st.Stale {
		t.Fatal("сразу после сверки данные не устарели")
	}
	if len(sprints) != 2 || sprints[0].SprintID != "101" || sprints[1].SprintID != "102" {
		t.Fatalf("порядок спринтов: %+v", sprints)
	}
	s7, s8 := sprints[0], sprints[1]
	if s7.Goal != "Схема и клиент" || s7.Total != 2 || s7.Done != 1 || len(s7.CarriedOver) != 0 || s7.State != ports.SprintClosed {
		t.Fatalf("спринт 7: %+v", s7)
	}
	if s8.Goal != "Изоляция хоста" || s8.Total != 3 || s8.Done != 1 || len(s8.Issues) != 3 {
		t.Fatalf("спринт 8: %+v", s8)
	}
	if len(s8.CarriedOver) != 1 || s8.CarriedOver[0] != "SOAR-102" {
		t.Fatalf("перенос: %v", s8.CarriedOver)
	}
	// Состав спринта виден только приватному контуру продукта.
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if _, _, err := f.svc.SprintStatuses(f.ctx, pmVM, f.soar); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался ErrForbidden: %v", err)
	}
}

// --- DL-03 ---

func TestDL03_ReadinessScopeCreepPlanFact(t *testing.T) {
	f := newFixture(t)
	conn := f.mapped(f.soar, "Коннектор EDR v2", "SOAR-42", d(2026, 12, 1), issue("SOAR-101", true), issue("SOAR-102", true), issue("SOAR-103", false))
	f.sync()
	m, err := f.svc.FeatureMetrics(f.ctx, f.cpo, conn)
	if err != nil {
		t.Fatal(err)
	}
	if m.Readiness.Done != 2 || m.Readiness.Total != 3 || m.Readiness.Percent < 66.6 || m.Readiness.Percent > 66.7 {
		t.Fatalf("готовность: %+v", m.Readiness)
	}
	if m.ScopeCreep.Initial != 3 || len(m.ScopeCreep.Added) != 0 {
		t.Fatalf("первый снимок: %+v", m.ScopeCreep)
	}
	if m.PlanFact.DeltaDays != 0 || m.PlanFact.Late {
		t.Fatalf("план/факт совпадают: %+v", m.PlanFact)
	}

	// В трекере добавили задачу и сдвинули due date на 21 день.
	f.tracker.issues["SOAR-42"] = append(f.tracker.issues["SOAR-42"], issue("SOAR-104", false))
	e := f.tracker.epics["SOAR-42"]
	e.DueDate = d(2026, 12, 22)
	f.tracker.epics["SOAR-42"] = e
	// Пользователь ужесточил план — трекер отстаёт от плана.
	if _, err := f.graph.ShiftFeatureDate(f.ctx, f.cpo, conn, d(2026, 12, 15), "план заказчика"); err != nil {
		t.Fatal(err)
	}
	f.clock.T = f.clock.T.Add(time.Hour)
	f.sync()
	m, err = f.svc.FeatureMetrics(f.ctx, f.cpo, conn)
	if err != nil {
		t.Fatal(err)
	}
	if m.Readiness.Done != 2 || m.Readiness.Total != 4 {
		t.Fatalf("готовность после добавления: %+v", m.Readiness)
	}
	if m.ScopeCreep.Initial != 3 || len(m.ScopeCreep.Added) != 1 || m.ScopeCreep.Added[0] != "SOAR-104" || m.ScopeCreep.Percent < 33.3 {
		t.Fatalf("scope creep: %+v", m.ScopeCreep)
	}
	// Sync сдвинул плановую дату фичи по due date эпика (сверка страхует потерянные webhooks).
	ft, _ := f.graph.Feature(f.ctx, f.cpo, conn)
	if ft.PlannedDate != d(2026, 12, 22) {
		t.Fatalf("плановая дата после сверки: %s", ft.PlannedDate)
	}
	if m.PlanFact.DueDate != d(2026, 12, 22) || m.PlanFact.DeltaDays != 0 {
		t.Fatalf("план/факт: %+v", m.PlanFact)
	}
	// Поля трекера в проекции — только чтение: нет метода записи, проекция отражает трекер.
	p, _, err := f.svc.FeatureProjection(f.ctx, f.cpo, conn)
	if err != nil || p.Status != "In Progress" || p.DueDate != d(2026, 12, 22) || len(p.FixVersions) != 1 {
		t.Fatalf("проекция: %+v %v", p, err)
	}
	// Приватный контур: PM VM не видит проекцию SOAR.
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if _, err := f.svc.FeatureMetrics(f.ctx, pmVM, conn); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался ErrForbidden: %v", err)
	}
}

// --- AD-05 ---

func TestAD05_ConnectorStatusRequiresAdmin(t *testing.T) {
	f := newFixture(t)
	f.svc.WithDLQ(fakeDLQ{n: 3})
	f.mapped(f.soar, "Коннектор EDR v2", "SOAR-42", d(2026, 12, 1))
	f.sync()
	cs, err := f.svc.ConnectorStatus(f.ctx, adminScope())
	if err != nil {
		t.Fatal(err)
	}
	if cs.Name != "jira-mock" || cs.DLQCount != 3 || cs.MappedEpics != 1 || cs.Sync.Stale || cs.Sync.LastError != "" {
		t.Fatalf("состояние коннектора: %+v", cs)
	}
	if cs.Mapping.StatusMap["Done"] != pg.FeatureDone {
		t.Fatalf("маппинг статусов по умолчанию: %+v", cs.Mapping)
	}
	// Маппинг полей и статусов настраивается без изменения кода.
	fm := cs.Mapping
	fm.FeatureRefField = "customfield_20000"
	fm.StatusMap["Ready"] = pg.FeaturePlanned
	if err := f.svc.SetFieldMapping(f.ctx, adminScope(), fm); err != nil {
		t.Fatal(err)
	}
	cs, _ = f.svc.ConnectorStatus(f.ctx, adminScope())
	if cs.Mapping.FeatureRefField != "customfield_20000" || cs.Mapping.FeatureStatus("Ready") != pg.FeaturePlanned {
		t.Fatalf("маппинг не сохранён: %+v", cs.Mapping)
	}
	fm.StatusMap["Odd"] = "nonsense"
	if err := f.svc.SetFieldMapping(f.ctx, adminScope(), fm); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный статус: %v", err)
	}
	// Отказ: CPO, PM и нулевой Scope не управляют коннекторами.
	for name, sc := range map[string]authz.Scope{"cpo": f.cpo, "pm": pmScope("pm", map[kernel.ID]authz.Access{f.soar: authz.AccessPrivate}), "zero": {}} {
		if _, err := f.svc.ConnectorStatus(f.ctx, sc); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("%s: ожидался ErrForbidden, получено %v", name, err)
		}
		if err := f.svc.SetFieldMapping(f.ctx, sc, cs.Mapping); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("%s: ожидался ErrForbidden, получено %v", name, err)
		}
	}
}

// --- NF-R05 ---

func TestNFR05_StaleFlagWhenTrackerDown(t *testing.T) {
	f := newFixture(t)
	conn := f.mapped(f.soar, "Коннектор EDR v2", "SOAR-42", d(2026, 12, 1), issue("SOAR-101", true))
	// До первой сверки проекции нет, состояние устаревшее.
	st, _ := f.svc.SyncState(f.ctx)
	if !st.Stale {
		t.Fatal("до первой сверки Stale=true")
	}
	f.sync()
	p, st, err := f.svc.FeatureProjection(f.ctx, f.cpo, conn)
	if err != nil || st.Stale || p.DueDate != d(2026, 12, 1) {
		t.Fatalf("после сверки: %+v %+v %v", p, st, err)
	}
	// Трекер выключен: сверка падает, проекция остаётся, признак устаревания появляется по порогу.
	f.tracker.down = true
	f.clock.T = f.clock.T.Add(16 * time.Minute)
	err = f.svc.Sync(f.ctx)
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable: %v", err)
	}
	p, st, err = f.svc.FeatureProjection(f.ctx, f.cpo, conn)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Stale || st.LastError == "" || st.Lag < 16*time.Minute {
		t.Fatalf("состояние: %+v", st)
	}
	if p.DueDate != d(2026, 12, 1) || len(p.Issues) != 1 {
		t.Fatalf("последняя проекция должна сохраниться: %+v", p)
	}
	m, err := f.svc.FeatureMetrics(f.ctx, f.cpo, conn)
	if err != nil || !m.Sync.Stale || m.Readiness.Done != 1 {
		t.Fatalf("метрики на устаревшей проекции: %+v %v", m, err)
	}
	// Трекер вернулся — признак снимается.
	f.tracker.down = false
	f.sync()
	_, st, _ = f.svc.FeatureProjection(f.ctx, f.cpo, conn)
	if st.Stale || st.LastError != "" {
		t.Fatalf("после восстановления: %+v", st)
	}
}

// --- NF-R06 ---

func TestNFR06_WebhookIdempotent(t *testing.T) {
	f := newFixture(t)
	conn := f.mapped(f.soar, "Коннектор EDR v2", "SOAR-42", d(2026, 12, 1))
	f.sync()
	ev := ports.WebhookEvent{ExternalID: "10042:50001", Type: ports.WebhookEpicUpdated, EpicKey: "SOAR-42",
		ChangedFields: []string{"due_date"}, NewDueDate: d(2026, 12, 22), OccurredAt: f.clock.Now()}
	if err := f.svc.HandleWebhook(f.ctx, ev); err != nil {
		t.Fatal(err)
	}
	shifts := len(f.pub.ofType(pg.EventDateShifted))
	ft, _ := f.graph.Feature(f.ctx, f.cpo, conn)
	if ft.PlannedDate != d(2026, 12, 22) {
		t.Fatalf("дата фичи: %s", ft.PlannedDate)
	}
	// Повторная доставка того же события ничего не меняет и не порождает новых событий.
	for i := 0; i < 3; i++ {
		if err := f.svc.HandleWebhook(f.ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(f.pub.ofType(pg.EventDateShifted)); got != shifts {
		t.Fatalf("повторная доставка породила события сдвига: %d → %d", shifts, got)
	}
	if got := len(f.pub.ofType(delivery.EventEpicDueDateChanged)); got != 1 {
		t.Fatalf("событий изменения due date: %d", got)
	}
	// Событие по непривязанному эпику и без ключа.
	if err := f.svc.HandleWebhook(f.ctx, ports.WebhookEvent{ExternalID: "x:1", EpicKey: "SOAR-999", Type: ports.WebhookEpicUpdated}); err != nil {
		t.Fatalf("непривязанный эпик игнорируется: %v", err)
	}
	if err := f.svc.HandleWebhook(f.ctx, ports.WebhookEvent{EpicKey: "SOAR-42"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("без внешнего ключа: %v", err)
	}
}

// --- 7.7 п.5 ---

func TestPG08_EpicShiftPropagatesToFeature(t *testing.T) {
	f := newFixture(t)
	api := f.feature(f.edr, "Response API v2", d(2026, 11, 1))
	conn := f.mapped(f.soar, "Коннектор EDR v2", "SOAR-42", d(2026, 12, 1), issue("SOAR-101", false))
	c, err := f.graph.SaveContract(f.ctx, f.cpo, kernel.NilID, pg.ContractInput{Name: "EDR ↔ SOAR", ProviderProductID: f.soar, ConsumerProductID: f.edr,
		ProviderFeatureIDs: []kernel.ID{conn}, ConsumerFeatureIDs: []kernel.ID{api}, Criticality: pg.CritBlocks})
	if err != nil {
		t.Fatal(err)
	}
	f.sync()

	// Мок Jira сдвигает эпик на 21 день — приходит webhook.
	ev := ports.WebhookEvent{ExternalID: "10042:50001", Type: ports.WebhookEpicUpdated, EpicKey: "SOAR-42",
		ChangedFields: []string{"due_date", "fix_versions"}, NewDueDate: d(2026, 12, 22), OccurredAt: f.clock.Now()}
	start := time.Now()
	if err := f.svc.HandleWebhook(f.ctx, ev); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("NF-P03: распространение дольше 10 с")
	}
	connF, _ := f.graph.Feature(f.ctx, f.cpo, conn)
	if connF.PlannedDate != d(2026, 12, 22) {
		t.Fatalf("плановая дата коннектора: %s", connF.PlannedDate)
	}
	apiF, _ := f.graph.Feature(f.ctx, f.cpo, api)
	if !apiF.Affected || apiF.AffectedBy != conn || apiF.ImpliedDate != d(2026, 12, 22) {
		t.Fatalf("фича EDR должна быть помечена как затронутая: %+v", apiF)
	}
	_, ready, _ := f.graph.Contract(f.ctx, f.cpo, c.ID)
	if ready != d(2026, 12, 22) {
		t.Fatalf("срок контракта: %s", ready)
	}
	// Причина записана в историю дат (событие сдвига с reason).
	shifted := f.pub.ofType(pg.EventDateShifted)
	if len(shifted) != 1 || shifted[0].AggregateID != conn {
		t.Fatalf("событий сдвига: %d", len(shifted))
	}
	var payload map[string]any
	_ = json.Unmarshal(shifted[0].Payload, &payload)
	reason, _ := payload["reason"].(string)
	if reason != "эпик SOAR-42 сдвинут в трекере" {
		t.Fatalf("причина: %q (payload %s)", reason, shifted[0].Payload)
	}
	p, _, _ := f.svc.FeatureProjection(f.ctx, f.cpo, conn)
	if p.DueDate != d(2026, 12, 22) || p.SourceEventID != "10042:50001" {
		t.Fatalf("проекция: %+v", p)
	}
}

func TestNFR05_RecordSyncFailureRequiresServiceAndKeepsLastSuccess(t *testing.T) {
	ctx := context.Background()
	store := delivery.NewMemStore()
	clock := kernel.FixedClock{T: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	previous := clock.Now().Add(-time.Hour)
	if err := store.SaveSyncState(ctx, delivery.SyncState{LastSuccessAt: previous}); err != nil {
		t.Fatal(err)
	}
	bare := delivery.NewService(store, nil, nil, nil, clock, delivery.Config{})
	failure := errors.New("synthetic connector outage")
	if err := bare.RecordSyncFailure(ctx, clock.Now(), failure); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("untrusted scope records sync state: %v", err)
	}
	svc := delivery.NewService(store, nil, nil, nil, clock, delivery.Config{ServiceScope: serviceScope()})
	if err := svc.Sync(ctx); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("disabled connector must be unavailable: %v", err)
	}
	if err := svc.RecordSyncFailure(ctx, clock.Now(), failure); err != nil {
		t.Fatal(err)
	}
	state, err := store.SyncState(ctx)
	if err != nil || state.LastSuccessAt != previous || state.LastAttemptAt != clock.Now() || state.LastError != failure.Error() {
		t.Fatalf("failure lost projection freshness: %+v %v", state, err)
	}
	if err := svc.RecordSyncFailure(ctx, previous, errors.New("older failed attempt")); err != nil {
		t.Fatal(err)
	}
	state, err = store.SyncState(ctx)
	if err != nil || state.LastError != failure.Error() {
		t.Fatalf("older worker overwrote latest sync state: %+v %v", state, err)
	}
}
