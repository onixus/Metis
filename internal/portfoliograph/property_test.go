package portfoliograph_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

// Property (PG-05): при любой последовательности попыток добавить зависимость граф фич остаётся ациклическим,
// а каждый отказ содержит корректный путь цикла по существующим рёбрам плюс новое.
func TestPG05_PropertyFeatureGraphStaysAcyclic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		f := newFixture(t)
		n := rapid.IntRange(2, 12).Draw(rt, "features")
		products := []kernel.ID{f.deception, f.vm, f.edr, f.soar}
		ids := make([]kernel.ID, n)
		for i := range ids {
			ids[i] = f.feature(products[i%len(products)], "F", kernel.Date{})
		}
		edges := map[[2]kernel.ID]bool{}
		steps := rapid.IntRange(1, 40).Draw(rt, "steps")
		for s := 0; s < steps; s++ {
			from := ids[rapid.IntRange(0, n-1).Draw(rt, "from")]
			to := ids[rapid.IntRange(0, n-1).Draw(rt, "to")]
			_, err := f.dep(from, to, pg.CritBlocks)
			var ce *pg.CycleError
			switch {
			case err == nil:
				edges[[2]kernel.ID{from, to}] = true
			case errors.As(err, &ce):
				// Путь замкнут и каждое ребро, кроме первого (нового), уже есть в графе.
				if len(ce.Path) < 2 || ce.Path[0] != ce.Path[len(ce.Path)-1] || ce.Path[0] != from || ce.Path[1] != to {
					rt.Fatalf("путь цикла неверен: %v (новое ребро %v→%v)", ce.Path, from, to)
				}
				for i := 1; i+1 < len(ce.Path); i++ {
					if !edges[[2]kernel.ID{ce.Path[i], ce.Path[i+1]}] {
						rt.Fatalf("в пути цикла ребро %v→%v, которого нет в графе", ce.Path[i], ce.Path[i+1])
					}
				}
			default:
				rt.Fatalf("неожиданная ошибка: %v", err)
			}
		}
		if !acyclic(edges) {
			rt.Fatal("граф стал циклическим")
		}
		// Rollup на ациклическом графе всегда считается.
		if _, err := f.svc.FeatureValues(context.Background(), f.cpo, f.soar); err != nil {
			rt.Fatal(err)
		}
	})
}

func acyclic(edges map[[2]kernel.ID]bool) bool {
	adj := map[kernel.ID][]kernel.ID{}
	for e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	state := map[kernel.ID]int{}
	var visit func(kernel.ID) bool
	visit = func(v kernel.ID) bool {
		state[v] = 1
		for _, w := range adj[v] {
			if state[w] == 1 || (state[w] == 0 && !visit(w)) {
				return false
			}
		}
		state[v] = 2
		return true
	}
	for v := range adj {
		if state[v] == 0 && !visit(v) {
			return false
		}
	}
	return true
}

// Property (PG-07): rollup хаба равен сумме собственных ценностей потребителей с коэффициентом 1,0 (звезда).
func TestPG07_PropertyStarRollupEqualsSum(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		f := newFixture(t)
		hub := f.feature(f.soar, "hub", kernel.Date{})
		n := rapid.IntRange(0, 10).Draw(rt, "n")
		var sum int64
		for i := 0; i < n; i++ {
			leaf := f.feature(f.edr, "leaf", kernel.Date{})
			v := rapid.Int64Range(0, 1_000_000_00).Draw(rt, "v")
			if _, err := f.dep(leaf, hub, pg.CritBlocks); err != nil {
				rt.Fatal(err)
			}
			if err := f.svc.SetFeatureOwnValue(context.Background(), f.cpo, leaf, kernel.RUB(v)); err != nil {
				rt.Fatal(err)
			}
			sum += v
		}
		got, err := f.svc.FeatureValue(context.Background(), f.cpo, hub)
		if err != nil {
			rt.Fatal(err)
		}
		if got.TotalValue.Amount != sum {
			rt.Fatalf("rollup %d, ожидалось %d", got.TotalValue.Amount, sum)
		}
	})
}

// Фаззинг (PG-05): случайный список рёбер, закодированный байтами, никогда не приводит к циклическому графу.
func FuzzPG05_FeatureGraphAcyclic(fz *testing.F) {
	fz.Add([]byte{0, 1, 1, 2, 2, 0})
	fz.Add([]byte{3, 3})
	fz.Fuzz(func(t *testing.T, data []byte) {
		f := newFixture(t)
		const n = 6
		ids := make([]kernel.ID, n)
		for i := range ids {
			ids[i] = f.feature(f.edr, "F", kernel.Date{})
		}
		edges := map[[2]kernel.ID]bool{}
		for i := 0; i+1 < len(data) && i < 64; i += 2 {
			from, to := ids[int(data[i])%n], ids[int(data[i+1])%n]
			if _, err := f.dep(from, to, pg.CritBlocks); err == nil {
				edges[[2]kernel.ID{from, to}] = true
			} else if !errors.Is(err, kernel.ErrConflict) {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
		}
		if !acyclic(edges) {
			t.Fatal("граф циклический")
		}
	})
}

// NF-P03/NF-P04: распространение сдвига и rollup на расчётном объёме укладываются в лимиты с запасом.
func TestNFP03_NFP04_PropagationAndRollupWithinLimits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const n = 3000
	ids := make([]kernel.ID, n)
	for i := range ids {
		ids[i] = f.feature([]kernel.ID{f.deception, f.vm, f.edr, f.soar}[i%4], "F", d(2026, 10, 1))
	}
	// Цепочки и веер на хаб: i зависит от i/2 (дерево), все листья → 0.
	for i := 1; i < n; i++ {
		if _, err := f.dep(ids[i], ids[i/2], pg.CritBlocks); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	if err := f.svc.SetFeatureOwnValue(ctx, f.cpo, ids[n-1], kernel.RUB(100)); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 60*time.Second {
		t.Fatalf("NF-P04: rollup занял %s", el)
	}
	start = time.Now()
	res, err := f.svc.ShiftFeatureDate(ctx, f.cpo, ids[0], d(2026, 12, 1), "нагрузка")
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 10*time.Second {
		t.Fatalf("NF-P03: распространение заняло %s", el)
	}
	if len(res.Affected) != n-1 {
		t.Fatalf("затронуто %d, ожидалось %d", len(res.Affected), n-1)
	}
}
