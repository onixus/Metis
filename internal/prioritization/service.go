package prioritization

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Service — публичный интерфейс модуля.
type Service struct {
	store   Store
	pub     kernel.Publisher
	clock   kernel.Clock
	money   MoneyMetrics  // может быть nil: денежные переменные равны 0
	derived DerivedDemand // может быть nil: переменные спроса равны 0
	impact  ImpactCost    // может быть nil: confirmation_cost равна 0
}

// NewService создаёт сервис.
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock) *Service {
	return &Service{store: store, pub: pub, clock: clock}
}

// WithMoneyMetrics подключает порт денежных метрик (PR-02).
func (s *Service) WithMoneyMetrics(m MoneyMetrics) *Service {
	s.money = m
	return s
}

// WithDerivedDemand подключает порт производного спроса (PR-03).
func (s *Service) WithDerivedDemand(d DerivedDemand) *Service {
	s.derived = d
	return s
}

// WithImpactCost подключает порт стоимости подтверждения изменений (PR-05).
func (s *Service) WithImpactCost(c ImpactCost) *Service {
	s.impact = c
	return s
}

func (s *Service) emit(ctx context.Context, typ string, aggregate, product kernel.ID, actor string, payload any) error {
	if s.pub == nil {
		return nil
	}
	ev, err := kernel.NewEvent(s.clock, typ, aggregate, product, actor, payload)
	if err != nil {
		return err
	}
	return s.pub.Publish(ctx, ev)
}

// ---- Модели (PR-01) ----

func resolveModel(in ModelInput) (formula string, inputs []string, err error) {
	switch in.Type {
	case ModelRICE:
		return FormulaRICE, append([]string(nil), InputsRICE...), nil
	case ModelWSJF:
		return FormulaWSJF, append([]string(nil), InputsWSJF...), nil
	case ModelCustom:
		f, err := ParseFormula(in.Formula)
		if err != nil {
			return "", nil, err
		}
		inputs = in.Inputs
		if len(inputs) == 0 {
			// входы выводятся из формулы: всё, что не системная переменная
			for _, v := range f.Variables() {
				if !isSystemVar(v) {
					inputs = append(inputs, v)
				}
			}
		}
		known := map[string]struct{}{}
		for _, v := range SystemVariables {
			known[v] = struct{}{}
		}
		for _, v := range inputs {
			if isSystemVar(v) {
				return "", nil, kernel.Invalid("inputs", fmt.Sprintf("переменная %q системная", v))
			}
			known[v] = struct{}{}
		}
		for _, v := range f.Variables() {
			if _, ok := known[v]; !ok {
				return "", nil, kernel.Invalid("formula", fmt.Sprintf("переменная %q не объявлена во входах", v))
			}
		}
		return in.Formula, append([]string(nil), inputs...), nil
	default:
		return "", nil, kernel.Invalid("type", fmt.Sprintf("неизвестный тип модели %q", in.Type))
	}
}

func isSystemVar(v string) bool {
	for _, sv := range SystemVariables {
		if sv == v {
			return true
		}
	}
	return false
}

func validateModelInput(in ModelInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return kernel.Invalid("name", "имя модели обязательно")
	}
	return nil
}

