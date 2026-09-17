package delivery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// closedSprint — закрытый спринт доски с заданным числом завершённых задач.
func closedSprint(id string, start kernel.Date, done, total int) ports.Sprint {
	issues := make([]ports.Issue, 0, total)
	for i := 0; i < total; i++ {
		issues = append(issues, issue(id+"-"+string(rune('a'+i)), i < done))
	}
	return ports.Sprint{ID: id, Name: "Спринт " + id, Goal: "цель", State: ports.SprintClosed,
		StartDate: start, EndDate: start.AddDays(13), Issues: issues}
}

// forecastFixture готовит фичу с эпиком и историю закрытых спринтов доски.
func forecastFixture(t *testing.T) (*fixture, kernel.ID) {
	t.Helper()
	f := newFixture(t)
	fm := delivery.DefaultFieldMapping()
	fm.Boards[f.soar] = "7"
	if err := f.svc.SetFieldMapping(f.ctx, adminScope(), fm); err != nil {
		t.Fatalf("маппинг полей: %v", err)
	}
	f.tracker.sprints["7"] = []ports.Sprint{
		closedSprint("s1", d(2026, 6, 1), 4, 6),
		closedSprint("s2", d(2026, 6, 15), 6, 6),
		closedSprint("s3", d(2026, 6, 29), 5, 5),
	}
	issues := []ports.Issue{
		issue("SOAR-1", true), issue("SOAR-2", true),
		issue("SOAR-3", false), issue("SOAR-4", false), issue("SOAR-5", false),
		issue("SOAR-6", false), issue("SOAR-7", false), issue("SOAR-8", false),
		issue("SOAR-9", false), issue("SOAR-10", false),
	}
	feature := f.mapped(f.soar, "Коннектор EDR v2", "SOAR-100", d(2026, 12, 1), issues...)
	f.sync()
	return f, feature
}

// TestDL04_MonteCarloConfidenceInterval: прогноз даты фичи по throughput даёт
// упорядоченные перцентили и доверительный интервал.
func TestDL04_MonteCarloConfidenceInterval(t *testing.T) {
	f, feature := forecastFixture(t)
	fc, err := f.svc.Forecast(f.ctx, f.cpo, feature, delivery.ForecastOptions{
		Samples: 2000, Seed: 42, From: d(2026, 9, 17)})
	if err != nil {
		t.Fatalf("прогноз: %v", err)
	}
	if fc.Remaining != 8 {
		t.Fatalf("остаток задач %d, ожидалось 8", fc.Remaining)
	}
	if len(fc.Throughput) != 3 || fc.IterationDays != 14 {
		t.Fatalf("история throughput %v, итерация %d дней", fc.Throughput, fc.IterationDays)
	}
	if fc.P50.After(fc.P85) || fc.P85.After(fc.P95) {
		t.Fatalf("перцентили не упорядочены: %s, %s, %s", fc.P50, fc.P85, fc.P95)
	}
	if fc.IntervalLow.After(fc.P50) || fc.IntervalHigh.Before(fc.P50) {
		t.Fatalf("доверительный интервал не содержит медиану: %s…%s при p50 %s",
			fc.IntervalLow, fc.IntervalHigh, fc.P50)
	}
	// Восемь задач при throughput 4–6 за двухнедельный спринт закрываются за 2–3 итерации.
	minDate, maxDate := d(2026, 9, 17).AddDays(14), d(2026, 9, 17).AddDays(3*14)
	if fc.P50.Before(minDate) || fc.P50.After(maxDate) {
		t.Fatalf("медиана прогноза вне разумных границ: %s", fc.P50)
	}
	// Прогноз воспроизводим при том же зерне.
	again, err := f.svc.Forecast(f.ctx, f.cpo, feature, delivery.ForecastOptions{
		Samples: 2000, Seed: 42, From: d(2026, 9, 17)})
	if err != nil {
		t.Fatalf("повторный прогноз: %v", err)
	}
	if again.P50 != fc.P50 || again.P95 != fc.P95 {
		t.Fatalf("прогноз не воспроизводится: %s/%s против %s/%s", again.P50, again.P95, fc.P50, fc.P95)
	}
}

