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

func TestSeed_TolerantToPartialState(t *testing.T) {
	svc := pg.NewService(pg.NewMemStore(), nil, kernel.SystemClock{})
	sc := identityaccess.ServiceScope("seed")
	ctx := context.Background()
	// В базе уже есть EDR из другого источника: seed не падает, переиспользует его и не дублирует ключи.
	if _, err := svc.CreateProduct(ctx, sc, pg.ProductInput{Key: "edr", Name: "EDR (импорт)", Type: pg.ProductTypeSecurity}); err != nil {
		t.Fatal(err)
	}
	res, err := seed.Security(ctx, svc, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Products) != 4 {
		t.Fatalf("продукты: %+v", res.Products)
	}
	if _, ok := res.Contracts["EDR ↔ SOAR"]; ok {
		t.Fatal("контракт с уже существовавшим продуктом не создаётся")
	}
	if _, ok := res.Contracts["VM ↔ SOAR"]; !ok {
		t.Fatal("контракты между созданными продуктами создаются")
	}
}