// CreateModel создаёт модель оценки. Портфельная модель (ProductID == NilID) требует прав CPO/admin.
func (s *Service) CreateModel(ctx context.Context, sc authz.Scope, in ModelInput) (ScoringModel, error) {
	if err := sc.Require(authz.ActionWritePriority, in.ProductID); err != nil {
		return ScoringModel{}, err
	}
	if err := validateModelInput(in); err != nil {
		return ScoringModel{}, err
	}
	formula, inputs, err := resolveModel(in)
	if err != nil {
		return ScoringModel{}, err
	}
	now := s.clock.Now()
	m := ScoringModel{ID: kernel.NewID(), ProductID: in.ProductID, Name: in.Name, Type: in.Type,
		Formula: formula, Inputs: inputs, CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveModel(ctx, m); err != nil {
		return ScoringModel{}, fmt.Errorf("save model: %w", err)
	}
	return m, s.emit(ctx, EventModelSaved, m.ID, m.ProductID, sc.Subject(), m)
}

// UpdateModel изменяет модель. Продукт модели не меняется.
func (s *Service) UpdateModel(ctx context.Context, sc authz.Scope, id kernel.ID, in ModelInput) (ScoringModel, error) {
	if !sc.Valid() {
		return ScoringModel{}, kernel.ErrForbidden
	}
	m, err := s.store.Model(ctx, id)
	if err != nil {
		return ScoringModel{}, err
	}
	if err := sc.Require(authz.ActionWritePriority, m.ProductID); err != nil {
		return ScoringModel{}, err
	}
	if in.ProductID != kernel.NilID && in.ProductID != m.ProductID {
		return ScoringModel{}, kernel.Invalid("product_id", "продукт модели не изменяется")
	}
	if err := validateModelInput(in); err != nil {
		return ScoringModel{}, err
	}
	formula, inputs, err := resolveModel(in)
	if err != nil {
		return ScoringModel{}, err
	}
	m.Name, m.Type, m.Formula, m.Inputs, m.UpdatedAt = in.Name, in.Type, formula, inputs, s.clock.Now()
	if err := s.store.SaveModel(ctx, m); err != nil {
		return ScoringModel{}, fmt.Errorf("save model: %w", err)
	}
	return m, s.emit(ctx, EventModelSaved, m.ID, m.ProductID, sc.Subject(), m)
}

// Models возвращает модели, доступные субъекту: портфельные и модели продуктов со стратегическим доступом.
func (s *Service) Models(ctx context.Context, sc authz.Scope) ([]ScoringModel, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	all, err := s.store.Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("load models: %w", err)
	}
	out := make([]ScoringModel, 0, len(all))
	for _, m := range all {
		if m.ProductID == kernel.NilID || sc.Allows(authz.ActionReadStrategic, m.ProductID) {
			out = append(out, m)
		}
	}
	return out, nil
}

// modelFor возвращает модель, проверяя, что она применима к продукту.
func (s *Service) modelFor(ctx context.Context, modelID, productID kernel.ID) (ScoringModel, error) {
	m, err := s.store.Model(ctx, modelID)
	if err != nil {
		return ScoringModel{}, err
	}
	if m.ProductID != kernel.NilID && m.ProductID != productID {
		return ScoringModel{}, fmt.Errorf("%w: модель %s принадлежит другому продукту", kernel.ErrValidation, modelID)
	}
	return m, nil
}

// ---- Входы фичи (PR-01) ----

// SetFeatureInputs задаёт значения входных переменных модели для фичи продукта.
func (s *Service) SetFeatureInputs(ctx context.Context, sc authz.Scope, modelID, productID, featureID kernel.ID, values map[string]decimal.Decimal) (FeatureScoreInput, error) {
	if err := sc.Require(authz.ActionWritePriority, productID); err != nil {
		return FeatureScoreInput{}, err
	}
	m, err := s.modelFor(ctx, modelID, productID)
	if err != nil {
		return FeatureScoreInput{}, err
	}
	allowed := map[string]struct{}{}
	for _, v := range m.Inputs {
		allowed[v] = struct{}{}
	}
	for k := range values {
		if _, ok := allowed[k]; !ok {
			return FeatureScoreInput{}, kernel.Invalid("values", fmt.Sprintf("переменная %q не входит в модель", k))
		}
	}
	in := FeatureScoreInput{ModelID: modelID, FeatureID: featureID, ProductID: productID,
		Values: values, UpdatedAt: s.clock.Now(), UpdatedBy: sc.Subject()}
	if err := s.store.SaveInputs(ctx, in); err != nil {
		return FeatureScoreInput{}, fmt.Errorf("save inputs: %w", err)
	}
	return in, s.emit(ctx, EventInputsSet, featureID, productID, sc.Subject(), in)
}

// ---- Скор (PR-01…PR-03) ----

func moneyToMajor(m kernel.Money) decimal.Decimal {
	return decimal.NewFromInt(m.Amount).Div(decimal.NewFromInt(100))
}

