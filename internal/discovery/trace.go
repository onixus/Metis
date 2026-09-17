package discovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/signals"
)

// ValidTraceKind сообщает, известен ли вид узла.
func ValidTraceKind(k TraceKind) bool {
	switch k {
	case TraceSignal, TraceInsight, TraceHypothesis, TraceFeature, TraceDecision:
		return true
	}
	return false
}

// tracer — состояние одного обхода: накопленные узлы и рёбра, очередь.
type tracer struct {
	s     *Service
	sc    authz.Scope
	nodes map[TraceRef]TraceNode
	edges map[TraceEdge]struct{}
	order []TraceRef
	queue []TraceRef
	// hidden — причина, по которой последний узел не раскрыт (нет доступа или не найден).
	hidden error
}

// Trace строит граф «сигнал → инсайт → гипотеза → фича → решение» вокруг узла, обходя связи
// в обе стороны (DS-04). Узлы продуктов, на которые нет приватного доступа, не раскрываются.
// Решения — листья через порт DecisionLinks; без порта они не включаются. Обход от решения
// требует обратного порта модуля decisions (см. вопрос 13 в docs/questions.md).
func (s *Service) Trace(ctx context.Context, sc authz.Scope, kind TraceKind, id kernel.ID) (TraceGraph, error) {
	if !ValidTraceKind(kind) {
		return TraceGraph{}, kernel.Invalid("kind", fmt.Sprintf("неизвестный вид узла %q", kind))
	}
	if kind == TraceDecision {
		return TraceGraph{}, kernel.Invalid("kind", "трассировка от решения — через модуль decisions")
	}
	if id == kernel.NilID {
		return TraceGraph{}, kernel.Invalid("id", "обязателен")
	}
	root := TraceRef{Kind: kind, ID: id}
	t := &tracer{s: s, sc: sc, nodes: map[TraceRef]TraceNode{}, edges: map[TraceEdge]struct{}{}}
	// Корень должен быть видим субъекту; иначе — ошибка, а не пустой граф.
	visible, err := t.resolve(ctx, root)
	if err != nil {
		return TraceGraph{}, err
	}
	if !visible {
		return TraceGraph{}, t.hidden
	}
	t.queue = append(t.queue, root)
	for len(t.queue) > 0 {
		ref := t.queue[0]
		t.queue = t.queue[1:]
		if err := t.expand(ctx, ref); err != nil {
			return TraceGraph{}, err
		}
	}
	g := TraceGraph{Root: root, Nodes: make([]TraceNode, 0, len(t.order)), Edges: make([]TraceEdge, 0, len(t.edges))}
	for _, ref := range t.order {
		g.Nodes = append(g.Nodes, t.nodes[ref])
	}
	// Рёбра — в порядке обнаружения узла-источника, затем узла-приёмника: детерминированно.
	index := make(map[TraceRef]int, len(t.order))
	for i, ref := range t.order {
		index[ref] = i
	}
	for e := range t.edges {
		g.Edges = append(g.Edges, e)
	}
	sortEdges(g.Edges, index)
	return g, nil
}

func sortEdges(edges []TraceEdge, index map[TraceRef]int) {
	for i := 1; i < len(edges); i++ {
		for j := i; j > 0 && less(edges[j], edges[j-1], index); j-- {
			edges[j], edges[j-1] = edges[j-1], edges[j]
		}
	}
}

func less(a, b TraceEdge, index map[TraceRef]int) bool {
	if index[a.From] != index[b.From] {
		return index[a.From] < index[b.From]
	}
	return index[a.To] < index[b.To]
}

// link добавляет ребро и оба узла; невидимый узел ребра не создаёт.
func (t *tracer) link(ctx context.Context, from, to TraceRef) error {
	for _, ref := range []TraceRef{from, to} {
		if _, ok := t.nodes[ref]; ok {
			continue
		}
		visible, err := t.resolve(ctx, ref)
		if err != nil {
			return err
		}
		if !visible {
			return nil
		}
		t.queue = append(t.queue, ref)
	}
	t.edges[TraceEdge{From: from, To: to}] = struct{}{}
	return nil
}

