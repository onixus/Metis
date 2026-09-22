package economics

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// FactFilter — фильтр строк данных. Пустой указатель означает «любое значение измерения».
type FactFilter struct {
	FieldKey string
	Product  *kernel.ID
	Team     *kernel.ID
	Period   *Period
	Item     *string
	// DataVersion — конкретная версия данных; nil — действующая (последняя применённая) версия периода.
	DataVersion *int
}

// Store — хранилище экономики. Авторизация выполняется в Service до вызова хранилища.
type Store interface {
	SaveField(ctx context.Context, f Field) error
	Field(ctx context.Context, key string) (Field, error)
	Fields(ctx context.Context) ([]Field, error)

	SaveMetric(ctx context.Context, m Metric) error
	Metric(ctx context.Context, key string) (Metric, error)
	Metrics(ctx context.Context) ([]Metric, error)

	SaveTemplate(ctx context.Context, t Template) error
	Template(ctx context.Context, id kernel.ID) (Template, error)
	Templates(ctx context.Context) ([]Template, error)

	SaveBatch(ctx context.Context, b ImportBatch) error
	Batch(ctx context.Context, id kernel.ID) (ImportBatch, error)
	// Batches возвращает историю загрузок; нулевой период — все загрузки.
	Batches(ctx context.Context, period Period) ([]ImportBatch, error)
	// AppendFacts добавляет строки данных; строки не изменяются и не удаляются.
	AppendFacts(ctx context.Context, rows []FactRow) error
	Facts(ctx context.Context, f FactFilter) ([]FactRow, error)

	SaveAllocationRule(ctx context.Context, r AllocationRule) error
	AllocationRules(ctx context.Context) ([]AllocationRule, error)
	SaveBundleRule(ctx context.Context, r BundleRule) error
	BundleRules(ctx context.Context) ([]BundleRule, error)

	SaveTeam(ctx context.Context, t Team) error
	Teams(ctx context.Context) ([]Team, error)
	SaveTeamShares(ctx context.Context, shares []TeamShare) error
	TeamShares(ctx context.Context, period Period) ([]TeamShare, error)

	ClosePeriod(ctx context.Context, p Period, actor string) error
	ClosedPeriods(ctx context.Context) ([]Period, error)

	SaveScenario(ctx context.Context, s Scenario) error
	Scenario(ctx context.Context, id kernel.ID) (Scenario, error)
	Scenarios(ctx context.Context) ([]Scenario, error)
}

// MemStore — хранилище в памяти (тесты, стенд). PG-хранилище — отдельной итерацией (вопрос 12).
type MemStore struct {
	mu        sync.RWMutex
	fields    map[string]Field
	metrics   map[string]Metric
	templates map[kernel.ID]Template
	batches   map[kernel.ID]ImportBatch
	facts     []FactRow
	alloc     []AllocationRule
	bundles   []BundleRule
	teams     map[kernel.ID]Team
	shares    []TeamShare
	closed    map[string]Period
	scenarios map[kernel.ID]Scenario
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore {
	return &MemStore{
		fields:    map[string]Field{},
		metrics:   map[string]Metric{},
		templates: map[kernel.ID]Template{},
		batches:   map[kernel.ID]ImportBatch{},
		teams:     map[kernel.ID]Team{},
		closed:    map[string]Period{},
		scenarios: map[kernel.ID]Scenario{},
	}
}

var _ Store = (*MemStore)(nil)

// SaveField сохраняет поле.
func (m *MemStore) SaveField(_ context.Context, f Field) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields[f.Key] = f
	return nil
}

// Field возвращает поле по ключу.
func (m *MemStore) Field(_ context.Context, key string) (Field, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.fields[key]
	if !ok {
		return Field{}, fmt.Errorf("%w: поле %q", kernel.ErrNotFound, key)
	}
	return f, nil
}

// Fields возвращает поля в порядке ключа.
func (m *MemStore) Fields(_ context.Context) ([]Field, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Field, 0, len(m.fields))
	for _, f := range m.fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SaveMetric сохраняет показатель.
func (m *MemStore) SaveMetric(_ context.Context, v Metric) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[v.Key] = v
	return nil
}

// Metric возвращает показатель по ключу.
func (m *MemStore) Metric(_ context.Context, key string) (Metric, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.metrics[key]
	if !ok {
		return Metric{}, fmt.Errorf("%w: показатель %q", kernel.ErrNotFound, key)
	}
	return v, nil
}

