package commitments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/commitments"
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

func (m *memPub) count(typ string) int {
	n := 0
	for _, e := range m.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// memWriter — реализация порта RoadmapWriter в памяти: один элемент на обязательство.
type memWriter struct {
	items map[kernel.ID]kernel.ID
	calls []renewalCall
}

type renewalCall struct {
	productID, commitmentID kernel.ID
	title                   string
	start, end              kernel.Date
}

func (w *memWriter) EnsureRenewalItem(_ context.Context, sc authz.Scope, productID, commitmentID kernel.ID, title string, start, end kernel.Date) (kernel.ID, error) {
	if !sc.Valid() {
		return kernel.NilID, kernel.ErrForbidden
	}
	w.calls = append(w.calls, renewalCall{productID, commitmentID, title, start, end})
	if id, ok := w.items[commitmentID]; ok {
		return id, nil
	}
	id := kernel.NewID()
	w.items[commitmentID] = id
	return id, nil
}

// memReader — реализация порта RoadmapReader: привязки элементов roadmap.
type memReader struct{ links map[kernel.ID][2]kernel.ID }

func (r *memReader) ItemLinks(_ context.Context, _ authz.Scope, itemID kernel.ID) (kernel.ID, kernel.ID, error) {
	l, ok := r.links[itemID]
	if !ok {
		return kernel.NilID, kernel.NilID, kernel.NotFound("roadmap item", itemID)
	}
	return l[0], l[1], nil
}

var (
	vm  = kernel.NewID()
	edr = kernel.NewID()
	now = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
)

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func adminScope() authz.Scope {
	return authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func serviceScope() authz.Scope {
	return authz.New(authz.Params{Subject: "service", Roles: []authz.Role{authz.RoleService}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(subject string, private map[kernel.ID]authz.Access) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM}, Products: private, Audience: authz.AudienceInternal})
}

func presaleScope() authz.Scope {
	return authz.New(authz.Params{Subject: "presale", Roles: []authz.Role{authz.RolePresale}, AllProducts: authz.AccessStrategic, Audience: authz.AudienceSalesSafe})
}

type fixture struct {
	t      *testing.T
	ctx    context.Context
	svc    *commitments.Service
	pub    *memPub
	writer *memWriter
	reader *memReader
	cpo    authz.Scope
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub := &memPub{}
	f := &fixture{t: t, ctx: context.Background(), pub: pub, cpo: cpoScope(),
		writer: &memWriter{items: map[kernel.ID]kernel.ID{}}, reader: &memReader{links: map[kernel.ID][2]kernel.ID{}}}
	f.svc = commitments.NewService(commitments.NewMemStore(), pub, kernel.FixedClock{T: now}).
		WithRoadmapWriter(f.writer).WithRoadmapReader(f.reader)
	return f
}

func customerInput(due kernel.Date, featureID kernel.ID) commitments.Input {
	return commitments.Input{Kind: commitments.KindCustomer, Counterparty: "acc-001", Subject: "Экспорт отчётов в SIEM",
		DueDate: due, Basis: "договор Д-001", Owner: "pm-vm", FeatureID: featureID}
}

func certInput(due kernel.Date) commitments.Input {
	return commitments.Input{Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeCertificateExpiry, Counterparty: "регулятор",
		Subject: "Сертификат № С-100", DueDate: due, Basis: "сертификат № С-100", Owner: "compliance"}
}

func (f *fixture) create(sc authz.Scope, product kernel.ID, in commitments.Input) commitments.Commitment {
	f.t.Helper()
	c, err := f.svc.Create(f.ctx, sc, product, in)
	if err != nil {
		f.t.Fatalf("create: %v", err)
	}
	return c
}

// TestCT01_CustomerRegistry — реестр клиентских обязательств: создание, чтение, изменение,
// исполнение, отмена, фильтры.
func TestCT01_CustomerRegistry(t *testing.T) {
	f := newFixture(t)
	feat := kernel.NewID()
	c := f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 12, 31), feat))
	if c.Status != commitments.StatusActive || c.ProductID != vm || c.CreatedBy != "cpo" {
		t.Fatalf("unexpected commitment: %+v", c)
	}
	if f.pub.count(commitments.EventCommitmentCreated) != 1 {
		t.Fatalf("expected created event")
	}
	got, err := f.svc.Get(f.ctx, f.cpo, c.ID)
	if err != nil || got.ID != c.ID {
		t.Fatalf("get: %v", err)
	}

	in := customerInput(kernel.DateOf(2027, 1, 31), feat)
	in.Owner = "pm-vm-2"
	upd, err := f.svc.Update(f.ctx, f.cpo, c.ID, in)
	if err != nil || upd.Owner != "pm-vm-2" || upd.DueDate != kernel.DateOf(2027, 1, 31) {
		t.Fatalf("update: %v %+v", err, upd)
	}

	later := f.create(f.cpo, vm, customerInput(kernel.DateOf(2027, 6, 30), kernel.NilID))
	f.create(f.cpo, edr, customerInput(kernel.DateOf(2026, 10, 1), kernel.NilID))

	tests := []struct {
		name   string
		filter commitments.Filter
		want   int
	}{
		{"по продукту", commitments.Filter{ProductID: vm}, 2},
		{"по виду", commitments.Filter{Kind: commitments.KindCustomer}, 3},
		{"по сроку", commitments.Filter{DueBefore: kernel.DateOf(2027, 1, 31)}, 2},
		{"по фиче", commitments.Filter{FeatureID: feat}, 1},
		{"по статусу", commitments.Filter{Statuses: []commitments.Status{commitments.StatusFulfilled}}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list, err := f.svc.List(f.ctx, f.cpo, tc.filter)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(list) != tc.want {
				t.Fatalf("want %d, got %d", tc.want, len(list))
			}
		})
	}

	if _, err := f.svc.Fulfil(f.ctx, f.cpo, c.ID); err != nil {
		t.Fatalf("fulfil: %v", err)
	}
	if _, err := f.svc.Fulfil(f.ctx, f.cpo, c.ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("second fulfil: want ErrConflict, got %v", err)
	}
	if _, err := f.svc.Update(f.ctx, f.cpo, c.ID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("update fulfilled: want ErrConflict, got %v", err)
	}
	if cc, err := f.svc.Cancel(f.ctx, f.cpo, later.ID); err != nil || cc.Status != commitments.StatusCancelled {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := f.svc.Get(f.ctx, f.cpo, kernel.NewID()); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
}

