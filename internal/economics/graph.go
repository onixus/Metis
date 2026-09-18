package economics

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/onixus/metis/internal/economics/formula"
	"github.com/onixus/metis/internal/kernel"
)

// dependencies строит карту «показатель → ссылки его последней версии» (EC-10).
func (s *Service) dependencies(ctx context.Context) (map[string][]formula.Ref, error) {
	metrics, err := s.store.Metrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	out := make(map[string][]formula.Ref, len(metrics))
	for _, m := range metrics {
		out[m.Key] = m.Latest().Refs
	}
	return out, nil
}

// checkCycle отклоняет формулу, создающую цикл, и показывает путь (EC-10).
func (s *Service) checkCycle(ctx context.Context, key string, refs []formula.Ref) error {
	deps, err := s.dependencies(ctx)
	if err != nil {
		return err
	}
	deps[key] = refs
	var path []string
	state := map[string]int{} // 0 — не посещён, 1 — в стеке, 2 — закрыт
	var walk func(k string) []string
	walk = func(k string) []string {
		state[k] = 1
		path = append(path, k)
		for _, r := range deps[k] {
			if r.Kind != formula.RefMetric {
				continue
			}
			switch state[r.Key] {
			case 1:
				return append(append([]string(nil), path...), r.Key)
			case 0:
				if cycle := walk(r.Key); cycle != nil {
					return cycle
				}
			}
		}
		path = path[:len(path)-1]
		state[k] = 2
		return nil
	}
	if cycle := walk(key); cycle != nil {
		return fmt.Errorf("%w: формула создаёт цикл: %s", kernel.ErrConflict, strings.Join(cycle, " → "))
	}
	return nil
}

// Affected возвращает показатели, которые нужно пересчитать при изменении поля
// или показателя, включая сам изменённый показатель (EC-10).
func (s *Service) Affected(ctx context.Context, changed formula.Ref) ([]string, error) {
	deps, err := s.dependencies(ctx)
	if err != nil {
		return nil, err
	}
	dependents := map[string][]string{}
	for key, refs := range deps {
		for _, r := range refs {
			dependents[r.Kind.String()+":"+r.Key] = append(dependents[r.Kind.String()+":"+r.Key], key)
		}
	}
	seen := map[string]bool{}
	var queue []string
	if changed.Kind == formula.RefMetric {
		if _, ok := deps[changed.Key]; ok {
			seen[changed.Key] = true
			queue = append(queue, changed.Key)
		}
	}
	queue = append(queue, dependents[changed.Kind.String()+":"+changed.Key]...)
	out := make([]string, 0, len(queue))
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if !seen[k] {
			seen[k] = true
		}
		out = append(out, k)
		queue = append(queue, dependents[string(formula.RefMetric)+":"+k]...)
	}
	uniq := out[:0]
	added := map[string]bool{}
	for _, k := range out {
		if added[k] {
			continue
		}
		added[k] = true
		uniq = append(uniq, k)
	}
	sort.Strings(uniq)
	return uniq, nil
}
