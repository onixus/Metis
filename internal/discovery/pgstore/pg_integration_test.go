//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/discovery/pgstore"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/signals"
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE discovery.hypotheses, discovery.interviews, discovery.insights, discovery.evidence,
		discovery.field_defs, discovery.status_defs, discovery.embeddings`); err != nil {
		t.Fatal(err)
	}
	return db
}

var now = time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)

func TestDS01_PGStoreHypotheses(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	product, feature := kernel.NewID(), kernel.NewID()
	h := discovery.Hypothesis{ID: kernel.NewID(), ProductID: product, Title: "SSO", Statement: "мы считаем, что …", Assumptions: []string{"a", "b"},
		ConfirmationCriterion: "5 интервью", Status: discovery.HypothesisTesting, FeatureID: feature,
		CustomFields: map[string]any{"segment": "enterprise", "score": float64(3)}, CreatedBy: "pm", CreatedAt: now, UpdatedAt: now}
	h2 := discovery.Hypothesis{ID: kernel.NewID(), ProductID: product, Title: "Другая", Status: discovery.HypothesisDraft, CreatedBy: "pm",
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, x := range []discovery.Hypothesis{h, h2} {
		if err := store.SaveHypothesis(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	h.Status, h.Resolution = discovery.HypothesisConfirmed, "подтверждено"
	if err := store.SaveHypothesis(ctx, h); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Hypothesis(ctx, h.ID); err != nil || !reflect.DeepEqual(got, h) {
		t.Fatalf("Hypothesis:\n got %+v\nwant %+v\nerr=%v", got, h, err)
	}
	if got, err := store.Hypothesis(ctx, h2.ID); err != nil || !reflect.DeepEqual(got, h2) {
		t.Fatalf("Hypothesis h2:\n got %+v\nwant %+v\nerr=%v", got, h2, err)
	}
	if list, err := store.Hypotheses(ctx, discovery.HypothesisFilter{ProductID: product}); err != nil || len(list) != 2 || list[0].ID != h.ID {
		t.Fatalf("Hypotheses by product: %+v err=%v", list, err)
	}
	if list, err := store.Hypotheses(ctx, discovery.HypothesisFilter{FeatureID: feature, Statuses: []discovery.HypothesisStatus{discovery.HypothesisConfirmed}}); err != nil || len(list) != 1 {
		t.Fatalf("Hypotheses by feature/status: %+v err=%v", list, err)
	}
	if list, err := store.Hypotheses(ctx, discovery.HypothesisFilter{Statuses: []discovery.HypothesisStatus{discovery.HypothesisRejected}}); err != nil || len(list) != 0 {
		t.Fatalf("Hypotheses rejected: %+v err=%v", list, err)
	}
	if _, err := store.Hypothesis(ctx, kernel.NewID()); !kernel.IsNotFound(err) {
		t.Fatalf("Hypothesis missing: %v", err)
	}
}

func TestDS02_PGStoreInterviewsAndInsights(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	product, hyp, sig := kernel.NewID(), kernel.NewID(), kernel.NewID()
	iv := discovery.Interview{ID: kernel.NewID(), ProductID: product, AccountID: "acc-1", Segment: "smb", Date: kernel.DateOf(2026, 9, 10),
		Participants: []string{"CTO"}, Notes: "заметки", HypothesisIDs: []kernel.ID{hyp}, CreatedBy: "pm", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveInterview(ctx, iv); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Interview(ctx, iv.ID); err != nil || !reflect.DeepEqual(got, iv) {
		t.Fatalf("Interview:\n got %+v\nwant %+v\nerr=%v", got, iv, err)
	}
	if list, err := store.Interviews(ctx, product); err != nil || len(list) != 1 {
		t.Fatalf("Interviews: %+v err=%v", list, err)
	}
	if list, err := store.Interviews(ctx, kernel.NilID); err != nil || len(list) != 1 {
		t.Fatalf("Interviews all: %+v err=%v", list, err)
	}
	in := discovery.Insight{ID: kernel.NewID(), ProductID: product, Text: "нужен SSO", InterviewID: iv.ID, HypothesisIDs: []kernel.ID{hyp},
		SignalIDs: []kernel.ID{sig}, Confidence: discovery.ConfidenceHigh, CreatedBy: "pm", CreatedAt: now, UpdatedAt: now}
	in2 := discovery.Insight{ID: kernel.NewID(), ProductID: product, Text: "без привязок", Confidence: discovery.ConfidenceLow, CreatedBy: "pm",
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, x := range []discovery.Insight{in, in2} {
		if err := store.SaveInsight(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := store.Insight(ctx, in.ID); err != nil || !reflect.DeepEqual(got, in) {
		t.Fatalf("Insight:\n got %+v\nwant %+v\nerr=%v", got, in, err)
	}
	if got, err := store.Insight(ctx, in2.ID); err != nil || !reflect.DeepEqual(got, in2) {
		t.Fatalf("Insight in2:\n got %+v\nwant %+v\nerr=%v", got, in2, err)
	}
	for name, f := range map[string]discovery.InsightFilter{
		"hypothesis": {HypothesisID: hyp}, "signal": {SignalID: sig}, "interview": {InterviewID: iv.ID},
	} {
		if list, err := store.Insights(ctx, f); err != nil || len(list) != 1 || list[0].ID != in.ID {
			t.Fatalf("Insights by %s: %+v err=%v", name, list, err)
		}
	}
	if list, err := store.Insights(ctx, discovery.InsightFilter{ProductID: product}); err != nil || len(list) != 2 {
		t.Fatalf("Insights by product: %+v err=%v", list, err)
	}
}

func TestDS03_PGStoreEvidence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	product, hyp, insight, feature := kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()
	e := discovery.Evidence{ID: kernel.NewID(), ProductID: product, Source: discovery.EvidenceSourceInterview, SourceRef: "https://kb/1",
		Date: kernel.DateOf(2026, 9, 10), Trust: discovery.ConfidenceMedium, Verification: discovery.VerificationUnverified,
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", HypothesisID: hyp, InsightID: insight, FeatureID: feature, CreatedBy: "pm", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveEvidence(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.Verification = discovery.VerificationVerified
	if err := store.SaveEvidence(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Evidence(ctx, e.ID); err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("Evidence:\n got %+v\nwant %+v\nerr=%v", got, e, err)
	}
	for name, f := range map[string]discovery.EvidenceFilter{
		"product": {ProductID: product}, "hypothesis": {HypothesisID: hyp}, "insight": {InsightID: insight}, "feature": {FeatureID: feature},
		"verified": {Verification: discovery.VerificationVerified},
	} {
		if list, err := store.EvidenceList(ctx, f); err != nil || len(list) != 1 {
			t.Fatalf("EvidenceList by %s: %+v err=%v", name, list, err)
		}
	}
	if list, err := store.EvidenceList(ctx, discovery.EvidenceFilter{Verification: discovery.VerificationRejected}); err != nil || len(list) != 0 {
		t.Fatalf("EvidenceList rejected: %+v err=%v", list, err)
	}
}

func TestAD03_PGStoreCustomDefs(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := pgstore.New(db)
	f1 := discovery.CustomFieldDef{ID: kernel.NewID(), Entity: discovery.EntityHypothesis, Key: "segment", Label: "Сегмент", Type: discovery.FieldEnum, Options: []string{"smb", "enterprise"}, Required: true}
	f2 := discovery.CustomFieldDef{ID: kernel.NewID(), Entity: discovery.EntityHypothesis, Key: "score", Label: "Балл", Type: discovery.FieldNumber}
	f3 := discovery.CustomFieldDef{ID: kernel.NewID(), Entity: discovery.EntitySignal, Key: "region", Label: "Регион", Type: discovery.FieldString}
	for _, d := range []discovery.CustomFieldDef{f1, f2, f3} {
		if err := store.SaveFieldDef(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	f1.Label = "Сегмент клиента"
	if err := store.SaveFieldDef(ctx, f1); err != nil {
		t.Fatal(err)
	}
	if got, err := store.FieldDefs(ctx, discovery.EntityHypothesis); err != nil || !reflect.DeepEqual(got, []discovery.CustomFieldDef{f1, f2}) {
		t.Fatalf("FieldDefs:\n got %+v\nwant %+v\nerr=%v", got, []discovery.CustomFieldDef{f1, f2}, err)
	}
	s1 := discovery.CustomStatusDef{Entity: discovery.EntityHypothesis, Key: "parked", Label: "Отложена", Category: "draft"}
	if err := store.SaveStatusDef(ctx, s1); err != nil {
		t.Fatal(err)
	}
	s1.Category = "rejected"
	if err := store.SaveStatusDef(ctx, s1); err != nil {
		t.Fatal(err)
	}
	if got, err := store.StatusDefs(ctx, discovery.EntityHypothesis); err != nil || !reflect.DeepEqual(got, []discovery.CustomStatusDef{s1}) {
		t.Fatalf("StatusDefs: %+v err=%v", got, err)
	}
	if got, err := store.StatusDefs(ctx, discovery.EntityFeature); err != nil || len(got) != 0 {
		t.Fatalf("StatusDefs feature: %+v err=%v", got, err)
	}
}

func TestSG04_PGIndexSimilar(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	index := pgstore.NewIndex(db)
	product, other := kernel.NewID(), kernel.NewID()
	docs := map[kernel.ID]string{
		kernel.NewID(): "Нужен экспорт отчётов в CSV для аудитора",
		kernel.NewID(): "Экспорт отчёта в формате CSV",
		kernel.NewID(): "Интеграция с SIEM по syslog",
	}
	var wantFirst, wantSecond, unrelated kernel.ID
	for id, text := range docs {
		if err := index.Upsert(ctx, signals.IndexKindSignal, id, product, text); err != nil {
			t.Fatal(err)
		}
		switch text {
		case "Экспорт отчёта в формате CSV":
			wantFirst = id
		case "Нужен экспорт отчётов в CSV для аудитора":
			wantSecond = id
		default:
			unrelated = id
		}
	}
	// Тот же текст в другом продукте не должен попадать в выдачу (граница продукта).
	if err := index.Upsert(ctx, signals.IndexKindSignal, kernel.NewID(), other, "Экспорт отчёта в формате CSV"); err != nil {
		t.Fatal(err)
	}
	matches, err := index.Similar(ctx, signals.IndexKindSignal, product, "экспорт отчёта CSV", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].ID != wantFirst || matches[1].ID != wantSecond {
		t.Fatalf("Similar: %+v (first=%s second=%s)", matches, wantFirst, wantSecond)
	}
	for _, m := range matches {
		if m.Score <= 0 || m.Score >= 1 || m.ID == unrelated {
			t.Fatalf("оценка вне (0,1) или нерелевантный документ: %+v", m)
		}
	}
	if matches[0].Score < matches[1].Score {
		t.Fatalf("порядок по убыванию нарушен: %+v", matches)
	}
	if got, err := index.Similar(ctx, signals.IndexKindSignal, product, "экспорт", 1); err != nil || len(got) != 1 {
		t.Fatalf("Similar limit: %+v err=%v", got, err)
	}
	if got, err := index.Similar(ctx, signals.IndexKindSignal, product, "", 10); err != nil || len(got) != 0 {
		t.Fatalf("Similar empty query: %+v err=%v", got, err)
	}
	if got, err := index.Similar(ctx, signals.IndexKindSignal, product, "ничего общего кроме", 10); err != nil || len(got) != 0 {
		t.Fatalf("Similar no overlap: %+v err=%v", got, err)
	}
	// Пустой текст удаляет документ; обновление меняет текст.
	if err := index.Upsert(ctx, signals.IndexKindSignal, wantFirst, product, ""); err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(ctx, signals.IndexKindSignal, wantSecond, product, "Совсем другая тема"); err != nil {
		t.Fatal(err)
	}
	if got, err := index.Similar(ctx, signals.IndexKindSignal, product, "экспорт CSV", 10); err != nil || len(got) != 0 {
		t.Fatalf("Similar after delete/update: %+v err=%v", got, err)
	}
	if err := index.Upsert(ctx, "", wantFirst, product, "x"); err == nil {
		t.Fatal("Upsert без kind должен быть отклонён")
	}
}
