package roadmap_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

// fakeContracts — фейк порта ContractReader (RM-05).
type fakeContracts struct{ contracts []pg.IntegrationContract }

func (f *fakeContracts) Contracts(_ context.Context, sc authz.Scope) ([]pg.IntegrationContract, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	return f.contracts, nil
}

// fakeReadiness — фейк порта ReadinessChecker (CM-05).
type fakeReadiness struct {
	ready map[kernel.ID]roadmap.Readiness
	calls int
}

func (f *fakeReadiness) ReleaseReadiness(_ context.Context, sc authz.Scope, id kernel.ID) (roadmap.Readiness, error) {
	f.calls++
	if !sc.Valid() {
		return roadmap.Readiness{}, kernel.ErrForbidden
	}
	return f.ready[id], nil
}

func TestRM04_CertifiedBranchAcceptsOnlyFixes(t *testing.T) {
	f := newFixture(t)
	base := f.release(f.edr, "3.0", d(2026, 10, 1))
	if base.Branch != roadmap.BranchEvolving {
		t.Fatalf("ветка по умолчанию evolving: %+v", base)
	}
	cert, err := f.svc.CreateRelease(f.ctx, f.cpo, f.edr, roadmap.ReleaseInput{Name: "EDR 3.0 cert", Version: "3.0-cert",
		PlannedDate: d(2026, 12, 1), Branch: roadmap.BranchCertified, BaseReleaseID: base.ID})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Branch != roadmap.BranchCertified || cert.BaseReleaseID != base.ID {
		t.Fatalf("сертифицированная ветка: %+v", cert)
	}
	// feature в сертифицированный релиз — конфликт с понятным сообщением
	_, err = f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "Новая фича", Bucket: roadmap.BucketNow, ReleaseID: cert.ID})
	if !errors.Is(err, kernel.ErrConflict) || !strings.Contains(err.Error(), "kind=fix") {
		t.Fatalf("feature в certified: ожидался ErrConflict с пояснением, получено %v", err)
	}
	fix, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "Исправление CVE", Bucket: roadmap.BucketNow, ReleaseID: cert.ID, Kind: roadmap.KindFix})
	if err != nil {
		t.Fatalf("fix в certified: %v", err)
	}
	if fix.Kind != roadmap.KindFix {
		t.Fatalf("вид элемента: %+v", fix)
	}
	// смена вида на feature при привязке к certified — тоже конфликт
	upd := roadmap.ItemInput{Title: fix.Title, Bucket: fix.Bucket, ReleaseID: cert.ID, Audience: fix.Audience, Status: fix.Status, Kind: roadmap.KindFeature}
	if _, err := f.svc.UpdateItem(f.ctx, f.cpo, fix.ID, upd); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("UpdateItem kind=feature в certified: %v", err)
	}
	// feature в развивающуюся ветку — можно, вид по умолчанию feature
	it, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "Фича", Bucket: roadmap.BucketNext, ReleaseID: base.ID})
	if err != nil || it.Kind != roadmap.KindFeature {
		t.Fatalf("feature в evolving: %v %+v", err, it)
	}
	// перевести evolving-релиз с feature-элементом в certified нельзя
	in := roadmap.ReleaseInput{Name: base.Name, Version: base.Version, PlannedDate: base.PlannedDate, Branch: roadmap.BranchCertified}
	if _, err := f.svc.UpdateRelease(f.ctx, f.cpo, base.ID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("перевод релиза с feature в certified: %v", err)
	}
	// недопустимые значения
	if _, err := f.svc.CreateRelease(f.ctx, f.cpo, f.edr, roadmap.ReleaseInput{Name: "x", Version: "9", Branch: "beta"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестная ветка: %v", err)
	}
	if _, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "x", Bucket: roadmap.BucketNow, Kind: "hotfix"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный вид: %v", err)
	}
	if _, err := f.svc.CreateRelease(f.ctx, f.cpo, f.edr, roadmap.ReleaseInput{Name: "x", Version: "9", Branch: roadmap.BranchCertified, BaseReleaseID: f.release(f.vm, "1", kernel.Date{}).ID}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("база другого продукта: %v", err)
	}
}

