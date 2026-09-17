package discovery_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/discovery"
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

// memDecisions — заглушка порта решений: kind/id → решения.
type memDecisions struct {
	links map[discovery.TraceRef][]discovery.DecisionRef
}

func (m *memDecisions) DecisionsFor(_ context.Context, _ authz.Scope, kind string, id kernel.ID) ([]discovery.DecisionRef, error) {
	return m.links[discovery.TraceRef{Kind: discovery.TraceKind(kind), ID: id}], nil
}

type fixture struct {
	t     *testing.T
	ctx   context.Context
	graph *pg.Service
	sig   *signals.Service
	svc   *discovery.Service
	pub   *memPub
	dec   *memDecisions
	cpo   authz.Scope
	admin authz.Scope
	// портфель ИБ
	vm, edr kernel.ID
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func adminScope() authz.Scope {
	return authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
}

func pmScope(subject string, private map[kernel.ID]authz.Access) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM}, Products: private, Audience: authz.AudienceInternal})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub := &memPub{}
	clock := kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	graph := pg.NewService(pg.NewMemStore(), pub, clock)
	index := discovery.NewMemIndex()
	sig := signals.NewService(signals.NewMemStore(), graph, pub, clock).WithIndexer(index)
	dec := &memDecisions{links: map[discovery.TraceRef][]discovery.DecisionRef{}}
	svc := discovery.NewService(discovery.NewMemStore(), pub, clock,
		discovery.WithSignals(sig), discovery.WithSignalMerger(sig), discovery.WithSignalLinker(sig),
		discovery.WithFeatures(graph), discovery.WithDecisions(dec), discovery.WithIndex(index))
	f := &fixture{t: t, ctx: context.Background(), graph: graph, sig: sig, svc: svc, pub: pub, dec: dec, cpo: cpoScope(), admin: adminScope()}
	f.vm = f.product("vm", "VM")
	f.edr = f.product("edr", "EDR")
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

func (f *fixture) hypothesis(product kernel.ID, title string, feature kernel.ID) discovery.Hypothesis {
	f.t.Helper()
	h, err := f.svc.SaveHypothesis(f.ctx, f.cpo, discovery.HypothesisInput{
		ProductID: product, Title: title, Statement: "мы считаем, что " + title,
		Assumptions: []string{"клиенты используют SIEM", " "}, ConfirmationCriterion: "3 из 5 интервью", FeatureID: feature,
	})
	if err != nil {
		f.t.Fatalf("hypothesis %s: %v", title, err)
	}
	return h
}

func (f *fixture) signal(product kernel.ID, text string) signals.Signal {
	f.t.Helper()
	s, err := f.sig.Ingest(f.ctx, f.cpo, signals.IngestInput{ProductID: product, Source: signals.SourceManual, Text: text})
	if err != nil {
		f.t.Fatalf("signal %q: %v", text, err)
	}
	return s
}

