package commitments

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// Filter — условия выборки обязательств. Пустое поле — без ограничения.
type Filter struct {
	ProductID kernel.ID
	Kind      Kind
	Subtype   Subtype
	Statuses  []Status
	FeatureID kernel.ID
	ReleaseID kernel.ID
	// DueBefore — срок не позже указанной даты (включительно).
	DueBefore kernel.Date
}

func (f Filter) matches(c Commitment) bool {
	if f.ProductID != kernel.NilID && c.ProductID != f.ProductID {
		return false
	}
	if f.Kind != "" && c.Kind != f.Kind {
		return false
	}
	if f.Subtype != "" && c.Subtype != f.Subtype {
		return false
	}
	if f.FeatureID != kernel.NilID && c.FeatureID != f.FeatureID {
		return false
	}
	if f.ReleaseID != kernel.NilID && c.ReleaseID != f.ReleaseID {
		return false
	}
	if !f.DueBefore.IsZero() && c.DueDate.After(f.DueBefore) {
		return false
	}
	if len(f.Statuses) > 0 {
		ok := false
		for _, st := range f.Statuses {
			if c.Status == st {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Store — хранилище обязательств, алертов и настроек. Реализации: память (тесты, стенд),
// PostgreSQL (следующая волна). Авторизация выполняется в Service до вызова хранилища.
type Store interface {
	Save(ctx context.Context, c Commitment) error
	Get(ctx context.Context, id kernel.ID) (Commitment, error)
	List(ctx context.Context, f Filter) ([]Commitment, error)

	// AppendAlert добавляет алерт; список append-only, меняется только подтверждение.
	AppendAlert(ctx context.Context, a Alert) error
	Alert(ctx context.Context, id kernel.ID) (Alert, error)
	// Alerts возвращает алерты продукта; onlyOpen — только неподтверждённые.
	Alerts(ctx context.Context, productID kernel.ID, onlyOpen bool) ([]Alert, error)
	// Acknowledge отмечает алерт подтверждённым.
	Acknowledge(ctx context.Context, a Alert) error
	// AlertByEvent возвращает алерт, поднятый по паре «обязательство + событие» (CT-03).
	// kernel.ErrNotFound, если такого алерта нет. Нужен для дедупликации при повторной
	// доставке события: отметка обработанного события ставится только в конце обработчика.
	AlertByEvent(ctx context.Context, commitmentID, eventID kernel.ID) (Alert, error)

	// EventProcessed сообщает, обрабатывалось ли событие (идемпотентность обработчика по Event.ID).
	EventProcessed(ctx context.Context, eventID kernel.ID) (bool, error)
	// MarkEventProcessed отмечает событие обработанным; в SQL-реализации — в одной транзакции с алертами.
	MarkEventProcessed(ctx context.Context, eventID kernel.ID) error

	Settings(ctx context.Context) (Settings, error)
	SaveSettings(ctx context.Context, st Settings) error
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu        sync.Mutex
	items     []Commitment
	alerts    []Alert
	processed map[kernel.ID]struct{}
	settings  Settings
}

// NewMemStore создаёт пустое хранилище с настройками по умолчанию.
func NewMemStore() *MemStore {
	return &MemStore{processed: map[kernel.ID]struct{}{}, settings: Settings{LeadMonths: DefaultLeadMonths}}
}

// Save создаёт или обновляет обязательство.
func (m *MemStore) Save(_ context.Context, c Commitment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].ID == c.ID {
			m.items[i] = c
			return nil
		}
	}
	m.items = append(m.items, c)
	return nil
}

// Get возвращает обязательство по идентификатору.
func (m *MemStore) Get(_ context.Context, id kernel.ID) (Commitment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.items {
		if c.ID == id {
			return c, nil
		}
	}
	return Commitment{}, kernel.NotFound("commitment", id)
}

// List возвращает обязательства по фильтру в порядке сохранения.
func (m *MemStore) List(_ context.Context, f Filter) ([]Commitment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Commitment, 0, len(m.items))
	for _, c := range m.items {
		if f.matches(c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// AppendAlert добавляет алерт.
func (m *MemStore) AppendAlert(_ context.Context, a Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, a)
	return nil
}

// Alert возвращает алерт по идентификатору.
func (m *MemStore) Alert(_ context.Context, id kernel.ID) (Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.alerts {
		if a.ID == id {
			return a, nil
		}
	}
	return Alert{}, kernel.NotFound("alert", id)
}

// Alerts возвращает алерты продукта в порядке добавления.
func (m *MemStore) Alerts(_ context.Context, productID kernel.ID, onlyOpen bool) ([]Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Alert, 0, len(m.alerts))
	for _, a := range m.alerts {
		if a.ProductID != productID || (onlyOpen && a.Acknowledged) {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// Acknowledge сохраняет подтверждение алерта.
func (m *MemStore) Acknowledge(_ context.Context, a Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.alerts {
		if m.alerts[i].ID == a.ID {
			m.alerts[i].Acknowledged, m.alerts[i].AcknowledgedBy, m.alerts[i].AcknowledgedAt = a.Acknowledged, a.AcknowledgedBy, a.AcknowledgedAt
			return nil
		}
	}
	return kernel.NotFound("alert", a.ID)
}

// AlertByEvent возвращает алерт по паре «обязательство + событие».
func (m *MemStore) AlertByEvent(_ context.Context, commitmentID, eventID kernel.ID) (Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if eventID == kernel.NilID {
		return Alert{}, kernel.NotFound("alert", eventID)
	}
	for _, a := range m.alerts {
		if a.CommitmentID == commitmentID && a.EventID == eventID {
			return a, nil
		}
	}
	return Alert{}, kernel.NotFound("alert", eventID)
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (m *MemStore) EventProcessed(_ context.Context, eventID kernel.ID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.processed[eventID]
	return ok, nil
}

// MarkEventProcessed отмечает событие обработанным.
func (m *MemStore) MarkEventProcessed(_ context.Context, eventID kernel.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processed[eventID] = struct{}{}
	return nil
}

// Settings возвращает настройки.
func (m *MemStore) Settings(_ context.Context) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings, nil
}

// SaveSettings сохраняет настройки.
func (m *MemStore) SaveSettings(_ context.Context, st Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = st
	return nil
}