// systemVars собирает системные переменные фичи через порты. Недоступность порта или отсутствие данных
// (ErrNotFound) даёт нули; отказ доступа и прочие ошибки возвращаются.
func (s *Service) systemVars(ctx context.Context, sc authz.Scope, featureID kernel.ID) (map[string]decimal.Decimal, error) {
	vars := map[string]decimal.Decimal{}
	for _, v := range SystemVariables {
		vars[v] = decimal.Zero
	}
	if s.money != nil {
		arr, err := s.money.ARRByFeature(ctx, sc, featureID)
		if err != nil && !kernel.IsNotFound(err) {
			return nil, fmt.Errorf("arr: %w", err)
		}
		blocked, err := s.money.BlockedDealsByFeature(ctx, sc, featureID)
		if err != nil && !kernel.IsNotFound(err) {
			return nil, fmt.Errorf("blocked deals: %w", err)
		}
		vars[VarARR], vars[VarBlockedDeals] = moneyToMajor(arr), moneyToMajor(blocked)
	}
	if s.derived != nil {
		fv, err := s.derived.FeatureValue(ctx, sc, featureID)
		if err != nil && !kernel.IsNotFound(err) {
			return nil, fmt.Errorf("derived demand: %w", err)
		}
		vars[VarOwnValue] = moneyToMajor(fv.OwnValue)
		vars[VarDerivedValue] = moneyToMajor(fv.DerivedValue)
		vars[VarTotalValue] = moneyToMajor(fv.TotalValue)
	}
	cost, err := s.cost(ctx, sc, kernel.NilID, featureID)
	if err != nil {
		return nil, err
	}
	vars[VarDevCost] = moneyToMajor(cost.DevCost)
	vars[VarConfirmationCost] = moneyToMajor(cost.ConfirmationCost)
	vars[VarCost] = moneyToMajor(cost.Total)
	return vars, nil
}

func (s *Service) score(ctx context.Context, sc authz.Scope, m ScoringModel, f *Formula, in FeatureScoreInput) (ScoreResult, error) {
	vars, err := s.systemVars(ctx, sc, in.FeatureID)
	if err != nil {
		return ScoreResult{}, err
	}
	res := ScoreResult{ModelID: m.ID, FeatureID: in.FeatureID, ProductID: in.ProductID}
	for _, name := range m.Inputs {
		v, ok := in.Values[name]
		if !ok {
			return ScoreResult{}, kernel.Invalid("values", fmt.Sprintf("для фичи %s не задана переменная %q", in.FeatureID, name))
		}
		vars[name] = v
		res.Components = append(res.Components, Component{Name: name, Value: v})
	}
	used := map[string]struct{}{}
	for _, v := range f.Variables() {
		used[v] = struct{}{}
	}
	for _, name := range SystemVariables {
		if _, ok := used[name]; ok {
			res.Components = append(res.Components, Component{Name: name, Value: vars[name], System: true})
		}
	}
	res.Score, err = f.Eval(vars)
	if err != nil {
		return ScoreResult{}, fmt.Errorf("фича %s: %w", in.FeatureID, err)
	}
	parts := make([]string, 0, len(res.Components))
	for _, c := range res.Components {
		parts = append(parts, c.Name+"="+c.Value.String())
	}
	res.Explanation = fmt.Sprintf("%s = %s при %s", m.Formula, res.Score.String(), strings.Join(parts, ", "))
	return res, nil
}

// Score считает скор фичи по модели.
func (s *Service) Score(ctx context.Context, sc authz.Scope, modelID, featureID kernel.ID) (ScoreResult, error) {
	if !sc.Valid() {
		return ScoreResult{}, kernel.ErrForbidden
	}
	in, err := s.store.Inputs(ctx, modelID, featureID)
	if err != nil {
		if errors.Is(err, kernel.ErrNotFound) {
			// не раскрываем существование фичи чужого продукта
			return ScoreResult{}, err
		}
		return ScoreResult{}, fmt.Errorf("load inputs: %w", err)
	}
	if err := sc.Require(authz.ActionReadStrategic, in.ProductID); err != nil {
		return ScoreResult{}, err
	}
	m, err := s.modelFor(ctx, modelID, in.ProductID)
	if err != nil {
		return ScoreResult{}, err
	}
	f, err := ParseFormula(m.Formula)
	if err != nil {
		return ScoreResult{}, err
	}
	return s.score(ctx, sc, m, f, in)
}

