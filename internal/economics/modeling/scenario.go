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
	FieldKey  string             `json:"field_key,omitempty"`
	Source    PlatformSourceKind `json:"source,omitempty"`
	ProductID kernel.ID          `json:"product_id,omitempty"`
	TeamID    kernel.ID          `json:"team_id,omitempty"`
	Period    Period             `json:"period,omitempty"`
	Value     decimal.Decimal    `json:"value"`
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
	ID             kernel.ID   `json:"id"`
	ProductID      kernel.ID   `json:"product_id"`
	Title          string      `json:"title"`
	DueDate        kernel.Date `json:"due_date"`
	Regulatory     bool        `json:"regulatory"`
	PlannedDate    kernel.Date `json:"planned_date"`
	PlanningSource string      `json:"planning_source"`
	PlanningStatus string      `json:"planning_status"`
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
	Commitment       CommitmentRef `json:"commitment"`
	Breached         bool          `json:"breached"`
	Reason           string        `json:"reason"`
	BaselineDate     kernel.Date   `json:"baseline_date"`
	ScenarioDate     kernel.Date   `json:"scenario_date"`
	Known            bool          `json:"known"`
	BaselineBreached bool          `json:"baseline_breached"`
	NewlyBreached    bool          `json:"newly_breached"`
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
	Scenario         Scenario               `json:"scenario"`
	Metrics          map[string]string      `json:"metrics"`
	BaseMetrics      map[string]string      `json:"base_metrics"`
	Explanations     map[string]Explanation `json:"explanations,omitempty"`
	BaseExplanations map[string]Explanation `json:"base_explanations,omitempty"`
	PnL              []PnL                  `json:"pnl"`
	BasePnL          []PnL                  `json:"base_pnl"`
	Commitments      []CommitmentImpact     `json:"commitments"`
	Tracks           []TrackImpact          `json:"tracks"`
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
	if err := s.validateScenarioInputs(ctx, sc, in); err != nil {
		return Scenario{}, err
	}
	now := s.clock.Now()
	if in.ID == kernel.NilID {
		in.ID, in.CreatedAt = kernel.NewID(), now
	} else if _, err := s.store.Scenario(ctx, sc, in.ID); err != nil {
		return Scenario{}, err
	}
	in.Actor, in.UpdatedAt = sc.Subject(), now
	if err := s.store.SaveScenario(ctx, sc, in); err != nil {
		return Scenario{}, fmt.Errorf("save scenario: %w", err)
	}
	return in, nil
}

// Scenarios возвращает сценарии.
func (s *Service) Scenarios(ctx context.Context, sc authz.Scope) ([]Scenario, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Scenarios(ctx, sc)
}

// RunScenario считает сценарий тем же движком формул с подменой входных значений (EC-13)
// и показывает влияние на обязательства и треки сертификации (DA-02).
// Фактические данные не изменяются: подмена живёт только внутри расчёта.
func (s *Service) RunScenario(ctx context.Context, sc authz.Scope, id kernel.ID, metrics []string) (ScenarioResult, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return ScenarioResult{}, err
	}
	sn, err := s.store.Scenario(ctx, sc, id)
	if err != nil {
		return ScenarioResult{}, err
	}
	// Stored scenarios can predate input validation.
	if err := s.validateScenarioInputs(ctx, sc, sn); err != nil {
		return ScenarioResult{}, err
	}
	if len(metrics) > 64 {
		return ScenarioResult{}, kernel.Invalid("metrics", "не более 64 показателей за расчёт")
	}
	products := sn.Products
	if len(products) == 0 {
		products, err = s.productsWithData(ctx, sc, sn.Period)
		if err != nil {
			return ScenarioResult{}, err
		}
	}
	res := ScenarioResult{Scenario: sn, Metrics: map[string]string{}, BaseMetrics: map[string]string{}, Explanations: map[string]Explanation{}, BaseExplanations: map[string]Explanation{}}

	base := s.source(ctx, sc, nil)
	withOverrides := s.source(ctx, sc, sn.Overrides)
	withOverrides.platformMemo = base.platformMemo
	withOverrides.platformCost = base.platformCost
	withOverrides.platformLock = base.platformLock
	withOverrides.explainBudget = base.explainBudget
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
			baselineExplanation, err := base.explainMetric(key, sl, 0)
			if err != nil {
				return ScenarioResult{}, err
			}
			scenarioExplanation, err := withOverrides.explainMetric(key, sl, 0)
			if err != nil {
				return ScenarioResult{}, err
			}
			res.BaseExplanations[key+"@"+pid.String()] = baselineExplanation
			res.Explanations[key+"@"+pid.String()] = scenarioExplanation
		}
	}
	for _, pid := range products {
		basePnL, err := s.pnl(ctx, sc, pid, sn.Period, nil)
		if err != nil {
			return ScenarioResult{}, err
		}
		scenarioPnL, err := s.pnl(ctx, sc, pid, sn.Period, sn.Overrides)
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
	if err := s.logAccessChecked(ctx, sc, "read", "economics.scenario:"+sn.ID.String(), kernel.NilID,
		map[string]any{"period": sn.Period.String(), "products": len(products)}); err != nil {
		return ScenarioResult{}, err
	}
	return res, nil
}