func TestRM04_UpdateReleaseForbidsBranchChangeAfterRelease(t *testing.T) {
	f := newFixture(t)
	r := f.release(f.edr, "3.0", d(2026, 10, 1))
	in := roadmap.ReleaseInput{Name: r.Name, Version: r.Version, PlannedDate: r.PlannedDate, Branch: roadmap.BranchCertified}
	upd, err := f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in)
	if err != nil || upd.Branch != roadmap.BranchCertified {
		t.Fatalf("смена ветки до выпуска: %v %+v", err, upd)
	}
	in.Status = roadmap.ReleaseReleased
	if _, err := f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in); err != nil {
		t.Fatal(err)
	}
	in.Branch = roadmap.BranchEvolving
	if _, err := f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("смена ветки после выпуска: ожидался ErrConflict, получено %v", err)
	}
	// пустая ветка во входе — без изменения
	in.Branch, in.Name = "", "EDR 3.0 GA"
	upd, err = f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in)
	if err != nil || upd.Branch != roadmap.BranchCertified || upd.Name != "EDR 3.0 GA" {
		t.Fatalf("обновление без смены ветки: %v %+v", err, upd)
	}
	// ready_for_certification через UpdateRelease не выставляется
	in.Status = roadmap.ReleaseReadyForCertification
	if _, err := f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("статус ready_for_certification через UpdateRelease: %v", err)
	}
	// конфликт версий
	f.release(f.edr, "3.1", d(2026, 11, 1))
	in.Status, in.Version = roadmap.ReleaseReleased, "3.1"
	if _, err := f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("дубликат версии: %v", err)
	}
}

func TestRM05_FeaturesNotesAndEOL(t *testing.T) {
	f := newFixture(t)
	r := f.release(f.edr, "3.0", d(2026, 10, 1))
	f1, f2 := kernel.NewID(), kernel.NewID()
	upd, err := f.svc.SetReleaseFeatures(f.ctx, f.cpo, r.ID, []kernel.ID{f1, f2, f1})
	if err != nil || len(upd.FeatureIDs) != 2 || upd.FeatureIDs[0] != f1 || upd.FeatureIDs[1] != f2 {
		t.Fatalf("состав релиза без дубликатов: %v %+v", err, upd.FeatureIDs)
	}
	if _, err := f.svc.SetReleaseFeatures(f.ctx, f.cpo, r.ID, []kernel.ID{kernel.NilID}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустой идентификатор: %v", err)
	}
	if upd, err = f.svc.SetReleaseNotes(f.ctx, f.cpo, r.ID, "Исправлен разбор событий"); err != nil || upd.ReleaseNotes != "Исправлен разбор событий" {
		t.Fatalf("release notes: %v %+v", err, upd)
	}
	if _, err := f.svc.SetReleaseEOL(f.ctx, f.cpo, r.ID, d(2026, 9, 1)); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("EOL раньше даты релиза: %v", err)
	}
	if upd, err = f.svc.SetReleaseEOL(f.ctx, f.cpo, r.ID, d(2029, 10, 1)); err != nil || upd.EOL != d(2029, 10, 1) {
		t.Fatalf("EOL: %v %+v", err, upd)
	}
	got, err := f.svc.Release(f.ctx, f.cpo, r.ID)
	if err != nil || len(got.FeatureIDs) != 2 || got.ReleaseNotes == "" || got.EOL != d(2029, 10, 1) {
		t.Fatalf("релиз целиком: %v %+v", err, got)
	}
	saved := 0
	for _, ev := range f.pub.events {
		if ev.Type == roadmap.EventReleaseSaved && ev.AggregateID == r.ID {
			saved++
		}
	}
	if saved != 4 { // создание + состав + notes + EOL
		t.Fatalf("событий release.saved: %d", saved)
	}
}