// TestCT01_Validation — обязательные поля и согласованность вида и подтипа.
func TestCT01_Validation(t *testing.T) {
	f := newFixture(t)
	due := kernel.DateOf(2026, 12, 31)
	tests := []struct {
		name    string
		product kernel.ID
		mutate  func(*commitments.Input)
	}{
		{"без продукта", kernel.NilID, func(*commitments.Input) {}},
		{"неизвестный вид", vm, func(in *commitments.Input) { in.Kind = "other" }},
		{"customer с подтипом", vm, func(in *commitments.Input) { in.Subtype = commitments.SubtypeSupportEnd }},
		{"без контрагента", vm, func(in *commitments.Input) { in.Counterparty = " " }},
		{"без предмета", vm, func(in *commitments.Input) { in.Subject = "" }},
		{"без срока", vm, func(in *commitments.Input) { in.DueDate = kernel.Date{} }},
		{"без основания", vm, func(in *commitments.Input) { in.Basis = "" }},
		{"без владельца", vm, func(in *commitments.Input) { in.Owner = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := customerInput(due, kernel.NilID)
			tc.mutate(&in)
			if _, err := f.svc.Create(f.ctx, f.cpo, tc.product, in); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
		})
	}
}

// TestCT02_RegulatorySubtypes — регуляторные обязательства требуют известный подтип.
func TestCT02_RegulatorySubtypes(t *testing.T) {
	f := newFixture(t)
	due := kernel.DateOf(2027, 3, 1)
	tests := []struct {
		name    string
		subtype commitments.Subtype
		wantErr bool
	}{
		{"срок сертификата", commitments.SubtypeCertificateExpiry, false},
		{"срок техподдержки", commitments.SubtypeSupportEnd, false},
		{"срок устранения уязвимости", commitments.SubtypeVulnFixDeadline, false},
		{"без подтипа", "", true},
		{"неизвестный подтип", "audit", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := certInput(due)
			in.Subtype = tc.subtype
			c, err := f.svc.Create(f.ctx, f.cpo, vm, in)
			if tc.wantErr {
				if !errors.Is(err, kernel.ErrValidation) {
					t.Fatalf("want ErrValidation, got %v", err)
				}
				return
			}
			if err != nil || c.Kind != commitments.KindRegulatory || c.Subtype != tc.subtype {
				t.Fatalf("create: %v %+v", err, c)
			}
		})
	}
	list, err := f.svc.List(f.ctx, f.cpo, commitments.Filter{Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeSupportEnd})
	if err != nil || len(list) != 1 {
		t.Fatalf("filter by subtype: %v, %d", err, len(list))
	}
}

