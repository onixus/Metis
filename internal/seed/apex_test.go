package seed_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/seed"
)

func TestAPEXTemplates_AreCanonicalAndCalculated(t *testing.T) {
	templates := seed.APEXProductTemplates()
	if len(templates) != 8 {
		t.Fatalf("APEX templates: got %d, want 8", len(templates))
	}
	seen := map[string]bool{}
	for _, tpl := range templates {
		if seen[tpl.Key] {
			t.Fatalf("duplicate key %q", tpl.Key)
		}
		seen[tpl.Key] = true
		if !strings.HasPrefix(tpl.Repo, "onixus/") {
			t.Fatalf("%s: unexpected repo %q", tpl.Key, tpl.Repo)
		}
		if len(tpl.Features) == 0 {
			t.Fatalf("%s: no workstreams", tpl.Key)
		}
		if got := tpl.CompletionPct(); got < 0 || got > 100 {
			t.Fatalf("%s: completion=%d", tpl.Key, got)
		}
		if got := tpl.ConfidencePct(); got < 0 || got > 100 {
			t.Fatalf("%s: confidence=%d", tpl.Key, got)
		}
		if tpl.RemainingEffortPoints() <= 0 {
			t.Fatalf("%s: remaining effort must be positive", tpl.Key)
		}
		for _, f := range tpl.Features {
			if f.EffortPoints <= 0 || f.ConfidencePct <= 0 || f.ConfidencePct > 100 || f.Evidence == "" {
				t.Fatalf("%s/%s: invalid estimate: %+v", tpl.Key, f.Name, f)
			}
		}
	}
	for _, key := range []string{"apex-gateway", "shapoclyack", "lariska", "ferrum", "bsdm-proxy", "oko-ra", "pulse", "asmodeus"} {
		if !seen[key] {
			t.Fatalf("canonical participant %q missing", key)
		}
	}
}

func TestAPEXSeed_LoadsIdempotently(t *testing.T) {
	ctx := context.Background()
	svc := pg.NewService(pg.NewMemStore(), nil, kernel.SystemClock{})
	sc := identityaccess.ServiceScope("seed")

	first, err := seed.APEX(ctx, svc, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Products) != 8 {
		t.Fatalf("products=%d, want 8", len(first.Products))
	}
	if len(first.Features) != 32 {
		t.Fatalf("features=%d, want 32", len(first.Features))
	}

	if _, err := seed.APEX(ctx, svc, sc); err != nil {
		t.Fatalf("second APEX seed: %v", err)
	}
	products, err := svc.Products(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 8 {
		t.Fatalf("products after second seed=%d, want 8", len(products))
	}
	for _, p := range products {
		features, err := svc.Features(ctx, sc, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(features) != 4 {
			t.Fatalf("%s features=%d, want 4", p.Key, len(features))
		}
	}
}
