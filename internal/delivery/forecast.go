package delivery

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// ForecastOptions — параметры прогноза даты фичи (DL-04).
type ForecastOptions struct {
	// Samples — число прогонов Монте-Карло; по умолчанию 10 000.
	Samples int
	// Seed — зерно генератора; задаётся в тестах для воспроизводимости.
	Seed uint64
	// IterationDays — длительность итерации throughput в днях; по умолчанию берётся из спринтов, иначе 14.
	IterationDays int
	// From — дата начала отсчёта; по умолчанию сегодня.
	From kernel.Date
}

// Forecast — прогноз даты завершения фичи методом Монте-Карло (DL-04).
type Forecast struct {
	FeatureID kernel.ID `json:"feature_id"`
	ProductID kernel.ID `json:"product_id"`
	EpicKey   string    `json:"epic_key"`
	// Remaining — незавершённые задачи эпика на момент прогноза.
	Remaining int `json:"remaining"`
	// Throughput — исторические значения завершённых задач за итерацию.
	Throughput    []int       `json:"throughput"`
	IterationDays int         `json:"iteration_days"`
	Samples       int         `json:"samples"`
	From          kernel.Date `json:"from"`
	P50           kernel.Date `json:"p50"`
	P85           kernel.Date `json:"p85"`
	P95           kernel.Date `json:"p95"`
	// IntervalLow и IntervalHigh — границы доверительного интервала 80 % (p10…p90).
	IntervalLow  kernel.Date `json:"interval_low"`
	IntervalHigh kernel.Date `json:"interval_high"`
	Sync         SyncState   `json:"sync"`
}

// ErrNoThroughput — истории завершённых задач недостаточно для прогноза.
var ErrNoThroughput = fmt.Errorf("%w: нет истории throughput для прогноза", kernel.ErrValidation)

// Forecast считает прогноз даты завершения фичи по throughput методом Монте-Карло
// с доверительным интервалом (DL-04). Прогноз — производная метрика: он ничего не меняет
// ни в платформе, ни в трекере.
func (s *Service) Forecast(ctx context.Context, sc authz.Scope, featureID kernel.ID, opts ForecastOptions) (Forecast, error) {
	f, err := s.portfolio.Feature(ctx, sc, featureID)
	if err != nil {
		return Forecast{}, err
	}
	if err := sc.Require(authz.ActionReadStrategic, f.ProductID); err != nil {
		return Forecast{}, err
	}
	epic, err := s.store.EpicByFeature(ctx, featureID)
	if err != nil {
		return Forecast{}, err
	}
	remaining := 0
	for _, i := range epic.Issues {
		if !i.Done {
			remaining++
		}
	}
	sprints, err := s.store.Sprints(ctx, f.ProductID)
	if err != nil {
		return Forecast{}, fmt.Errorf("sprints: %w", err)
	}
	throughput, iterationDays := throughputHistory(sprints)
	if opts.IterationDays > 0 {
		iterationDays = opts.IterationDays
	}
	if iterationDays <= 0 {
		iterationDays = 14
	}
	if len(throughput) == 0 || allZero(throughput) {
		return Forecast{}, ErrNoThroughput
	}
	samples := opts.Samples
	if samples <= 0 {
		samples = 10_000
	}
	from := opts.From
	if from.IsZero() {
		from = kernel.DateFromTime(s.clock.Now())
	}
	state, err := s.syncState(ctx)
	if err != nil {
		return Forecast{}, err
	}
	out := Forecast{FeatureID: featureID, ProductID: f.ProductID, EpicKey: epic.EpicKey,
		Remaining: remaining, Throughput: throughput, IterationDays: iterationDays,
		Samples: samples, From: from, Sync: state}
	if remaining == 0 {
		out.P50, out.P85, out.P95 = from, from, from
		out.IntervalLow, out.IntervalHigh = from, from
		return out, nil
	}
	iterations := simulate(remaining, throughput, samples, opts.Seed)
	day := func(p float64) kernel.Date {
		return from.AddDays(percentile(iterations, p) * iterationDays)
	}
	out.P50, out.P85, out.P95 = day(0.5), day(0.85), day(0.95)
	out.IntervalLow, out.IntervalHigh = day(0.1), day(0.9)
	return out, nil
}

// throughputHistory собирает завершённые задачи по закрытым спринтам и типичную длину итерации.
func throughputHistory(sprints []SprintStatus) ([]int, int) {
	closed := make([]SprintStatus, 0, len(sprints))
	for _, sp := range sprints {
		if sp.State == ports.SprintClosed {
			closed = append(closed, sp)
		}
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].StartDate.Before(closed[j].StartDate) })
	out := make([]int, 0, len(closed))
	days := 0
	for _, sp := range closed {
		out = append(out, sp.Done)
		if !sp.StartDate.IsZero() && !sp.EndDate.IsZero() {
			if d := sp.StartDate.DaysUntil(sp.EndDate) + 1; d > 0 {
				days = d
			}
		}
	}
	return out, days
}

func allZero(v []int) bool {
	for _, x := range v {
		if x > 0 {
			return false
		}
	}
	return true
}

// simulate прогоняет модель: на каждой итерации берётся случайное историческое значение
// throughput, пока не закрыт остаток. Возвращает число итераций по каждому прогону.
// Источник случайности — math/rand/v2 с явным зерном: это имитационная модель,
// а не криптография (см. docs/questions.md, вопрос 33).
func simulate(remaining int, throughput []int, samples int, seed uint64) []int {
	if seed == 0 {
		seed = 0x5EED_1F0
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B9)) //nolint:gosec // имитационная модель, не криптография (вопрос 33)
	maxIterations := remaining*len(throughput) + 1000
	out := make([]int, 0, samples)
	for i := 0; i < samples; i++ {
		left, steps := remaining, 0
		for left > 0 && steps < maxIterations {
			left -= throughput[rng.IntN(len(throughput))]
			steps++
		}
		out = append(out, steps)
	}
	sort.Ints(out)
	return out
}

// percentile возвращает значение перцентиля из отсортированного среза.
func percentile(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
