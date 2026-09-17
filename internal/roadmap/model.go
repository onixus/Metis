// Package roadmap реализует плановые элементы и релизы (ТЗ 3.5, RM-01…RM-03):
// представления timeline / Now-Next-Later / по релизам, аудиторные срезы и историю дат.
// Аудитория среза определяется только authz.Scope (RM-02).
package roadmap

import (
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
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

// ReleaseStatus — статус релиза.
type ReleaseStatus string

const (
	ReleasePlanned  ReleaseStatus = "planned"
	ReleaseReleased ReleaseStatus = "released"
	ReleaseEOL      ReleaseStatus = "eol"
)

func (s ReleaseStatus) valid() bool {
	return s == ReleasePlanned || s == ReleaseReleased || s == ReleaseEOL
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
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Release — релиз продукта. Состав фич и release notes — RM-05 (этап 2).
type Release struct {
	ID          kernel.ID     `json:"id"`
	ProductID   kernel.ID     `json:"product_id"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	PlannedDate kernel.Date   `json:"planned_date"`
	Status      ReleaseStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
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
}

func toSalesSafe(it RoadmapItem) SalesSafeItem {
	return SalesSafeItem{ID: it.ID, ProductID: it.ProductID, Title: it.Title, Bucket: it.Bucket,
		StartDate: it.StartDate, EndDate: it.EndDate, ReleaseID: it.ReleaseID}
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

// ReleaseGroup — релиз и его элементы.
type ReleaseGroup struct {
	Release   Release         `json:"release"`
	Items     []RoadmapItem   `json:"items,omitempty"`
	SalesSafe []SalesSafeItem `json:"sales_safe,omitempty"`
}

// Названия доменных событий.
const (
	EventItemSaved    = "roadmap.item.saved"
	EventDatesChanged = "roadmap.dates.changed"
	EventReleaseSaved = "roadmap.release.saved"
)