// TestCT03_AffectedCommitments — порт portfoliograph.CommitmentChecker: обязательства
// затронутых фич со сроком раньше implied_date.
func TestCT03_AffectedCommitments(t *testing.T) {
	f := newFixture(t)
	feat := kernel.NewID()
	other := kernel.NewID()
	early := f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), feat))
	f.create(f.cpo, vm, customerInput(kernel.DateOf(2027, 6, 1), feat))
	cancelled := f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 10, 1), feat))
	if _, err := f.svc.Cancel(f.ctx, f.cpo, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 10, 1), other))

	tests := []struct {
		name     string
		affected []pg.AffectedFeature
		want     []kernel.ID
	}{
		{"срок раньше implied_date", []pg.AffectedFeature{{FeatureID: feat, ProductID: vm, ImpliedDate: kernel.DateOf(2026, 12, 1)}}, []kernel.ID{early.ID}},
		{"implied_date раньше срока", []pg.AffectedFeature{{FeatureID: feat, ProductID: vm, ImpliedDate: kernel.DateOf(2026, 10, 15)}}, nil},
		{"без implied_date", []pg.AffectedFeature{{FeatureID: feat, ProductID: vm}}, nil},
		{"чужая фича", []pg.AffectedFeature{{FeatureID: kernel.NewID(), ProductID: vm, ImpliedDate: kernel.DateOf(2027, 12, 1)}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.svc.AffectedCommitments(f.ctx, tc.affected, nil)
			if err != nil {
				t.Fatalf("affected: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("want %v, got %v", tc.want, got)
				}
			}
		})
	}
}

