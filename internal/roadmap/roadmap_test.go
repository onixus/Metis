package roadmap_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

type memPub struct{ events []kernel.Event }

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

type fixture struct {
	t     *testing.T
	ctx   context.Context
	svc   *roadmap.Service
	pub   *memPub
	clock kernel.FixedClock
	cpo   authz.Scope
	edr   kernel.ID
	vm    kernel.ID
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func presaleScope() authz.Scope {
	return authz.New(authz.Params{Subject: "presale", Roles: []authz.Role{authz.RolePresale}, AllProducts: authz.AccessStrategic, Audience: authz.AudienceSalesSafe})
}

func pmScope(subject string, private kernel.ID) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM},
		Products: map[kernel.ID]authz.Access{private: authz.AccessPrivate}, Audience: authz.AudienceInternal})
}

func serviceScope() authz.Scope {
	return authz.New(authz.Params{Subject: "worker", Roles: []authz.Role{authz.RoleService, authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func d(y int, m time.Month, day int) kernel.Date { return kernel.DateOf(y, m, day) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub := &memPub{}
	clock := kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	return &fixture{
		t: t, ctx: context.Background(), pub: pub, clock: clock,
		svc: roadmap.NewService(roadmap.NewMemStore(), pub, clock),
		cpo: cpoScope(), edr: kernel.NewID(), vm: kernel.NewID(),
	}
}

func (f *fixture) item(product kernel.ID, title string, b roadmap.Bucket, start, end kernel.Date, aud authz.Audience) roadmap.RoadmapItem {
	f.t.Helper()
	it, err := f.svc.CreateItem(f.ctx, f.cpo, product, roadmap.ItemInput{Title: title, Bucket: b, StartDate: start, EndDate: end, Audience: aud})
	if err != nil {
		f.t.Fatalf("create item %s: %v", title, err)
	}
	return it
}

func (f *fixture) release(product kernel.ID, version string, date kernel.Date) roadmap.Release {
	f.t.Helper()
	r, err := f.svc.CreateRelease(f.ctx, f.cpo, product, roadmap.ReleaseInput{Name: "EDR " + version, Version: version, PlannedDate: date})
	if err != nil {
		f.t.Fatalf("create release %s: %v", version, err)
	}
	return r
}

func TestRM01_TimelineSortedByDateAndSkipsUndated(t *testing.T) {
	f := newFixture(t)
	f.item(f.edr, "C", roadmap.BucketLater, d(2027, 1, 10), d(2027, 3, 1), authz.AudienceInternal)
	f.item(f.edr, "A", roadmap.BucketNow, d(2026, 10, 1), d(2026, 12, 1), authz.AudienceInternal)
	f.item(f.edr, "B", roadmap.BucketNext, kernel.Date{}, d(2026, 12, 15), authz.AudienceInternal)
	f.item(f.edr, "undated", roadmap.BucketLater, kernel.Date{}, kernel.Date{}, authz.AudienceInternal)
	f.item(f.vm, "other product", roadmap.BucketNow, d(2026, 1, 1), d(2026, 2, 1), authz.AudienceInternal)

	tl, err := f.svc.Timeline(f.ctx, f.cpo, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Items) != 3 || tl.SalesSafe != nil {
		t.Fatalf("ожидалось 3 датированных элемента, получено %+v", tl)
	}
	for i, want := range []string{"A", "B", "C"} {
		if tl.Items[i].Title != want {
			t.Fatalf("порядок: позиция %d = %s, ожидалось %s", i, tl.Items[i].Title, want)
		}
	}
}

func TestRM01_NowNextLaterGroupsByBucket(t *testing.T) {
	f := newFixture(t)
	f.item(f.edr, "n1", roadmap.BucketNow, d(2026, 10, 1), d(2026, 11, 1), authz.AudienceInternal)
	f.item(f.edr, "n2", roadmap.BucketNow, kernel.Date{}, kernel.Date{}, authz.AudienceInternal)
	f.item(f.edr, "x1", roadmap.BucketNext, kernel.Date{}, kernel.Date{}, authz.AudienceInternal)
	f.item(f.edr, "l1", roadmap.BucketLater, kernel.Date{}, kernel.Date{}, authz.AudienceInternal)

	v, err := f.svc.NowNextLater(f.ctx, f.cpo, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Now.Items) != 2 || len(v.Next.Items) != 1 || len(v.Later.Items) != 1 {
		t.Fatalf("группировка: now=%d next=%d later=%d", len(v.Now.Items), len(v.Next.Items), len(v.Later.Items))
	}
	if v.Now.Items[0].Title != "n1" {
		t.Fatalf("датированный элемент должен идти первым: %s", v.Now.Items[0].Title)
	}
}

func TestRM01_ByReleaseGroupsItemsAndUnassigned(t *testing.T) {
	f := newFixture(t)
	r2 := f.release(f.edr, "2.0", d(2027, 3, 1))
	r1 := f.release(f.edr, "1.5", d(2026, 12, 1))
	if _, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "in 1.5", Bucket: roadmap.BucketNow, ReleaseID: r1.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "in 2.0", Bucket: roadmap.BucketNext, ReleaseID: r2.ID}); err != nil {
		t.Fatal(err)
	}
	f.item(f.edr, "free", roadmap.BucketLater, kernel.Date{}, kernel.Date{}, authz.AudienceInternal)
	// релиз чужого продукта привязать нельзя
	rv := f.release(f.vm, "9.0", d(2027, 1, 1))
	if _, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "bad", Bucket: roadmap.BucketNow, ReleaseID: rv.ID}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("релиз другого продукта должен давать ErrValidation, получено %v", err)
	}

	v, err := f.svc.ByRelease(f.ctx, f.cpo, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Releases) != 2 || v.Releases[0].Release.Version != "1.5" || v.Releases[1].Release.Version != "2.0" {
		t.Fatalf("релизы по дате: %+v", v.Releases)
	}
	if len(v.Releases[0].Items) != 1 || v.Releases[0].Items[0].Title != "in 1.5" || len(v.Releases[1].Items) != 1 {
		t.Fatalf("состав релизов: %+v", v.Releases)
	}
	if len(v.Unassigned.Items) != 1 || v.Unassigned.Items[0].Title != "free" {
		t.Fatalf("без релиза: %+v", v.Unassigned)
	}
}