func TestDS01_HypothesisFieldsAndStatusTransitions(t *testing.T) {
	f := newFixture(t)
	feat := f.feature(f.edr, "Экспорт в SIEM")
	h := f.hypothesis(f.edr, "экспорт нужен enterprise", feat)
	if h.Status != discovery.HypothesisDraft || len(h.Assumptions) != 1 || h.FeatureID != feat || h.CreatedBy != "cpo" {
		t.Fatalf("гипотеза: %+v", h)
	}
	// Валидация обязательных полей и фичи чужого продукта.
	bad := []struct {
		name string
		in   discovery.HypothesisInput
	}{
		{"без продукта", discovery.HypothesisInput{Title: "t", Statement: "s", ConfirmationCriterion: "c"}},
		{"без названия", discovery.HypothesisInput{ProductID: f.edr, Statement: "s", ConfirmationCriterion: "c"}},
		{"без критерия", discovery.HypothesisInput{ProductID: f.edr, Title: "t", Statement: "s"}},
		{"фича другого продукта", discovery.HypothesisInput{ProductID: f.vm, Title: "t", Statement: "s", ConfirmationCriterion: "c", FeatureID: feat}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.svc.SaveHypothesis(f.ctx, f.cpo, c.in); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("ожидалась ошибка валидации, получено %v", err)
			}
		})
	}
	// Обновление сохраняет статус и автора, не меняет продукт.
	upd, err := f.svc.SaveHypothesis(f.ctx, f.cpo, discovery.HypothesisInput{ID: h.ID, ProductID: f.edr, Title: "новое", Statement: "s", ConfirmationCriterion: "c"})
	if err != nil || upd.ID != h.ID || upd.Title != "новое" || upd.Status != discovery.HypothesisDraft || upd.CreatedBy != "cpo" {
		t.Fatalf("обновление: %+v %v", upd, err)
	}
	if _, err := f.svc.SaveHypothesis(f.ctx, f.cpo, discovery.HypothesisInput{ID: h.ID, ProductID: f.vm, Title: "t", Statement: "s", ConfirmationCriterion: "c"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("смена продукта: %v", err)
	}

	steps := []struct {
		name string
		ch   discovery.StatusChange
		want error
	}{
		{"draft → confirmed запрещён", discovery.StatusChange{Status: discovery.HypothesisConfirmed, Resolution: "x"}, kernel.ErrConflict},
		{"неизвестный статус", discovery.StatusChange{Status: "wild"}, kernel.ErrValidation},
		{"draft → testing", discovery.StatusChange{Status: discovery.HypothesisTesting}, nil},
		{"testing → confirmed без причины", discovery.StatusChange{Status: discovery.HypothesisConfirmed}, kernel.ErrValidation},
		{"testing → confirmed", discovery.StatusChange{Status: discovery.HypothesisConfirmed, Resolution: "4 из 5 интервью"}, nil},
		{"confirmed → testing запрещён", discovery.StatusChange{Status: discovery.HypothesisTesting}, kernel.ErrConflict},
		{"confirmed → draft (переоткрытие)", discovery.StatusChange{Status: discovery.HypothesisDraft}, nil},
		{"draft → rejected", discovery.StatusChange{Status: discovery.HypothesisRejected, Resolution: "не подтвердилось"}, nil},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			got, err := f.svc.ChangeHypothesisStatus(f.ctx, f.cpo, h.ID, s.ch)
			if !errors.Is(err, s.want) {
				t.Fatalf("ожидалось %v, получено %v", s.want, err)
			}
			if err == nil && (got.Status != s.ch.Status || got.Resolution != strings.TrimSpace(s.ch.Resolution)) {
				t.Fatalf("статус: %+v", got)
			}
		})
	}
	if f.pub.count(discovery.EventHypothesisSaved) != 2+4 {
		t.Fatalf("событий hypothesis.saved: %d", f.pub.count(discovery.EventHypothesisSaved))
	}
	list, err := f.svc.Hypotheses(f.ctx, f.cpo, f.edr, discovery.HypothesisFilter{Statuses: []discovery.HypothesisStatus{discovery.HypothesisRejected}})
	if err != nil || len(list) != 1 {
		t.Fatalf("фильтр по статусу: %v %v", list, err)
	}
}

func TestDS01_SignalLinkedToHypothesisOfSameProduct(t *testing.T) {
	f := newFixture(t)
	h := f.hypothesis(f.edr, "h", kernel.NilID)
	own := f.signal(f.edr, "свой")
	alien := f.signal(f.vm, "чужой")
	if _, err := f.svc.LinkSignalToHypothesis(f.ctx, f.cpo, alien.ID, h.ID); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("сигнал другого продукта: %v", err)
	}
	got, err := f.svc.LinkSignalToHypothesis(f.ctx, f.cpo, own.ID, h.ID)
	if err != nil || got.HypothesisID != h.ID || got.Status != signals.StatusLinked {
		t.Fatalf("привязка: %+v %v", got, err)
	}
}