func shiftEvent(t *testing.T, source kernel.ID, newDate kernel.Date, affected []pg.AffectedFeature) kernel.Event {
	t.Helper()
	payload := struct {
		pg.ShiftResult
		Reason string `json:"reason"`
	}{ShiftResult: pg.ShiftResult{SourceFeature: source, OldDate: kernel.DateOf(2026, 10, 1), NewDate: newDate, Affected: affected}, Reason: "задержка поставщика"}
	ev, err := kernel.NewEvent(kernel.FixedClock{T: now}, pg.EventDateShifted, source, vm, "cpo", payload)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// TestCT03_AlertOnFeatureShift — алерт при сдвиге фичи, нарушающем срок обязательства;
// событие commitments.alert.raised; подтверждение алерта.
func TestCT03_AlertOnFeatureShift(t *testing.T) {
	f := newFixture(t)
	src := kernel.NewID()
	dep := kernel.NewID()
	breached := f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), src))
	f.create(f.cpo, vm, customerInput(kernel.DateOf(2027, 6, 1), src)) // срок позже новой даты — без алерта
	depBreached := f.create(f.cpo, edr, customerInput(kernel.DateOf(2026, 11, 15), dep))

	h := commitments.NewShiftHandler(f.svc, serviceScope())
	ev := shiftEvent(t, src, kernel.DateOf(2026, 12, 1), []pg.AffectedFeature{{FeatureID: dep, ProductID: edr, ImpliedDate: kernel.DateOf(2026, 12, 15)}})
	if err := h.Handle(f.ctx, ev); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := f.pub.count(commitments.EventAlertRaised); got != 2 {
		t.Fatalf("want 2 alert events, got %d", got)
	}

	alerts, err := f.svc.Alerts(f.ctx, f.cpo, vm, true)
	if err != nil || len(alerts) != 1 {
		t.Fatalf("alerts vm: %v, %d", err, len(alerts))
	}
	a := alerts[0]
	if a.CommitmentID != breached.ID || a.Kind != commitments.AlertRoadmapShift || a.EventID != ev.ID ||
		a.NewDate != kernel.DateOf(2026, 12, 1) || a.DueDate != kernel.DateOf(2026, 11, 1) || a.Acknowledged {
		t.Fatalf("unexpected alert: %+v", a)
	}
	if !strings.Contains(a.Message, "задержка поставщика") {
		t.Fatalf("message without reason: %q", a.Message)
	}
	edrAlerts, err := f.svc.Alerts(f.ctx, f.cpo, edr, true)
	if err != nil || len(edrAlerts) != 1 || edrAlerts[0].CommitmentID != depBreached.ID || edrAlerts[0].NewDate != kernel.DateOf(2026, 12, 15) {
		t.Fatalf("alerts edr: %v, %+v", err, edrAlerts)
	}

	ack, err := f.svc.AcknowledgeAlert(f.ctx, f.cpo, a.ID)
	if err != nil || !ack.Acknowledged || ack.AcknowledgedBy != "cpo" {
		t.Fatalf("acknowledge: %v %+v", err, ack)
	}
	if open, _ := f.svc.Alerts(f.ctx, f.cpo, vm, true); len(open) != 0 {
		t.Fatalf("acknowledged alert still open")
	}
	if all, _ := f.svc.Alerts(f.ctx, f.cpo, vm, false); len(all) != 1 {
		t.Fatalf("alert list is append-only, want 1, got %d", len(all))
	}
	if f.pub.count(commitments.EventAlertAcknowledged) != 1 {
		t.Fatalf("expected acknowledged event")
	}
}

