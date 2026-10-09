package commitments

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
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
	Save(ctx context.Context, sc authz.Scope, c Commitment) error
	Get(ctx context.Context, sc authz.Scope, id kernel.ID) (Commitment, error)
	List(ctx context.Context, sc authz.Scope, f Filter) ([]Commitment, error)

	// AppendAlert добавляет алерт; список append-only, меняется только подтверждение.
	AppendAlert(ctx context.Context, sc authz.Scope, a Alert) error
	Alert(ctx context.Context, sc authz.Scope, id kernel.ID) (Alert, error)
	// Alerts возвращает алерты продукта; onlyOpen — только неподтверждённые.
	Alerts(ctx context.Context, sc authz.Scope, productID kernel.ID, onlyOpen bool) ([]Alert, error)
	// Acknowledge отмечает алерт подтверждённым.
	Acknowledge(ctx context.Context, sc authz.Scope, a Alert) error
	// AlertByEvent возвращает алерт, поднятый по паре «обязательство + событие» (CT-03).
	// kernel.ErrNotFound, если такого алерта нет. Нужен для дедупликации при повторной
	// доставке события: отметка обработанного события ставится только в конце обработчика.
	AlertByEvent(ctx context.Context, sc authz.Scope, commitmentID, eventID kernel.ID) (Alert, error)

	// EventProcessed сообщает, обрабатывалось ли событие (идемпотентность обработчика по Event.ID).
	EventProcessed(ctx context.Context, sc authz.Scope, eventID kernel.ID) (bool, error)
	// MarkEventProcessed отмечает событие обработанным; в SQL-реализации — в одной транзакции с алертами.
	MarkEventProcessed(ctx context.Context, sc authz.Scope, eventID kernel.ID) error

	Settings(ctx context.Context, sc authz.Scope) (Settings, error)
	SaveSettings(ctx context.Context, sc authz.Scope, st Settings) error
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
func (m *MemStore) Save(_ context.Context, sc authz.Scope, c Commitment) error {
	c = kernel.CloneValue(c)
	if c.ProductID == kernel.NilID || sc.Product(c.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteCommitments, c.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.items {
		if m.items[i].ID == c.ID {
			if m.items[i].ProductID != c.ProductID {
				return kernel.ErrForbidden
			}
			m.items[i] = c
			return nil
		}
	}
	m.items = append(m.items, c)
	return nil
}

// Get возвращает обязательство по идентификатору.
func (m *MemStore) Get(_ context.Context, sc authz.Scope, id kernel.ID) (Commitment, error) {
	if !sc.Valid() {
		return Commitment{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.items {
		if c.ID == id {
			if !sc.Allows(authz.ActionReadStrategic, c.ProductID) {
				return Commitment{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(c), nil
		}
	}
	return Commitment{}, kernel.NotFound("commitment", id)
}

// List возвращает обязательства по фильтру в порядке сохранения.
func (m *MemStore) List(_ context.Context, sc authz.Scope, f Filter) ([]Commitment, error) {
	f = kernel.CloneValue(f)
	if f.ProductID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, f.ProductID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Commitment, 0, len(m.items))
	for _, c := range m.items {
		if !sc.Allows(authz.ActionReadStrategic, c.ProductID) {
			continue
		}
		if f.matches(c) {
			out = append(out, c)
		}
	}
	return kernel.CloneValue(out), nil
}

// AppendAlert добавляет алерт.
func (m *MemStore) AppendAlert(_ context.Context, sc authz.Scope, a Alert) error {
	a = kernel.CloneValue(a)
	if a.ProductID == kernel.NilID || sc.Product(a.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteCommitments, a.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, c := range m.items {
		if c.ID == a.CommitmentID && c.ProductID == a.ProductID {
			found = true
		}
	}
	if !found {
		return kernel.ErrForbidden
	}
	for _, old := range m.alerts {
		if old.ID == a.ID {
			return kernel.ErrConflict
		}
	}
	m.alerts = append(m.alerts, a)
	return nil
}

// Alert возвращает алерт по идентификатору.
func (m *MemStore) Alert(_ context.Context, sc authz.Scope, id kernel.ID) (Alert, error) {
	if !sc.Valid() {
		return Alert{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.alerts {
		if a.ID == id {
			if !sc.Allows(authz.ActionReadStrategic, a.ProductID) {
				return Alert{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(a), nil
		}
	}
	return Alert{}, kernel.NotFound("alert", id)
}

// Alerts возвращает алерты продукта в порядке добавления.
func (m *MemStore) Alerts(_ context.Context, sc authz.Scope, productID kernel.ID, onlyOpen bool) ([]Alert, error) {
	if productID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, productID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Alert, 0, len(m.alerts))
	for _, a := range m.alerts {
		if !sc.Allows(authz.ActionReadStrategic, a.ProductID) {
			continue
		}
		if a.ProductID != productID || (onlyOpen && a.Acknowledged) {
			continue
		}
		out = append(out, a)
	}
	return kernel.CloneValue(out), nil
}

// Acknowledge сохраняет подтверждение алерта.
func (m *MemStore) Acknowledge(_ context.Context, sc authz.Scope, a Alert) error {
	a = kernel.CloneValue(a)
	if a.ProductID == kernel.NilID || sc.Product(a.ProductID) < authz.AccessPrivate || !sc.Allows(authz.ActionWriteCommitments, a.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.alerts {
		if m.alerts[i].ID == a.ID {
			if m.alerts[i].ProductID != a.ProductID {
				return kernel.ErrForbidden
			}
			m.alerts[i].Acknowledged, m.alerts[i].AcknowledgedBy, m.alerts[i].AcknowledgedAt = a.Acknowledged, a.AcknowledgedBy, a.AcknowledgedAt
			return nil
		}
	}
	return kernel.NotFound("alert", a.ID)
}

// AlertByEvent возвращает алерт по паре «обязательство + событие».
func (m *MemStore) AlertByEvent(_ context.Context, sc authz.Scope, commitmentID, eventID kernel.ID) (Alert, error) {
	if !sc.Valid() {
		return Alert{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if eventID == kernel.NilID {
		return Alert{}, kernel.NotFound("alert", eventID)
	}
	for _, a := range m.alerts {
		if a.CommitmentID == commitmentID && a.EventID == eventID {
			if !sc.Allows(authz.ActionReadStrategic, a.ProductID) {
				return Alert{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(a), nil
		}
	}
	return Alert{}, kernel.NotFound("alert", eventID)
}

// EventProcessed сообщает, обрабатывалось ли событие.
func (m *MemStore) EventProcessed(_ context.Context, sc authz.Scope, eventID kernel.ID) (bool, error) {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return false, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.processed[eventID]
	return kernel.CloneValue(ok), nil
}

// MarkEventProcessed отмечает событие обработанным.
func (m *MemStore) MarkEventProcessed(_ context.Context, sc authz.Scope, eventID kernel.ID) error {
	if !sc.Valid() || !sc.HasRole(authz.RoleService) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processed[eventID] = struct{}{}
	return nil
}

// Settings возвращает настройки.
func (m *MemStore) Settings(_ context.Context, sc authz.Scope) (Settings, error) {
	if !sc.Valid() {
		return Settings{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return kernel.CloneValue(m.settings), nil
}

// SaveSettings сохраняет настройки.
func (m *MemStore) SaveSettings(_ context.Context, sc authz.Scope, st Settings) error {
	st = kernel.CloneValue(st)
	if !sc.Allows(authz.ActionAdminSettings, kernel.NilID) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = st
	return nil
}