func TestDS02_InterviewAndInsightBindings(t *testing.T) {
	f := newFixture(t)
	h := f.hypothesis(f.edr, "h", kernel.NilID)
	hVM := f.hypothesis(f.vm, "vm", kernel.NilID)
	sig := f.signal(f.edr, "нужен экспорт")
	if _, err := f.svc.SaveInterview(f.ctx, f.cpo, discovery.InterviewInput{ProductID: f.edr, AccountID: "acc-1"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("интервью без даты: %v", err)
	}
	if _, err := f.svc.SaveInterview(f.ctx, f.cpo, discovery.InterviewInput{ProductID: f.edr, Date: kernel.DateOf(2026, 9, 1), HypothesisIDs: []kernel.ID{hVM.ID}}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("гипотеза другого продукта: %v", err)
	}
	iv, err := f.svc.SaveInterview(f.ctx, f.cpo, discovery.InterviewInput{
		ProductID: f.edr, AccountID: "acc-1", Segment: "enterprise", Date: kernel.DateOf(2026, 9, 1),
		Participants: []string{"CISO", ""}, Notes: "заметки", HypothesisIDs: []kernel.ID{h.ID, h.ID},
	})
	if err != nil || iv.AccountID != "acc-1" || iv.Segment != "enterprise" || len(iv.Participants) != 1 || len(iv.HypothesisIDs) != 1 {
		t.Fatalf("интервью: %+v %v", iv, err)
	}
	cases := []struct {
		name string
		in   discovery.InsightInput
		want error
	}{
		{"без текста", discovery.InsightInput{ProductID: f.edr, Confidence: discovery.ConfidenceLow}, kernel.ErrValidation},
		{"уверенность", discovery.InsightInput{ProductID: f.edr, Text: "t", Confidence: "sure"}, kernel.ErrValidation},
		{"интервью другого продукта", discovery.InsightInput{ProductID: f.vm, Text: "t", Confidence: discovery.ConfidenceLow, InterviewID: iv.ID}, kernel.ErrValidation},
		{"сигнал другого продукта", discovery.InsightInput{ProductID: f.vm, Text: "t", Confidence: discovery.ConfidenceLow, SignalIDs: []kernel.ID{sig.ID}}, kernel.ErrValidation},
		{"неизвестный сигнал", discovery.InsightInput{ProductID: f.edr, Text: "t", Confidence: discovery.ConfidenceLow, SignalIDs: []kernel.ID{kernel.NewID()}}, kernel.ErrNotFound},
		{"успех", discovery.InsightInput{ProductID: f.edr, Text: "экспорт критичен", Confidence: discovery.ConfidenceHigh, InterviewID: iv.ID, HypothesisIDs: []kernel.ID{h.ID}, SignalIDs: []kernel.ID{sig.ID}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.svc.SaveInsight(f.ctx, f.cpo, c.in); !errors.Is(err, c.want) {
				t.Fatalf("ожидалось %v, получено %v", c.want, err)
			}
		})
	}
	byHyp, err := f.svc.Insights(f.ctx, f.cpo, f.edr, discovery.InsightFilter{HypothesisID: h.ID})
	if err != nil || len(byHyp) != 1 || byHyp[0].InterviewID != iv.ID {
		t.Fatalf("инсайты по гипотезе: %v %v", byHyp, err)
	}
	if f.pub.count(discovery.EventInterviewSaved) != 1 || f.pub.count(discovery.EventInsightSaved) != 1 {
		t.Fatalf("события: interview=%d insight=%d", f.pub.count(discovery.EventInterviewSaved), f.pub.count(discovery.EventInsightSaved))
	}
}

func TestDS03_EvidenceDefaultsToUnverified(t *testing.T) {
	f := newFixture(t)
	h := f.hypothesis(f.edr, "h", kernel.NilID)
	feat := f.feature(f.vm, "vm feature")
	base := discovery.EvidenceInput{ProductID: f.edr, Source: discovery.EvidenceSourceInterview, Date: kernel.DateOf(2026, 9, 1), Trust: discovery.ConfidenceMedium, HypothesisID: h.ID}
	cases := []struct {
		name string
		mod  func(*discovery.EvidenceInput)
		want error
	}{
		{"успех по умолчанию", func(*discovery.EvidenceInput) {}, nil},
		{"без источника", func(e *discovery.EvidenceInput) { e.Source = " " }, kernel.ErrValidation},
		{"без даты", func(e *discovery.EvidenceInput) { e.Date = kernel.Date{} }, kernel.ErrValidation},
		{"доверие", func(e *discovery.EvidenceInput) { e.Trust = "absolute" }, kernel.ErrValidation},
		{"статус проверки", func(e *discovery.EvidenceInput) { e.Verification = "maybe" }, kernel.ErrValidation},
		{"короткий хеш", func(e *discovery.EvidenceInput) { e.SHA256 = "abcd" }, kernel.ErrValidation},
		{"не hex", func(e *discovery.EvidenceInput) { e.SHA256 = strings.Repeat("zz", 32) }, kernel.ErrValidation},
		{"хеш верный", func(e *discovery.EvidenceInput) { e.SHA256 = strings.Repeat("AB", 32) }, nil},
		{"фича другого продукта", func(e *discovery.EvidenceInput) { e.FeatureID = feat }, kernel.ErrValidation},
		{"неизвестный инсайт", func(e *discovery.EvidenceInput) { e.InsightID = kernel.NewID() }, kernel.ErrNotFound},
		{"проверено", func(e *discovery.EvidenceInput) { e.Verification = discovery.VerificationVerified }, nil},
	}
	var saved []discovery.Evidence
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mod(&in)
			e, err := f.svc.SaveEvidence(f.ctx, f.cpo, in)
			if !errors.Is(err, c.want) {
				t.Fatalf("ожидалось %v, получено %v", c.want, err)
			}
			if err == nil {
				saved = append(saved, e)
			}
		})
	}
	if len(saved) != 3 || saved[0].Verification != discovery.VerificationUnverified || saved[1].SHA256 != strings.Repeat("ab", 32) || saved[2].Verification != discovery.VerificationVerified {
		t.Fatalf("сохранённые evidence: %+v", saved)
	}
	unverified, err := f.svc.EvidenceList(f.ctx, f.cpo, f.edr, discovery.EvidenceFilter{HypothesisID: h.ID, Verification: discovery.VerificationUnverified})
	if err != nil || len(unverified) != 2 {
		t.Fatalf("непроверенные: %v %v", unverified, err)
	}
	if f.pub.count(discovery.EventEvidenceSaved) != 3 {
		t.Fatalf("событий evidence.saved: %d", f.pub.count(discovery.EventEvidenceSaved))
	}
}