// TestCT03_AlertOnRoadmapDatesChanged — алерт при сдвиге элемента roadmap, привязанного к фиче
// или релизу обязательства.
func TestCT03_AlertOnRoadmapDatesChanged(t *testing.T) {
	f := newFixture(t)
	feat, rel := kernel.NewID(), kernel.NewID()
	byFeature := f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), feat))
	in := certInput(kernel.DateOf(2026, 11, 20))
	in.ReleaseID = rel
	byRelease := f.create(f.cpo, vm, in)
	item := kernel.NewID()
	f.reader.links[item] = [2]kernel.ID{feat, rel}
	h := commitments.NewShiftHandler(f.svc, serviceScope())

	change := func(itemID kernel.ID, oldEnd, newEnd kernel.Date) kernel.Event {
		ch := roadmap.DateChange{ID: kernel.NewID(), ItemID: itemID, ProductID: vm, OldEnd: oldEnd, NewEnd: newEnd, Reason: "перенос релиза", Actor: "cpo", At: now}
		ev, err := kernel.NewEvent(kernel.FixedClock{T: now}, roadmap.EventDatesChanged, itemID, vm, "cpo", ch)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	tests := []struct {
		name string
		ev   kernel.Event
		want int
	}{
		{"сдвиг вперёд нарушает оба", change(item, kernel.DateOf(2026, 10, 1), kernel.DateOf(2026, 12, 1)), 2},
		{"сдвиг назад", change(item, kernel.DateOf(2026, 12, 1), kernel.DateOf(2026, 10, 1)), 0},
		{"элемент без привязок", change(func() kernel.ID { id := kernel.NewID(); f.reader.links[id] = [2]kernel.ID{}; return id }(), kernel.DateOf(2026, 10, 1), kernel.DateOf(2027, 1, 1)), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := f.pub.count(commitments.EventAlertRaised)
			if err := h.Handle(f.ctx, tc.ev); err != nil {
				t.Fatalf("handle: %v", err)
			}
			if got := f.pub.count(commitments.EventAlertRaised) - before; got != tc.want {
				t.Fatalf("want %d alerts, got %d", tc.want, got)
			}
		})
	}
	alerts, _ := f.svc.Alerts(f.ctx, f.cpo, vm, false)
	seen := map[kernel.ID]bool{}
	for _, a := range alerts {
		seen[a.CommitmentID] = true
	}
	if !seen[byFeature.ID] || !seen[byRelease.ID] {
		t.Fatalf("alerts for feature and release commitments expected, got %+v", alerts)
	}
}

// TestCT03_HandlerIdempotent — повторная доставка события не дублирует алерты; чужие типы пропускаются;
// нулевой Scope запрещает всё.
func TestCT03_HandlerIdempotent(t *testing.T) {
	f := newFixture(t)
	src := kernel.NewID()
	f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), src))
	h := commitments.NewShiftHandler(f.svc, serviceScope())
	ev := shiftEvent(t, src, kernel.DateOf(2026, 12, 1), nil)
	for i := 0; i < 3; i++ {
		if err := h.Handle(f.ctx, ev); err != nil {
			t.Fatalf("handle #%d: %v", i, err)
		}
	}
	if alerts, _ := f.svc.Alerts(f.ctx, f.cpo, vm, false); len(alerts) != 1 {
		t.Fatalf("want 1 alert after redelivery, got %d", len(alerts))
	}
	if f.pub.count(commitments.EventAlertRaised) != 1 {
		t.Fatalf("want 1 raised event")
	}

	other := ev
	other.ID, other.Type = kernel.NewID(), "signals.signal.ingested"
	if err := h.Handle(f.ctx, other); err != nil {
		t.Fatalf("foreign event: %v", err)
	}
	if err := commitments.NewShiftHandler(f.svc, authz.Scope{}).Handle(f.ctx, shiftEvent(t, src, kernel.DateOf(2027, 1, 1), nil)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope: want ErrForbidden, got %v", err)
	}
	bad := ev
	bad.ID, bad.Payload = kernel.NewID(), []byte("{")
	if err := h.Handle(f.ctx, bad); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("bad payload: want ErrValidation, got %v", err)
	}
}