func TestRM05_CompatibilityMatrixFromContracts(t *testing.T) {
	f := newFixture(t)
	soar := kernel.NewID()
	cID := kernel.NewID()
	contracts := &fakeContracts{contracts: []pg.IntegrationContract{
		{ID: cID, Name: "Response API", ProviderProductID: f.edr, ConsumerProductID: soar, Compatibility: []pg.VersionPair{
			{ProviderVersion: "3.0", ConsumerVersion: "5.1", Compatible: true},
			{ProviderVersion: "3.0", ConsumerVersion: "5.0", Compatible: false},
			{ProviderVersion: "2.9", ConsumerVersion: "5.0", Compatible: true},
		}},
		{ID: kernel.NewID(), Name: "Assets feed", ProviderProductID: f.vm, ConsumerProductID: f.edr, Compatibility: []pg.VersionPair{
			{ProviderVersion: "7", ConsumerVersion: "3.0", Compatible: true},
			{ProviderVersion: "6", ConsumerVersion: "2.9", Compatible: true},
		}},
		{ID: kernel.NewID(), Name: "Unrelated", ProviderProductID: f.vm, ConsumerProductID: soar, Compatibility: []pg.VersionPair{
			{ProviderVersion: "3.0", ConsumerVersion: "3.0", Compatible: true},
		}},
	}}
	f.svc.WithContracts(contracts)
	r := f.release(f.edr, "3.0", d(2026, 10, 1))
	rows, err := f.svc.CompatibilityMatrix(f.ctx, f.cpo, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("ожидалось 3 строки (2 как поставщик, 1 как потребитель), получено %+v", rows)
	}
	if rows[0].ContractName != "Assets feed" || rows[0].ConsumerVersion != "3.0" || rows[0].ProviderVersion != "7" {
		t.Fatalf("строка как потребитель: %+v", rows[0])
	}
	if rows[1].ContractID != cID || rows[1].ConsumerVersion != "5.0" || rows[1].Compatible || !rows[2].Compatible {
		t.Fatalf("строки как поставщик: %+v", rows[1:])
	}
	// матрица есть и в списке релизов, и в ByRelease
	rels, err := f.svc.Releases(f.ctx, f.cpo, f.edr)
	if err != nil || len(rels) != 1 || len(rels[0].CompatibilityMatrix) != 3 {
		t.Fatalf("Releases с матрицей: %v %+v", err, rels)
	}
	br, err := f.svc.ByRelease(f.ctx, f.cpo, f.edr)
	if err != nil || len(br.Releases[0].Release.CompatibilityMatrix) != 3 || br.Releases[0].SalesSafeRelease != nil {
		t.Fatalf("ByRelease с матрицей: %v %+v", err, br)
	}
	// без порта — пусто, не ошибка
	f.svc.WithContracts(nil)
	if rows, err := f.svc.CompatibilityMatrix(f.ctx, f.cpo, r.ID); err != nil || len(rows) != 0 {
		t.Fatalf("без порта: %v %+v", err, rows)
	}
}

func TestRM05_MarkReadyForCertificationRequiresReadiness(t *testing.T) {
	f := newFixture(t)
	r := f.release(f.edr, "3.0", d(2026, 10, 1))
	// без порта — недоступно
	if _, err := f.svc.MarkReadyForCertification(f.ctx, f.cpo, r.ID); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("без порта: ожидался ErrUnavailable, получено %v", err)
	}
	rd := &fakeReadiness{ready: map[kernel.ID]roadmap.Readiness{r.ID: {Ready: false, OpenItems: []string{"SAST: отчёт не приложен"}}}}
	f.svc.WithReadiness(rd)
	_, err := f.svc.MarkReadyForCertification(f.ctx, f.cpo, r.ID)
	if !errors.Is(err, kernel.ErrConflict) || !strings.Contains(err.Error(), "SAST") {
		t.Fatalf("не готов: ожидался ErrConflict с открытыми пунктами, получено %v", err)
	}
	rd.ready[r.ID] = roadmap.Readiness{Ready: true}
	upd, err := f.svc.MarkReadyForCertification(f.ctx, f.cpo, r.ID)
	if err != nil || upd.Status != roadmap.ReleaseReadyForCertification {
		t.Fatalf("готов: %v %+v", err, upd)
	}
	// повтор идемпотентен
	if _, err := f.svc.MarkReadyForCertification(f.ctx, f.cpo, r.ID); err != nil {
		t.Fatal(err)
	}
	ready := 0
	for _, ev := range f.pub.events {
		if ev.Type == roadmap.EventReleaseReadyForCertification && ev.AggregateID == r.ID {
			ready++
		}
	}
	if ready != 1 {
		t.Fatalf("событие ready_for_certification должно быть одно, получено %d", ready)
	}
	// после выпуска — нельзя
	in := roadmap.ReleaseInput{Name: r.Name, Version: r.Version, PlannedDate: r.PlannedDate, Status: roadmap.ReleaseReleased}
	if _, err := f.svc.UpdateRelease(f.ctx, f.cpo, r.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.MarkReadyForCertification(f.ctx, f.cpo, r.ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("после выпуска: %v", err)
	}
}

