package economics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Override — подмена входного значения в сценарии (EC-13). Пустой продукт или период
// означают «во всех срезах».
type Override struct {
	FieldKey  string          `json:"field_key"`
	ProductID kernel.ID       `json:"product_id,omitempty"`
	Period    Period          `json:"period,omitempty"`
	Value     decimal.Decimal `json:"value"`
}

// Scenario — сценарий «что если» под capacity и бюджет (DA-02). Фактические данные не меняются.
type Scenario struct {
	ID          kernel.ID   `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Period      Period      `json:"period"`
	Products    []kernel.ID `json:"products,omitempty"`
	Overrides   []Override  `json:"overrides,omitempty"`
	// CapacityShiftDays — сдвиг сроков поставки в днях (положительный — позже).
	CapacityShiftDays int `json:"capacity_shift_days"`
	// BudgetDelta — изменение бюджета периода; отрицательное значение урезает бюджет.
	BudgetDelta kernel.Money `json:"budget_delta"`
	Actor       string       `json:"actor"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// CommitmentRef — обязательство, как его видит экономика (порт модуля commitments).
type CommitmentRef struct {
	ID         kernel.ID   `json:"id"`
	ProductID  kernel.ID   `json:"product_id"`
	Title      string      `json:"title"`
	DueDate    kernel.Date `json:"due_date"`
	Regulatory bool        `json:"regulatory"`
}

// TrackRef — трек сертификации, как его видит экономика (порт модуля compliance).
type TrackRef struct {
	ID          kernel.ID    `json:"id"`
	ProductID   kernel.ID    `json:"product_id"`
	Name        string       `json:"name"`
	PlannedCost kernel.Money `json:"planned_cost"`
	Deadline    kernel.Date  `json:"deadline"`
}

// CommitmentsPort — источник обязательств для сценариев (DA-02).
type CommitmentsPort interface {
	Due(ctx context.Context, sc authz.Scope, products []kernel.ID) ([]CommitmentRef, error)
}

// TracksPort — источник треков сертификации для сценариев (DA-02).
type TracksPort interface {
	Active(ctx context.Context, sc authz.Scope, products []kernel.ID) ([]TrackRef, error)
}

// CommitmentImpact — влияние сценария на обязательство.
type CommitmentImpact struct {
	Commitment CommitmentRef `json:"commitment"`
	Breached   bool          `json:"breached"`
	Reason     string        `json:"reason"`
}

// TrackImpact — влияние сценария на трек сертификации.
type TrackImpact struct {
	Track    TrackRef     `json:"track"`
	AtRisk   bool         `json:"at_risk"`
	Shortage kernel.Money `json:"shortage"`
	Reason   string       `json:"reason"`
}

// ScenarioResult — результат расчёта сценария (DA-02, EC-13).
type ScenarioResult struct {
	Scenario    Scenario           `json:"scenario"`
	Metrics     map[string]string  `json:"metrics"`
	BaseMetrics map[string]string  `json:"base_metrics"`
	PnL         []PnL              `json:"pnl"`
	BasePnL     []PnL              `json:"base_pnl"`
	Commitments []CommitmentImpact `json:"commitments"`
	Tracks      []TrackImpact      `json:"tracks"`
}

// WithCommitments подключает источник обязательств.
func (s *Service) WithCommitments(p CommitmentsPort) *Service { s.commitments = p; return s }

// WithTracks подключает источник треков сертификации.
func (s *Service) WithTracks(p TracksPort) *Service { s.tracksPort = p; return s }

// SaveScenario создаёт или изменяет сценарий.
func (s *Service) SaveScenario(ctx context.Context, sc authz.Scope, in Scenario) (Scenario, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return Scenario{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return Scenario{}, kernel.Invalid("name", "название обязательно")
	}
	if in.Period.IsZero() {
		return Scenario{}, kernel.Invalid("period", "период обязателен")
	}
	for _, o := range in.Overrides {
		if _, err := s.store.Field(ctx, o.FieldKey); err != nil {
			return Scenario{}, fmt.Errorf("подмена поля %q: %w", o.FieldKey, err)
		}
	}
	now := s.clock.Now()
	if in.ID == kernel.NilID {
		in.ID, in.CreatedAt = kernel.NewID(), now
	} else if _, err := s.store.Scenario(ctx, in.ID); err != nil {
		return Scenario{}, err
	}
	in.Actor, in.UpdatedAt = sc.Subject(), now
	if err := s.store.SaveScenario(ctx, in); err != nil {
		return Scenario{}, fmt.Errorf("save scenario: %w", err)
	}
	return in, nil
}

// Scenarios возвращает сценарии.
func (s *Service) Scenarios(ctx context.Context, sc authz.Scope) ([]Scenario, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Scenarios(ctx)
}

// RunScenario считает сценарий тем же движком формул с подменой входных значений (EC-13)
// и показывает влияние на обязательства и треки сертификации (DA-02).
// Фактические данные не изменяются: подмена живёт только внутри расчёта.
func (s *Service) RunScenario(ctx context.Context, sc authz.Scope, id kernel.ID, metrics []string) (ScenarioResult, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return ScenarioResult{}, err
	}
	sn, err := s.store.Scenario(ctx, id)
	if err != nil {
		return ScenarioResult{}, err
	}
	products := sn.Products
	if len(products) == 0 {
		products, err = s.productsWithData(ctx, sn.Period)
		if err != nil {
			return ScenarioResult{}, err
		}
	}
	res := ScenarioResult{Scenario: sn, Metrics: map[string]string{}, BaseMetrics: map[string]string{}}

	base := s.source(ctx, nil)
	withOverrides := s.source(ctx, sn.Overrides)
	for _, key := range metrics {
		for _, pid := range products {
			sl := Slice{ProductID: pid, Period: sn.Period}.formulaSlice()
			b, err := base.Metric(key, sl)
			if err != nil {
				return ScenarioResult{}, err
			}
			v, err := withOverrides.Metric(key, sl)
			if err != nil {
				return ScenarioResult{}, err
			}
			res.BaseMetrics[key+"@"+pid.String()] = b.String()
			res.Metrics[key+"@"+pid.String()] = v.String()
		}
	}
	for _, pid := range products {
		basePnL, err := s.pnl(ctx, pid, sn.Period, nil)
		if err != nil {
			return ScenarioResult{}, err
		}
		scenarioPnL, err := s.pnl(ctx, pid, sn.Period, sn.Overrides)
		if err != nil {
			return ScenarioResult{}, err
		}
		res.BasePnL = append(res.BasePnL, basePnL)
		res.PnL = append(res.PnL, scenarioPnL)
	}
	if res.Commitments, err = s.commitmentImpact(ctx, sc, sn, products); err != nil {
		return ScenarioResult{}, err
	}
	if res.Tracks, err = s.trackImpact(ctx, sc, sn, products); err != nil {
		return ScenarioResult{}, err
	}
	s.logAccess(ctx, sc, "read", "economics.scenario:"+sn.ID.String(), kernel.NilID,
		map[string]any{"period": sn.Period.String(), "products": len(products)})
	return res, nil
}