func TestDS04_TraceBothDirections(t *testing.T) {
	f := newFixture(t)
	feat := f.feature(f.edr, "Экспорт в SIEM")
	h := f.hypothesis(f.edr, "экспорт нужен", feat)
	sigA := f.signal(f.edr, "экспорт в SIEM")
	sigB := f.signal(f.edr, "выгрузка событий")
	if _, err := f.svc.LinkSignalToHypothesis(f.ctx, f.cpo, sigB.ID, h.ID); err != nil {
		t.Fatal(err)
	}
	ins, err := f.svc.SaveInsight(f.ctx, f.cpo, discovery.InsightInput{ProductID: f.edr, Text: "нужен экспорт", Confidence: discovery.ConfidenceHigh, HypothesisIDs: []kernel.ID{h.ID}, SignalIDs: []kernel.ID{sigA.ID}})
	if err != nil {
		t.Fatal(err)
	}
	decision := discovery.DecisionRef{ID: kernel.NewID(), Title: "делаем экспорт"}
	f.dec.links[discovery.TraceRef{Kind: discovery.TraceFeature, ID: feat}] = []discovery.DecisionRef{decision}

	kinds := func(g discovery.TraceGraph) map[discovery.TraceKind]int {
		m := map[discovery.TraceKind]int{}
		for _, n := range g.Nodes {
			m[n.Kind]++
		}
		return m
	}
	hasEdge := func(g discovery.TraceGraph, from, to discovery.TraceRef) bool {
		for _, e := range g.Edges {
			if e.From == from && e.To == to {
				return true
			}
		}
		return false
	}
	starts := []struct {
		name string
		kind discovery.TraceKind
		id   kernel.ID
	}{
		{"от сигнала", discovery.TraceSignal, sigA.ID},
		{"от инсайта", discovery.TraceInsight, ins.ID},
		{"от гипотезы", discovery.TraceHypothesis, h.ID},
		{"от фичи", discovery.TraceFeature, feat},
	}
	for _, s := range starts {
		t.Run(s.name, func(t *testing.T) {
			g, err := f.svc.Trace(f.ctx, f.cpo, s.kind, s.id)
			if err != nil {
				t.Fatal(err)
			}
			k := kinds(g)
			if k[discovery.TraceSignal] != 2 || k[discovery.TraceInsight] != 1 || k[discovery.TraceHypothesis] != 1 || k[discovery.TraceFeature] != 1 || k[discovery.TraceDecision] != 1 {
				t.Fatalf("узлы: %v", k)
			}
			if len(g.Edges) != 5 ||
				!hasEdge(g, discovery.TraceRef{Kind: discovery.TraceSignal, ID: sigA.ID}, discovery.TraceRef{Kind: discovery.TraceInsight, ID: ins.ID}) ||
				!hasEdge(g, discovery.TraceRef{Kind: discovery.TraceSignal, ID: sigB.ID}, discovery.TraceRef{Kind: discovery.TraceHypothesis, ID: h.ID}) ||
				!hasEdge(g, discovery.TraceRef{Kind: discovery.TraceInsight, ID: ins.ID}, discovery.TraceRef{Kind: discovery.TraceHypothesis, ID: h.ID}) ||
				!hasEdge(g, discovery.TraceRef{Kind: discovery.TraceHypothesis, ID: h.ID}, discovery.TraceRef{Kind: discovery.TraceFeature, ID: feat}) ||
				!hasEdge(g, discovery.TraceRef{Kind: discovery.TraceFeature, ID: feat}, discovery.TraceRef{Kind: discovery.TraceDecision, ID: decision.ID}) {
				t.Fatalf("рёбра: %+v", g.Edges)
			}
			if g.Root.Kind != s.kind || g.Nodes[0].TraceRef != g.Root {
				t.Fatalf("корень: %+v", g.Root)
			}
		})
	}
	// Без порта решений граф строится без узла решения.
	bare := discovery.NewService(discovery.NewMemStore(), f.pub, kernel.FixedClock{T: time.Now()}, discovery.WithFeatures(f.graph))
	hh, err := bare.SaveHypothesis(f.ctx, f.cpo, discovery.HypothesisInput{ProductID: f.edr, Title: "t", Statement: "s", ConfirmationCriterion: "c", FeatureID: feat})
	if err != nil {
		t.Fatal(err)
	}
	g, err := bare.Trace(f.ctx, f.cpo, discovery.TraceFeature, feat)
	if err != nil || len(g.Nodes) != 2 || kinds(g)[discovery.TraceDecision] != 0 || g.Nodes[1].ID != hh.ID {
		t.Fatalf("без решений: %+v %v", g, err)
	}
	if _, err := f.svc.Trace(f.ctx, f.cpo, discovery.TraceDecision, decision.ID); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("от решения: %v", err)
	}
	if _, err := f.svc.Trace(f.ctx, f.cpo, discovery.TraceHypothesis, kernel.NewID()); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("неизвестный корень: %v", err)
	}
}

