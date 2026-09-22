// Package portfoliograph — ядро платформы: типизированный граф портфеля.
// Продукты, иерархия Capability → Feature → Requirement, типизированные связи,
// интеграционные контракты, обнаружение циклов, роль хаба, rollup производного
// спроса и распространение сдвига сроков (PG-01…PG-10).
//
// Публичный интерфейс — этот пакет. Хранилища лежат в internal/.
package portfoliograph

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
)

// ProductType задаёт шаблон compliance-трека (этап 2) и роль по умолчанию.
type ProductType string

const (
	ProductTypeSecurity       ProductType = "security"
	ProductTypeInfrastructure ProductType = "infrastructure"
	ProductTypePlatform       ProductType = "platform"
	ProductTypeOther          ProductType = "other"
)

// Lifecycle — стадия жизненного цикла продукта.
type Lifecycle string

const (
	LifecycleIdea    Lifecycle = "idea"
	LifecycleActive  Lifecycle = "active"
	LifecycleSunset  Lifecycle = "sunset"
	LifecycleRetired Lifecycle = "retired"
)

// Product — продукт портфеля (PG-01).
type Product struct {
	ID             kernel.ID   `json:"id"`
	Key            string      `json:"key"` // короткий ключ, совпадает с клеймом products в токене
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	Type           ProductType `json:"type"`
	Owner          string      `json:"owner"`
	Lifecycle      Lifecycle   `json:"lifecycle"`
	SSDLCCertified bool        `json:"ssdlc_certified"` // признак «процессы РБПО сертифицированы»
	HubManual      bool        `json:"hub_manual"`      // ручное назначение хаба (PG-06)
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// Capability — возможность продукта (PG-02).
type Capability struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	Name      string    `json:"name"`
}

// FeatureStatus — статус фичи.
type FeatureStatus string

const (
	FeatureIdea       FeatureStatus = "idea"
	FeatureDiscovery  FeatureStatus = "discovery"
	FeaturePlanned    FeatureStatus = "planned"
	FeatureInProgress FeatureStatus = "in_progress"
	FeatureDone       FeatureStatus = "done"
	FeatureRejected   FeatureStatus = "rejected"
)