// Metrics возвращает показатели в порядке ключа.
func (m *MemStore) Metrics(_ context.Context) ([]Metric, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Metric, 0, len(m.metrics))
	for _, v := range m.metrics {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SaveTemplate сохраняет шаблон импорта.
func (m *MemStore) SaveTemplate(_ context.Context, t Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[t.ID] = t
	return nil
}

// Template возвращает шаблон импорта.
func (m *MemStore) Template(_ context.Context, id kernel.ID) (Template, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.templates[id]
	if !ok {
		return Template{}, kernel.NotFound("import_template", id)
	}
	return t, nil
}

// Templates возвращает шаблоны импорта.
func (m *MemStore) Templates(_ context.Context) ([]Template, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Template, 0, len(m.templates))
	for _, t := range m.templates {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SaveBatch сохраняет загрузку.
func (m *MemStore) SaveBatch(_ context.Context, b ImportBatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches[b.ID] = b
	return nil
}

// Batch возвращает загрузку.
func (m *MemStore) Batch(_ context.Context, id kernel.ID) (ImportBatch, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.batches[id]
	if !ok {
		return ImportBatch{}, kernel.NotFound("import_batch", id)
	}
	return b, nil
}

// Batches возвращает историю загрузок периода (нулевой период — все).
func (m *MemStore) Batches(_ context.Context, p Period) ([]ImportBatch, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ImportBatch, 0, len(m.batches))
	for _, b := range m.batches {
		if !p.IsZero() && b.Period != p {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Period != out[j].Period {
			return out[i].Period.Before(out[j].Period)
		}
		return out[i].DataVersion < out[j].DataVersion
	})
	return out, nil
}

// AppendFacts добавляет строки данных.
func (m *MemStore) AppendFacts(_ context.Context, rows []FactRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.facts = append(m.facts, rows...)
	return nil
}

// Facts возвращает строки данных по фильтру. Без указания версии берётся действующая
// версия данных периода — последняя применённая загрузка (EC-07).
func (m *MemStore) Facts(_ context.Context, f FactFilter) ([]FactRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	latest := map[string]int{}
	applied := map[kernel.ID]bool{}
	for _, b := range m.batches {
		if b.Status != BatchApplied {
			continue
		}
		applied[b.ID] = true
		if v, ok := latest[b.Period.String()]; !ok || b.DataVersion > v {
			latest[b.Period.String()] = b.DataVersion
		}
	}
	out := make([]FactRow, 0)
	for _, r := range m.facts {
		if !applied[r.BatchID] {
			continue
		}
		if f.FieldKey != "" && r.FieldKey != f.FieldKey {
			continue
		}
		if f.Product != nil && r.ProductID != *f.Product {
			continue
		}
		if f.Team != nil && r.TeamID != *f.Team {
			continue
		}
		if f.Period != nil && r.Period != *f.Period {
			continue
		}
		if f.Item != nil && r.Item != *f.Item {
			continue
		}
		switch {
		case f.DataVersion != nil:
			if r.DataVersion != *f.DataVersion {
				continue
			}
		default:
			if r.DataVersion != latest[r.Period.String()] {
				continue
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}

// SaveAllocationRule добавляет версию правила аллокации.
func (m *MemStore) SaveAllocationRule(_ context.Context, r AllocationRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alloc = append(m.alloc, r)
	return nil
}

// AllocationRules возвращает правила аллокации в порядке даты действия.
func (m *MemStore) AllocationRules(_ context.Context) ([]AllocationRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]AllocationRule(nil), m.alloc...)
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// SaveBundleRule добавляет версию правила атрибуции бандла.
func (m *MemStore) SaveBundleRule(_ context.Context, r BundleRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bundles = append(m.bundles, r)
	return nil
}

// BundleRules возвращает правила атрибуции бандлов.
func (m *MemStore) BundleRules(_ context.Context) ([]BundleRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]BundleRule(nil), m.bundles...)
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// SaveTeam сохраняет команду.
func (m *MemStore) SaveTeam(_ context.Context, t Team) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.teams[t.ID] = t
	return nil
}

// Teams возвращает команды.
func (m *MemStore) Teams(_ context.Context) ([]Team, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Team, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SaveTeamShares заменяет доли команд в периодах, к которым относятся переданные записи.
func (m *MemStore) SaveTeamShares(_ context.Context, shares []TeamShare) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	affected := map[string]map[kernel.ID]bool{}
	for _, s := range shares {
		if affected[s.Period.String()] == nil {
			affected[s.Period.String()] = map[kernel.ID]bool{}
		}
		affected[s.Period.String()][s.TeamID] = true
	}
	kept := m.shares[:0]
	for _, s := range m.shares {
		if affected[s.Period.String()][s.TeamID] {
			continue
		}
		kept = append(kept, s)
	}
	m.shares = append(kept, shares...)
	return nil
}

// TeamShares возвращает доли команд периода (нулевой период — все).
func (m *MemStore) TeamShares(_ context.Context, p Period) ([]TeamShare, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TeamShare, 0, len(m.shares))
	for _, s := range m.shares {
		if !p.IsZero() && s.Period != p {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// ClosePeriod закрывает период.
func (m *MemStore) ClosePeriod(_ context.Context, p Period, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed[p.String()] = p
	return nil
}

// ClosedPeriods возвращает закрытые периоды.
func (m *MemStore) ClosedPeriods(_ context.Context) ([]Period, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Period, 0, len(m.closed))
	for _, p := range m.closed {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}

// SaveScenario сохраняет сценарий.
func (m *MemStore) SaveScenario(_ context.Context, s Scenario) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scenarios[s.ID] = s
	return nil
}

// Scenario возвращает сценарий.
func (m *MemStore) Scenario(_ context.Context, id kernel.ID) (Scenario, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.scenarios[id]
	if !ok {
		return Scenario{}, kernel.NotFound("scenario", id)
	}
	return s, nil
}

// Scenarios возвращает сценарии.
func (m *MemStore) Scenarios(_ context.Context) ([]Scenario, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Scenario, 0, len(m.scenarios))
	for _, s := range m.scenarios {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
