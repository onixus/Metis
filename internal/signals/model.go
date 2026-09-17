// Package signals — приём и разбор сигналов: запросов и потребностей с привязкой к продукту,
// аккаунту, сделке, версии (SG-01…SG-03, SG-05). Сырьё приходит из CRM через порт ports.CRM,
// из service desk, ручного ввода и импорта файлов. Публичный интерфейс — этот пакет.
//
// TODO(question-07): гипотезы (discovery) — этап 2; поле HypothesisID зарезервировано.
package signals

import (
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// Source — источник сигнала (SG-01).
type Source string

const (
	SourceCRM         Source = "crm"
	SourceServiceDesk Source = "service_desk"
	SourceManual      Source = "manual"
	SourceImport      Source = "import"
)

// ValidSource сообщает, известен ли источник.
func ValidSource(s Source) bool {
	switch s {
	case SourceCRM, SourceServiceDesk, SourceManual, SourceImport:
		return true
	}
	return false
}

// Status — статус разбора (SG-03).
type Status string

const (
	StatusNew      Status = "new"
	StatusInReview Status = "in_review"
	StatusLinked   Status = "linked"
	StatusRejected Status = "rejected"
	// StatusMerged — слит с другим сигналом (SG-04, этап 2). Через Triage не выставляется.
	StatusMerged Status = "merged"
)

// ValidStatus сообщает, известен ли статус.
func ValidStatus(s Status) bool {
	switch s {
	case StatusNew, StatusInReview, StatusLinked, StatusRejected, StatusMerged:
		return true
	}
	return false
}

// Signal — запрос или потребность (ТЗ 2.1). ProductID обязателен (SG-02).
type Signal struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	Source    Source    `json:"source"`
	Text      string    `json:"text"`
	// ExternalKey — ключ во внешней системе для идемпотентного импорта (ТЗ 4.2).
	ExternalKey string `json:"external_key,omitempty"`

	// Опциональные привязки (SG-02). AccountID и DealID — внешние идентификаторы CRM.
	AccountID string `json:"account_id,omitempty"`
	DealID    string `json:"deal_id,omitempty"`
	Version   string `json:"version,omitempty"`
	Segment   string `json:"segment,omitempty"`

	// Weight — денежный вес: сумма сделки, а без сделки — ARR аккаунта (SG-05).
	Weight kernel.Money `json:"weight"`
	// AccountARR — ARR аккаунта на момент приёма; для ARRByFeature.
	AccountARR kernel.Money `json:"account_arr"`
	// BlocksDeal — без запрошенной фичи сделка не закроется; для BlockedDealsByFeature.
	BlocksDeal bool `json:"blocks_deal"`

	// Разбор (SG-03).
	Status  Status      `json:"status"`
	DueDate kernel.Date `json:"due_date"` // срок разбора; без времени (инвариант 7)

	// Привязка (SG-05): ровно одна из FeatureID / ContractID / HypothesisID.
	FeatureID    kernel.ID `json:"feature_id,omitempty"`
	ContractID   kernel.ID `json:"contract_id,omitempty"`
	HypothesisID kernel.ID `json:"hypothesis_id,omitempty"` // этап 2, зарезервировано

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsLinked сообщает, привязан ли сигнал к фиче, контракту или гипотезе.
func (s Signal) IsLinked() bool {
	return s.FeatureID != kernel.NilID || s.ContractID != kernel.NilID || s.HypothesisID != kernel.NilID
}

// Названия доменных событий.
const (
	EventSignalIngested = "signals.signal.ingested"
	EventSignalTriaged  = "signals.signal.triaged"
	EventSignalLinked   = "signals.signal.linked"
)