// Ranking возвращает фичи продукта, отсортированные по убыванию скора (стабильно по идентификатору фичи).
// Регуляторно обязательные фичи (PR-04) исключены; полный результат даёт Rank.
func (s *Service) Ranking(ctx context.Context, sc authz.Scope, modelID, productID kernel.ID) ([]ScoreResult, error) {
	res, err := s.Rank(ctx, sc, modelID, productID)
	if err != nil {
		return nil, err
	}
	return res.Ranked, nil
}

// Rank ранжирует фичи продукта: общий список по убыванию скора и отдельно регуляторно обязательные (PR-04),
// тоже по убыванию скора.
func (s *Service) Rank(ctx context.Context, sc authz.Scope, modelID, productID kernel.ID) (RankingResult, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return RankingResult{}, err
	}
	m, err := s.modelFor(ctx, modelID, productID)
	if err != nil {
		return RankingResult{}, err
	}
	f, err := ParseFormula(m.Formula)
	if err != nil {
		return RankingResult{}, err
	}
	inputs, err := s.store.InputsByProduct(ctx, modelID, productID)
	if err != nil {
		return RankingResult{}, fmt.Errorf("load inputs: %w", err)
	}
	res := RankingResult{Ranked: make([]ScoreResult, 0, len(inputs)), Mandatory: []ScoreResult{}}
	for _, in := range inputs {
		r, err := s.score(ctx, sc, m, f, in)
		if err != nil {
			return RankingResult{}, err
		}
		mandatory, err := s.isMandatory(ctx, in.FeatureID)
		if err != nil {
			return RankingResult{}, err
		}
		if mandatory {
			res.Mandatory = append(res.Mandatory, r)
		} else {
			res.Ranked = append(res.Ranked, r)
		}
	}
	sortScores(res.Ranked)
	sortScores(res.Mandatory)
	return res, nil
}

func sortScores(out []ScoreResult) {
	sort.SliceStable(out, func(i, j int) bool {
		if c := out[i].Score.Cmp(out[j].Score); c != 0 {
			return c > 0
		}
		return out[i].FeatureID.String() < out[j].FeatureID.String()
	})
}

// ---- Регуляторно обязательные фичи (PR-04) ----

func (s *Service) isMandatory(ctx context.Context, featureID kernel.ID) (bool, error) {
	fl, err := s.store.Flags(ctx, featureID)
	if err != nil {
		if kernel.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("load flags: %w", err)
	}
	return fl.RegulatoryMandatory, nil
}

// SetRegulatoryMandatory ставит или снимает флаг «регуляторно обязательно». Требует ActionWritePriority.
// При установке флага причина обязательна.
func (s *Service) SetRegulatoryMandatory(ctx context.Context, sc authz.Scope, productID, featureID kernel.ID, on bool, reason string) (FeatureFlags, error) {
	if err := sc.Require(authz.ActionWritePriority, productID); err != nil {
		return FeatureFlags{}, err
	}
	if featureID == kernel.NilID {
		return FeatureFlags{}, kernel.Invalid("feature_id", "идентификатор фичи обязателен")
	}
	if on && strings.TrimSpace(reason) == "" {
		return FeatureFlags{}, kernel.Invalid("reason", "основание регуляторной обязательности обязательно")
	}
	if existing, err := s.store.Flags(ctx, featureID); err == nil && existing.ProductID != productID {
		return FeatureFlags{}, fmt.Errorf("%w: фича %s принадлежит другому продукту", kernel.ErrValidation, featureID)
	} else if err != nil && !kernel.IsNotFound(err) {
		return FeatureFlags{}, fmt.Errorf("load flags: %w", err)
	}
	fl := FeatureFlags{FeatureID: featureID, ProductID: productID, RegulatoryMandatory: on, Reason: reason,
		SetBy: sc.Subject(), SetAt: s.clock.Now()}
	if err := s.store.SaveFlags(ctx, fl); err != nil {
		return FeatureFlags{}, fmt.Errorf("save flags: %w", err)
	}
	return fl, s.emit(ctx, EventFlagsSet, featureID, productID, sc.Subject(), fl)
}