// TestDL04_NoThroughputHistoryRejected: без истории закрытых спринтов прогноз не строится.
func TestDL04_NoThroughputHistoryRejected(t *testing.T) {
	f := newFixture(t)
	feature := f.mapped(f.soar, "Фича без истории", "SOAR-200", d(2026, 12, 1), issue("SOAR-201", false))
	f.sync()
	if _, err := f.svc.Forecast(f.ctx, f.cpo, feature, delivery.ForecastOptions{Samples: 10, Seed: 1}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка отсутствия истории, получено %v", err)
	}
}

// TestDL04_ForbiddenForOtherProduct: прогноз чужого продукта недоступен.
func TestDL04_ForbiddenForOtherProduct(t *testing.T) {
	f, feature := forecastFixture(t)
	pmVM := pmScope("pm-vm", map[kernel.ID]authz.Access{f.vm: authz.AccessPrivate})
	if _, err := f.svc.Forecast(f.ctx, pmVM, feature, delivery.ForecastOptions{Samples: 10, Seed: 1}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался отказ, получено %v", err)
	}
	if _, err := f.svc.Forecast(f.ctx, authz.Scope{}, feature, delivery.ForecastOptions{Samples: 10, Seed: 1}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
}

// TestDL05_WorklogCostBase: списания времени превращаются в доли по продуктам и фичам.
func TestDL05_WorklogCostBase(t *testing.T) {
	f := newFixture(t)
	soarFeature := f.mapped(f.soar, "Коннектор EDR", "SOAR-300", d(2026, 12, 1),
		issue("SOAR-301", true), issue("SOAR-302", false))
	edrFeature := f.mapped(f.edr, "Агент Astra", "EDR-400", d(2026, 12, 1), issue("EDR-401", false))
	f.sync()
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	f.tracker.worklogs = []ports.Worklog{
		{IssueKey: "SOAR-301", Author: "dev1", Started: start, Spent: 6 * time.Hour},
		{IssueKey: "SOAR-302", Author: "dev2", Started: start, Spent: 2 * time.Hour},
		{IssueKey: "EDR-401", Author: "dev3", Started: start, Spent: 2 * time.Hour},
		{IssueKey: "OTHER-1", Author: "dev4", Started: start, Spent: 40 * time.Hour},
		{IssueKey: "SOAR-301", Author: "dev1", Started: start.AddDate(0, -2, 0), Spent: 100 * time.Hour},
	}
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rep, err := f.svc.WorklogCostBase(f.ctx, f.cpo, from, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("база затрат: %v", err)
	}
	if rep.TotalSeconds != int64(10*time.Hour/time.Second) {
		t.Fatalf("всего списано %d секунд", rep.TotalSeconds)
	}
	shares := rep.ProductShares()
	if got := shares[f.soar].String(); got != "0.8" {
		t.Fatalf("доля SOAR %s, ожидалось 0.8", got)
	}
	if got := shares[f.edr].String(); got != "0.2" {
		t.Fatalf("доля EDR %s, ожидалось 0.2", got)
	}
	byFeature := map[kernel.ID]int64{}
	for _, s := range rep.ByFeature {
		byFeature[s.FeatureID] = s.Seconds
	}
	if byFeature[soarFeature] != int64(8*time.Hour/time.Second) || byFeature[edrFeature] != int64(2*time.Hour/time.Second) {
		t.Fatalf("списания по фичам: %+v", rep.ByFeature)
	}
}

// TestDL05_ForbiddenWithoutScope: без Scope база затрат недоступна.
func TestDL05_ForbiddenWithoutScope(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.WorklogCostBase(f.ctx, authz.Scope{}, time.Time{}, time.Time{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался отказ, получено %v", err)
	}
}
