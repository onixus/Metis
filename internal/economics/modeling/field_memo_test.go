package economics_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type countedFactsStore struct {
	*economics.MemStore
	rows        map[string][]economics.FactRow
	factCalls   int
	metricCalls int
}

func (s *countedFactsStore) Facts(ctx context.Context, sc authz.Scope, filter economics.FactFilter) ([]economics.FactRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !sc.Valid() || sc.Finance() < authz.FinanceFull {
		return nil, kernel.ErrForbidden
	}
	s.factCalls++
	out := []economics.FactRow{}
	for _, row := range s.rows[filter.FieldKey] {
		if sc.Product(row.ProductID) < authz.AccessStrategic {
			continue
		}
		if filter.Product != nil && *filter.Product != row.ProductID {
			continue
		}
		if filter.Team != nil && *filter.Team != row.TeamID {
			continue
		}
		if filter.Period != nil && *filter.Period != row.Period {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}
func (s *countedFactsStore) Metric(ctx context.Context, sc authz.Scope, key string) (economics.Metric, error) {
	s.metricCalls++
	return s.MemStore.Metric(ctx, sc, key)
}
func countedService(t *testing.T, rows map[string][]economics.FactRow) (*economics.Service, *countedFactsStore) {
	t.Helper()
	store := &countedFactsStore{MemStore: economics.NewMemStore(), rows: rows}
	service, err := economics.NewService(store, nil, kernel.SystemClock{}, economics.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}
func saveCountedMetric(t *testing.T, service *economics.Service, key, expression string) {
	t.Helper()
	if _, err := service.SaveMetric(context.Background(), financeScope(), economics.MetricInput{Key: key, Name: key, Expression: expression}); err != nil {
		t.Fatal(err)
	}
}

func TestEC09_EC10_RepeatedFieldFilterReadsOnceAndExplanationDeduplicates(t *testing.T) {
	product, other := kernel.NewID(), kernel.NewID()
	precise := decimal.RequireFromString("9007199254740993.01")
	service, store := countedService(t, map[string][]economics.FactRow{"precise": {{ProductID: product, Period: jan(), FieldKey: "precise", Value: precise}, {ProductID: other, Period: jan(), FieldKey: "precise", Value: decimal.NewFromInt(2)}}})
	saveCountedMetric(t, service, "same_filter", `field("precise") + field("precise") + sum("precise")`)
	explained, err := service.Explain(context.Background(), financeScope(), "same_filter", economics.Slice{ProductID: product, Period: jan()})
	if err != nil || explained.Value.String() != "27021597764222979.03" || store.factCalls != 1 || len(explained.Inputs) != 1 || len(explained.Inputs[0].Rows) != 1 {
		t.Fatalf("field snapshot/dedupe/precision: %+v err%v calls%d", explained, err, store.factCalls)
	}
	saveCountedMetric(t, service, "different_filter", `sum("precise","product","*") + field("precise")`)
	store.factCalls = 0
	explained, err = service.Explain(context.Background(), financeScope(), "different_filter", economics.Slice{ProductID: product, Period: jan()})
	if err != nil || store.factCalls != 2 || len(explained.Inputs) != 2 || len(explained.Inputs[0].Rows) != 2 || len(explained.Inputs[1].Rows) != 1 {
		t.Fatalf("different filters lost: %+v %v calls%d", explained, err, store.factCalls)
	}
}

func TestEC09_EC10_ExplainOutputBudgetDoesNotRestrictScalarValue(t *testing.T) {
	product := kernel.NewID()
	rows := make([]economics.FactRow, 50001)
	for i := range rows {
		rows[i] = economics.FactRow{ProductID: product, Period: jan(), FieldKey: "large", Value: decimal.NewFromInt(1)}
	}
	service, store := countedService(t, map[string][]economics.FactRow{"large": rows})
	saveCountedMetric(t, service, "large_scalar", `field("large")`)
	sl := economics.Slice{ProductID: product, Period: jan()}
	value, err := service.Value(context.Background(), financeScope(), "large_scalar", sl)
	if err != nil || value.String() != "50001" {
		t.Fatalf("legitimate scalar blocked by Explain budget: %v %v", value, err)
	}
	if _, err := service.Explain(context.Background(), financeScope(), "large_scalar", sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("oversized Explain not refused: %v", err)
	}
	if store.factCalls != 2 {
		t.Fatalf("Explain repeated fact scans: %d", store.factCalls)
	}
}

func TestEC10_SharedExplainBudgetAndZeroRowTreeExplosionRefuseWithoutTruncation(t *testing.T) {
	product := kernel.NewID()
	rows := make([]economics.FactRow, 30000)
	for i := range rows {
		rows[i] = economics.FactRow{ProductID: product, Period: jan(), FieldKey: "large", Value: decimal.NewFromInt(1)}
	}
	service, store := countedService(t, map[string][]economics.FactRow{"large": rows})
	saveCountedMetric(t, service, "shared_rows", `field("large")`)
	ctx := economics.WithFormulaSnapshot(context.Background())
	sl := economics.Slice{ProductID: product, Period: jan()}
	if _, err := service.Explain(ctx, financeScope(), "shared_rows", sl); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Explain(ctx, financeScope(), "shared_rows", sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("composed Explain bypassed output row budget: %v", err)
	}
	saveCountedMetric(t, service, "tier_0", `d("1")`)
	for level := 1; level <= 5; level++ {
		saveCountedMetric(t, service, fmt.Sprintf("tier_%d", level), strings.TrimSuffix(strings.Repeat(fmt.Sprintf(`metric("tier_%d") + `, level-1), 10), " + "))
	}
	value, err := service.Value(context.Background(), financeScope(), "tier_5", sl)
	if err != nil || value.String() != "100000" {
		t.Fatalf("scalar graph failed: %v %v", value, err)
	}
	store.metricCalls = 0
	if _, err := service.Explain(context.Background(), financeScope(), "tier_5", sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unbounded zero-row tree allowed: %v", err)
	}
	if store.metricCalls > 4200 {
		t.Fatalf("Explain exploded before budget: metric reads%d", store.metricCalls)
	}
}