// Sales-safe срез (RM-02 + RM-05): presale видит матрицу совместимости и EOL, но не release notes и состав,
// и не может переводить релиз в ready_for_certification.
func TestRM05_ABAC_SalesSafeHidesNotesAndCannotMarkReady(t *testing.T) {
	f := newFixture(t)
	soar := kernel.NewID()
	f.svc.WithContracts(&fakeContracts{contracts: []pg.IntegrationContract{{ID: kernel.NewID(), Name: "Response API",
		ProviderProductID: f.edr, ConsumerProductID: soar, Compatibility: []pg.VersionPair{{ProviderVersion: "3.0", ConsumerVersion: "5.1", Compatible: true}}}}})
	f.svc.WithReadiness(&fakeReadiness{ready: map[kernel.ID]roadmap.Readiness{}})
	r := f.release(f.edr, "3.0", d(2026, 10, 1))
	if _, err := f.svc.SetReleaseNotes(f.ctx, f.cpo, r.ID, "внутренние детали"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetReleaseFeatures(f.ctx, f.cpo, r.ID, []kernel.ID{kernel.NewID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetReleaseEOL(f.ctx, f.cpo, r.ID, d(2029, 10, 1)); err != nil {
		t.Fatal(err)
	}
	presale := presaleScope()
	for name, err := range map[string]error{
		"MarkReadyForCertification": func() error { _, e := f.svc.MarkReadyForCertification(f.ctx, presale, r.ID); return e }(),
		"SetReleaseNotes":           func() error { _, e := f.svc.SetReleaseNotes(f.ctx, presale, r.ID, "x"); return e }(),
		"SetReleaseFeatures":        func() error { _, e := f.svc.SetReleaseFeatures(f.ctx, presale, r.ID, nil); return e }(),
		"SetReleaseEOL":             func() error { _, e := f.svc.SetReleaseEOL(f.ctx, presale, r.ID, kernel.Date{}); return e }(),
		"UpdateRelease": func() error {
			_, e := f.svc.UpdateRelease(f.ctx, presale, r.ID, roadmap.ReleaseInput{Name: "x", Version: "3.0"})
			return e
		}(),
		"EnsureRenewalItem": func() error {
			_, e := f.svc.EnsureRenewalItem(f.ctx, presale, f.edr, kernel.NewID(), "x", kernel.Date{}, kernel.Date{})
			return e
		}(),
	} {
		if !errors.Is(err, kernel.ErrForbidden) {
			t.Errorf("%s для presale: ожидался ErrForbidden, получено %v", name, err)
		}
	}
	check := func(name string, got roadmap.Release) {
		t.Helper()
		if got.ReleaseNotes != "" || got.FeatureIDs != nil {
			t.Fatalf("%s: sales-safe получил release notes или состав: %+v", name, got)
		}
		if len(got.CompatibilityMatrix) != 1 || got.EOL != d(2029, 10, 1) {
			t.Fatalf("%s: sales-safe должен видеть матрицу и EOL: %+v", name, got)
		}
	}
	got, err := f.svc.Release(f.ctx, presale, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("Release", got)
	rels, err := f.svc.Releases(f.ctx, presale, f.edr)
	if err != nil || len(rels) != 1 {
		t.Fatalf("Releases: %v %d", err, len(rels))
	}
	check("Releases", rels[0])
	br, err := f.svc.ByRelease(f.ctx, presale, f.edr)
	if err != nil || len(br.Releases) != 1 {
		t.Fatalf("ByRelease: %v %+v", err, br)
	}
	check("ByRelease.Release", br.Releases[0].Release)
	ss := br.Releases[0].SalesSafeRelease
	if ss == nil || ss.ID != r.ID || len(ss.CompatibilityMatrix) != 1 || ss.EOL != d(2029, 10, 1) {
		t.Fatalf("SalesSafeRelease: %+v", ss)
	}
	// внутренняя аудитория видит всё
	full, err := f.svc.Release(f.ctx, f.cpo, r.ID)
	if err != nil || full.ReleaseNotes == "" || len(full.FeatureIDs) != 1 {
		t.Fatalf("внутренняя аудитория: %v %+v", err, full)
	}
	var zero authz.Scope
	if _, err := f.svc.Release(f.ctx, zero, r.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope Release: %v", err)
	}
}

func TestCT04_RoadmapEnsureRenewalItemIdempotent(t *testing.T) {
	f := newFixture(t)
	commitment := kernel.NewID()
	// часы фикстуры: 2026-09-17; срок через ~13 месяцев → next
	id1, err := f.svc.EnsureRenewalItem(f.ctx, f.cpo, f.edr, commitment, "Продление сертификата EDR", d(2027, 4, 1), d(2027, 10, 15))
	if err != nil {
		t.Fatal(err)
	}
	id2, err := f.svc.EnsureRenewalItem(f.ctx, f.cpo, f.edr, commitment, "Продление сертификата EDR (повтор)", d(2027, 4, 1), d(2027, 10, 15))
	if err != nil || id1 != id2 {
		t.Fatalf("повторный вызов должен вернуть тот же элемент: %v %s %s", err, id1, id2)
	}
	items, err := f.svc.Items(f.ctx, f.cpo, f.edr)
	if err != nil || len(items) != 1 {
		t.Fatalf("ожидался один элемент: %v %d", err, len(items))
	}
	it := items[0]
	if it.CommitmentID != commitment || it.Bucket != roadmap.BucketLater || it.Audience != authz.AudienceInternal ||
		it.Kind != roadmap.KindFeature || it.Title != "Продление сертификата EDR" || it.EndDate != d(2027, 10, 15) {
		t.Fatalf("элемент по обязательству: %+v", it)
	}
	// корзина по сроку: ≤90 дней — now, ≤365 — next, иначе later
	for _, tc := range []struct {
		end  kernel.Date
		want roadmap.Bucket
	}{{d(2026, 12, 1), roadmap.BucketNow}, {d(2027, 6, 1), roadmap.BucketNext}, {d(2028, 6, 1), roadmap.BucketLater}} {
		id, err := f.svc.EnsureRenewalItem(f.ctx, f.cpo, f.edr, kernel.NewID(), "x", kernel.Date{}, tc.end)
		if err != nil {
			t.Fatal(err)
		}
		all, _ := f.svc.Items(f.ctx, f.cpo, f.edr)
		for _, it := range all {
			if it.ID == id && it.Bucket != tc.want {
				t.Fatalf("срок %s: корзина %s, ожидалась %s", tc.end, it.Bucket, tc.want)
			}
		}
	}
	// тот же commitment для другого продукта — конфликт
	if _, err := f.svc.EnsureRenewalItem(f.ctx, f.cpo, f.vm, commitment, "x", kernel.Date{}, kernel.Date{}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("обязательство другого продукта: %v", err)
	}
	if _, err := f.svc.EnsureRenewalItem(f.ctx, f.cpo, f.edr, kernel.NilID, "x", kernel.Date{}, kernel.Date{}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустой commitment: %v", err)
	}
	// ABAC: PM другого продукта не создаёт элемент
	if _, err := f.svc.EnsureRenewalItem(f.ctx, pmScope("pm-vm", f.vm), f.edr, kernel.NewID(), "x", kernel.Date{}, kernel.Date{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чужой PM: %v", err)
	}
}

// TestRM05_UpdateReleaseKeepsEOLWhenOmitted — частичное обновление релиза не стирает
// необязательные поля: опущенные eol и base_release_id сохраняют текущее значение (как status
// и branch). Снять EOL можно только через SetReleaseEOL.
func TestRM05_UpdateReleaseKeepsEOLWhenOmitted(t *testing.T) {
	f := newFixture(t)
	base := f.release(f.edr, "3.0", d(2026, 10, 1))
	cert, err := f.svc.CreateRelease(f.ctx, f.cpo, f.edr, roadmap.ReleaseInput{Name: "EDR 3.0 cert", Version: "3.0-cert",
		PlannedDate: d(2026, 12, 1), Branch: roadmap.BranchCertified, BaseReleaseID: base.ID, EOL: d(2031, 12, 1)})
	if err != nil {
		t.Fatal(err)
	}

	// Тело только с обязательными полями: ветка, статус, EOL и базовый релиз не меняются.
	got, err := f.svc.UpdateRelease(f.ctx, f.cpo, cert.ID, roadmap.ReleaseInput{Name: "EDR 3.0 сертифицированный", Version: "3.0-cert"})
	if err != nil {
		t.Fatal(err)
	}
	if got.EOL != d(2031, 12, 1) {
		t.Fatalf("дата окончания поддержки стёрта: %+v", got.EOL)
	}
	if got.BaseReleaseID != base.ID {
		t.Fatalf("ссылка на базовый релиз стёрта: %v", got.BaseReleaseID)
	}
	if got.Branch != roadmap.BranchCertified || got.Status != cert.Status || got.Name != "EDR 3.0 сертифицированный" {
		t.Fatalf("неожиданный релиз после обновления: %+v", got)
	}

	// Непустое значение по-прежнему меняет поле.
	got, err = f.svc.UpdateRelease(f.ctx, f.cpo, cert.ID, roadmap.ReleaseInput{Name: got.Name, Version: got.Version, EOL: d(2032, 6, 1)})
	if err != nil || got.EOL != d(2032, 6, 1) {
		t.Fatalf("новая дата EOL не применена: %+v %v", got.EOL, err)
	}

	// Снятие EOL — отдельной операцией.
	got, err = f.svc.SetReleaseEOL(f.ctx, f.cpo, cert.ID, kernel.Date{})
	if err != nil || !got.EOL.IsZero() {
		t.Fatalf("SetReleaseEOL не снял дату: %+v %v", got.EOL, err)
	}
}

// TestCM03_ReleaseProductRequiresStrategicAccess — порт для compliance: продукт релиза выдаётся
// только при стратегическом доступе к этому продукту (CM-03).
func TestCM03_ReleaseProductRequiresStrategicAccess(t *testing.T) {
	f := newFixture(t)
	rel := f.release(f.edr, "4.0", d(2027, 3, 1))

	product, err := f.svc.ReleaseProduct(f.ctx, f.cpo, rel.ID)
	if err != nil || product != f.edr {
		t.Fatalf("продукт релиза: %v %v", product, err)
	}
	// Стратегического доступа достаточно.
	if product, err := f.svc.ReleaseProduct(f.ctx, presaleScope(), rel.ID); err != nil || product != f.edr {
		t.Fatalf("presale: %v %v", product, err)
	}
	// Нет доступа к продукту релиза — отказ.
	if _, err := f.svc.ReleaseProduct(f.ctx, pmScope("pm-vm", f.vm), rel.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM чужого продукта: ожидался ErrForbidden, получено %v", err)
	}
	if _, err := f.svc.ReleaseProduct(f.ctx, authz.Scope{}, rel.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался ErrForbidden, получено %v", err)
	}
	if _, err := f.svc.ReleaseProduct(f.ctx, f.cpo, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("отсутствующий релиз: ожидался NotFound, получено %v", err)
	}
}
