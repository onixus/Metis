// Package prioritization — модели оценки фич (PR-01), денежные метрики (PR-02)
// и учёт производного спроса хаба (PR-03). Домен не зависит от HTTP и БД.
package prioritization

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// ModelType — тип модели оценки.
type ModelType string

const (
	ModelRICE   ModelType = "rice"
	ModelWSJF   ModelType = "wsjf"
	ModelCustom ModelType = "custom"
)

// Встроенные формулы.
const (
	FormulaRICE = "reach * impact * confidence / effort"
	FormulaWSJF = "(user_business_value + time_criticality + risk_reduction) / job_size"
)

// Входные переменные встроенных моделей.
var (
	InputsRICE = []string{"reach", "impact", "confidence", "effort"}
	InputsWSJF = []string{"user_business_value", "time_criticality", "risk_reduction", "job_size"}
)

// Системные переменные, доступные в любой формуле (PR-02, PR-03). Деньги — в основных единицах валюты
// (Amount/100) как decimal.
const (
	VarARR          = "arr"
	VarBlockedDeals = "blocked_deals"
	VarOwnValue     = "own_value"
	VarDerivedValue = "derived_value"
	VarTotalValue   = "total_value"
)

// SystemVariables — перечень системных переменных.
var SystemVariables = []string{VarARR, VarBlockedDeals, VarOwnValue, VarDerivedValue, VarTotalValue}

// ScoringModel — модель оценки. ProductID == NilID означает портфельную модель для всех продуктов.
type ScoringModel struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	Name      string    `json:"name"`
	Type      ModelType `json:"type"`
	Formula   string    `json:"formula"`
	Inputs    []string  `json:"inputs"` // входные переменные, задаваемые PM
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ModelInput — данные для создания или изменения модели.
type ModelInput struct {
	ProductID kernel.ID
	Name      string
	Type      ModelType
	Formula   string   // только для custom
	Inputs    []string // только для custom; для rice/wsjf берутся встроенные
}

// FeatureScoreInput — значения входных переменных модели для фичи (задаёт PM).
type FeatureScoreInput struct {
	ModelID   kernel.ID                  `json:"model_id"`
	FeatureID kernel.ID                  `json:"feature_id"`
	ProductID kernel.ID                  `json:"product_id"`
	Values    map[string]decimal.Decimal `json:"values"`
	UpdatedAt time.Time                  `json:"updated_at"`
	UpdatedBy string                     `json:"updated_by"`
}

// Component — переменная и её значение в пояснении скора.
type Component struct {
	Name   string          `json:"name"`
	Value  decimal.Decimal `json:"value"`
	System bool            `json:"system"` // системная (деньги, производный спрос), а не введённая PM
}

// ScoreResult — скор фичи с компонентами и пояснением.
type ScoreResult struct {
	ModelID     kernel.ID       `json:"model_id"`
	FeatureID   kernel.ID       `json:"feature_id"`
	ProductID   kernel.ID       `json:"product_id"`
	Score       decimal.Decimal `json:"score"`
	Components  []Component     `json:"components"`
	Explanation string          `json:"explanation"`
}

// MoneyMetrics — порт денежных метрик фичи (PR-02); реализует модуль signals.
type MoneyMetrics interface {
	ARRByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error)
	BlockedDealsByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error)
}

// DerivedDemand — порт производного спроса (PR-03, PG-07); реализует *portfoliograph.Service.
type DerivedDemand interface {
	FeatureValue(ctx context.Context, sc authz.Scope, featureID kernel.ID) (portfoliograph.FeatureValue, error)
}

// События модуля.
const (
	EventModelSaved = "prioritization.model.saved"
	EventInputsSet  = "prioritization.inputs.set"
)
