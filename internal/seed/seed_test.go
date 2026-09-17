package seed_test

import (
	"context"
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/seed"
)

func TestSeed_BothReferencePortfoliosLoadIdempotently(t *testing.T) {
	svc := pg.NewService(pg.NewMemStore(), nil, kernel.SystemClock{})
	sc := identityaccess.ServiceScope("seed")
	ctx := context.Background()
	sec, err := seed.Security(ctx, svc, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(sec.Products) != 4 || len(sec.Contracts) != 3 {
		t.Fatalf("портфель ИБ: %+v", sec)
	}
	inf, err := seed.Infrastructure(ctx, svc, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.Products) != 6 {
		t.Fatalf("инфраструктурный: %+v", inf)
	}
	hubs, _ := svc.Hubs(ctx, sc)
	if hubs[0].InDegree != 5 || hubs[0].ProductID != inf.Products["mgmt"] {
		t.Fatalf("хаб инфраструктуры: %+v", hubs[0])
	}
	if _, err := seed.Security(ctx, svc, sc); err != nil {
		t.Fatal("повторный seed должен быть идемпотентным")
	}
	ps, _ := svc.Products(ctx, sc)
	if len(ps) != 10 {
		t.Fatalf("продуктов %d", len(ps))
	}
}
