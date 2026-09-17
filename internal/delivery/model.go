// Package delivery — проекция трекера поставки и производные метрики (DL-01…DL-03, AD-05, NF-R05, NF-R06).
//
// Трекер владеет статусом поставки (ТЗ 4.4): поля эпиков, задач и спринтов в проекции доступны только
// на чтение (инвариант 4). Запись во внешнюю систему идёт только через outbox (инвариант 5):
// сервис публикует событие-команду, обработчик в воркере вызывает порт ports.DeliveryTracker.
// Пакет не содержит кода, специфичного для Jira.
package delivery

import (
	"context"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

// Типы событий модуля.
const (
	// EventEpicCreateRequested — команда воркеру создать эпик в трекере (DL-01).
	EventEpicCreateRequested = "delivery.epic.create_requested"
	// EventEpicLinked — эпик привязан к фиче (ключ записан).
	EventEpicLinked = "delivery.epic.linked"
	// EventEpicDueDateChanged — due date эпика изменилась в трекере.
	EventEpicDueDateChanged = "delivery.epic.due_date_changed"
)

// EpicCreateRequest — payload события EventEpicCreateRequested.
type EpicCreateRequest struct {
	FeatureID   kernel.ID `json:"feature_id"`
	ProductID   kernel.ID `json:"product_id"`
	Project     string    `json:"project"`
	Summary     string    `json:"summary"`
	Description string    `json:"description"`
}

// Mapping — привязка фичи к эпику трекера (DL-01).
type Mapping struct {
	FeatureID kernel.ID `json:"feature_id"`
	ProductID kernel.ID `json:"product_id"`
	EpicKey   string    `json:"epic_key"`
	Project   string    `json:"project"`
	CreatedAt time.Time `json:"created_at"`
}

// ReleaseMapping — привязка релиза к версии трекера (DL-01).
type ReleaseMapping struct {
	ReleaseID  kernel.ID `json:"release_id"`
	ProductID  kernel.ID `json:"product_id"`
	Project    string    `json:"project"`
	FixVersion string    `json:"fix_version"`
	CreatedAt  time.Time `json:"created_at"`
}

// IssueSnapshot — задача эпика в проекции (только чтение).
type IssueSnapshot struct {
	Key       string    `json:"key"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

// EpicProjection — проекция эпика трекера, привязанного к фиче. Поля трекера — только чтение.
type EpicProjection struct {
	FeatureID   kernel.ID       `json:"feature_id"`
	ProductID   kernel.ID       `json:"product_id"`
	EpicKey     string          `json:"epic_key"`
	Summary     string          `json:"summary"`
	Status      string          `json:"status"`
	DueDate     kernel.Date     `json:"due_date"`
	FixVersions []string        `json:"fix_versions"`
	Issues      []IssueSnapshot `json:"issues"`
	// InitialScope — ключи задач в первом снимке состава; база для scope creep (DL-03).
	InitialScope  []string  `json:"initial_scope"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	SyncedAt      time.Time `json:"synced_at"`
	SourceEventID string    `json:"source_event_id,omitempty"` // последнее применённое событие трекера
}

// SprintStatus — статус спринта (DL-02): цель, состав, прогресс, перенос.
type SprintStatus struct {
	ProductID kernel.ID         `json:"product_id"`
	Board     string            `json:"board"`
	SprintID  string            `json:"sprint_id"`
	Name      string            `json:"name"`
	Goal      string            `json:"goal"`
	State     ports.SprintState `json:"state"`
	StartDate kernel.Date       `json:"start_date"`
	EndDate   kernel.Date       `json:"end_date"`
	Issues    []IssueSnapshot   `json:"issues"`
	Total     int               `json:"total"`
	Done      int               `json:"done"`
	// CarriedOver — ключи задач, которые были в предыдущем спринте доски и перешли в этот.
	CarriedOver []string  `json:"carried_over"`
	SyncedAt    time.Time `json:"synced_at"`
}

// Readiness — готовность фичи: done/total задач эпика.
type Readiness struct {
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Percent float64 `json:"percent"` // 0..100; 0 при пустом эпике
}

// ScopeCreep — рост объёма: задачи, добавленные после первого снимка.
type ScopeCreep struct {
	Initial    int       `json:"initial"`
	Added      []string  `json:"added"`
	Removed    []string  `json:"removed"`
	Percent    float64   `json:"percent"` // добавлено / initial × 100; 0 при пустом initial
	SnapshotAt time.Time `json:"snapshot_at"`
}

// PlanFact — план/факт: PlannedDate фичи против due date эпика.
type PlanFact struct {
	PlannedDate kernel.Date `json:"planned_date"`
	DueDate     kernel.Date `json:"due_date"`
	DeltaDays   int         `json:"delta_days"` // > 0 — трекер позже плана
	Late        bool        `json:"late"`
}

// FeatureMetrics — производные метрики фичи (DL-03).
type FeatureMetrics struct {
	FeatureID  kernel.ID  `json:"feature_id"`
	ProductID  kernel.ID  `json:"product_id"`
	EpicKey    string     `json:"epic_key"`
	Readiness  Readiness  `json:"readiness"`
	ScopeCreep ScopeCreep `json:"scope_creep"`
	PlanFact   PlanFact   `json:"plan_fact"`
	Sync       SyncState  `json:"sync"`
}

// SyncState — состояние синхронизации с трекером (NF-R05, AD-05).
type SyncState struct {
	LastSuccessAt time.Time     `json:"last_success_at"`
	LastAttemptAt time.Time     `json:"last_attempt_at"`
	LastError     string        `json:"last_error,omitempty"`
	Lag           time.Duration `json:"lag"`   // время с последней успешной сверки
	Stale         bool          `json:"stale"` // данные старше порога или сверки ещё не было
}

// FieldMapping — настраиваемый маппинг полей и статусов трекера (AD-05, ТЗ 4.2). Меняется без кода.
type FieldMapping struct {
	Project         string                                  `json:"project"`
	Boards          map[kernel.ID]string                    `json:"boards"` // продукт → доска трекера
	EpicIssueType   string                                  `json:"epic_issue_type"`
	FeatureRefField string                                  `json:"feature_ref_field"`
	DueDateField    string                                  `json:"due_date_field"`
	StatusMap       map[string]portfoliograph.FeatureStatus `json:"status_map"` // статус трекера → статус фичи
}

// DefaultFieldMapping — маппинг по умолчанию.
func DefaultFieldMapping() FieldMapping {
	return FieldMapping{
		EpicIssueType: "Epic",
		DueDateField:  "due_date",
		Boards:        map[kernel.ID]string{},
		StatusMap: map[string]portfoliograph.FeatureStatus{
			"To Do":       portfoliograph.FeaturePlanned,
			"In Progress": portfoliograph.FeatureInProgress,
			"Done":        portfoliograph.FeatureDone,
		},
	}
}

// FeatureStatus переводит статус трекера в статус фичи; пусто — маппинга нет.
func (m FieldMapping) FeatureStatus(tracker string) portfoliograph.FeatureStatus {
	return m.StatusMap[tracker]
}

// ConnectorStatus — состояние коннектора для администратора (AD-05, NF-R06).
type ConnectorStatus struct {
	Name        string        `json:"name"`
	Mapping     FieldMapping  `json:"mapping"`
	Sync        SyncState     `json:"sync"`
	DLQCount    int           `json:"dlq_count"`
	MappedEpics int           `json:"mapped_epics"`
	StaleAfter  time.Duration `json:"stale_after"`
}

// DLQReader — счётчик недоставленных записей outbox (DLQ живёт в outbox; здесь только чтение).
type DLQReader interface {
	DLQCount(ctx context.Context) (int, error)
}