// Сценарий приёмки 7.7 п.7: sales-safe срез не отдаёт внутренние элементы при прямом запросе.
func TestRM02_SalesSafeSliceHidesInternalItems(t *testing.T) {
	f := newFixture(t)
	internal := f.item(f.edr, "секретная фича", roadmap.BucketNow, d(2026, 10, 1), d(2026, 11, 1), authz.AudienceInternal)
	public := f.item(f.edr, "публичная фича", roadmap.BucketNext, d(2026, 11, 1), d(2026, 12, 1), authz.AudienceSalesSafe)
	if _, err := f.svc.ChangeDates(f.ctx, f.cpo, public.ID, d(2026, 11, 1), d(2027, 1, 1), "внутренняя причина"); err != nil {
		t.Fatal(err)
	}
	presale := presaleScope()

	tl, err := f.svc.Timeline(f.ctx, presale, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Audience != authz.AudienceSalesSafe || tl.Items != nil || len(tl.SalesSafe) != 1 || tl.SalesSafe[0].ID != public.ID {
		t.Fatalf("sales-safe timeline должен содержать только публичный элемент: %+v", tl)
	}
	nnl, err := f.svc.NowNextLater(f.ctx, presale, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(nnl.Now.SalesSafe) != 0 || len(nnl.Next.SalesSafe) != 1 || nnl.Now.Items != nil || nnl.Next.Items != nil {
		t.Fatalf("sales-safe now/next/later: %+v", nnl)
	}
	br, err := f.svc.ByRelease(f.ctx, presale, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if len(br.Unassigned.SalesSafe) != 1 || br.Unassigned.Items != nil {
		t.Fatalf("sales-safe by release: %+v", br)
	}
	// Прямой запрос полных элементов и истории presale не получает — аудитория берётся из Scope.
	if _, err := f.svc.Items(f.ctx, presale, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Items для sales-safe: ожидался ErrForbidden, получено %v", err)
	}
	if _, err := f.svc.DateHistory(f.ctx, presale, public.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("DateHistory для sales-safe: ожидался ErrForbidden, получено %v", err)
	}
	if _, err := f.svc.DateHistory(f.ctx, presale, internal.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("DateHistory внутреннего элемента для sales-safe: ожидался ErrForbidden, получено %v", err)
	}
}

func TestRM02_InternalAudienceSeesAll(t *testing.T) {
	f := newFixture(t)
	f.item(f.edr, "internal", roadmap.BucketNow, d(2026, 10, 1), d(2026, 11, 1), authz.AudienceInternal)
	f.item(f.edr, "public", roadmap.BucketNow, d(2026, 11, 1), d(2026, 12, 1), authz.AudienceSalesSafe)

	tl, err := f.svc.Timeline(f.ctx, f.cpo, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Audience != authz.AudienceInternal || len(tl.Items) != 2 || tl.SalesSafe != nil {
		t.Fatalf("внутренняя аудитория видит всё: %+v", tl)
	}
	items, err := f.svc.Items(f.ctx, f.cpo, f.edr)
	if err != nil || len(items) != 2 {
		t.Fatalf("Items: %v, %d", err, len(items))
	}
	if items[0].Status != roadmap.ItemPlanned {
		t.Fatalf("внутренний статус должен присутствовать: %+v", items[0])
	}
}

func TestRM03_ChangeRequiresReason(t *testing.T) {
	f := newFixture(t)
	it := f.item(f.edr, "x", roadmap.BucketNow, d(2026, 10, 1), d(2026, 11, 1), authz.AudienceInternal)
	for _, reason := range []string{"", "   "} {
		if _, err := f.svc.ChangeDates(f.ctx, f.cpo, it.ID, d(2026, 10, 1), d(2026, 12, 1), reason); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("пустая причина %q: ожидался ErrValidation, получено %v", reason, err)
		}
	}
	hist, err := f.svc.DateHistory(f.ctx, f.cpo, it.ID)
	if err != nil || len(hist) != 0 {
		t.Fatalf("история не должна пополняться при отказе: %v, %d", err, len(hist))
	}
	// даты через UpdateItem не меняются
	if _, err := f.svc.UpdateItem(f.ctx, f.cpo, it.ID, roadmap.ItemInput{Title: "x", Bucket: roadmap.BucketNow, StartDate: d(2026, 10, 1), EndDate: d(2027, 1, 1), Audience: authz.AudienceInternal, Status: roadmap.ItemPlanned}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("UpdateItem с новыми датами: ожидался ErrValidation, получено %v", err)
	}
}

func TestRM03_HistoryRecorded(t *testing.T) {
	f := newFixture(t)
	it := f.item(f.edr, "x", roadmap.BucketNow, d(2026, 10, 1), d(2026, 11, 1), authz.AudienceInternal)
	upd, err := f.svc.ChangeDates(f.ctx, f.cpo, it.ID, d(2026, 10, 15), d(2026, 12, 1), "смещение зависимости")
	if err != nil {
		t.Fatal(err)
	}
	if upd.StartDate != d(2026, 10, 15) || upd.EndDate != d(2026, 12, 1) {
		t.Fatalf("даты не применились: %+v", upd)
	}
	hist, err := f.svc.DateHistory(f.ctx, f.cpo, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("ожидалась одна запись, получено %d", len(hist))
	}
	h := hist[0]
	if h.OldStart != d(2026, 10, 1) || h.OldEnd != d(2026, 11, 1) || h.NewStart != d(2026, 10, 15) || h.NewEnd != d(2026, 12, 1) ||
		h.Reason != "смещение зависимости" || h.Actor != "cpo" || !h.At.Equal(f.clock.T) || h.ItemID != it.ID {
		t.Fatalf("запись истории: %+v", h)
	}
	var seen bool
	for _, ev := range f.pub.events {
		if ev.Type == roadmap.EventDatesChanged && ev.AggregateID == it.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("событие roadmap.dates.changed не опубликовано")
	}
}

func TestRM03_ShiftEventRecordsHistoryIdempotently(t *testing.T) {
	f := newFixture(t)
	srcFeature, depFeature := kernel.NewID(), kernel.NewID()
	src, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "Коннектор", Bucket: roadmap.BucketNow, FeatureID: srcFeature,
		StartDate: d(2026, 10, 1), EndDate: d(2026, 11, 1)})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := f.svc.CreateItem(f.ctx, f.cpo, f.vm, roadmap.ItemInput{Title: "Response API", Bucket: roadmap.BucketNext, FeatureID: depFeature,
		StartDate: d(2026, 11, 1), EndDate: d(2026, 12, 1)})
	if err != nil {
		t.Fatal(err)
	}
	unrelated := f.item(f.edr, "unrelated", roadmap.BucketLater, kernel.Date{}, kernel.Date{}, authz.AudienceInternal)

	payload := struct {
		pg.ShiftResult
		Reason string `json:"reason"`
	}{pg.ShiftResult{SourceFeature: srcFeature, OldDate: d(2026, 11, 1), NewDate: d(2026, 11, 22),
		Affected: []pg.AffectedFeature{{FeatureID: depFeature, ProductID: f.vm, PlannedDate: d(2026, 12, 1), ImpliedDate: d(2026, 12, 22), ViaFeature: srcFeature, Depth: 1}}},
		"эпик сдвинут в Jira на 21 день"}
	ev, err := kernel.NewEvent(f.clock, pg.EventDateShifted, srcFeature, f.edr, "jira", payload)
	if err != nil {
		t.Fatal(err)
	}
	h := roadmap.NewShiftHandler(f.svc, serviceScope())
	for i := 0; i < 3; i++ { // повторная доставка
		if err := h.Handle(f.ctx, ev); err != nil {
			t.Fatalf("доставка %d: %v", i, err)
		}
	}

	got, err := f.svc.Items(f.ctx, f.cpo, f.edr)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range got {
		if it.ID == src.ID && it.EndDate != d(2026, 11, 22) {
			t.Fatalf("EndDate источника не сдвинут: %+v", it)
		}
	}
	srcHist, err := f.svc.DateHistory(f.ctx, f.cpo, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcHist) != 1 || srcHist[0].Reason != "эпик сдвинут в Jira на 21 день" || srcHist[0].NewEnd != d(2026, 11, 22) || srcHist[0].EventID != ev.ID {
		t.Fatalf("история источника: %+v", srcHist)
	}
	depHist, err := f.svc.DateHistory(f.ctx, f.cpo, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(depHist) != 1 || depHist[0].Reason != "затронуто сдвигом фичи "+srcFeature.String()+": эпик сдвинут в Jira на 21 день" {
		t.Fatalf("история затронутого: %+v", depHist)
	}
	if hist, _ := f.svc.DateHistory(f.ctx, f.cpo, unrelated.ID); len(hist) != 0 {
		t.Fatalf("непривязанный элемент не должен получать историю: %+v", hist)
	}
	// чужие события пропускаются
	other := ev
	other.ID, other.Type = kernel.NewID(), pg.EventFeatureCreated
	if err := h.Handle(f.ctx, other); err != nil {
		t.Fatal(err)
	}
	if hist, _ := f.svc.DateHistory(f.ctx, f.cpo, src.ID); len(hist) != 1 {
		t.Fatalf("чужое событие изменило историю: %+v", hist)
	}
}

func TestRM_ABAC_PMOfVMCannotWriteEDRRoadmap(t *testing.T) {
	f := newFixture(t)
	pmVM := pmScope("pm-vm", f.vm)
	it := f.item(f.edr, "x", roadmap.BucketNow, d(2026, 10, 1), d(2026, 11, 1), authz.AudienceInternal)
	if _, err := f.svc.CreateItem(f.ctx, pmVM, f.edr, roadmap.ItemInput{Title: "y", Bucket: roadmap.BucketNow}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("CreateItem: ожидался ErrForbidden, получено %v", err)
	}
	if _, err := f.svc.ChangeDates(f.ctx, pmVM, it.ID, d(2026, 10, 1), d(2027, 1, 1), "причина"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ChangeDates: ожидался ErrForbidden, получено %v", err)
	}
	if _, err := f.svc.CreateRelease(f.ctx, pmVM, f.edr, roadmap.ReleaseInput{Name: "r", Version: "1"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("CreateRelease: ожидался ErrForbidden, получено %v", err)
	}
	// PM VM не имеет и стратегического доступа к EDR — чтение тоже запрещено
	if _, err := f.svc.Timeline(f.ctx, pmVM, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("Timeline: ожидался ErrForbidden, получено %v", err)
	}
	// свой продукт — можно
	if _, err := f.svc.CreateItem(f.ctx, pmVM, f.vm, roadmap.ItemInput{Title: "y", Bucket: roadmap.BucketNow}); err != nil {
		t.Fatalf("CreateItem в своём продукте: %v", err)
	}
	// нулевой Scope запрещает всё
	var zero authz.Scope
	if _, err := f.svc.Timeline(f.ctx, zero, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope Timeline: %v", err)
	}
	if _, err := f.svc.DateHistory(f.ctx, zero, it.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope DateHistory: %v", err)
	}
	if err := roadmap.NewShiftHandler(f.svc, zero).Handle(f.ctx, kernel.Event{ID: kernel.NewID(), Type: pg.EventDateShifted}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope обработчика: %v", err)
	}
}

// TestRM06_LaunchCalendar: уровни запуска попадают в календарь, период фильтруется,
// sales-safe аудитория видит только свой срез.
func TestRM06_LaunchCalendar(t *testing.T) {
	f := newFixture(t)
	mustItem := func(title string, audience authz.Audience, tier roadmap.LaunchTier, launch kernel.Date) roadmap.RoadmapItem {
		t.Helper()
		it, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{
			Title: title, Bucket: roadmap.BucketNow, Audience: audience, Status: roadmap.ItemPlanned,
			StartDate: d(2026, 9, 1), EndDate: d(2026, 11, 1), LaunchTier: tier, LaunchDate: launch})
		if err != nil {
			t.Fatalf("элемент %q: %v", title, err)
		}
		return it
	}
	mustItem("Запуск 4.0", authz.AudienceSalesSafe, roadmap.LaunchTier1, d(2026, 11, 10))
	mustItem("Внутренний рефакторинг", authz.AudienceInternal, roadmap.LaunchTier3, d(2026, 11, 20))
	mustItem("Запуск в следующем году", authz.AudienceSalesSafe, roadmap.LaunchTier2, d(2027, 2, 1))
	mustItem("Без запуска", authz.AudienceInternal, roadmap.LaunchNone, kernel.Date{})

	cal, err := f.svc.LaunchCalendar(f.ctx, f.cpo, f.edr, d(2026, 10, 1), d(2026, 12, 31))
	if err != nil {
		t.Fatalf("календарь: %v", err)
	}
	if len(cal.Entries) != 2 {
		t.Fatalf("записей календаря %d, ожидалось 2: %+v", len(cal.Entries), cal.Entries)
	}
	if cal.Entries[0].Title != "Запуск 4.0" || cal.Entries[0].Tier != roadmap.LaunchTier1 {
		t.Fatalf("первая запись: %+v", cal.Entries[0])
	}
	salesSafe, err := f.svc.LaunchCalendar(f.ctx, presaleScope(), f.edr, kernel.Date{}, kernel.Date{})
	if err != nil {
		t.Fatalf("sales-safe календарь: %v", err)
	}
	for _, e := range salesSafe.Entries {
		if e.Audience != authz.AudienceSalesSafe {
			t.Fatalf("во внешнем календаре внутренний запуск: %+v", e)
		}
	}
	if len(salesSafe.Entries) != 2 {
		t.Fatalf("sales-safe записей %d, ожидалось 2", len(salesSafe.Entries))
	}
	var zero authz.Scope
	if _, err := f.svc.LaunchCalendar(f.ctx, zero, f.edr, kernel.Date{}, kernel.Date{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
}

// TestRM06_LaunchTierRequiresDate: уровень запуска без даты запуска не сохраняется.
func TestRM06_LaunchTierRequiresDate(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{
		Title: "Запуск без даты", Bucket: roadmap.BucketNow, Audience: authz.AudienceInternal,
		Status: roadmap.ItemPlanned, LaunchTier: roadmap.LaunchTier1})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
}