// commitmentImpact: сдвиг сроков поставки нарушает обязательства, срок которых наступает
// внутри окна сдвига. TODO(question-32): точная дата поставки обязательства появится
// вместе с полным модулем commitments этапа 2.
func (s *Service) commitmentImpact(ctx context.Context, sc authz.Scope, sn Scenario, products []kernel.ID) ([]CommitmentImpact, error) {
	if s.commitments == nil || sn.CapacityShiftDays == 0 {
		return nil, nil
	}
	refs, err := s.commitments.Due(ctx, sc, products)
	if err != nil {
		return nil, fmt.Errorf("обязательства: %w", err)
	}
	from := kernel.DateFromTime(s.clock.Now())
	until := from.AddDays(sn.CapacityShiftDays)
	out := make([]CommitmentImpact, 0, len(refs))
	for _, r := range refs {
		impact := CommitmentImpact{Commitment: r}
		if !r.DueDate.IsZero() && !r.DueDate.Before(from) && !r.DueDate.After(until) {
			impact.Breached = true
			impact.Reason = fmt.Sprintf("срок %s попадает в окно сдвига поставки на %d дн.", r.DueDate, sn.CapacityShiftDays)
		}
		out = append(out, impact)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Commitment.ID.String() < out[j].Commitment.ID.String() })
	return out, nil
}

// trackImpact: урезание бюджета ставит под угрозу треки сертификации, плановые затраты
// которых больше доступного бюджета продукта в периоде.
func (s *Service) trackImpact(ctx context.Context, sc authz.Scope, sn Scenario, products []kernel.ID) ([]TrackImpact, error) {
	if s.tracksPort == nil {
		return nil, nil
	}
	refs, err := s.tracksPort.Active(ctx, sc, products)
	if err != nil {
		return nil, fmt.Errorf("треки: %w", err)
	}
	byProduct := map[kernel.ID][]TrackRef{}
	for _, r := range refs {
		byProduct[r.ProductID] = append(byProduct[r.ProductID], r)
	}
	out := make([]TrackImpact, 0, len(refs))
	for pid, tracks := range byProduct {
		budget, err := s.sumField(ctx, s.cfg.BudgetField, pid, sn.Period, nil)
		if err != nil {
			return nil, err
		}
		available := budget.Add(decimal.NewFromInt(sn.BudgetDelta.Amount))
		planned := decimal.Zero
		for _, t := range tracks {
			planned = planned.Add(decimal.NewFromInt(t.PlannedCost.Amount))
		}
		shortage := planned.Sub(available)
		for _, t := range tracks {
			impact := TrackImpact{Track: t}
			if shortage.IsPositive() {
				impact.AtRisk = true
				impact.Shortage = s.money(shortage)
				impact.Reason = fmt.Sprintf("плановые затраты треков продукта %s превышают доступный бюджет на %s",
					pid, s.money(shortage))
			}
			out = append(out, impact)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Track.ID.String() < out[j].Track.ID.String() })
	return out, nil
}
