// Package discovery — гипотезы, интервью, инсайты и evidence (DS-01…DS-04), подсказки похожих
// сигналов и их слияние (SG-04), кастомные поля и статусы (AD-03). Публичный интерфейс — этот
// пакет. Сигналы, фичи и решения доступны только через порты (инвариант 1).
package discovery

import (
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// HypothesisStatus — статус гипотезы (DS-01). Помимо четырёх встроенных допускается
// пользовательский ключ, определённый через CustomStatusDef и отображённый на категорию (AD-03).
type HypothesisStatus string

const (
	HypothesisDraft     HypothesisStatus = "draft"
	HypothesisTesting   HypothesisStatus = "testing"
	HypothesisConfirmed HypothesisStatus = "confirmed"
	HypothesisRejected  HypothesisStatus = "rejected"
)

// BuiltinHypothesisStatus сообщает, встроен ли статус.
func BuiltinHypothesisStatus(s HypothesisStatus) bool {
	switch s {
	case HypothesisDraft, HypothesisTesting, HypothesisConfirmed, HypothesisRejected:
		return true
	}
	return false
}

// Hypothesis — гипотеза discovery (DS-01). ProductID обязателен (инвариант 3).
type Hypothesis struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	Title     string    `json:"title"`
	// Statement — формулировка «мы считаем, что …».
	Statement string `json:"statement"`
	// Assumptions — допущения, на которых держится гипотеза.
	Assumptions []string `json:"assumptions,omitempty"`
	// ConfirmationCriterion — критерий подтверждения.
	ConfirmationCriterion string           `json:"confirmation_criterion"`
	Status                HypothesisStatus `json:"status"`
	// Resolution — причина подтверждения или отклонения; обязательна для confirmed/rejected.
	Resolution string `json:"resolution,omitempty"`
	// FeatureID — фича того же продукта, которую проверяет гипотеза (DS-04).
	FeatureID    kernel.ID      `json:"feature_id,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"` // AD-03
	CreatedBy    string         `json:"created_by"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Interview — интервью с аккаунтом (DS-02). AccountID — внешний идентификатор CRM.
type Interview struct {
	ID           kernel.ID   `json:"id"`
	ProductID    kernel.ID   `json:"product_id"`
	AccountID    string      `json:"account_id,omitempty"`
	Segment      string      `json:"segment,omitempty"`
	Date         kernel.Date `json:"date"` // без времени (инвариант 7)
	Participants []string    `json:"participants,omitempty"`
	Notes        string      `json:"notes,omitempty"`
	// HypothesisIDs — гипотезы, которые проверялись на интервью.
	HypothesisIDs []kernel.ID `json:"hypothesis_ids,omitempty"`
	CreatedBy     string      `json:"created_by"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// Confidence — уровень уверенности инсайта и доверия к evidence.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

// ValidConfidence сообщает, известен ли уровень.
func ValidConfidence(c Confidence) bool {
	switch c {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	}
	return false
}

// Insight — вывод из интервью или сигналов (DS-02); узел трассировки (DS-04).
type Insight struct {
	ID            kernel.ID   `json:"id"`
	ProductID     kernel.ID   `json:"product_id"`
	Text          string      `json:"text"`
	InterviewID   kernel.ID   `json:"interview_id,omitempty"`
	HypothesisIDs []kernel.ID `json:"hypothesis_ids,omitempty"`
	SignalIDs     []kernel.ID `json:"signal_ids,omitempty"`
	Confidence    Confidence  `json:"confidence"`
	CreatedBy     string      `json:"created_by"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// Verification — статус проверки evidence (DS-03). По умолчанию — unverified (задел под AI-04).
type Verification string

const (
	VerificationUnverified Verification = "unverified"
	VerificationVerified   Verification = "verified"
	VerificationRejected   Verification = "rejected"
)

// ValidVerification сообщает, известен ли статус проверки.
func ValidVerification(v Verification) bool {
	switch v {
	case VerificationUnverified, VerificationVerified, VerificationRejected:
		return true
	}
	return false
}

// Известные источники evidence; Source — свободная строка, эти константы — договорённость.
const (
	EvidenceSourceManual           = "manual"
	EvidenceSourceInterview        = "interview"
	EvidenceSourceExternalResearch = "external_research"
)

// Evidence — свидетельство в пользу или против гипотезы, инсайта или фичи (DS-03).
type Evidence struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	// Source — происхождение: manual, interview, external_research и т. п.
	Source string `json:"source"`
	// SourceRef — ссылка на артефакт во внешней системе.
	SourceRef    string       `json:"source_ref,omitempty"`
	Date         kernel.Date  `json:"date"`
	Trust        Confidence   `json:"trust"`
	Verification Verification `json:"verification"`
	// SHA256 — хеш артефакта в hex (64 символа), если артефакт зафиксирован.
	SHA256       string    `json:"sha256,omitempty"`
	HypothesisID kernel.ID `json:"hypothesis_id,omitempty"`
	InsightID    kernel.ID `json:"insight_id,omitempty"`
	FeatureID    kernel.ID `json:"feature_id,omitempty"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Entity — сущность с кастомными полями и статусами (AD-03).
type Entity string

const (
	EntityFeature    Entity = "feature"
	EntitySignal     Entity = "signal"
	EntityHypothesis Entity = "hypothesis"
)

// ValidEntity сообщает, известна ли сущность.
func ValidEntity(e Entity) bool {
	switch e {
	case EntityFeature, EntitySignal, EntityHypothesis:
		return true
	}
	return false
}

// FieldType — тип кастомного поля (AD-03).
type FieldType string

const (
	FieldString FieldType = "string"
	FieldNumber FieldType = "number"
	FieldDate   FieldType = "date"
	FieldEnum   FieldType = "enum"
)

// ValidFieldType сообщает, известен ли тип.
func ValidFieldType(t FieldType) bool {
	switch t {
	case FieldString, FieldNumber, FieldDate, FieldEnum:
		return true
	}
	return false
}

// CustomFieldDef — определение кастомного поля (AD-03). Ключ уникален в пределах сущности.
type CustomFieldDef struct {
	ID       kernel.ID `json:"id"`
	Entity   Entity    `json:"entity"`
	Key      string    `json:"key"` // ^[a-z][a-z0-9_]{0,31}$
	Label    string    `json:"label"`
	Type     FieldType `json:"type"`
	Options  []string  `json:"options,omitempty"` // допустимые значения для enum
	Required bool      `json:"required"`
}

// CustomStatusDef — пользовательский статус, отображённый на встроенную категорию (AD-03).
// Для hypothesis категория — один из draft|testing|confirmed|rejected.
type CustomStatusDef struct {
	Entity   Entity `json:"entity"`
	Key      string `json:"key"` // ^[a-z][a-z0-9_]{0,31}$
	Label    string `json:"label"`
	Category string `json:"category"`
}

// TraceKind — вид узла трассировки (DS-04).
type TraceKind string

const (
	TraceSignal     TraceKind = "signal"
	TraceInsight    TraceKind = "insight"
	TraceHypothesis TraceKind = "hypothesis"
	TraceFeature    TraceKind = "feature"
	TraceDecision   TraceKind = "decision"
)

// TraceRef — ссылка на узел трассировки.
type TraceRef struct {
	Kind TraceKind `json:"kind"`
	ID   kernel.ID `json:"id"`
}

// TraceNode — узел графа трассировки.
type TraceNode struct {
	TraceRef
	ProductID kernel.ID `json:"product_id,omitempty"`
	Title     string    `json:"title"`
}

// TraceEdge — направленное ребро «от причины к следствию»: сигнал → инсайт → гипотеза → фича → решение.
type TraceEdge struct {
	From TraceRef `json:"from"`
	To   TraceRef `json:"to"`
}

// TraceGraph — результат трассировки (DS-04). Root — узел, от которого шёл обход.
type TraceGraph struct {
	Root  TraceRef    `json:"root"`
	Nodes []TraceNode `json:"nodes"`
	Edges []TraceEdge `json:"edges"`
}

// DecisionRef — ссылка на решение (DA-01) из порта DecisionLinks.
type DecisionRef struct {
	ID    kernel.ID `json:"id"`
	Title string    `json:"title"`
}

// Match — совпадение из индекса похожести (SG-04). Score — косинусная близость в [0, 1].
type Match struct {
	ID    kernel.ID `json:"id"`
	Score float64   `json:"score"`
}

// Названия доменных событий.
const (
	EventHypothesisSaved = "discovery.hypothesis.saved"
	EventInterviewSaved  = "discovery.interview.saved"
	EventInsightSaved    = "discovery.insight.saved"
	EventEvidenceSaved   = "discovery.evidence.saved"
)
