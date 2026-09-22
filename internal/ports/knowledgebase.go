package ports

import (
	"context"
	"time"
)

// Page — страница базы знаний (ТЗ 2.1 Document, 4.3). Текст живёт во внешней системе;
// платформа хранит метаданные, ссылку и текст для индекса. Поля принадлежат базе знаний
// и в платформе доступны только на чтение (инвариант 4).
type Page struct {
	ID         string
	Title      string
	SpaceKey   string
	URL        string
	Labels     []string
	Properties map[string]string // page properties, которыми управляет платформа (metis_*)
	Version    int
	UpdatedAt  time.Time
	BodyText   string // текст без разметки для индекса
}

// PageTemplate — шаблон страницы (ADR, PRD, discovery brief). Body — разметка адаптера.
type PageTemplate struct {
	Key   string
	Title string
	Body  string
}

// CreatePageInput — данные для создания страницы из шаблона.
// Body — разметка в формате адаптера (storage/html-подобная); текст в ней экранирует вызывающий
// (internal/knowledgedocs) либо адаптер.
type CreatePageInput struct {
	// IdempotencyKey is a stable identity for recovery after lost responses.
	// Adapters may use a deterministic title; callers must retain the same key on retry.
	IdempotencyKey string
	SpaceKey       string
	ParentID       string
	Title          string
	Body           string
	Labels         []string
	Properties     map[string]string
}

// KnowledgeBase — порт базы знаний (ТЗ 4.1). Чтение метаданных, текста для индекса и меток;
// запись — страницы из шаблонов, page properties, labels. Запись во внешнюю систему
// (CreatePage, SetProperties, AddLabels) вызывается исключительно из обработчика outbox
// (инвариант 5); доменные сервисы публикуют событие, а не вызывают порт напрямую.
type KnowledgeBase interface {
	// Page возвращает страницу по идентификатору.
	Page(ctx context.Context, id string) (Page, error)
	// Search возвращает страницы пространства с меткой label (пустая метка — все страницы пространства).
	Search(ctx context.Context, spaceKey, label string) ([]Page, error)

	// CreatePage создаёт страницу с метками и свойствами; возвращает созданную страницу.
	CreatePage(ctx context.Context, in CreatePageInput) (Page, error)
	// SetProperties задаёт page properties страницы (существующие с теми же ключами перезаписываются).
	SetProperties(ctx context.Context, id string, props map[string]string) error
	// AddLabels добавляет метки к странице.
	AddLabels(ctx context.Context, id string, labels []string) error
}