// TestCT04_EnsureRenewals — элемент roadmap на продление сертификата за LeadMonths до истечения,
// один раз на обязательство; настройка срока через UpdateSettings.
func TestCT04_EnsureRenewals(t *testing.T) {
	today := kernel.DateOf(2026, 9, 17)
	tests := []struct {
		name       string
		leadMonths int // 0 — по умолчанию
		due        kernel.Date
		wantItem   bool
	}{
		{"в пределах 18 месяцев", 0, kernel.DateOf(2028, 3, 1), true},
		{"ровно на горизонте", 0, kernel.DateOf(2028, 3, 17), true},
		{"за горизонтом", 0, kernel.DateOf(2028, 3, 18), false},
		{"горизонт сокращён до 6 месяцев", 6, kernel.DateOf(2027, 6, 1), false},
		{"в пределах 6 месяцев", 6, kernel.DateOf(2027, 3, 1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.leadMonths > 0 {
				if err := f.svc.UpdateSettings(f.ctx, adminScope(), commitments.Settings{LeadMonths: tc.leadMonths}); err != nil {
					t.Fatalf("settings: %v", err)
				}
			}
			c := f.create(f.cpo, vm, certInput(tc.due))
			f.create(f.cpo, vm, customerInput(tc.due, kernel.NilID)) // клиентское — не продлевается
			created, err := f.svc.EnsureRenewals(f.ctx, serviceScope(), today)
			if err != nil {
				t.Fatalf("ensure: %v", err)
			}
			if !tc.wantItem {
				if len(created) != 0 || len(f.writer.calls) != 0 {
					t.Fatalf("unexpected renewal: %+v", f.writer.calls)
				}
				return
			}
			if len(created) != 1 || created[0].ID != c.ID || created[0].RenewalItemID == kernel.NilID {
				t.Fatalf("want renewal for %s, got %+v", c.ID, created)
			}
			call := f.writer.calls[0]
			if call.productID != vm || call.commitmentID != c.ID || call.title != "Продление сертификата: Сертификат № С-100" ||
				call.start != today || call.end != tc.due.AddDays(-1) {
				t.Fatalf("unexpected call: %+v", call)
			}
			got, _ := f.svc.Get(f.ctx, f.cpo, c.ID)
			if got.RenewalItemID != created[0].RenewalItemID {
				t.Fatalf("renewal item id not persisted")
			}
			if f.pub.count(commitments.EventRenewalPlanned) != 1 {
				t.Fatalf("expected renewal event")
			}
			// Повторный вызов — без нового элемента.
			again, err := f.svc.EnsureRenewals(f.ctx, serviceScope(), today.AddDays(30))
			if err != nil || len(again) != 0 || len(f.writer.calls) != 1 {
				t.Fatalf("second ensure: %v, created %d, calls %d", err, len(again), len(f.writer.calls))
			}
		})
	}
}

// TestCT04_Settings — настройки: значение по умолчанию, валидация, только admin.
func TestCT04_Settings(t *testing.T) {
	f := newFixture(t)
	st, err := f.svc.Settings(f.ctx)
	if err != nil || st.LeadMonths != commitments.DefaultLeadMonths {
		t.Fatalf("default settings: %v %+v", err, st)
	}
	if err := f.svc.UpdateSettings(f.ctx, f.cpo, commitments.Settings{LeadMonths: 6}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("cpo settings: want ErrForbidden, got %v", err)
	}
	if err := f.svc.UpdateSettings(f.ctx, adminScope(), commitments.Settings{LeadMonths: 0}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("zero lead: want ErrValidation, got %v", err)
	}
	if err := f.svc.UpdateSettings(f.ctx, adminScope(), commitments.Settings{LeadMonths: 12}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if st, _ = f.svc.Settings(f.ctx); st.LeadMonths != 12 {
		t.Fatalf("want 12, got %d", st.LeadMonths)
	}
	// Без порта RoadmapWriter продление недоступно.
	bare := commitments.NewService(commitments.NewMemStore(), f.pub, kernel.FixedClock{T: now})
	if _, err := bare.EnsureRenewals(f.ctx, serviceScope(), kernel.DateOf(2026, 9, 17)); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("no writer: want ErrUnavailable, got %v", err)
	}
}

