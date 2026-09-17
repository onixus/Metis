// Package commitments — реестр обязательств продукта: клиентских (кому, что, срок, основание,
// владелец) и регуляторных (срок сертификата, срок техподдержки, срок устранения уязвимости)
// (CT-01, CT-02). Модуль реализует порт portfoliograph.CommitmentChecker, поднимает алерты при
// сдвигах roadmap, нарушающих обязательство (CT-03), и заводит элемент roadmap на продление
// сертификата через порт RoadmapWriter (CT-04). Публичный интерфейс — этот пакет.
package commitments

import (
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// Kind — вид обязательства.
type Kind string

const (
	KindCustomer   Kind = "customer"   // клиентское (CT-01)
	KindRegulatory Kind = "regulatory" // регуляторное (CT-02)
)

// ValidKind сообщает, известен ли вид.
func ValidKind(k Kind) bool { return k == KindCustomer || k == KindRegulatory }

// Subtype — подтип регуляторного обязательства (CT-02). Для клиентских — пусто.
type Subtype string

const (
	SubtypeCertificateExpiry Subtype = "certificate_expiry" // срок действия сертификата
	SubtypeSupportEnd        Subtype = "support_end"        // срок технической поддержки
	SubtypeVulnFixDeadline   Subtype = "vuln_fix_deadline"  // срок устранения уязвимости
)

// ValidSubtype сообщает, известен ли подтип.
func ValidSubtype(s Subtype) bool {
	switch s {
	case SubtypeCertificateExpiry, SubtypeSupportEnd, SubtypeVulnFixDeadline:
		return true
	}
	return false
}

// Status — статус обязательства.
type Status string

const (
	StatusActive    Status = "active"
	StatusFulfilled Status = "fulfilled"
	StatusBreached  Status = "breached" // срок наступил, обязательство не исполнено
	StatusCancelled Status = "cancelled"
)

// ValidStatus сообщает, известен ли статус.
func ValidStatus(s Status) bool {
	switch s {
	case StatusActive, StatusFulfilled, StatusBreached, StatusCancelled:
		return true
	}
	return false
}

// Commitment — обязательство (ТЗ 2.1). ProductID обязателен; обязательства входят в
// стратегический срез продукта (ТЗ 2.4).
type Commitment struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	Kind      Kind      `json:"kind"`
	Subtype   Subtype   `json:"subtype,omitempty"`
	// Counterparty — кому: аккаунт CRM (внешний идентификатор) или регулятор.
	Counterparty string `json:"counterparty"`
	// Subject — что обещано.
	Subject string      `json:"subject"`
	DueDate kernel.Date `json:"due_date"`
	// Basis — основание: договор, сертификат №, регламент.
	Basis  string `json:"basis"`
	Owner  string `json:"owner"`
	Status Status `json:"status"`

	// Опциональные привязки.
	FeatureID kernel.ID `json:"feature_id,omitempty"`
	ReleaseID kernel.ID `json:"release_id,omitempty"`
	// RenewalItemID — элемент roadmap на продление сертификата (CT-04); создаётся один раз.
	RenewalItemID kernel.ID `json:"renewal_item_id,omitempty"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AlertKind — причина алерта.
type AlertKind string

// AlertRoadmapShift — сдвиг roadmap, из-за которого срок обязательства нарушается (CT-03).
const AlertRoadmapShift AlertKind = "roadmap_shift"

// Alert — запись append-only списка алертов по обязательству (CT-03).
type Alert struct {
	ID           kernel.ID `json:"id"`
	CommitmentID kernel.ID `json:"commitment_id"`
	ProductID    kernel.ID `json:"product_id"`
	Kind         AlertKind `json:"kind"`
	Message      string    `json:"message"`
	// EventID — событие-источник (portfoliograph.EventDateShifted или roadmap.EventDatesChanged).
	EventID  kernel.ID   `json:"event_id"`
	NewDate  kernel.Date `json:"new_date"`
	DueDate  kernel.Date `json:"due_date"`
	RaisedAt time.Time   `json:"raised_at"`
	// Acknowledged — владелец подтвердил, что алерт увидел; единственное изменяемое поле.
	Acknowledged   bool      `json:"acknowledged"`
	AcknowledgedBy string    `json:"acknowledged_by,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
}

// Settings — настройки модуля.
type Settings struct {
	// LeadMonths — за сколько месяцев до истечения сертификата создаётся элемент roadmap на
	// продление (CT-04). По умолчанию 18.
	LeadMonths int `json:"lead_months"`
}

// DefaultLeadMonths — срок упреждения по умолчанию (CT-04).
const DefaultLeadMonths = 18

// Типы доменных событий модуля.
const (
	EventCommitmentCreated   = "commitments.commitment.created"
	EventCommitmentUpdated   = "commitments.commitment.updated"
	EventCommitmentFulfilled = "commitments.commitment.fulfilled"
	EventCommitmentCancelled = "commitments.commitment.cancelled"
	EventAlertRaised         = "commitments.alert.raised"
	EventAlertAcknowledged   = "commitments.alert.acknowledged"
	EventRenewalPlanned      = "commitments.renewal.planned"
)
