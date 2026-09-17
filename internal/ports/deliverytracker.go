// Package ports — интерфейсы портов ядра к внешним системам (ТЗ 4.1).
// Типы нейтральны к конкретной системе; адаптеры лежат в internal/adapters.
package ports

import (
	"context"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// Epic — эпик трекера поставки. Поля принадлежат трекеру и в платформе доступны только на чтение.
type Epic struct {
	Key         string
	Summary     string
	Description string
	Status      string
	Priority    string
	DueDate     kernel.Date
	FixVersions []string
	FeatureRef  string // ссылка на фичу платформы (поле или метка в трекере)
	CreatedAt   time.Time
}

// Issue — задача трекера, входящая в эпик или спринт.
type Issue struct {
	Key       string
	Summary   string
	Status    string
	Done      bool // задача в финальной категории статусов трекера
	EpicKey   string
	SprintIDs []string // спринты, в которых задача побывала (в порядке трекера)
	CreatedAt time.Time
}

// SprintState — состояние спринта.
type SprintState string

const (
	SprintFuture SprintState = "future"
	SprintActive SprintState = "active"
	SprintClosed SprintState = "closed"
)

// Sprint — спринт доски с целью и составом.
type Sprint struct {
	ID        string
	Name      string
	Goal      string
	State     SprintState
	StartDate kernel.Date
	EndDate   kernel.Date
	Issues    []Issue
}

// Version — версия (релиз) проекта трекера.
type Version struct {
	ID          string
	Name        string
	Released    bool
	ReleaseDate kernel.Date
}

// Worklog — списание времени (DL-05, этап 3: только тип).
type Worklog struct {
	IssueKey string
	Author   string
	Started  time.Time
	Spent    time.Duration
}

// Типы входящих событий трекера.
const (
	WebhookEpicUpdated = "epic.updated"
	WebhookEpicCreated = "epic.created"
	WebhookEpicDeleted = "epic.deleted"
)

// WebhookEvent — входящее событие трекера, приведённое к нейтральному виду.
type WebhookEvent struct {
	ExternalID    string // ключ события внешней системы; по нему обеспечивается идемпотентность (ТЗ 4.2)
	Type          string
	EpicKey       string
	ChangedFields []string
	NewDueDate    kernel.Date
	NewStatus     string
	OccurredAt    time.Time
}

// DeliveryTracker — порт трекера поставки (ТЗ 4.1). Чтение эпиков, задач, спринтов, версий, worklogs;
// запись — только создание эпика, приоритет и ссылка на фичу. Запись во внешнюю систему
// вызывается исключительно из обработчика outbox (инвариант 5).
type DeliveryTracker interface {
	// Epics возвращает эпики проекта.
	Epics(ctx context.Context, project string) ([]Epic, error)
	// Epic возвращает эпик по ключу.
	Epic(ctx context.Context, key string) (Epic, error)
	// EpicIssues возвращает задачи эпика.
	EpicIssues(ctx context.Context, epicKey string) ([]Issue, error)
	// Sprints возвращает спринты доски вместе с составом.
	Sprints(ctx context.Context, board string) ([]Sprint, error)
	// Versions возвращает версии проекта.
	Versions(ctx context.Context, project string) ([]Version, error)
	// Worklogs возвращает списания по задачам с момента since (этап 3).
	Worklogs(ctx context.Context, issueKeys []string, since time.Time) ([]Worklog, error)

	// CreateEpic создаёт эпик и возвращает его ключ. featureRef — ссылка на фичу платформы.
	CreateEpic(ctx context.Context, project, summary, description, featureRef string) (string, error)
	// SetEpicPriority задаёт приоритет эпика.
	SetEpicPriority(ctx context.Context, key, priority string) error
	// LinkEpicToFeature записывает ссылку на фичу в поле или метку эпика.
	LinkEpicToFeature(ctx context.Context, key, featureRef string) error
}