// TestCT01_ABACDenied — тест отказа: PM продукта VM не может писать обязательства EDR;
// presale видит обязательства (стратегический срез), но не пишет; нулевой Scope запрещает всё.
func TestCT01_ABACDenied(t *testing.T) {
	f := newFixture(t)
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{vm: authz.AccessPrivate, edr: authz.AccessStrategic})
	presale := presaleScope()
	due := kernel.DateOf(2026, 12, 31)

	own := f.create(pmVM, vm, customerInput(due, kernel.NilID))
	foreign := f.create(f.cpo, edr, customerInput(due, kernel.NilID))
	h := commitments.NewShiftHandler(f.svc, serviceScope())
	if err := h.Handle(f.ctx, shiftEvent(t, kernel.NewID(), kernel.DateOf(2027, 1, 1), nil)); err != nil {
		t.Fatal(err)
	}
	src := kernel.NewID()
	f.create(f.cpo, edr, customerInput(kernel.DateOf(2026, 10, 1), src))
	if err := h.Handle(f.ctx, shiftEvent(t, src, kernel.DateOf(2027, 1, 1), nil)); err != nil {
		t.Fatal(err)
	}
	edrAlerts, _ := f.svc.Alerts(f.ctx, f.cpo, edr, true)
	if len(edrAlerts) != 1 {
		t.Fatalf("setup: want 1 edr alert, got %d", len(edrAlerts))
	}

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"pm VM создаёт в EDR", func() error { _, err := f.svc.Create(f.ctx, pmVM, edr, customerInput(due, kernel.NilID)); return err }, kernel.ErrForbidden},
		{"pm VM меняет EDR", func() error {
			_, err := f.svc.Update(f.ctx, pmVM, foreign.ID, customerInput(due, kernel.NilID))
			return err
		}, kernel.ErrForbidden},
		{"pm VM исполняет EDR", func() error { _, err := f.svc.Fulfil(f.ctx, pmVM, foreign.ID); return err }, kernel.ErrForbidden},
		{"pm VM отменяет EDR", func() error { _, err := f.svc.Cancel(f.ctx, pmVM, foreign.ID); return err }, kernel.ErrForbidden},
		{"pm VM подтверждает алерт EDR", func() error { _, err := f.svc.AcknowledgeAlert(f.ctx, pmVM, edrAlerts[0].ID); return err }, kernel.ErrForbidden},
		{"pm VM читает EDR (стратегический доступ)", func() error { _, err := f.svc.Get(f.ctx, pmVM, foreign.ID); return err }, nil},
		{"presale читает VM", func() error { _, err := f.svc.Get(f.ctx, presale, own.ID); return err }, nil},
		{"presale читает алерты EDR", func() error { _, err := f.svc.Alerts(f.ctx, presale, edr, false); return err }, nil},
		{"presale создаёт", func() error { _, err := f.svc.Create(f.ctx, presale, vm, customerInput(due, kernel.NilID)); return err }, kernel.ErrForbidden},
		{"presale подтверждает алерт", func() error { _, err := f.svc.AcknowledgeAlert(f.ctx, presale, edrAlerts[0].ID); return err }, kernel.ErrForbidden},
		{"presale меняет настройки", func() error { return f.svc.UpdateSettings(f.ctx, presale, commitments.Settings{LeadMonths: 1}) }, kernel.ErrForbidden},
		{"нулевой Scope читает", func() error { _, err := f.svc.Get(f.ctx, authz.Scope{}, own.ID); return err }, kernel.ErrForbidden},
		{"нулевой Scope перечисляет", func() error { _, err := f.svc.List(f.ctx, authz.Scope{}, commitments.Filter{}); return err }, kernel.ErrForbidden},
		{"нулевой Scope продлевает", func() error { _, err := f.svc.EnsureRenewals(f.ctx, authz.Scope{}, due); return err }, kernel.ErrForbidden},
		{"нет доступа к продукту", func() error {
			_, err := f.svc.List(f.ctx, pmScope("pm-soar", map[kernel.ID]authz.Access{}), commitments.Filter{ProductID: vm})
			return err
		}, kernel.ErrForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if tc.want == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}

	// Список без продукта отдаёт только доступные продукты; presale — все.
	all, err := f.svc.List(f.ctx, presale, commitments.Filter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("presale list: %v, %d", err, len(all))
	}
	mine, err := f.svc.List(f.ctx, pmScope("pm-only-vm", map[kernel.ID]authz.Access{vm: authz.AccessPrivate}), commitments.Filter{})
	if err != nil || len(mine) != 1 || mine[0].ID != own.ID {
		t.Fatalf("pm list: %v, %+v", err, mine)
	}
	// EnsureRenewals пропускает продукты без права записи.
	f.create(f.cpo, edr, certInput(kernel.DateOf(2027, 1, 1)))
	created, err := f.svc.EnsureRenewals(f.ctx, pmVM, kernel.DateOf(2026, 9, 17))
	if err != nil || len(created) != 0 || len(f.writer.calls) != 0 {
		t.Fatalf("pm VM renewals for EDR: %v, %d", err, len(created))
	}
}