func TestSG04_MemIndexRanksByCosine(t *testing.T) {
	ix := discovery.NewMemIndex()
	ctx := context.Background()
	prod, other := kernel.NewID(), kernel.NewID()
	docs := map[string]kernel.ID{
		"экспорт событий в SIEM":           kernel.NewID(),
		"выгрузка событий во внешний SIEM": kernel.NewID(),
		"поддержка ARM-процессоров":        kernel.NewID(),
	}
	for text, id := range docs {
		if err := ix.Upsert(ctx, "signal", id, prod, text); err != nil {
			t.Fatal(err)
		}
	}
	if err := ix.Upsert(ctx, "signal", kernel.NewID(), other, "экспорт событий в SIEM"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		query string
		limit int
		first kernel.ID
		n     int
	}{
		{"ближайший — тот же текст", "экспорт событий в SIEM", 10, docs["экспорт событий в SIEM"], 2},
		{"лимит", "события SIEM", 1, docs["экспорт событий в SIEM"], 1},
		{"нет пересечения", "лицензирование", 10, kernel.NilID, 0},
		{"пустой запрос", "  ", 10, kernel.NilID, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ix.Similar(ctx, "signal", prod, c.query, c.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.n {
				t.Fatalf("совпадений %d, ожидалось %d: %+v", len(got), c.n, got)
			}
			if c.n > 0 && (got[0].ID != c.first || got[0].Score <= 0 || got[0].Score > 1.000001) {
				t.Fatalf("первое совпадение: %+v", got)
			}
			for i := 1; i < len(got); i++ {
				if got[i].Score > got[i-1].Score {
					t.Fatalf("не отсортировано: %+v", got)
				}
			}
		})
	}
	if err := ix.Upsert(ctx, "", kernel.NewID(), prod, "x"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустой вид: %v", err)
	}
	// Пустой текст удаляет документ.
	if err := ix.Upsert(ctx, "signal", docs["экспорт событий в SIEM"], prod, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := ix.Similar(ctx, "signal", prod, "экспорт событий в SIEM", 10); len(got) != 1 {
		t.Fatalf("после удаления: %+v", got)
	}
}

func TestSG04_SimilarSignalsAndMergeThroughPorts(t *testing.T) {
	f := newFixture(t)
	a := f.signal(f.edr, "экспорт событий в SIEM")
	b := f.signal(f.edr, "выгрузка событий во внешний SIEM")
	c := f.signal(f.edr, "поддержка ARM")
	f.signal(f.vm, "экспорт событий в SIEM") // другой продукт — не подсказывается
	got, err := f.svc.SimilarSignals(f.ctx, f.cpo, a.ID, 5)
	if err != nil || len(got) != 1 || got[0].Signal.ID != b.ID {
		t.Fatalf("похожие: %+v %v", got, err)
	}
	if _, err := f.svc.SimilarSignals(f.ctx, f.cpo, a.ID, 0); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("limit: %v", err)
	}
	pm := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if _, err := f.svc.SimilarSignals(f.ctx, pm, a.ID, 5); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM VM: %v", err)
	}
	if err := f.svc.MergeSignals(f.ctx, pm, a.ID, []kernel.ID{b.ID}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("слияние PM VM: %v", err)
	}
	if err := f.svc.MergeSignals(f.ctx, f.cpo, a.ID, []kernel.ID{b.ID, c.ID}); err != nil {
		t.Fatal(err)
	}
	merged, _ := f.sig.Signal(f.ctx, f.cpo, b.ID)
	if merged.Status != signals.StatusMerged || merged.MergedInto != a.ID {
		t.Fatalf("после слияния: %+v", merged)
	}
	// Слитые сигналы больше не подсказываются.
	if got, err := f.svc.SimilarSignals(f.ctx, f.cpo, a.ID, 5); err != nil || len(got) != 0 {
		t.Fatalf("после слияния: %+v %v", got, err)
	}
	// Без портов — ErrUnavailable.
	bare := discovery.NewService(discovery.NewMemStore(), f.pub, kernel.FixedClock{T: time.Now()})
	if _, err := bare.SimilarSignals(f.ctx, f.cpo, a.ID, 5); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("без порта: %v", err)
	}
	if err := bare.MergeSignals(f.ctx, f.cpo, a.ID, []kernel.ID{c.ID}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("без порта: %v", err)
	}
}

