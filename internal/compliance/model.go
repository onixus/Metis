// Package compliance — SSDLC и сертификация (CM-01…CM-07): каталог версионируемых наборов
// требований, шаблоны треков, треки сертификации версии с гейтами и чек-листами,
// журнал доказательств только INSERT со сцепкой хешей, класс влияния фичи и
// сертифицированные конфигурации (CertifiedBaseline) с распространением влияния по графу.
//
// Публичный интерфейс — этот пакет. Номера документов ФСТЭК и реестра не указываются (ТЗ 8.2):
// коды наборов требований синтетические.
package compliance

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// RequirementSetStatus — статус набора требований (CM-01).
type RequirementSetStatus string

const (
	RequirementSetDraft     RequirementSetStatus = "draft"
	RequirementSetPublished RequirementSetStatus = "published"
	RequirementSetRetired   RequirementSetStatus = "retired"
)

// RequirementItem — одно требование набора.
type RequirementItem struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// RequirementSet — версионируемый набор требований, привязанный к типу продукта (CM-01).
// Новая версия — новая запись с тем же Code; опубликованные версии неизменяемы.
type RequirementSet struct {
	ID          kernel.ID                  `json:"id"`
	Code        string                     `json:"code"` // синтетический код, например «REESTR», «FSTEC-UD4»
	Version     int                        `json:"version"`
	ProductType portfoliograph.ProductType `json:"product_type"`
	Items       []RequirementItem          `json:"items"`
	Status      RequirementSetStatus       `json:"status"`
	CreatedBy   string                     `json:"created_by"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

// GateKind — категория гейта: по ней релиз получает статус «готов к сертификации» (CM-05).
type GateKind string

const (
	GateKindSSDLC    GateKind = "ssdlc"
	GateKindRegistry GateKind = "registry"
	GateKindFSTEC    GateKind = "fstec"
	GateKindSupport  GateKind = "support"
)

// ValidGateKind сообщает, известна ли категория.
func ValidGateKind(k GateKind) bool {
	switch k {
	case GateKindSSDLC, GateKindRegistry, GateKindFSTEC, GateKindSupport:
		return true
	}
	return false
}

// GateKeyCertificate — ключ гейта «сертификат»: его прохождение завершает трек и создаёт baseline.
const GateKeyCertificate = "certificate"

// GateTemplate — гейт шаблона (CM-02).
//
// ParallelGroup задаёт ветку, идущую параллельно другим веткам: гейты разных непустых групп
// не блокируют друг друга; внутри группы и для гейтов без группы действует порядок Order.
type GateTemplate struct {
	Key                string   `json:"key"`
	Name               string   `json:"name"`
	Kind               GateKind `json:"kind"`
	Order              int      `json:"order"`
	ParallelGroup      string   `json:"parallel_group,omitempty"`
	RequirementSetCode string   `json:"requirement_set_code,omitempty"`
	Checklist          []string `json:"checklist"`
}

// TrackTemplate — шаблон трека по типу продукта (CM-02).
type TrackTemplate struct {
	ID          kernel.ID                  `json:"id"`
	ProductType portfoliograph.ProductType `json:"product_type"`
	Name        string                     `json:"name"`
	Gates       []GateTemplate             `json:"gates"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

// DefaultTemplates — шаблоны по умолчанию из ТЗ 2.5 для каждого типа продукта:
// гейты SSDLC релиза → реестр Минцифры ∥ ФСТЭК (заявка → лаборатория → орган по сертификации →
// сертификат) → поддержка. Используются для seed.
func DefaultTemplates() []TrackTemplate {
	types := []portfoliograph.ProductType{
		portfoliograph.ProductTypeSecurity,
		portfoliograph.ProductTypeInfrastructure,
		portfoliograph.ProductTypePlatform,
		portfoliograph.ProductTypeOther,
	}
	out := make([]TrackTemplate, 0, len(types))
	for _, pt := range types {
		out = append(out, TrackTemplate{
			ProductType: pt,
			Name:        "Сертификация версии (" + string(pt) + ")",
			Gates:       defaultGates(),
		})
	}
	return out
}

func defaultGates() []GateTemplate {
	return []GateTemplate{
		{Key: "ssdlc", Name: "Гейты SSDLC релиза", Kind: GateKindSSDLC, Order: 1,
			Checklist: []string{"sast", "sca", "dast", "fuzzing", "sbom"}},
		{Key: "registry", Name: "Реестр Минцифры", Kind: GateKindRegistry, Order: 2, ParallelGroup: "registry",
			RequirementSetCode: "REESTR", Checklist: []string{"application", "decision"}},
		{Key: "fstec_application", Name: "ФСТЭК: заявка", Kind: GateKindFSTEC, Order: 3, ParallelGroup: "fstec",
			RequirementSetCode: "FSTEC-UD4", Checklist: []string{"application"}},
		{Key: "fstec_lab", Name: "Испытательная лаборатория", Kind: GateKindFSTEC, Order: 4, ParallelGroup: "fstec",
			RequirementSetCode: "FSTEC-UD4", Checklist: []string{"test_report"}},
		{Key: "fstec_body", Name: "Орган по сертификации", Kind: GateKindFSTEC, Order: 5, ParallelGroup: "fstec",
			RequirementSetCode: "FSTEC-UD4", Checklist: []string{"expert_conclusion"}},
		{Key: GateKeyCertificate, Name: "Сертификат", Kind: GateKindFSTEC, Order: 6, ParallelGroup: "fstec",
			RequirementSetCode: "FSTEC-UD4", Checklist: []string{"certificate_scan"}},
		{Key: "support", Name: "Поддержка: регуляторные обязательства", Kind: GateKindSupport, Order: 7,
			Checklist: []string{"vuln_process", "update_channel"}},
	}
}

// TrackStatus — статус трека (CM-03).
type TrackStatus string

const (
	TrackActive    TrackStatus = "active"
	TrackCertified TrackStatus = "certified"
	TrackFailed    TrackStatus = "failed"
)

// GateStatus — статус гейта (CM-03).
type GateStatus string

const (
	GatePending    GateStatus = "pending"
	GateInProgress GateStatus = "in_progress"
	GatePassed     GateStatus = "passed"
	GateFailed     GateStatus = "failed"
)

// ChecklistItem — пункт чек-листа доказательств гейта.
type ChecklistItem struct {
	Key        string    `json:"key"`
	Text       string    `json:"text"`
	Done       bool      `json:"done"`
	EvidenceID kernel.ID `json:"evidence_id,omitempty"`
}

// Gate — гейт трека: владелец, срок, затраты, чек-лист, статус (CM-03).
type Gate struct {
	ID                 kernel.ID       `json:"id"`
	Key                string          `json:"key"`
	Name               string          `json:"name"`
	Kind               GateKind        `json:"kind"`
	Order              int             `json:"order"`
	ParallelGroup      string          `json:"parallel_group,omitempty"`
	RequirementSetCode string          `json:"requirement_set_code,omitempty"`
	Owner              string          `json:"owner,omitempty"`
	DueDate            kernel.Date     `json:"due_date"` // без времени (инвариант 7)
	Cost               kernel.Money    `json:"cost"`
	Checklist          []ChecklistItem `json:"checklist"`
	Status             GateStatus      `json:"status"`
	PassedAt           time.Time       `json:"passed_at,omitempty"`
}

// ChecklistDone сообщает, закрыты ли все пункты чек-листа.
func (g Gate) ChecklistDone() bool {
	for _, it := range g.Checklist {
		if !it.Done {
			return false
		}
	}
	return true
}

// Track — трек сертификации версии продукта (CM-03). ReleaseID — релиз roadmap.
type Track struct {
	ID         kernel.ID   `json:"id"`
	ProductID  kernel.ID   `json:"product_id"`
	ReleaseID  kernel.ID   `json:"release_id"`
	Version    string      `json:"version"`
	TemplateID kernel.ID   `json:"template_id"`
	Status     TrackStatus `json:"status"`
	Gates      []Gate      `json:"gates"`
	BaselineID kernel.ID   `json:"baseline_id,omitempty"` // создан при прохождении гейта «сертификат»
	CreatedBy  string      `json:"created_by"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// EvidenceStatus — статус доказательства (CM-04).
type EvidenceStatus string

const (
	EvidenceSubmitted EvidenceStatus = "submitted"
	EvidenceAccepted  EvidenceStatus = "accepted"
	EvidenceRejected  EvidenceStatus = "rejected"
)

// ValidEvidenceStatus сообщает, известен ли статус.
func ValidEvidenceStatus(s EvidenceStatus) bool {
	switch s {
	case EvidenceSubmitted, EvidenceAccepted, EvidenceRejected:
		return true
	}
	return false
}

// EvidenceItem — запись журнала доказательств (CM-04). Журнал только INSERT: изменение статуса —
// новая запись с тем же ID и Supersedes = Seq предыдущей. Hash = SHA-256(PrevHash || canonical_json).
type EvidenceItem struct {
	Seq        int64          `json:"seq"`
	ID         kernel.ID      `json:"id"`
	ProductID  kernel.ID      `json:"product_id"`
	TrackID    kernel.ID      `json:"track_id"`
	GateID     kernel.ID      `json:"gate_id"`
	URL        string         `json:"url"`
	SHA256     string         `json:"sha256"` // hex, 64 символа
	Status     EvidenceStatus `json:"status"`
	Comment    string         `json:"comment,omitempty"`
	Supersedes int64          `json:"supersedes,omitempty"`
	Actor      string         `json:"actor"`
	At         time.Time      `json:"at"`
	PrevHash   string         `json:"-"`
	Hash       string         `json:"-"`
}

// Readiness — готовность релиза к сертификации (CM-05).
type Readiness struct {
	Ready     bool     `json:"ready"`
	OpenItems []string `json:"open_items"` // незакрытые пункты «гейт/пункт» или причина
}

// ImpactClass — класс влияния фичи на сертифицированную конфигурацию (CM-06).
type ImpactClass string

const (
	ImpactNone              ImpactClass = "none"
	ImpactAnalysisRequired  ImpactClass = "analysis_required"
	ImpactSecurityFunctions ImpactClass = "security_functions"
)

// ValidImpactClass сообщает, известен ли класс.
func ValidImpactClass(c ImpactClass) bool {
	switch c {
	case ImpactNone, ImpactAnalysisRequired, ImpactSecurityFunctions:
		return true
	}
	return false
}

// ImpactAssessment — оценка класса влияния с обоснованием и автором; история — append-only.
type ImpactAssessment struct {
	ID            kernel.ID   `json:"id"`
	FeatureID     kernel.ID   `json:"feature_id"`
	ProductID     kernel.ID   `json:"product_id"`
	Class         ImpactClass `json:"class"`
	Justification string      `json:"justification"`
	Author        string      `json:"author"`
	At            time.Time   `json:"at"`
}

// CertifiedBaseline — сертифицированная конфигурация версии продукта (CM-07).
type CertifiedBaseline struct {
	ID               kernel.ID   `json:"id"`
	ProductID        kernel.ID   `json:"product_id"`
	TrackID          kernel.ID   `json:"track_id,omitempty"`
	Version          string      `json:"version"`
	RequirementSetID kernel.ID   `json:"requirement_set_id,omitempty"`
	CertificateNo    string      `json:"certificate_no"` // синтетический номер
	CertifiedAt      kernel.Date `json:"certified_at"`
	EOL              kernel.Date `json:"eol"`
	CreatedAt        time.Time   `json:"created_at"`
}

// Procedure — процедура подтверждения изменений затронутого baseline (ТЗ 2.5).
type Procedure string

const (
	// ProcedureSimplified — упрощённое подтверждение: процессы РБПО продукта сертифицированы.
	ProcedureSimplified Procedure = "simplified_confirmation"
	// ProcedureFull — полная процедура.
	ProcedureFull Procedure = "full_procedure"
)

// AffectedBaseline — baseline, затронутый фичей, с путём распространения по продуктам.
type AffectedBaseline struct {
	Baseline  CertifiedBaseline `json:"baseline"`
	Path      []kernel.ID       `json:"path"` // продукты от продукта фичи до продукта baseline
	Procedure Procedure         `json:"procedure"`
}

// Settings — настройки модуля: стоимость подтверждения по классу влияния (PR-05),
// скидка при сертифицированных процессах РБПО и срок действия baseline.
type Settings struct {
	CostByClass map[ImpactClass]kernel.Money `json:"cost_by_class"`
	// CertifiedProcessDiscount — доля скидки (0…1) для продуктов с SSDLCCertified.
	CertifiedProcessDiscount decimal.Decimal `json:"certified_process_discount"`
	// BaselineLifetimeYears — срок действия сертификата для EOL baseline.
	BaselineLifetimeYears int `json:"baseline_lifetime_years"`
}

// DefaultSettings — значения по умолчанию.
// TODO(question-22): стоимости подтверждения и срок действия сертификата не заданы в ТЗ.
func DefaultSettings() Settings {
	return Settings{
		CostByClass: map[ImpactClass]kernel.Money{
			ImpactNone:              kernel.RUB(0),
			ImpactAnalysisRequired:  kernel.RUB(50_000_00),
			ImpactSecurityFunctions: kernel.RUB(300_000_00),
		},
		CertifiedProcessDiscount: decimal.RequireFromString("0.5"),
		BaselineLifetimeYears:    5,
	}
}

func (s Settings) validate() error {
	if s.CertifiedProcessDiscount.IsNegative() || s.CertifiedProcessDiscount.GreaterThan(decimal.NewFromInt(1)) {
		return kernel.Invalid("certified_process_discount", "доля должна быть в пределах 0…1")
	}
	if s.BaselineLifetimeYears <= 0 {
		return kernel.Invalid("baseline_lifetime_years", "должен быть положительным")
	}
	for _, class := range []ImpactClass{ImpactNone, ImpactAnalysisRequired, ImpactSecurityFunctions} {
		if _, ok := s.CostByClass[class]; !ok {
			return kernel.Invalid("cost_by_class", "обязательна стоимость класса "+string(class))
		}
	}
	for c, m := range s.CostByClass {
		if !ValidImpactClass(c) {
			return kernel.Invalid("cost_by_class", "неизвестный класс "+string(c))
		}
		if m.Amount < 0 {
			return kernel.Invalid("cost_by_class", "отрицательная сумма")
		}
		if len(m.Currency) != 3 || strings.IndexFunc(m.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
			return kernel.Invalid("cost_by_class", "валюта должна быть трёхбуквенным кодом")
		}
	}
	return nil
}

// Названия доменных событий.
const (
	EventTrackStarted     = "compliance.track.started"
	EventGatePassed       = "compliance.gate.passed"
	EventGateFailed       = "compliance.gate.failed"
	EventEvidenceAppended = "compliance.evidence.appended"
	EventImpactSet        = "compliance.impact.set"
	EventBaselineCreated  = "compliance.baseline.created"
)