// resolve загружает узел и проверяет доступ; false — узел не видим или не найден.
// Ошибка возвращается только при отказе хранилища или порта.
func (t *tracer) resolve(ctx context.Context, ref TraceRef) (bool, error) {
	if _, ok := t.nodes[ref]; ok {
		return true, nil
	}
	var node TraceNode
	var err error
	switch ref.Kind {
	case TraceSignal:
		if t.s.signals == nil {
			return false, unavailable("signals")
		}
		var sig signals.Signal
		sig, err = t.s.signals.Signal(ctx, t.sc, ref.ID)
		node = TraceNode{TraceRef: ref, ProductID: sig.ProductID, Title: sig.Text}
	case TraceInsight:
		var i Insight
		i, err = t.s.Insight(ctx, t.sc, ref.ID)
		node = TraceNode{TraceRef: ref, ProductID: i.ProductID, Title: i.Text}
	case TraceHypothesis:
		var h Hypothesis
		h, err = t.s.Hypothesis(ctx, t.sc, ref.ID)
		node = TraceNode{TraceRef: ref, ProductID: h.ProductID, Title: h.Title}
	case TraceFeature:
		if t.s.features == nil {
			return false, unavailable("features")
		}
		f, ferr := t.s.features.Feature(ctx, t.sc, ref.ID)
		err = ferr
		node = TraceNode{TraceRef: ref, ProductID: f.ProductID, Title: f.Name}
	case TraceDecision:
		// Решения добавляются через addDecisions с заголовком из порта.
		return false, nil
	}
	if err != nil {
		if errors.Is(err, kernel.ErrForbidden) || kernel.IsNotFound(err) {
			t.hidden = err
			return false, nil
		}
		return false, fmt.Errorf("узел %s %s: %w", ref.Kind, ref.ID, err)
	}
	t.nodes[ref] = node
	t.order = append(t.order, ref)
	return true, nil
}

// expand раскрывает связи узла в обе стороны.
func (t *tracer) expand(ctx context.Context, ref TraceRef) error {
	if err := t.addDecisions(ctx, ref); err != nil {
		return err
	}
	switch ref.Kind {
	case TraceSignal:
		sig, err := t.s.signals.Signal(ctx, t.sc, ref.ID)
		if err != nil {
			return fmt.Errorf("signal: %w", err)
		}
		if sig.HypothesisID != kernel.NilID {
			if err := t.link(ctx, ref, TraceRef{Kind: TraceHypothesis, ID: sig.HypothesisID}); err != nil {
				return err
			}
		}
		insights, err := t.s.store.Insights(ctx, InsightFilter{SignalID: ref.ID})
		if err != nil {
			return fmt.Errorf("insights: %w", err)
		}
		for _, i := range insights {
			if err := t.link(ctx, ref, TraceRef{Kind: TraceInsight, ID: i.ID}); err != nil {
				return err
			}
		}
	case TraceInsight:
		i := t.nodes[ref]
		ins, err := t.s.store.Insight(ctx, i.ID)
		if err != nil {
			return err
		}
		for _, sid := range ins.SignalIDs {
			if err := t.link(ctx, TraceRef{Kind: TraceSignal, ID: sid}, ref); err != nil {
				return err
			}
		}
		for _, hid := range ins.HypothesisIDs {
			if err := t.link(ctx, ref, TraceRef{Kind: TraceHypothesis, ID: hid}); err != nil {
				return err
			}
		}
	case TraceHypothesis:
		h, err := t.s.store.Hypothesis(ctx, ref.ID)
		if err != nil {
			return err
		}
		if h.FeatureID != kernel.NilID {
			if err := t.link(ctx, ref, TraceRef{Kind: TraceFeature, ID: h.FeatureID}); err != nil {
				return err
			}
		}
		insights, err := t.s.store.Insights(ctx, InsightFilter{HypothesisID: ref.ID})
		if err != nil {
			return fmt.Errorf("insights: %w", err)
		}
		for _, i := range insights {
			if err := t.link(ctx, TraceRef{Kind: TraceInsight, ID: i.ID}, ref); err != nil {
				return err
			}
		}
		if t.s.signals != nil {
			sigs, err := t.s.signals.SignalsByHypothesis(ctx, t.sc, ref.ID)
			if err != nil {
				return fmt.Errorf("signals: %w", err)
			}
			for _, sg := range sigs {
				if err := t.link(ctx, TraceRef{Kind: TraceSignal, ID: sg.ID}, ref); err != nil {
					return err
				}
			}
		}
	case TraceFeature:
		hyps, err := t.s.store.Hypotheses(ctx, HypothesisFilter{FeatureID: ref.ID})
		if err != nil {
			return fmt.Errorf("hypotheses: %w", err)
		}
		for _, h := range hyps {
			if err := t.link(ctx, TraceRef{Kind: TraceHypothesis, ID: h.ID}, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// addDecisions добавляет решения, ссылающиеся на узел, как листья.
func (t *tracer) addDecisions(ctx context.Context, ref TraceRef) error {
	if t.s.decisions == nil || ref.Kind == TraceDecision {
		return nil
	}
	refs, err := t.s.decisions.DecisionsFor(ctx, t.sc, string(ref.Kind), ref.ID)
	if err != nil {
		return fmt.Errorf("decisions: %w", err)
	}
	for _, d := range refs {
		dref := TraceRef{Kind: TraceDecision, ID: d.ID}
		if _, ok := t.nodes[dref]; !ok {
			t.nodes[dref] = TraceNode{TraceRef: dref, ProductID: t.nodes[ref].ProductID, Title: d.Title}
			t.order = append(t.order, dref)
		}
		t.edges[TraceEdge{From: ref, To: dref}] = struct{}{}
	}
	return nil
}
