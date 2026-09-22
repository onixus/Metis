//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/compliance/pgstore"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
)

func openTestDB(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL не задан: интеграционный тест пропущен (docs/questions.md №05)")
	}
	ctx := context.Background()
	db, err := pgdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrate.Up(ctx, db.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	// TRUNCATE не проходит через триггер строк; журналы тестовой БД чистятся от владельца.
	if _, err := db.Pool().Exec(ctx, `TRUNCATE compliance.requirement_sets, compliance.track_templates, compliance.tracks,
		compliance.impact_assessments, compliance.baselines, compliance.evidence_log, compliance.settings`); err != nil {
		t.Fatal(err)
	}
	return db
}

var now = time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)

func TestCM03_PR05_PGSettingsPersistAcrossServicesAndScopes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	sc := authz.New(authz.Params{Subject: "settings-admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	first := compliance.NewService(pgstore.New(db), nil, nil, nil, kernel.SystemClock{})
	settings, err := first.Settings(ctx, sc)
	if err != nil || settings.BaselineLifetimeYears != 5 {
		t.Fatalf("default settings: %+v %v", settings, err)
	}
	settings.BaselineLifetimeYears = 3
	settings.CostByClass[compliance.ImpactSecurityFunctions] = kernel.RUB(75_000_00)
	if err := first.UpdateSettings(ctx, sc, settings); err != nil {
		t.Fatal(err)
	}
	// A separate connection/pool simulates a second API or worker process.
	otherDB, err := pgdb.Open(ctx, os.Getenv("METIS_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer otherDB.Close()
	store := pgstore.New(otherDB)
	second := compliance.NewService(store, nil, nil, nil, kernel.SystemClock{})
	got, err := second.Settings(ctx, sc)
	if err != nil || !reflect.DeepEqual(got, settings) {
		t.Fatalf("settings changed after restart: %+v %v", got, err)
	}
	if _, err := store.Settings(ctx, authz.Scope{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope read: %v", err)
	}
	if err := store.SaveSettings(ctx, authz.Scope{}, settings); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope write: %v", err)
	}
}

func TestCM01_PGStoreRequirementSets(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	v1 := compliance.RequirementSet{ID: kernel.NewID(), Code: "FSTEC-UD4", Version: 1, ProductType: portfoliograph.ProductTypeSecurity,
		Items: []compliance.RequirementItem{{Key: "r1", Text: "требование 1"}}, Status: compliance.RequirementSetPublished, CreatedBy: "compliance", CreatedAt: now, UpdatedAt: now}
	v2 := compliance.RequirementSet{ID: kernel.NewID(), Code: "FSTEC-UD4", Version: 2, ProductType: portfoliograph.ProductTypeSecurity,
		Items: []compliance.RequirementItem{}, Status: compliance.RequirementSetDraft, CreatedBy: "compliance", CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	other := compliance.RequirementSet{ID: kernel.NewID(), Code: "REESTR", Version: 1, ProductType: portfoliograph.ProductTypeOther,
		Items: []compliance.RequirementItem{}, Status: compliance.RequirementSetPublished, CreatedAt: now, UpdatedAt: now}
	for _, rs := range []compliance.RequirementSet{v2, v1, other} {
		if err := store.SaveRequirementSet(ctx, rs); err != nil {
			t.Fatal(err)
		}
	}
	v1.Status = compliance.RequirementSetRetired
	if err := store.SaveRequirementSet(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RequirementSet(ctx, v1.ID); err != nil || !reflect.DeepEqual(got, v1) {
		t.Fatalf("RequirementSet:\n got %+v\nwant %+v\nerr=%v", got, v1, err)
	}
	if list, err := store.RequirementSets(ctx, "FSTEC-UD4"); err != nil || len(list) != 2 || list[0].Version != 1 || list[1].Version != 2 {
		t.Fatalf("RequirementSets by code: %+v err=%v", list, err)
	}
	if list, err := store.RequirementSets(ctx, ""); err != nil || len(list) != 3 {
		t.Fatalf("RequirementSets all: %+v err=%v", list, err)
	}
	if _, err := store.RequirementSet(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("RequirementSet missing: %v", err)
	}
}

func TestCM02_PGStoreTemplates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	var templates []compliance.TrackTemplate
	for i, tpl := range compliance.DefaultTemplates() {
		tpl.ID = kernel.NewID()
		tpl.CreatedAt, tpl.UpdatedAt = now.Add(time.Duration(i)*time.Second), now.Add(time.Duration(i)*time.Second)
		if err := store.SaveTemplate(ctx, tpl); err != nil {
			t.Fatal(err)
		}
		templates = append(templates, tpl)
	}
	templates[0].Name = "Изменённый"
	if err := store.SaveTemplate(ctx, templates[0]); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Template(ctx, templates[0].ID); err != nil || !reflect.DeepEqual(got, templates[0]) {
		t.Fatalf("Template:\n got %+v\nwant %+v\nerr=%v", got, templates[0], err)
	}
	if list, err := store.Templates(ctx, portfoliograph.ProductTypeSecurity); err != nil || len(list) != 1 || !reflect.DeepEqual(list[0], templates[0]) {
		t.Fatalf("Templates by type: %+v err=%v", list, err)
	}
	if list, err := store.Templates(ctx, ""); err != nil || !reflect.DeepEqual(list, templates) {
		t.Fatalf("Templates all: %d err=%v", len(list), err)
	}
}

func TestCM03_PGStoreTracks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	product, release, template, evidence := kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()
	tr := compliance.Track{ID: kernel.NewID(), ProductID: product, ReleaseID: release, Version: "1.0", TemplateID: template, Status: compliance.TrackActive,
		Gates: []compliance.Gate{
			{ID: kernel.NewID(), Key: "ssdlc", Name: "SSDLC", Kind: compliance.GateKindSSDLC, Order: 1, Owner: "sec", DueDate: kernel.DateOf(2026, 10, 1),
				Cost: kernel.RUB(100_000_00), Checklist: []compliance.ChecklistItem{{Key: "sast", Text: "SAST", Done: true, EvidenceID: evidence}, {Key: "sca", Text: "SCA"}},
				Status: compliance.GateInProgress},
			{ID: kernel.NewID(), Key: compliance.GateKeyCertificate, Name: "Сертификат", Kind: compliance.GateKindFSTEC, Order: 2, ParallelGroup: "fstec",
				RequirementSetCode: "FSTEC-UD4", Checklist: []compliance.ChecklistItem{}, Status: compliance.GatePassed, PassedAt: now},
		}, CreatedBy: "compliance", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveTrack(ctx, tr); err != nil {
		t.Fatal(err)
	}
	tr.Status, tr.BaselineID = compliance.TrackCertified, kernel.NewID()
	if err := store.SaveTrack(ctx, tr); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Track(ctx, tr.ID); err != nil || !reflect.DeepEqual(got, tr) {
		t.Fatalf("Track:\n got %+v\nwant %+v\nerr=%v", got, tr, err)
	}
	if list, err := store.Tracks(ctx, compliance.TrackFilter{ProductID: product, ReleaseID: release}); err != nil || len(list) != 1 {
		t.Fatalf("Tracks: %+v err=%v", list, err)
	}
	if list, err := store.Tracks(ctx, compliance.TrackFilter{ReleaseID: kernel.NewID()}); err != nil || len(list) != 0 {
		t.Fatalf("Tracks other release: %+v err=%v", list, err)
	}
	if _, err := store.Track(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Track missing: %v", err)
	}
}

func TestCM06_PGImpactAppendOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	feature, product := kernel.NewID(), kernel.NewID()
	history := []compliance.ImpactAssessment{
		{ID: kernel.NewID(), FeatureID: feature, ProductID: product, Class: compliance.ImpactAnalysisRequired, Justification: "меняет API", Author: "compliance", At: now},
		{ID: kernel.NewID(), FeatureID: feature, ProductID: product, Class: compliance.ImpactSecurityFunctions, Justification: "затрагивает СЗИ", Author: "compliance", At: now.Add(time.Second)},
	}
	for _, a := range history {
		if err := store.AppendImpact(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.ImpactHistory(ctx, feature); err != nil || !reflect.DeepEqual(got, history) {
		t.Fatalf("ImpactHistory:\n got %+v\nwant %+v\nerr=%v", got, history, err)
	}
	if got, err := store.ImpactHistory(ctx, kernel.NewID()); err != nil || len(got) != 0 {
		t.Fatalf("ImpactHistory unknown: %+v err=%v", got, err)
	}
	if _, err := db.Pool().Exec(ctx, "UPDATE compliance.impact_assessments SET class = 'none' WHERE id = $1", history[0].ID); err == nil {
		t.Fatal("UPDATE compliance.impact_assessments должен быть отклонён триггером")
	}
	if _, err := db.Pool().Exec(ctx, "DELETE FROM compliance.impact_assessments WHERE id = $1", history[0].ID); err == nil {
		t.Fatal("DELETE FROM compliance.impact_assessments должен быть отклонён триггером")
	}
}

func TestCM07_PGStoreBaselines(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	product := kernel.NewID()
	b := compliance.CertifiedBaseline{ID: kernel.NewID(), ProductID: product, TrackID: kernel.NewID(), Version: "1.0", RequirementSetID: kernel.NewID(),
		CertificateNo: "SYN-1", CertifiedAt: kernel.DateOf(2026, 9, 1), EOL: kernel.DateOf(2031, 9, 1), CreatedAt: now}
	b2 := compliance.CertifiedBaseline{ID: kernel.NewID(), ProductID: kernel.NewID(), Version: "2.0", CreatedAt: now.Add(time.Second)}
	for _, x := range []compliance.CertifiedBaseline{b, b2} {
		if err := store.SaveBaseline(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	b.CertificateNo = "SYN-2"
	if err := store.SaveBaseline(ctx, b); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Baseline(ctx, b.ID); err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("Baseline:\n got %+v\nwant %+v\nerr=%v", got, b, err)
	}
	if got, err := store.Baseline(ctx, b2.ID); err != nil || !reflect.DeepEqual(got, b2) {
		t.Fatalf("Baseline b2:\n got %+v\nwant %+v\nerr=%v", got, b2, err)
	}
	if list, err := store.Baselines(ctx, product); err != nil || len(list) != 1 {
		t.Fatalf("Baselines by product: %+v err=%v", list, err)
	}
	if list, err := store.Baselines(ctx, kernel.NilID); err != nil || len(list) != 2 || list[0].ID != b.ID {
		t.Fatalf("Baselines all: %+v err=%v", list, err)
	}
}

// TestCM08_PGStoreBaselineComponents: состав поставки сохраняется вместе с baseline,
// и по ключу компонента находятся затронутые сертифицированные версии.
func TestCM08_PGStoreBaselineComponents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	openssl := compliance.Component{Key: "pkg:generic/openssl", Version: "3.0.12"}
	affected := compliance.CertifiedBaseline{ID: kernel.NewID(), ProductID: kernel.NewID(), Version: "3.1",
		CertificateNo: "SYN-10", CertifiedAt: kernel.DateOf(2026, 9, 1), EOL: kernel.DateOf(2031, 9, 1),
		Components: []compliance.Component{openssl, {Key: "pkg:generic/zlib", Version: "1.3"}}, CreatedAt: now}
	other := compliance.CertifiedBaseline{ID: kernel.NewID(), ProductID: kernel.NewID(), Version: "4.0",
		Components: []compliance.Component{{Key: "pkg:generic/openssl", Version: "3.5.0"}}, CreatedAt: now.Add(time.Second)}
	empty := compliance.CertifiedBaseline{ID: kernel.NewID(), ProductID: kernel.NewID(), Version: "1.0", CreatedAt: now.Add(2 * time.Second)}
	for _, b := range []compliance.CertifiedBaseline{affected, other, empty} {
		if err := store.SaveBaseline(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Baseline(ctx, affected.ID)
	if err != nil || !reflect.DeepEqual(got, affected) {
		t.Fatalf("baseline с составом:\n got %+v\nwant %+v\nerr=%v", got, affected, err)
	}
	if got, err := store.Baseline(ctx, empty.ID); err != nil || got.Components != nil {
		t.Fatalf("пустой состав читается как %+v, err=%v", got.Components, err)
	}
	list, err := store.BaselinesWithComponent(ctx, openssl.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("по ключу компонента найдено %d baseline, ожидалось 2", len(list))
	}
	matched := 0
	for _, b := range list {
		if b.HasComponent(openssl) {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("уязвимую версию компонента содержит %d baseline, ожидался 1", matched)
	}
	if list, err := store.BaselinesWithComponent(ctx, "pkg:generic/нет-такого"); err != nil || len(list) != 0 {
		t.Fatalf("неизвестный компонент: %+v err=%v", list, err)
	}
}

func fillEvidence(t *testing.T, store compliance.EvidenceStore, n int) []compliance.EvidenceItem {
	t.Helper()
	ctx := context.Background()
	clock := pgstore.Clock{Inner: kernel.SystemClock{}}
	product, track, gate := kernel.NewID(), kernel.NewID(), kernel.NewID()
	var out []compliance.EvidenceItem
	prevHash := compliance.EvidenceGenesisHash
	for i := 1; i <= n; i++ {
		e := compliance.EvidenceItem{Seq: int64(i), ID: kernel.NewID(), ProductID: product, TrackID: track, GateID: gate, URL: "https://kb/e",
			SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Status: compliance.EvidenceSubmitted, Comment: "тест <b>",
			Actor: "compliance", At: clock.Now(), PrevHash: prevHash}
		if i > 1 {
			e.Supersedes = int64(i - 1)
		}
		h, err := compliance.ComputeEvidenceHash(e)
		if err != nil {
			t.Fatal(err)
		}
		e.Hash = h
		if err := store.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
		prevHash = h
		out = append(out, e)
	}
	return out
}

func TestCM04_PGEvidenceLogRejectsUpdate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.NewEvidenceStore(db)
	if _, err := store.Last(ctx); !kernel.IsNotFound(err) {
		t.Fatalf("Last на пустом журнале: %v", err)
	}
	items := fillEvidence(t, store, 2)
	if last, err := store.Last(ctx); err != nil || !reflect.DeepEqual(last, items[1]) {
		t.Fatalf("Last:\n got %+v\nwant %+v\nerr=%v", last, items[1], err)
	}
	if _, err := db.Pool().Exec(ctx, "UPDATE compliance.evidence_log SET status = 'accepted' WHERE seq = 1"); err == nil {
		t.Fatal("UPDATE compliance.evidence_log должен быть отклонён триггером")
	}
	if _, err := db.Pool().Exec(ctx, "DELETE FROM compliance.evidence_log WHERE seq = 1"); err == nil {
		t.Fatal("DELETE FROM compliance.evidence_log должен быть отклонён триггером")
	}
	if err := store.Insert(ctx, items[1]); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("повторный seq должен давать ErrConflict: %v", err)
	}
	res, err := compliance.VerifyEvidenceLog(ctx, store)
	if err != nil || !res.OK || res.Checked != 2 {
		t.Fatalf("VerifyEvidenceLog: %+v err=%v", res, err)
	}
}

func TestCM04_PGEvidenceChainVerify(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.NewEvidenceStore(db)
	fillEvidence(t, store, 5)
	res, err := compliance.VerifyEvidenceLog(ctx, store)
	if err != nil || !res.OK || res.Checked != 5 {
		t.Fatalf("VerifyEvidenceLog: %+v err=%v", res, err)
	}
	if _, err := db.Pool().Exec(ctx, "ALTER TABLE compliance.evidence_log DISABLE TRIGGER evidence_log_immutable"); err != nil {
		t.Skipf("нет прав отключить триггер (нужен владелец таблицы): %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "ALTER TABLE compliance.evidence_log ENABLE TRIGGER evidence_log_immutable")
	})
	if _, err := db.Pool().Exec(ctx, "UPDATE compliance.evidence_log SET comment = 'подменено' WHERE seq = 3"); err != nil {
		t.Fatal(err)
	}
	res, err = compliance.VerifyEvidenceLog(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq != 3 {
		t.Fatalf("VerifyEvidenceLog должен указать на запись 3: %+v", res)
	}
}

func TestCM04_PGEvidenceStoreRejectsSubMicrosecondTime(t *testing.T) {
	db := openTestDB(t)
	e := compliance.EvidenceItem{Seq: 1, ID: kernel.NewID(), At: time.Date(2026, 9, 17, 0, 0, 0, 1, time.UTC), Actor: "a",
		Status: compliance.EvidenceSubmitted, PrevHash: compliance.EvidenceGenesisHash, Hash: compliance.EvidenceGenesisHash}
	if err := pgstore.NewEvidenceStore(db).Insert(context.Background(), e); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("Insert с наносекундами должен быть отклонён: %v", err)
	}
}

// TestCM01_PGRequirementSetVersionUnique — версия набора требований уникальна в пределах кода:
// вторая запись той же версии отклоняется, а нарушение уникальности отображается в
// kernel.ErrConflict, на который опирается повтор в CreateRequirementSet.
func TestCM01_PGRequirementSetVersionUnique(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	first := compliance.RequirementSet{ID: kernel.NewID(), Code: "SYN-UNIQ", Version: 1, ProductType: portfoliograph.ProductTypeSecurity,
		Items: []compliance.RequirementItem{{Key: "r1", Text: "требование 1"}}, Status: compliance.RequirementSetDraft, CreatedBy: "compliance", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveRequirementSet(ctx, first); err != nil {
		t.Fatalf("первая версия: %v", err)
	}
	// Повторное сохранение той же записи (по id) — обновление, не конфликт.
	first.Status = compliance.RequirementSetPublished
	if err := store.SaveRequirementSet(ctx, first); err != nil {
		t.Fatalf("обновление по id: %v", err)
	}
	race := first
	race.ID, race.Status = kernel.NewID(), compliance.RequirementSetDraft
	if err := store.SaveRequirementSet(ctx, race); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("дубль версии принят: %v", err)
	}
	race.Version = 2
	if err := store.SaveRequirementSet(ctx, race); err != nil {
		t.Fatalf("следующая версия: %v", err)
	}
	list, err := store.RequirementSets(ctx, "SYN-UNIQ")
	if err != nil || len(list) != 2 || list[0].Version != 1 || list[1].Version != 2 {
		t.Fatalf("версии набора: %+v, %v", list, err)
	}
}
