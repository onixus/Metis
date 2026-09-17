// Package roadmap реализует плановые элементы и релизы (ТЗ 3.5, RM-01…RM-05):
// представления timeline / Now-Next-Later / по релизам, аудиторные срезы, историю дат,
// ветки релизов (RM-04), состав, release notes, матрицу совместимости и EOL релиза (RM-05).
// Аудитория среза определяется только authz.Scope (RM-02).
package roadmap

import (
	"context"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// Bucket — корзина Now/Next/Later.
type Bucket string

const (
	BucketNow   Bucket = "now"
	BucketNext  Bucket = "next"
	BucketLater Bucket = "later"
)

func (b Bucket) valid() bool { return b == BucketNow || b == BucketNext || b == BucketLater }

// ItemStatus — внутренний статус элемента roadmap. В sales-safe срез не попадает.
type ItemStatus string

const (
	ItemPlanned    ItemStatus = "planned"
	ItemInProgress ItemStatus = "in_progress"
	ItemDone       ItemStatus = "done"
	ItemCancelled  ItemStatus = "cancelled"
)

func (s ItemStatus) valid() bool {
	switch s {
	case ItemPlanned, ItemInProgress, ItemDone, ItemCancelled:
		return true
	}
	return false
}

// ItemKind — вид элемента roadmap (RM-04): новая функциональность или исправление.
// В релиз сертифицированной ветки допускаются только исправления.
type ItemKind string

const (
	KindFeature ItemKind = "feature"
	KindFix     ItemKind = "fix"
)

func (k ItemKind) valid() bool { return k == KindFeature || k == KindFix }

// ReleaseStatus — статус релиза.
type ReleaseStatus string

const (
	ReleasePlanned ReleaseStatus = "planned"
	// ReleaseReadyForCertification — гейты SSDLC закрыты, релиз готов к сертификации (RM-05, CM-05).
	ReleaseReadyForCertification ReleaseStatus = "ready_for_certification"
	ReleaseReleased              ReleaseStatus = "released"
	ReleaseEOL                   ReleaseStatus = "eol"
)

func (s ReleaseStatus) valid() bool {
	switch s {
	case ReleasePlanned, ReleaseReadyForCertification, ReleaseReleased, ReleaseEOL:
		return true
	}
	return false
}

// Branch — ветка версии (RM-04): сертифицированная принимает только исправления,
// развивающаяся — любые элементы.
type Branch string

const (
	BranchCertified Branch = "certified"
	BranchEvolving  Branch = "evolving"
)

func (b Branch) valid() bool { return b == BranchCertified || b == BranchEvolving }

// CompatRow — строка матрицы совместимости релиза (RM-05), вычисленная из контракта интеграции (PG-04).
type CompatRow struct {
	ContractID        kernel.ID `json:"contract_id"`
	ContractName      string    `json:"contract_name"`
	ProviderProductID kernel.ID `json:"provider_product_id"`
	ConsumerProductID kernel.ID `json:"consumer_product_id"`
	ProviderVersion   string    `json:"provider_version"`
	ConsumerVersion   string    `json:"consumer_version"`
	Compatible        bool      `json:"compatible"`
}

// Readiness — результат проверки готовности релиза к сертификации (CM-05).
// Такой же тип объявляет модуль compliance; соединяются адаптером в app.
type Readiness struct {
	Ready     bool     `json:"ready"`
	OpenItems []string `json:"open_items,omitempty"` // незакрытые пункты чек-листов гейтов SSDLC
}

// ContractReader — порт чтения контрактов интеграции для матрицы совместимости (RM-05).
// Реализует *portfoliograph.Service.
type ContractReader interface {
	Contracts(ctx context.Context, sc authz.Scope) ([]portfoliograph.IntegrationContract, error)
}

// ReadinessChecker — порт готовности релиза к сертификации (CM-05); реализует модуль compliance.
type ReadinessChecker interface {
	ReleaseReadiness(ctx context.Context, sc authz.Scope, releaseID kernel.ID) (Readiness, error)
}

// RoadmapItem — плановый элемент roadmap. Виден sales-safe аудитории только при Audience = sales_safe.
type RoadmapItem struct {
	ID        kernel.ID      `json:"id"`
	ProductID kernel.ID      `json:"product_id"`
	FeatureID kernel.ID      `json:"feature_id,omitempty"` // опционально: привязка к фиче портфеля
	Title     string         `json:"title"`
	Bucket    Bucket         `json:"bucket"`
	StartDate kernel.Date    `json:"start_date"`
	EndDate   kernel.Date    `json:"end_date"`
	ReleaseID kernel.ID      `json:"release_id,omitempty"`
	Audience  authz.Audience `json:"audience"`
	Status    ItemStatus     `json:"status"`
	// Kind — вид элемента (RM-04); по умолчанию feature.
	Kind ItemKind `json:"kind"`
	// CommitmentID — обязательство, породившее элемент (CT-04); NilID для обычных элементов.
	CommitmentID kernel.ID `json:"commitment_id,omitempty"`
	// LaunchTier и LaunchDate — уровень и дата запуска для маркетинга (RM-06).
	LaunchTier LaunchTier  `json:"launch_tier,omitempty"`
	LaunchDate kernel.Date `json:"launch_date"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// LaunchTier — уровень запуска (RM-06): объём поддержки запуска маркетингом.
type LaunchTier string

// Уровни запуска.
const (
	LaunchNone LaunchTier = ""
	// LaunchTier1 — полноценный запуск: пресс-релиз, кампания, обучение продаж.
	LaunchTier1 LaunchTier = "tier1"
	// LaunchTier2 — анонс в блоге и release notes.
	LaunchTier2 LaunchTier = "tier2"
	// LaunchTier3 — только release notes.
	LaunchTier3 LaunchTier = "tier3"
)

// ValidLaunchTier сообщает, известен ли уровень запуска.
func ValidLaunchTier(t LaunchTier) bool {
	switch t {
	case LaunchNone, LaunchTier1, LaunchTier2, LaunchTier3:
		return true
	}
	return false
}

// LaunchEntry — запись календаря запусков (RM-06).
type LaunchEntry struct {
	ItemID     kernel.ID      `json:"item_id"`
	ProductID  kernel.ID      `json:"product_id"`
	Title      string         `json:"title"`
	Tier       LaunchTier     `json:"tier"`
	LaunchDate kernel.Date    `json:"launch_date"`
	ReleaseID  kernel.ID      `json:"release_id,omitempty"`
	Audience   authz.Audience `json:"audience"`
}

// LaunchCalendar — календарь запусков продукта за период (RM-06).
// Аудитория берётся из Scope: sales-safe видит только свой срез (RM-02).
type LaunchCalendar struct {
	From     kernel.Date    `json:"from"`
	To       kernel.Date    `json:"to"`
	Audience authz.Audience `json:"audience"`
	Entries  []LaunchEntry  `json:"entries"`
}

// Release — релиз продукта (RM-04, RM-05).
type Release struct {
	ID          kernel.ID     `json:"id"`
	ProductID   kernel.ID     `json:"product_id"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	PlannedDate kernel.Date   `json:"planned_date"`
	Status      ReleaseStatus `json:"status"`
	// Branch — ветка версии (RM-04); по умолчанию evolving.
	Branch Branch `json:"branch"`
	// BaseReleaseID — для сертифицированной ветки: релиз, от которого она ответвлена (опционально).
	BaseReleaseID kernel.ID `json:"base_release_id,omitempty"`
	// FeatureIDs — состав релиза (RM-05). Внутренняя информация.
	FeatureIDs []kernel.ID `json:"feature_ids,omitempty"`
	// ReleaseNotes — release notes (RM-05). Внутренняя информация.
	ReleaseNotes string `json:"release_notes,omitempty"`
	// EOL — дата окончания поддержки (RM-05).
	EOL kernel.Date `json:"eol"`
	// CompatibilityMatrix — матрица совместимости из контрактов (RM-05); вычисляется при чтении, не хранится.
	CompatibilityMatrix []CompatRow `json:"compatibility_matrix,omitempty"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

// SalesSafeRelease — релиз в sales-safe срезе (RM-02, RM-05): без release notes и состава.
// Матрица совместимости и EOL доступны presale (ТЗ 1.3).
type SalesSafeRelease struct {
	ID                  kernel.ID     `json:"id"`
	ProductID           kernel.ID     `json:"product_id"`
	Name                string        `json:"name"`
	Version             string        `json:"version"`
	PlannedDate         kernel.Date   `json:"planned_date"`
	Status              ReleaseStatus `json:"status"`
	Branch              Branch        `json:"branch"`
	EOL                 kernel.Date   `json:"eol"`
	CompatibilityMatrix []CompatRow   `json:"compatibility_matrix,omitempty"`
}

func toSalesSafeRelease(r Release) SalesSafeRelease {
	return SalesSafeRelease{ID: r.ID, ProductID: r.ProductID, Name: r.Name, Version: r.Version, PlannedDate: r.PlannedDate,
		Status: r.Status, Branch: r.Branch, EOL: r.EOL, CompatibilityMatrix: r.CompatibilityMatrix}
}

// stripInternal убирает из релиза внутренние поля (release notes, состав) для sales-safe аудитории.
func stripInternal(r Release) Release {
	r.ReleaseNotes, r.FeatureIDs = "", nil
	return r
}

// DateChange — запись истории изменения дат элемента (RM-03). Причина обязательна.
type DateChange struct {
	ID        kernel.ID   `json:"id"`
	ItemID    kernel.ID   `json:"item_id"`
	ProductID kernel.ID   `json:"product_id"`
	OldStart  kernel.Date `json:"old_start"`
	OldEnd    kernel.Date `json:"old_end"`
	NewStart  kernel.Date `json:"new_start"`
	NewEnd    kernel.Date `json:"new_end"`
	Reason    string      `json:"reason"`
	Actor     string      `json:"actor"`
	At        time.Time   `json:"at"`
	// EventID — идентификатор события-источника (для сдвигов из portfoliograph); NilID для ручных изменений.
	EventID kernel.ID `json:"event_id,omitempty"`
}

// SalesSafeItem — элемент в sales-safe срезе (RM-02): без внутренних статусов, причин и истории.
type SalesSafeItem struct {
	ID        kernel.ID   `json:"id"`
	ProductID kernel.ID   `json:"product_id"`
	Title     string      `json:"title"`
	Bucket    Bucket      `json:"bucket"`
	StartDate kernel.Date `json:"start_date"`
	EndDate   kernel.Date `json:"end_date"`
	ReleaseID kernel.ID   `json:"release_id,omitempty"`
	// LaunchTier и LaunchDate — публичная часть плана запуска (RM-06).
	LaunchTier LaunchTier  `json:"launch_tier,omitempty"`
	LaunchDate kernel.Date `json:"launch_date"`
}

func toSalesSafe(it RoadmapItem) SalesSafeItem {
	return SalesSafeItem{ID: it.ID, ProductID: it.ProductID, Title: it.Title, Bucket: it.Bucket,
		StartDate: it.StartDate, EndDate: it.EndDate, ReleaseID: it.ReleaseID,
		LaunchTier: it.LaunchTier, LaunchDate: it.LaunchDate}
}

// Timeline — представление RM-01: элементы с датами, отсортированные по началу, затем по концу.
// Заполнено ровно одно из полей Items / SalesSafe в зависимости от аудитории Scope.
type Timeline struct {
	ProductID kernel.ID       `json:"product_id"`
	Audience  authz.Audience  `json:"audience"`
	Items     []RoadmapItem   `json:"items,omitempty"`
	SalesSafe []SalesSafeItem `json:"sales_safe,omitempty"`
}

// NowNextLater — представление RM-01: группировка по корзинам.
type NowNextLater struct {
	ProductID kernel.ID      `json:"product_id"`
	Audience  authz.Audience `json:"audience"`
	Now       BucketGroup    `json:"now"`
	Next      BucketGroup    `json:"next"`
	Later     BucketGroup    `json:"later"`
}

// BucketGroup — элементы одной корзины.
type BucketGroup struct {
	Items     []RoadmapItem   `json:"items,omitempty"`
	SalesSafe []SalesSafeItem `json:"sales_safe,omitempty"`
}

// ByRelease — представление RM-01: группировка по релизам; элементы без релиза — в Unassigned.
type ByRelease struct {
	ProductID  kernel.ID      `json:"product_id"`
	Audience   authz.Audience `json:"audience"`
	Releases   []ReleaseGroup `json:"releases"`
	Unassigned BucketGroup    `json:"unassigned"`
}

// ReleaseGroup — релиз и его элементы. Для sales-safe аудитории Release очищен от внутренних полей,
// а SalesSafeRelease заполнен.
type ReleaseGroup struct {
	Release          Release           `json:"release"`
	SalesSafeRelease *SalesSafeRelease `json:"sales_safe_release,omitempty"`
	Items            []RoadmapItem     `json:"items,omitempty"`
	SalesSafe        []SalesSafeItem   `json:"sales_safe,omitempty"`
}

// Названия доменных событий.
const (
	EventItemSaved    = "roadmap.item.saved"
	EventDatesChanged = "roadmap.dates.changed"
	EventReleaseSaved = "roadmap.release.saved"
	// EventReleaseReadyForCertification — релиз переведён в статус ready_for_certification (RM-05).
	EventReleaseReadyForCertification = "roadmap.release.ready_for_certification"
)