// Flags возвращает флаги фичи; если не задавались — нулевые флаги без ошибки.
func (s *Service) Flags(ctx context.Context, sc authz.Scope, productID, featureID kernel.ID) (FeatureFlags, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return FeatureFlags{}, err
	}
	fl, err := s.store.Flags(ctx, featureID)
	if err != nil {
		if kernel.IsNotFound(err) {
			return FeatureFlags{FeatureID: featureID, ProductID: productID}, nil
		}
		return FeatureFlags{}, fmt.Errorf("load flags: %w", err)
	}
	if fl.ProductID != productID {
		return FeatureFlags{}, kernel.NotFound("feature flags", featureID)
	}
	return fl, nil
}

// ---- Стоимость фичи (PR-05) ----

// SetDevCost задаёт стоимость разработки фичи (задаёт PM). Требует ActionWritePriority.
func (s *Service) SetDevCost(ctx context.Context, sc authz.Scope, productID, featureID kernel.ID, cost kernel.Money) (FeatureCost, error) {
	if err := sc.Require(authz.ActionWritePriority, productID); err != nil {
		return FeatureCost{}, err
	}
	if featureID == kernel.NilID {
		return FeatureCost{}, kernel.Invalid("feature_id", "идентификатор фичи обязателен")
	}
	if cost.Amount < 0 {
		return FeatureCost{}, kernel.Invalid("dev_cost", "стоимость не может быть отрицательной")
	}
	if cost.Amount != 0 && cost.Currency == "" {
		return FeatureCost{}, kernel.Invalid("dev_cost", "код валюты обязателен")
	}
	if err := s.store.SaveDevCost(ctx, productID, featureID, cost); err != nil {
		return FeatureCost{}, fmt.Errorf("save dev cost: %w", err)
	}
	fc, err := s.cost(ctx, sc, productID, featureID)
	if err != nil {
		return FeatureCost{}, err
	}
	return fc, s.emit(ctx, EventDevCostSet, featureID, productID, sc.Subject(), fc)
}

// Cost возвращает стоимость фичи: разработка + подтверждение изменений по классу влияния (PR-05).
// Без порта ImpactCost стоимость подтверждения равна 0. Требует ActionReadStrategic по продукту.
func (s *Service) Cost(ctx context.Context, sc authz.Scope, productID, featureID kernel.ID) (FeatureCost, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return FeatureCost{}, err
	}
	return s.cost(ctx, sc, productID, featureID)
}

// cost собирает стоимость без проверки прав (вызывающий уже проверил доступ к продукту фичи).
func (s *Service) cost(ctx context.Context, sc authz.Scope, productID, featureID kernel.ID) (FeatureCost, error) {
	fc := FeatureCost{FeatureID: featureID, ProductID: productID}
	dev, err := s.store.DevCost(ctx, featureID)
	if err != nil && !kernel.IsNotFound(err) {
		return FeatureCost{}, fmt.Errorf("load dev cost: %w", err)
	}
	fc.DevCost = dev
	if s.impact != nil {
		conf, err := s.impact.ConfirmationCost(ctx, sc, featureID)
		if err != nil && !kernel.IsNotFound(err) {
			return FeatureCost{}, fmt.Errorf("confirmation cost: %w", err)
		}
		fc.ConfirmationCost = conf
	}
	if fc.Total, err = fc.DevCost.Add(fc.ConfirmationCost); err != nil {
		return FeatureCost{}, fmt.Errorf("стоимость фичи %s: %w", featureID, err)
	}
	return fc, nil
}
