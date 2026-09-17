package signals_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/signals"
)

type memIndexer struct{ docs map[kernel.ID]string }

func (m *memIndexer) Upsert(_ context.Context, _ string, id, _ kernel.ID, text string) error {
	if m.docs == nil {
		m.docs = map[kernel.ID]string{}
	}
	m.docs[id] = text
	return nil
}

func TestSG04_MergeMarksDuplicatesAndKeepsTargetWeight(t *testing.T) {
	f := newFixture(t)
	feat := f.feature(f.edr, "Экспорт в SIEM")
	target := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "экспорт событий в SIEM", AccountID: "a1", AccountARR: kernel.RUB(100_00)})
	dup1 := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "выгрузка в SIEM", AccountID: "a2", AccountARR: kernel.RUB(50_00)})
	dup2 := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "интеграция с SIEM", AccountID: "a3", AccountARR: kernel.RUB(30_00)})
	other := f.ingest(f.cpo, signals.IngestInput{ProductID: f.vm, Text: "чужой продукт"})
	for _, id := range []kernel.ID{target.ID, dup1.ID} {
		if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, id, feat); err != nil {
			t.Fatal(err)
		}
	}
	if ft, _ := f.graph.Feature(f.ctx, f.cpo, feat); ft.OwnValue != kernel.RUB(150_00) {
		t.Fatalf("ценность до слияния: %v", ft.OwnValue)
	}

	cases := []struct {
		name   string
		sc     authz.Scope
		target kernel.ID
		dups   []kernel.ID
		want   error
	}{
		{"пустой список", f.cpo, target.ID, nil, kernel.ErrValidation},
		{"сам себе дубликат", f.cpo, target.ID, []kernel.ID{target.ID}, kernel.ErrValidation},
		{"другой продукт", f.cpo, target.ID, []kernel.ID{other.ID}, kernel.ErrValidation},
		{"PM VM не сливает EDR", pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate}), target.ID, []kernel.ID{dup1.ID}, kernel.ErrForbidden},
		{"нулевой Scope", authz.Scope{}, target.ID, []kernel.ID{dup1.ID}, kernel.ErrForbidden},
		{"успех", f.cpo, target.ID, []kernel.ID{dup1.ID, dup2.ID, dup2.ID}, nil},
		{"повторное слияние", f.cpo, target.ID, []kernel.ID{dup1.ID}, kernel.ErrConflict},
		{"слитый как цель", f.cpo, dup1.ID, []kernel.ID{other.ID}, kernel.ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := f.svc.Merge(f.ctx, c.sc, c.target, c.dups)
			if !errors.Is(err, c.want) {
				t.Fatalf("ожидалось %v, получено %v", c.want, err)
			}
		})
	}
	got, _ := f.svc.Signal(f.ctx, f.cpo, dup1.ID)
	if got.Status != signals.StatusMerged || got.MergedInto != target.ID || got.FeatureID != kernel.NilID {
		t.Fatalf("дубликат после слияния: %+v", got)
	}
	tg, _ := f.svc.Signal(f.ctx, f.cpo, target.ID)
	if tg.Weight != kernel.RUB(100_00) || tg.Status != signals.StatusLinked || tg.UpdatedAt != target.UpdatedAt {
		t.Fatalf("целевой сигнал изменился: %+v", tg)
	}
	// Вес дубликата больше не участвует в ценности фичи — без повторного суммирования.
	if ft, _ := f.graph.Feature(f.ctx, f.cpo, feat); ft.OwnValue != kernel.RUB(100_00) {
		t.Fatalf("ценность после слияния: %v", ft.OwnValue)
	}
	if got := f.pub.count(signals.EventSignalMerged); got != 2 {
		t.Fatalf("событий merged: %d", got)
	}
	if _, err := f.svc.Triage(f.ctx, f.cpo, dup2.ID, signals.TriageInput{Status: signals.StatusInReview}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("слитый сигнал не разбирается: %v", err)
	}
	queue, _ := f.svc.TriageQueue(f.ctx, f.cpo, f.edr)
	for _, s := range queue {
		if s.Status == signals.StatusMerged {
			t.Fatalf("слитый сигнал в очереди: %+v", s)
		}
	}
}

func TestSG04_IngestIndexesSignalText(t *testing.T) {
	f := newFixture(t)
	ix := &memIndexer{}
	f.svc.WithIndexer(ix)
	sig := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "  экспорт в SIEM  "})
	if ix.docs[sig.ID] != "экспорт в SIEM" {
		t.Fatalf("индекс: %v", ix.docs)
	}
}

func TestDS01_LinkToHypothesisWithoutValueRecompute(t *testing.T) {
	f := newFixture(t)
	feat := f.feature(f.edr, "Экспорт")
	hyp := kernel.NewID()
	sig := f.ingest(f.cpo, signals.IngestInput{ProductID: f.edr, Text: "нужно", AccountID: "a1", AccountARR: kernel.RUB(100_00)})
	if _, err := f.svc.LinkToFeature(f.ctx, f.cpo, sig.ID, feat); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		sc   authz.Scope
		hyp  kernel.ID
		want error
	}{
		{"пустая гипотеза", f.cpo, kernel.NilID, kernel.ErrValidation},
		{"PM VM", pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate}), hyp, kernel.ErrForbidden},
		{"успех", f.cpo, hyp, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.svc.LinkToHypothesis(f.ctx, c.sc, sig.ID, c.hyp); !errors.Is(err, c.want) {
				t.Fatalf("ожидалось %v, получено %v", c.want, err)
			}
		})
	}
	got, _ := f.svc.Signal(f.ctx, f.cpo, sig.ID)
	if got.HypothesisID != hyp || got.FeatureID != kernel.NilID || got.Status != signals.StatusLinked {
		t.Fatalf("после привязки: %+v", got)
	}
	// Перепривязка с фичи снимает вес с фичи; гипотеза денежной оценки не несёт.
	if ft, _ := f.graph.Feature(f.ctx, f.cpo, feat); !ft.OwnValue.IsZero() {
		t.Fatalf("ценность фичи после перепривязки: %v", ft.OwnValue)
	}
	list, err := f.svc.SignalsByHypothesis(f.ctx, f.cpo, hyp)
	if err != nil || len(list) != 1 || list[0].ID != sig.ID {
		t.Fatalf("по гипотезе: %v %v", list, err)
	}
	pm := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if list, err := f.svc.SignalsByHypothesis(f.ctx, pm, hyp); err != nil || len(list) != 0 {
		t.Fatalf("PM VM видит сигналы EDR: %v %v", list, err)
	}
	if _, err := f.svc.SignalsByHypothesis(f.ctx, authz.Scope{}, hyp); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: %v", err)
	}
}