// Feature — фича продукта (PG-02).
type Feature struct {
	ID           kernel.ID     `json:"id"`
	ProductID    kernel.ID     `json:"product_id"`
	CapabilityID kernel.ID     `json:"capability_id"`
	Name         string        `json:"name"`
	Status       FeatureStatus `json:"status"`
	OwnValue     kernel.Money  `json:"own_value"`    // собственная ценность (сигналы, привязанные к фиче)
	PlannedDate  kernel.Date   `json:"planned_date"` // плановая дата готовности
	// Affected — фича затронута сдвигом срока поставщика (PG-08); снимается при изменении даты.
	Affected    bool        `json:"affected"`
	AffectedBy  kernel.ID   `json:"affected_by,omitempty"`
	ImpliedDate kernel.Date `json:"implied_date"`           // не раньше этой даты по зависимостям
	ExternalKey string      `json:"external_key,omitempty"` // ключ эпика в трекере (DL-01)
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Requirement — требование фичи (PG-02).
type Requirement struct {
	ID        kernel.ID `json:"id"`
	ProductID kernel.ID `json:"product_id"`
	FeatureID kernel.ID `json:"feature_id"`
	Text      string    `json:"text"`
}

// LinkType — четыре типа связей (ТЗ 2.2).
type LinkType string

const (
	LinkIntegration     LinkType = "integration"      // контракт с фичами на обеих сторонах
	LinkSharedComponent LinkType = "shared_component" // общий агент, библиотека, SDK
	LinkCommercial      LinkType = "commercial"       // бандл, cross-sell
	LinkBundled         LinkType = "bundled"          // вхождение в поставку
)

// Criticality — критичность зависимости; задаёт коэффициент производного спроса.
type Criticality string

const (
	CritBlocks      Criticality = "blocks"
	CritAccelerates Criticality = "accelerates"
	CritDesirable   Criticality = "desirable"
)

// Link — типизированная связь (PG-03). Направление: From зависит от To (From — потребитель, To — поставщик).
// Ценность идёт от From к To (rollup), сдвиг сроков — от To к From.
// Связь уровня продуктов: FromFeatureID и ToFeatureID пусты. Связь уровня фич: заполнены обе.
type Link struct {
	ID            kernel.ID   `json:"id"`
	Type          LinkType    `json:"type"`
	FromProductID kernel.ID   `json:"from_product_id"`
	ToProductID   kernel.ID   `json:"to_product_id"`
	FromFeatureID kernel.ID   `json:"from_feature_id,omitempty"`
	ToFeatureID   kernel.ID   `json:"to_feature_id,omitempty"`
	Criticality   Criticality `json:"criticality"`
	ContractID    kernel.ID   `json:"contract_id,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}

// IsFeatureLevel сообщает, связывает ли связь фичи.
func (l Link) IsFeatureLevel() bool {
	return l.FromFeatureID != kernel.NilID && l.ToFeatureID != kernel.NilID
}

// ContractStatus — статус контракта.
type ContractStatus string

const (
	ContractDraft      ContractStatus = "draft"
	ContractActive     ContractStatus = "active"
	ContractDeprecated ContractStatus = "deprecated"
)

// VersionPair — строка матрицы совместимости.
type VersionPair struct {
	ProviderVersion string `json:"provider_version"`
	ConsumerVersion string `json:"consumer_version"`
	Compatible      bool   `json:"compatible"`
}

// IntegrationContract — контракт интеграции двух продуктов (PG-04).
type IntegrationContract struct {
	ID                 kernel.ID      `json:"id"`
	Name               string         `json:"name"`
	ProviderProductID  kernel.ID      `json:"provider_product_id"`
	ConsumerProductID  kernel.ID      `json:"consumer_product_id"`
	ProviderFeatureIDs []kernel.ID    `json:"provider_feature_ids"`
	ConsumerFeatureIDs []kernel.ID    `json:"consumer_feature_ids"`
	InterfaceVersion   string         `json:"interface_version"`
	Owner              string         `json:"owner"`
	Status             ContractStatus `json:"status"`
	Criticality        Criticality    `json:"criticality"`
	Compatibility      []VersionPair  `json:"compatibility"`
	SignalValue        kernel.Money   `json:"signal_value"` // сумма привязанных сигналов (SG-05)
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// Settings — настройки портфеля: коэффициенты критичности (PG-07).
// TODO(question-02): значения по умолчанию согласовать с финансами.
type Settings struct {
	Coefficients map[Criticality]decimal.Decimal `json:"coefficients"`
}

// DefaultSettings — коэффициенты из ТЗ 2.4.
func DefaultSettings() Settings {
	return Settings{Coefficients: map[Criticality]decimal.Decimal{
		CritBlocks:      decimal.NewFromInt(1),
		CritAccelerates: decimal.RequireFromString("0.5"),
		CritDesirable:   decimal.RequireFromString("0.2"),
	}}
}

// Coef возвращает коэффициент критичности; неизвестная критичность — 0.
func (s Settings) Coef(c Criticality) decimal.Decimal {
	if s.Coefficients == nil {
		return DefaultSettings().Coefficients[c]
	}
	return s.Coefficients[c]
}

// FeatureValue — результат rollup для фичи (PG-07).
type FeatureValue struct {
	FeatureID    kernel.ID    `json:"feature_id"`
	ProductID    kernel.ID    `json:"product_id"`
	OwnValue     kernel.Money `json:"own_value"`
	DerivedValue kernel.Money `json:"derived_value"` // Σ value(fᵢ) × k
	TotalValue   kernel.Money `json:"total_value"`
	ComputedAt   time.Time    `json:"computed_at"`
}

// HubInfo — роль хаба (PG-06).
type HubInfo struct {
	ProductID kernel.ID `json:"product_id"`
	InDegree  int       `json:"in_degree"` // число входящих продуктовых связей (продукт — поставщик)
	Manual    bool      `json:"manual"`
	Computed  bool      `json:"computed"` // максимум входящей связности
}

// CycleError — попытка создать цикл в графе зависимостей фич (PG-05).
type CycleError struct {
	Path []kernel.ID // путь цикла: f1 → f2 → … → f1
}

func (e *CycleError) Error() string {
	return "связь создаёт цикл в графе зависимостей фич"
}

// Is позволяет errors.Is(err, kernel.ErrConflict).
func (e *CycleError) Is(target error) bool { return target == kernel.ErrConflict }

// AffectedFeature — фича, затронутая сдвигом срока (PG-08).
type AffectedFeature struct {
	FeatureID   kernel.ID   `json:"feature_id"`
	ProductID   kernel.ID   `json:"product_id"`
	PlannedDate kernel.Date `json:"planned_date"`
	ImpliedDate kernel.Date `json:"implied_date"`
	ViaFeature  kernel.ID   `json:"via_feature"` // непосредственный поставщик, из-за которого затронута
	Depth       int         `json:"depth"`
}

// ShiftResult — результат распространения сдвига (PG-08).
type ShiftResult struct {
	SourceFeature kernel.ID         `json:"source_feature"`
	OldDate       kernel.Date       `json:"old_date"`
	NewDate       kernel.Date       `json:"new_date"`
	Affected      []AffectedFeature `json:"affected"`
	Contracts     []kernel.ID       `json:"contracts"` // контракты, срок готовности которых изменился
	Commitments   []kernel.ID       `json:"commitments"`
}

// StrategicSlice — стратегический срез продукта для владельца хаба (PG-10).
// Не содержит сигналов, бэклога, discovery.
type StrategicSlice struct {
	Product   Product               `json:"product"`
	Features  []StrategicFeature    `json:"features"`
	Contracts []IntegrationContract `json:"contracts"`
}

// StrategicFeature — фича в стратегическом срезе.
type StrategicFeature struct {
	ID          kernel.ID     `json:"id"`
	Name        string        `json:"name"`
	Status      FeatureStatus `json:"status"`
	TotalValue  kernel.Money  `json:"total_value"`
	PlannedDate kernel.Date   `json:"planned_date"`
	Affected    bool          `json:"affected"`
}

// Названия доменных событий.
const (
	EventProductCreated  = "portfoliograph.product.created"
	EventProductDeleted  = "portfoliograph.product.deleted"
	EventFeatureCreated  = "portfoliograph.feature.created"
	EventLinkCreated     = "portfoliograph.link.created"
	EventLinkDeleted     = "portfoliograph.link.deleted"
	EventContractSaved   = "portfoliograph.contract.saved"
	EventRollupComputed  = "portfoliograph.rollup.computed"
	EventDateShifted     = "portfoliograph.feature.date_shifted"
	EventFeatureValueSet = "portfoliograph.feature.value_set"
)