func TestAD03_CustomFieldsAndStatuses(t *testing.T) {
	f := newFixture(t)
	defs := []struct {
		name string
		def  discovery.CustomFieldDef
		want error
	}{
		{"ключ с заглавной", discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "Segment", Label: "l", Type: discovery.FieldString}, kernel.ErrValidation},
		{"неизвестная сущность", discovery.CustomFieldDef{Entity: "release", Key: "k", Label: "l", Type: discovery.FieldString}, kernel.ErrValidation},
		{"enum без вариантов", discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "k", Label: "l", Type: discovery.FieldEnum}, kernel.ErrValidation},
		{"segment enum", discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "segment", Label: "Сегмент", Type: discovery.FieldEnum, Options: []string{"smb", "enterprise"}, Required: true}, nil},
		{"budget number", discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "budget", Label: "Бюджет", Type: discovery.FieldNumber}, nil},
		{"review date", discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "review_at", Label: "Ревизия", Type: discovery.FieldDate}, nil},
		{"owner string", discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "owner", Label: "Владелец", Type: discovery.FieldString}, nil},
		{"поле фичи", discovery.CustomFieldDef{Entity: discovery.EntityFeature, Key: "tier", Label: "Tier", Type: discovery.FieldString}, nil},
	}
	for _, d := range defs {
		t.Run(d.name, func(t *testing.T) {
			if _, err := f.svc.DefineCustomField(f.ctx, f.admin, d.def); !errors.Is(err, d.want) {
				t.Fatalf("ожидалось %v, получено %v", d.want, err)
			}
		})
	}
	if _, err := f.svc.DefineCustomField(f.ctx, f.cpo, discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "x", Label: "l", Type: discovery.FieldString}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("CPO не администрирует настройки: %v", err)
	}
	// Повторное определение с тем же ключом обновляет, а не дублирует.
	if _, err := f.svc.DefineCustomField(f.ctx, f.admin, discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "owner", Label: "Ответственный", Type: discovery.FieldString}); err != nil {
		t.Fatal(err)
	}
	fields, err := f.svc.CustomFields(f.ctx, f.cpo, discovery.EntityHypothesis)
	if err != nil || len(fields) != 4 || fields[3].Label != "Ответственный" {
		t.Fatalf("определения: %+v %v", fields, err)
	}

	values := []struct {
		name string
		v    map[string]any
		want error
	}{
		{"нет обязательного", map[string]any{}, kernel.ErrValidation},
		{"неизвестный ключ", map[string]any{"segment": "smb", "color": "red"}, kernel.ErrValidation},
		{"enum вне вариантов", map[string]any{"segment": "gov"}, kernel.ErrValidation},
		{"число строкой", map[string]any{"segment": "smb", "budget": "10"}, kernel.ErrValidation},
		{"дата неверная", map[string]any{"segment": "smb", "review_at": "вчера"}, kernel.ErrValidation},
		{"строка числом", map[string]any{"segment": "smb", "owner": 1}, kernel.ErrValidation},
		{"всё верно", map[string]any{"segment": "enterprise", "budget": 12.5, "review_at": "2026-12-01", "owner": "pm"}, nil},
		{"дата типом", map[string]any{"segment": "enterprise", "review_at": kernel.DateOf(2026, 12, 1), "budget": int64(3)}, nil},
	}
	for _, c := range values {
		t.Run(c.name, func(t *testing.T) {
			if err := f.svc.ValidateCustomFields(f.ctx, discovery.EntityHypothesis, c.v); !errors.Is(err, c.want) {
				t.Fatalf("ожидалось %v, получено %v", c.want, err)
			}
			_, err := f.svc.SaveHypothesis(f.ctx, f.cpo, discovery.HypothesisInput{ProductID: f.edr, Title: "t", Statement: "s", ConfirmationCriterion: "c", CustomFields: c.v})
			if !errors.Is(err, c.want) {
				t.Fatalf("сохранение гипотезы: ожидалось %v, получено %v", c.want, err)
			}
		})
	}
	// Для feature — публичная функция валидации по определениям.
	featDefs, _ := f.svc.CustomFields(f.ctx, f.cpo, discovery.EntityFeature)
	if err := discovery.ValidateValues(featDefs, map[string]any{"tier": "gold"}); err != nil {
		t.Fatalf("feature: %v", err)
	}
	if err := discovery.ValidateValues(featDefs, map[string]any{"segment": "smb"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("feature: чужой ключ: %v", err)
	}

	// Пользовательский статус отображён на категорию и участвует в переходах.
	statuses := []struct {
		name string
		def  discovery.CustomStatusDef
		want error
	}{
		{"категория вне списка", discovery.CustomStatusDef{Entity: discovery.EntityHypothesis, Key: "parked", Label: "l", Category: "frozen"}, kernel.ErrValidation},
		{"ключ встроенный", discovery.CustomStatusDef{Entity: discovery.EntityHypothesis, Key: "draft", Label: "l", Category: "draft"}, kernel.ErrValidation},
		{"validated → confirmed", discovery.CustomStatusDef{Entity: discovery.EntityHypothesis, Key: "validated", Label: "Подтверждена", Category: "confirmed"}, nil},
		{"in_research → testing", discovery.CustomStatusDef{Entity: discovery.EntityHypothesis, Key: "in_research", Label: "В исследовании", Category: "testing"}, nil},
	}
	for _, s := range statuses {
		t.Run(s.name, func(t *testing.T) {
			if _, err := f.svc.DefineCustomStatus(f.ctx, f.admin, s.def); !errors.Is(err, s.want) {
				t.Fatalf("ожидалось %v, получено %v", s.want, err)
			}
		})
	}
	if _, err := f.svc.DefineCustomStatus(f.ctx, f.cpo, discovery.CustomStatusDef{Entity: discovery.EntityHypothesis, Key: "k", Label: "l", Category: "draft"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("CPO не администрирует статусы: %v", err)
	}
	h, err := f.svc.SaveHypothesis(f.ctx, f.cpo, discovery.HypothesisInput{ProductID: f.edr, Title: "t", Statement: "s", ConfirmationCriterion: "c", CustomFields: map[string]any{"segment": "smb"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ChangeHypothesisStatus(f.ctx, f.cpo, h.ID, discovery.StatusChange{Status: "validated", Resolution: "r"}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("draft → validated(confirmed): %v", err)
	}
	if _, err := f.svc.ChangeHypothesisStatus(f.ctx, f.cpo, h.ID, discovery.StatusChange{Status: "in_research"}); err != nil {
		t.Fatalf("draft → in_research(testing): %v", err)
	}
	if _, err := f.svc.ChangeHypothesisStatus(f.ctx, f.cpo, h.ID, discovery.StatusChange{Status: "validated"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("validated без причины: %v", err)
	}
	got, err := f.svc.ChangeHypothesisStatus(f.ctx, f.cpo, h.ID, discovery.StatusChange{Status: "validated", Resolution: "подтверждено"})
	if err != nil || got.Status != "validated" {
		t.Fatalf("in_research → validated: %+v %v", got, err)
	}
	list, _ := f.svc.CustomStatuses(f.ctx, f.cpo, discovery.EntityHypothesis)
	if len(list) != 2 {
		t.Fatalf("статусов: %d", len(list))
	}
}

func TestABAC_PMOfVMDoesNotSeeEDRDiscovery(t *testing.T) {
	f := newFixture(t)
	feat := f.feature(f.edr, "f")
	h := f.hypothesis(f.edr, "h", feat)
	iv, err := f.svc.SaveInterview(f.ctx, f.cpo, discovery.InterviewInput{ProductID: f.edr, Date: kernel.DateOf(2026, 9, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := f.svc.SaveInsight(f.ctx, f.cpo, discovery.InsightInput{ProductID: f.edr, Text: "t", Confidence: discovery.ConfidenceLow})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := f.svc.SaveEvidence(f.ctx, f.cpo, discovery.EvidenceInput{ProductID: f.edr, Source: "manual", Date: kernel.DateOf(2026, 9, 1), Trust: discovery.ConfidenceLow})
	if err != nil {
		t.Fatal(err)
	}
	sig := f.signal(f.edr, "s")
	pm := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	marketingStrategic := authz.New(authz.Params{Subject: "mkt", Roles: []authz.Role{authz.RoleMarketing}, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessStrategic}})
	scopes := map[string]authz.Scope{"PM VM": pm, "marketing со стратегическим срезом": marketingStrategic, "нулевой": {}}
	ops := []struct {
		name string
		call func(sc authz.Scope) error
	}{
		{"гипотеза", func(sc authz.Scope) error { _, err := f.svc.Hypothesis(f.ctx, sc, h.ID); return err }},
		{"список гипотез", func(sc authz.Scope) error {
			_, err := f.svc.Hypotheses(f.ctx, sc, f.edr, discovery.HypothesisFilter{})
			return err
		}},
		{"создание гипотезы", func(sc authz.Scope) error {
			_, err := f.svc.SaveHypothesis(f.ctx, sc, discovery.HypothesisInput{ProductID: f.edr, Title: "t", Statement: "s", ConfirmationCriterion: "c"})
			return err
		}},
		{"смена статуса", func(sc authz.Scope) error {
			_, err := f.svc.ChangeHypothesisStatus(f.ctx, sc, h.ID, discovery.StatusChange{Status: discovery.HypothesisTesting})
			return err
		}},
		{"интервью", func(sc authz.Scope) error { _, err := f.svc.Interview(f.ctx, sc, iv.ID); return err }},
		{"список интервью", func(sc authz.Scope) error { _, err := f.svc.Interviews(f.ctx, sc, f.edr); return err }},
		{"создание интервью", func(sc authz.Scope) error {
			_, err := f.svc.SaveInterview(f.ctx, sc, discovery.InterviewInput{ProductID: f.edr, Date: kernel.DateOf(2026, 9, 1)})
			return err
		}},
		{"инсайт", func(sc authz.Scope) error { _, err := f.svc.Insight(f.ctx, sc, ins.ID); return err }},
		{"список инсайтов", func(sc authz.Scope) error {
			_, err := f.svc.Insights(f.ctx, sc, f.edr, discovery.InsightFilter{})
			return err
		}},
		{"создание инсайта", func(sc authz.Scope) error {
			_, err := f.svc.SaveInsight(f.ctx, sc, discovery.InsightInput{ProductID: f.edr, Text: "t", Confidence: discovery.ConfidenceLow})
			return err
		}},
		{"evidence", func(sc authz.Scope) error { _, err := f.svc.Evidence(f.ctx, sc, ev.ID); return err }},
		{"список evidence", func(sc authz.Scope) error {
			_, err := f.svc.EvidenceList(f.ctx, sc, f.edr, discovery.EvidenceFilter{})
			return err
		}},
		{"создание evidence", func(sc authz.Scope) error {
			_, err := f.svc.SaveEvidence(f.ctx, sc, discovery.EvidenceInput{ProductID: f.edr, Source: "manual", Date: kernel.DateOf(2026, 9, 1), Trust: discovery.ConfidenceLow})
			return err
		}},
		{"трассировка", func(sc authz.Scope) error {
			_, err := f.svc.Trace(f.ctx, sc, discovery.TraceHypothesis, h.ID)
			return err
		}},
		{"привязка сигнала", func(sc authz.Scope) error {
			_, err := f.svc.LinkSignalToHypothesis(f.ctx, sc, sig.ID, h.ID)
			return err
		}},
		{"похожие сигналы", func(sc authz.Scope) error { _, err := f.svc.SimilarSignals(f.ctx, sc, sig.ID, 3); return err }},
		{"настройка поля", func(sc authz.Scope) error {
			_, err := f.svc.DefineCustomField(f.ctx, sc, discovery.CustomFieldDef{Entity: discovery.EntityHypothesis, Key: "k", Label: "l", Type: discovery.FieldString})
			return err
		}},
	}
	for scName, sc := range scopes {
		for _, op := range ops {
			t.Run(scName+"/"+op.name, func(t *testing.T) {
				if err := op.call(sc); !errors.Is(err, kernel.ErrForbidden) {
					t.Fatalf("ожидался отказ, получено %v", err)
				}
			})
		}
	}
	// Marketing с приватным доступом пишет discovery своего продукта; чужие фичи в трассировке скрыты.
	marketing := authz.New(authz.Params{Subject: "mkt", Roles: []authz.Role{authz.RoleMarketing}, Products: map[kernel.ID]authz.Access{f.edr: authz.AccessPrivate}})
	if _, err := f.svc.SaveHypothesis(f.ctx, marketing, discovery.HypothesisInput{ProductID: f.edr, Title: "t", Statement: "s", ConfirmationCriterion: "c"}); err != nil {
		t.Fatalf("marketing пишет discovery: %v", err)
	}
	if _, err := f.svc.CustomFields(f.ctx, authz.Scope{}, discovery.EntityHypothesis); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope читает настройки: %v", err)
	}
}
