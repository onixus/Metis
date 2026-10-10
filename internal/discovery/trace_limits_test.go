package discovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

type traceNetwork struct {
	decisions       map[kernel.ID]discovery.DecisionTrace
	reads           int
	failure         error
	reverseFailure  error
	cancelOnReverse context.CancelFunc
}

func (n *traceNetwork) Decision(_ context.Context, _ authz.Scope, id kernel.ID) (discovery.DecisionTrace, error) {
	n.reads++
	if n.failure != nil {
		return discovery.DecisionTrace{}, n.failure
	}
	d, ok := n.decisions[id]
	if !ok {
		return d, kernel.ErrNotFound
	}
	return d, nil
}
func (n *traceNetwork) DecisionsFor(_ context.Context, _ authz.Scope, kind string, id kernel.ID) ([]discovery.DecisionRef, error) {
	n.reads++
	if n.cancelOnReverse != nil {
		n.cancelOnReverse()
	}
	if n.reverseFailure != nil {
		return nil, n.reverseFailure
	}
	out := []discovery.DecisionRef{}
	for _, d := range n.decisions {
		for _, l := range d.Links {
			if string(l.Kind) == kind && l.ID == id {
				out = append(out, discovery.DecisionRef{ID: d.Node.ID, Title: d.Node.Title})
				break
			}
		}
	}
	return out, nil
}
func (n *traceNetwork) Feature(_ context.Context, _ authz.Scope, id kernel.ID) (pg.Feature, error) {
	n.reads++
	return pg.Feature{ID: id, Name: "Synthetic feature"}, nil
}
func networkService(n *traceNetwork) *discovery.Service {
	return discovery.NewService(discovery.NewMemStore(), nil, kernel.FixedClock{T: time.Now()}, discovery.WithDecisions(n), discovery.WithFeatures(n))
}

func TestDS04_DA01_BoundsNodeReadsAndCycles(t *testing.T) {
	n := &traceNetwork{decisions: map[kernel.ID]discovery.DecisionTrace{}}
	root := kernel.NewID()
	links := []discovery.TraceRef{}
	for i := 0; i < 320; i++ {
		links = append(links, discovery.TraceRef{Kind: discovery.TraceFeature, ID: kernel.NewID()})
	}
	n.decisions[root] = discovery.DecisionTrace{Node: discovery.TraceNode{TraceRef: discovery.TraceRef{Kind: discovery.TraceDecision, ID: root}, Title: "Synthetic decision"}, Links: links}
	g, err := networkService(n).Trace(context.Background(), cpoScope(), discovery.TraceDecision, root)
	if err != nil || !g.Truncated || len(g.Nodes) != 256 || len(g.Edges) != 255 {
		t.Fatalf("bounded graph: %d/%d %t %v", len(g.Nodes), len(g.Edges), g.Truncated, err)
	}
	if n.reads > 512 {
		t.Fatalf("unbounded queries: %d", n.reads)
	}
}
func TestDS04_DA01_BoundsDenseEdges(t *testing.T) {
	n := &traceNetwork{decisions: map[kernel.ID]discovery.DecisionTrace{}}
	features := []discovery.TraceRef{}
	root := kernel.NewID()
	for i := 0; i < 34; i++ {
		features = append(features, discovery.TraceRef{Kind: discovery.TraceFeature, ID: kernel.NewID()})
	}
	for i := 0; i < 34; i++ {
		id := kernel.NewID()
		if i == 0 {
			id = root
		}
		n.decisions[id] = discovery.DecisionTrace{Node: discovery.TraceNode{TraceRef: discovery.TraceRef{Kind: discovery.TraceDecision, ID: id}, Title: "Synthetic"}, Links: features}
	}
	g, err := networkService(n).Trace(context.Background(), cpoScope(), discovery.TraceDecision, root)
	if err != nil || !g.Truncated || len(g.Edges) != 1024 || len(g.Nodes) > 68 {
		t.Fatalf("dense graph: %d/%d %t %v", len(g.Nodes), len(g.Edges), g.Truncated, err)
	}
}

func TestDS04_DA01_StopsQueriesAfterTruncation(t *testing.T) {
	root := kernel.NewID()
	links := make([]discovery.TraceRef, 320)
	for i := range links {
		links[i] = discovery.TraceRef{Kind: discovery.TraceFeature, ID: kernel.NewID()}
	}
	n := &traceNetwork{decisions: map[kernel.ID]discovery.DecisionTrace{
		root: {Node: discovery.TraceNode{TraceRef: discovery.TraceRef{Kind: discovery.TraceDecision, ID: root}}, Links: links},
	}, reverseFailure: kernel.ErrUnavailable}
	g, err := networkService(n).Trace(context.Background(), cpoScope(), discovery.TraceDecision, root)
	if err != nil || !g.Truncated || len(g.Nodes) != 256 || len(g.Edges) != 255 {
		t.Fatalf("truncation lost to unnecessary query: %+v %v", g, err)
	}
	if n.reads != 256 {
		t.Fatalf("queries after limit: %d", n.reads)
	}
}

func TestDS04_DA01_CancelsOnKnownEdgeAtEndOfQueue(t *testing.T) {
	root, feature := kernel.NewID(), kernel.NewID()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := &traceNetwork{decisions: map[kernel.ID]discovery.DecisionTrace{
		root: {Node: discovery.TraceNode{TraceRef: discovery.TraceRef{Kind: discovery.TraceDecision, ID: root}}, Links: []discovery.TraceRef{{Kind: discovery.TraceFeature, ID: feature}}},
	}, cancelOnReverse: cancel}
	if _, err := networkService(n).Trace(ctx, cpoScope(), discovery.TraceDecision, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled final query returned a graph: %v", err)
	}
}
func TestDS04_DA01_PropagatesCancellationAndPortFailure(t *testing.T) {
	n := &traceNetwork{failure: kernel.ErrUnavailable}
	if _, err := networkService(n).Trace(context.Background(), cpoScope(), discovery.TraceDecision, kernel.NewID()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("hidden infrastructure failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := networkService(n).Trace(ctx, cpoScope(), discovery.TraceDecision, kernel.NewID()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled trace: %v", err)
	}
	if n.reads != 1 {
		t.Fatalf("cancelled trace performed I/O: %d", n.reads)
	}
	svc := discovery.NewService(discovery.NewMemStore(), nil, kernel.SystemClock{})
	if _, err := svc.Trace(context.Background(), cpoScope(), discovery.TraceDecision, kernel.NewID()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing port: %v", err)
	}
	if _, err := svc.Trace(context.Background(), authz.Scope{}, discovery.TraceDecision, kernel.NewID()); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope: %v", err)
	}
}
