package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/signals"
)

const traceTestSecret = "synthetic-trace-test-key-32-bytes-only"

type traceFixture struct {
	product         portfoliograph.Product
	refs            []discovery.TraceRef
	hidden, missing kernel.ID
	hiddenDecision  decisions.DecisionRecord
}

func makeTraceFixture(t *testing.T, a *App) traceFixture {
	t.Helper()
	ctx := context.Background()
	sc := identityaccess.ServiceScope("trace-fixture")
	var f traceFixture
	err := a.runOperation(ctx, func(ctx context.Context) error {
		var err error
		f.product, err = a.Portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "trace-" + kernel.NewID().String(), Name: "Synthetic trace product", Type: portfoliograph.ProductTypeSecurity})
		if err != nil {
			return err
		}
		other, err := a.Portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "trace-hidden-" + kernel.NewID().String(), Name: "Hidden synthetic product", Type: portfoliograph.ProductTypeSecurity})
		if err != nil {
			return err
		}
		feature, err := a.Portfolio.CreateFeature(ctx, sc, f.product.ID, portfoliograph.FeatureInput{Name: "Synthetic feature"})
		if err != nil {
			return err
		}
		h, err := a.Discovery.SaveHypothesis(ctx, sc, discovery.HypothesisInput{ProductID: f.product.ID, Title: "Synthetic hypothesis", Statement: "Synthetic statement", ConfirmationCriterion: "Synthetic confirmation", FeatureID: feature.ID})
		if err != nil {
			return err
		}
		signal, err := a.Signals.Ingest(ctx, sc, signals.IngestInput{ProductID: f.product.ID, Source: signals.SourceManual, Text: "Synthetic signal"})
		if err != nil {
			return err
		}
		insight, err := a.Discovery.SaveInsight(ctx, sc, discovery.InsightInput{ProductID: f.product.ID, Text: "Synthetic insight", Confidence: discovery.ConfidenceHigh, SignalIDs: []kernel.ID{signal.ID}, HypothesisIDs: []kernel.ID{h.ID}})
		if err != nil {
			return err
		}
		hidden, err := a.Signals.Ingest(ctx, sc, signals.IngestInput{ProductID: other.ID, Source: signals.SourceManual, Text: "Hidden synthetic signal"})
		if err != nil {
			return err
		}
		f.hidden = hidden.ID
		f.missing = kernel.NewID()
		dec, err := a.Decisions.Create(ctx, sc, decisions.Input{ProductID: f.product.ID, Title: "Synthetic decision", Context: "Synthetic decision context", Links: []decisions.Link{{Kind: decisions.LinkFeature, ID: feature.ID}, {Kind: decisions.LinkHypothesis, ID: h.ID}}})
		if err != nil {
			return err
		}
		f.hiddenDecision, err = a.Decisions.Create(ctx, sc, decisions.Input{ProductID: other.ID, Title: "Hidden synthetic decision", Context: "Hidden synthetic context"})
		if err != nil {
			return err
		}
		f.refs = []discovery.TraceRef{{Kind: discovery.TraceSignal, ID: signal.ID}, {Kind: discovery.TraceInsight, ID: insight.ID}, {Kind: discovery.TraceHypothesis, ID: h.ID}, {Kind: discovery.TraceFeature, ID: feature.ID}, {Kind: discovery.TraceDecision, ID: dec.ID}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func traceSets(g discovery.TraceGraph) (map[discovery.TraceRef]bool, map[discovery.TraceEdge]bool) {
	nodes := map[discovery.TraceRef]bool{}
	edges := map[discovery.TraceEdge]bool{}
	for _, n := range g.Nodes {
		nodes[n.TraceRef] = true
	}
	for _, e := range g.Edges {
		edges[e] = true
	}
	return nodes, edges
}
func verifyTraceComponent(t *testing.T, a *App, f traceFixture) {
	t.Helper()
	ctx := context.Background()
	sc := identityaccess.ServiceScope("trace-reader")
	var expected discovery.TraceGraph
	for i, ref := range f.refs {
		g, err := a.Discovery.Trace(ctx, sc, ref.Kind, ref.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Nodes) != 5 || len(g.Edges) != 5 || g.Root != ref || g.Incomplete || g.Truncated {
			t.Fatalf("component from %s: %+v", ref.Kind, g)
		}
		for _, n := range g.Nodes {
			if n.ProductID != f.product.ID {
				t.Fatalf("wrong owner: %+v", n)
			}
		}
		if i == 0 {
			expected = g
			continue
		}
		nodes, edges := traceSets(expected)
		gotNodes, gotEdges := traceSets(g)
		for node := range nodes {
			if !gotNodes[node] {
				t.Fatalf("missing node from %s: %+v", ref.Kind, node)
			}
		}
		for edge := range edges {
			if !gotEdges[edge] {
				t.Fatalf("missing edge from %s: %+v", ref.Kind, edge)
			}
		}
	}
}
func TestDS04_DA01_TracePublicPortsFromEveryRoot(t *testing.T) {
	a, err := Build(context.Background(), Config{Storage: "memory", AuthMode: "hmac", HMACSecret: traceTestSecret, HMACIssuer: "trace-test", OTelExport: "none"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	f := makeTraceFixture(t, a)
	verifyTraceComponent(t, a, f)
	token, err := identityaccess.MintHS256([]byte(traceTestSecret), "trace-test", "synthetic-user", []string{"cpo"}, nil, "", time.Hour, kernel.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range f.refs {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/trace/%s/%s", ref.Kind, ref.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("HTTP %s: %d %s", ref.Kind, w.Code, w.Body.String())
		}
	}
}
func TestDS04_DA01_AD02_NFS01_TraceOmitsHiddenAndDeletedSources(t *testing.T) {
	a, err := Build(context.Background(), Config{Storage: "memory", AuthMode: "hmac", HMACSecret: traceTestSecret, HMACIssuer: "trace-test", OTelExport: "none"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	f := makeTraceFixture(t, a)
	ctx := context.Background()
	public, err := a.Decisions.Create(ctx, identityaccess.ServiceScope("trace-fixture"), decisions.Input{ProductID: f.product.ID, Title: "Visible synthetic decision", Context: "Visible context", Links: []decisions.Link{{Kind: decisions.LinkSignal, ID: f.hidden}, {Kind: decisions.LinkSignal, ID: f.missing}, {Kind: decisions.LinkHypothesis, ID: f.refs[2].ID}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []authz.Access{authz.AccessPrivate, authz.AccessStrategic} {
		sc := authz.New(authz.Params{Subject: "synthetic-reader", Products: map[kernel.ID]authz.Access{f.product.ID: access}})
		g, err := a.Discovery.Trace(ctx, sc, discovery.TraceDecision, public.ID)
		if err != nil || !g.Incomplete {
			t.Fatalf("incomplete: %+v %v", g, err)
		}
		raw, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		for _, hidden := range []string{f.hidden.String(), f.missing.String(), "Hidden synthetic"} {
			if strings.Contains(string(raw), hidden) {
				t.Fatalf("hidden source leaked: %s", raw)
			}
		}
		if access == authz.AccessStrategic {
			for _, node := range g.Nodes {
				if node.Kind == discovery.TraceSignal || node.Kind == discovery.TraceInsight || node.Kind == discovery.TraceHypothesis {
					t.Fatalf("private node leaked: %+v", node)
				}
			}
		}
		if _, err := a.Discovery.Trace(ctx, sc, discovery.TraceDecision, f.hiddenDecision.ID); err == nil {
			t.Fatal("foreign root allowed")
		}
	}
	// The HTTP response carries the generic incomplete flag, never hidden IDs.
	token, err := identityaccess.MintHS256([]byte(traceTestSecret), "trace-test", "synthetic-pm", []string{"pm"}, []string{f.product.Key}, "", time.Hour, kernel.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/trace/decision/"+public.ID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"incomplete":true`) || strings.Contains(w.Body.String(), f.hidden.String()) || strings.Contains(w.Body.String(), f.missing.String()) {
		t.Fatalf("HTTP redaction: %d %s", w.Code, w.Body.String())
	}
}