// failingAppendStore — хранилище, у которого AppendAlert падает на заданном по счёту вызове:
// имитирует сбой в середине обработки события (дефект 9).
type failingAppendStore struct {
	*commitments.MemStore
	calls  int
	failOn int
}

func (s *failingAppendStore) AppendAlert(ctx context.Context, a commitments.Alert) error {
	s.calls++
	if s.calls == s.failOn {
		return errors.New("сбой хранилища алертов")
	}
	return s.MemStore.AppendAlert(ctx, a)
}

// TestCT03_AlertNotDuplicatedOnEventRetry — отметка обработанного события ставится только в конце
// обработчика, поэтому сбой в середине приводит к повторной доставке. Алерты дедуплицируются по паре
// «обязательство + событие»: по обязательству, алерт которого уже записан, второй не появляется.
func TestCT03_AlertNotDuplicatedOnEventRetry(t *testing.T) {
	store := &failingAppendStore{MemStore: commitments.NewMemStore(), failOn: 2}
	pub := &memPub{}
	svc := commitments.NewService(store, pub, kernel.FixedClock{T: now})
	ctx := context.Background()
	cpo := cpoScope()
	src := kernel.NewID()
	first, err := svc.Create(ctx, cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), src))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(ctx, cpo, vm, customerInput(kernel.DateOf(2026, 11, 5), src))
	if err != nil {
		t.Fatal(err)
	}

	h := commitments.NewShiftHandler(svc, serviceScope())
	ev := shiftEvent(t, src, kernel.DateOf(2026, 12, 1), nil)
	// Первый алерт записан, на втором обработчик падает: событие не отмечено обработанным.
	if err := h.Handle(ctx, ev); err == nil {
		t.Fatal("ожидался сбой на втором обязательстве")
	}
	if alerts, _ := svc.Alerts(ctx, cpo, vm, false); len(alerts) != 1 {
		t.Fatalf("после сбоя ожидался 1 алерт, получено %d", len(alerts))
	}

	// Повторная доставка того же события: первое обязательство не дублируется, второе догоняет.
	store.failOn = 0
	if err := h.Handle(ctx, ev); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	alerts, err := svc.Alerts(ctx, cpo, vm, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("после повтора ожидалось 2 алерта (по одному на обязательство), получено %d", len(alerts))
	}
	byCommitment := map[kernel.ID]int{}
	for _, a := range alerts {
		if a.EventID != ev.ID {
			t.Fatalf("алерт без события: %+v", a)
		}
		byCommitment[a.CommitmentID]++
	}
	if byCommitment[first.ID] != 1 || byCommitment[second.ID] != 1 {
		t.Fatalf("дубль алерта по обязательству: %v", byCommitment)
	}

	// Третья доставка ничего не добавляет (событие уже отмечено обработанным).
	if err := h.Handle(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if alerts, _ := svc.Alerts(ctx, cpo, vm, false); len(alerts) != 2 {
		t.Fatalf("третья доставка добавила алерт: %d", len(alerts))
	}
	if pub.count(commitments.EventAlertRaised) != 2 {
		t.Fatalf("событий alert.raised: %d", pub.count(commitments.EventAlertRaised))
	}
}
