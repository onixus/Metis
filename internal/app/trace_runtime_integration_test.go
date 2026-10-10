//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/onixus/metis/internal/discovery"
)

func TestDS04_DA01_PostgresTraceSurvivesRestart(t *testing.T) {
	api, reader, _ := runtimeApps(t)
	fixture := makeTraceFixture(t, api)
	verifyRuntimeTrace(t, reader, fixture)
	restarted, err := Build(context.Background(), api.Cfg, api.Log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })
	verifyRuntimeTrace(t, restarted, fixture)
}

func verifyRuntimeTrace(t *testing.T, a *App, f traceFixture) {
	t.Helper()
	var expected discovery.TraceGraph
	for i, ref := range f.refs {
		// Exercise the production HTTP transaction boundary, which refreshes
		// the portfolio graph before reading across independent API instances.
		w := runtimeRequest(a, "GET", fmt.Sprintf("/trace/%s/%s", ref.Kind, ref.ID), "", "synthetic-reader", []string{"cpo"}, nil)
		var g discovery.TraceGraph
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
			t.Fatalf("HTTP trace: %d %s", w.Code, w.Body.String())
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