// commitmentImpact compares explicitly linked baseline and scenario delivery dates.
// Missing or ambiguous plans remain unknown; wall-clock time never substitutes a plan.
func (s *Service) commitmentImpact(ctx context.Context, sc authz.Scope, sn Scenario, products []kernel.ID) ([]CommitmentImpact, error) {
	if s.commitments == nil {
		return nil, nil
	}
	if sn.CapacityShiftDays < -36500 || sn.CapacityShiftDays > 36500 {
		return nil, kernel.Invalid("capacity_shift_days", "сдвиг ограничен 36500 днями в обе стороны")
	}
	refs, err := s.commitments.Due(ctx, sc, products)
	if err != nil {
		return nil, fmt.Errorf("обязательства: %w", err)
	}
	out := make([]CommitmentImpact, 0, len(refs))
	for _, r := range refs {
		impact := CommitmentImpact{Commitment: r}
		if r.PlannedDate.IsZero() || r.DueDate.IsZero() || (r.PlanningStatus != "" && r.PlanningStatus != "known") {
			impact.Reason = "влияние неизвестно: нет однозначной плановой даты связанной поставки и срока обязательства"
			out = append(out, impact)
			continue
		}
		impact.BaselineDate = r.PlannedDate
		impact.ScenarioDate = r.PlannedDate.AddDays(sn.CapacityShiftDays)
		if impact.ScenarioDate.Year < 1 || impact.ScenarioDate.Year > 9999 {
			return nil, kernel.Invalid("capacity_shift_days", "сценарная дата вне допустимого диапазона")
		}
		impact.Known = true
		impact.BaselineBreached = impact.BaselineDate.After(r.DueDate)
		impact.Breached = impact.ScenarioDate.After(r.DueDate)
		impact.NewlyBreached = impact.Breached && !impact.BaselineBreached
		switch {
		case impact.NewlyBreached:
			impact.Reason = fmt.Sprintf("новое нарушение: план %s → %s после сдвига на %d дн.; срок %s", impact.BaselineDate, impact.ScenarioDate, sn.CapacityShiftDays, r.DueDate)
		case impact.Breached:
			impact.Reason = fmt.Sprintf("исходный план %s уже позже срока %s; сценарный план %s также нарушает срок", impact.BaselineDate, r.DueDate, impact.ScenarioDate)
		case impact.BaselineBreached:
			impact.Reason = fmt.Sprintf("сценарий устраняет исходное нарушение: план %s → %s, срок %s", impact.BaselineDate, impact.ScenarioDate, r.DueDate)
		default:
			impact.Reason = fmt.Sprintf("сценарный план %s укладывается в срок %s (исходный %s)", impact.ScenarioDate, r.DueDate, impact.BaselineDate)
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
		if err := s.scenarioMoney(r.PlannedCost, "track.planned_cost"); err != nil {
			return nil, err
		}
		byProduct[r.ProductID] = append(byProduct[r.ProductID], r)
	}
	if len(refs) > 0 {
		field, err := s.store.Field(ctx, sc, s.cfg.BudgetField)
		if err != nil && !kernel.IsNotFound(err) {
			return nil, fmt.Errorf("поле бюджета: %w", err)
		}
		if err == nil {
			version, ok := field.At(sn.Period.End())
			if !ok || version.Type != FieldMoney || version.Currency != s.cfg.Currency {
				return nil, kernel.Invalid("budget", "действующее поле бюджета должно быть денежным в валюте модели")
			}
		}
	}
	out := make([]TrackImpact, 0, len(refs))
	for pid, tracks := range byProduct {
		budget, err := s.sumField(ctx, sc, s.cfg.BudgetField, pid, sn.Period, sn.Overrides)
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

func (s *Service) scenarioMoney(m kernel.Money, field string) error {
	if m.Amount == 0 && m.Currency == "" {
		return nil
	}
	if m.Currency != s.cfg.Currency {
		return kernel.Invalid(field, "валюта должна совпадать с валютой модели; конвертация не поддерживается")
	}
	return nil
}

// validateScenarioInputs validates legacy documents before arithmetic as well as new saves.
func (s *Service) validateScenarioInputs(ctx context.Context, sc authz.Scope, sn Scenario) error {
	if sn.Period.IsZero() {
		return kernel.Invalid("period", "период обязателен")
	}
	if _, err := ParsePeriod(sn.Period.String()); err != nil {
		return err
	}
	if sn.CapacityShiftDays < -36500 || sn.CapacityShiftDays > 36500 {
		return kernel.Invalid("capacity_shift_days", "сдвиг ограничен 36500 днями в обе стороны")
	}
	if err := s.scenarioMoney(sn.BudgetDelta, "budget_delta"); err != nil {
		return err
	}
	if len(sn.Overrides) > 128 || len(sn.Products) > 10000 {
		return kernel.Invalid("scenario", "превышен предел входов сценария")
	}
	products := make(map[kernel.ID]bool, len(sn.Products))
	for _, product := range sn.Products {
		if product == kernel.NilID || products[product] {
			return kernel.Invalid("products", "продукты должны быть уникальными и непустыми")
		}
		if err := s.requireRead(sc, product, authz.FinanceAggregates); err != nil {
			return err
		}
		products[product] = true
	}
	for i, o := range sn.Overrides {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !o.Period.IsZero() && o.Period != sn.Period {
			return kernel.Invalid("overrides", "подмена относится к другому периоду")
		}
		if o.ProductID != kernel.NilID {
			if err := s.requireRead(sc, o.ProductID, authz.FinanceAggregates); err != nil {
				return err
			}
			if len(products) > 0 && !products[o.ProductID] {
				return kernel.Invalid("overrides", "продукт подмены отсутствует в сценарии")
			}
		}
		if err := boundedNumericInput(o.Value); err != nil {
			return err
		}
		if o.Source != "" {
			if o.FieldKey != "" {
				return kernel.Invalid("overrides", "поле и прямой источник взаимоисключающие")
			}
			if o.Source != SourceWorklogs && o.Source != SourceCRMRevenue && o.Source != SourceHeadcount {
				return kernel.Invalid("source", "неизвестный источник подмены")
			}
			if err := s.requireRead(sc, o.ProductID, authz.FinanceFull); err != nil {
				return err
			}
			if o.Value.IsNegative() || !o.Value.Equal(o.Value.Truncate(0)) || !o.Value.BigInt().IsInt64() {
				return kernel.Invalid("overrides", "подмена источника требует неотрицательное целое int64")
			}
		} else {
			if o.FieldKey == "" {
				return kernel.Invalid("overrides", "укажите поле или прямой источник")
			}
			if o.TeamID != kernel.NilID {
				return kernel.Invalid("overrides", "командная подмена доступна только прямому источнику")
			}
			field, err := s.store.Field(ctx, sc, o.FieldKey)
			if err != nil {
				return fmt.Errorf("подмена поля %q: %w", o.FieldKey, err)
			}
			version, ok := field.At(sn.Period.End())
			if !ok || (version.Source != SourceImport && version.Source != SourceManual) {
				return kernel.Invalid("overrides", "требуется действующее входное поле")
			}
			switch version.Type {
			case FieldMoney:
				if version.Currency != s.cfg.Currency || !o.Value.Equal(o.Value.Truncate(0)) || !o.Value.BigInt().IsInt64() {
					return kernel.Invalid("overrides", "денежная подмена требует целые минорные единицы int64 в валюте модели")
				}
			case FieldNumber, FieldPercent:
			default:
				return kernel.Invalid("overrides", "подмена доступна только для числовых полей")
			}
		}
		for _, previous := range sn.Overrides[:i] {
			if previous.FieldKey == o.FieldKey && previous.Source == o.Source && (previous.ProductID == kernel.NilID || o.ProductID == kernel.NilID || previous.ProductID == o.ProductID) && (previous.TeamID == kernel.NilID || o.TeamID == kernel.NilID || previous.TeamID == o.TeamID) {
				return kernel.Invalid("overrides", "пересекающиеся подмены одного поля запрещены")
			}
		}
	}
	return nil
}
