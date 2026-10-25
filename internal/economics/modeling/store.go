package economics

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
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

// Store — хранилище экономики; Scope проверяется и сервисом, и хранилищем.
type Store interface {
	SaveField(ctx context.Context, sc authz.Scope, f Field) error
	Field(ctx context.Context, sc authz.Scope, key string) (Field, error)
	Fields(ctx context.Context, sc authz.Scope) ([]Field, error)

	SaveMetric(ctx context.Context, sc authz.Scope, m Metric) error
	Metric(ctx context.Context, sc authz.Scope, key string) (Metric, error)
	Metrics(ctx context.Context, sc authz.Scope) ([]Metric, error)

	SaveTemplate(ctx context.Context, sc authz.Scope, t Template) error
	Template(ctx context.Context, sc authz.Scope, id kernel.ID) (Template, error)
	Templates(ctx context.Context, sc authz.Scope) ([]Template, error)

	SaveBatch(ctx context.Context, sc authz.Scope, b ImportBatch) error
	Batch(ctx context.Context, sc authz.Scope, id kernel.ID) (ImportBatch, error)
	// Batches возвращает историю загрузок; нулевой период — все загрузки.
	Batches(ctx context.Context, sc authz.Scope, period Period) ([]ImportBatch, error)
	// AppendFacts добавляет строки данных; строки не изменяются и не удаляются.
	AppendFacts(ctx context.Context, sc authz.Scope, rows []FactRow) error
	Facts(ctx context.Context, sc authz.Scope, f FactFilter) ([]FactRow, error)

	SaveAllocationRule(ctx context.Context, sc authz.Scope, r AllocationRule) error
	AllocationRules(ctx context.Context, sc authz.Scope) ([]AllocationRule, error)
	SaveBundleRule(ctx context.Context, sc authz.Scope, r BundleRule) error
	BundleRules(ctx context.Context, sc authz.Scope) ([]BundleRule, error)

	SaveTeam(ctx context.Context, sc authz.Scope, t Team) error
	Teams(ctx context.Context, sc authz.Scope) ([]Team, error)
	SaveTeamShares(ctx context.Context, sc authz.Scope, shares []TeamShare) error
	TeamShares(ctx context.Context, sc authz.Scope, period Period) ([]TeamShare, error)

	ClosePeriod(ctx context.Context, sc authz.Scope, p Period, actor string) error
	ClosedPeriods(ctx context.Context, sc authz.Scope) ([]Period, error)

	SaveScenario(ctx context.Context, sc authz.Scope, s Scenario) error
	Scenario(ctx context.Context, sc authz.Scope, id kernel.ID) (Scenario, error)
	Scenarios(ctx context.Context, sc authz.Scope) ([]Scenario, error)
}

// TransactionalStore — порт атомарных изменений модели вместе с аудитом и outbox.
type TransactionalStore interface {
	Transact(ctx context.Context, sc authz.Scope, fn func(context.Context) error) error
}

// MemStore — хранилище в памяти (тесты, стенд).
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
func (m *MemStore) SaveField(_ context.Context, sc authz.Scope, f Field) error {
	f = kernel.CloneValue(f)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields[f.Key] = f
	return nil
}

// Field возвращает поле по ключу.
func (m *MemStore) Field(_ context.Context, sc authz.Scope, key string) (Field, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return Field{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.fields[key]
	if !ok {
		return Field{}, fmt.Errorf("%w: поле %q", kernel.ErrNotFound, key)
	}
	return kernel.CloneValue(f), nil
}

// Fields возвращает поля в порядке ключа.
func (m *MemStore) Fields(_ context.Context, sc authz.Scope) ([]Field, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Field, 0, len(m.fields))
	for _, f := range m.fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return kernel.CloneValue(out), nil
}

// SaveMetric сохраняет показатель.
func (m *MemStore) SaveMetric(_ context.Context, sc authz.Scope, v Metric) error {
	v = kernel.CloneValue(v)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metrics[v.Key] = v
	return nil
}

// Metric возвращает показатель по ключу.
func (m *MemStore) Metric(_ context.Context, sc authz.Scope, key string) (Metric, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return Metric{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.metrics[key]
	if !ok {
		return Metric{}, fmt.Errorf("%w: показатель %q", kernel.ErrNotFound, key)
	}
	return kernel.CloneValue(v), nil
}

// Metrics возвращает показатели в порядке ключа.
func (m *MemStore) Metrics(_ context.Context, sc authz.Scope) ([]Metric, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Metric, 0, len(m.metrics))
	for _, v := range m.metrics {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return kernel.CloneValue(out), nil
}

// SaveTemplate сохраняет шаблон импорта.
func (m *MemStore) SaveTemplate(_ context.Context, sc authz.Scope, t Template) error {
	t = kernel.CloneValue(t)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.templates[t.ID] = t
	return nil
}

// Template возвращает шаблон импорта.
func (m *MemStore) Template(_ context.Context, sc authz.Scope, id kernel.ID) (Template, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return Template{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.templates[id]
	if !ok {
		return Template{}, kernel.NotFound("import_template", id)
	}
	return kernel.CloneValue(t), nil
}

// Templates возвращает шаблоны импорта.
func (m *MemStore) Templates(_ context.Context, sc authz.Scope) ([]Template, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Template, 0, len(m.templates))
	for _, t := range m.templates {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return kernel.CloneValue(out), nil
}

// SaveBatch сохраняет загрузку.
func (m *MemStore) SaveBatch(_ context.Context, sc authz.Scope, b ImportBatch) error {
	b = kernel.CloneValue(b)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches[b.ID] = b
	return nil
}

// Batch возвращает загрузку.
func (m *MemStore) Batch(_ context.Context, sc authz.Scope, id kernel.ID) (ImportBatch, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceFull {
		return ImportBatch{}, kernel.ErrForbidden
	}
	if !sc.SeesAllProducts() {
		return ImportBatch{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.batches[id]
	if !ok {
		return ImportBatch{}, kernel.NotFound("import_batch", id)
	}
	return kernel.CloneValue(b), nil
}

// Batches возвращает историю загрузок периода (нулевой период — все).
func (m *MemStore) Batches(_ context.Context, sc authz.Scope, p Period) ([]ImportBatch, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceFull {
		return nil, kernel.ErrForbidden
	}
	if !sc.SeesAllProducts() {
		return nil, kernel.ErrForbidden
	}
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
	return kernel.CloneValue(out), nil
}

// AppendFacts добавляет строки данных.
func (m *MemStore) AppendFacts(_ context.Context, sc authz.Scope, rows []FactRow) error {
	rows = kernel.CloneValue(rows)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	for _, row := range rows {
		if !sc.Allows(authz.ActionReadModelFinance, row.ProductID) {
			return kernel.ErrForbidden
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.facts = append(m.facts, rows...)
	return nil
}

// Facts возвращает строки данных по фильтру. Без указания версии берётся действующая
// версия данных периода — последняя применённая загрузка (EC-07).
func (m *MemStore) Facts(_ context.Context, sc authz.Scope, f FactFilter) ([]FactRow, error) {
	f = kernel.CloneValue(f)
	if !sc.Valid() || sc.Finance() < authz.FinanceFull {
		return nil, kernel.ErrForbidden
	}
	if f.Product != nil && !sc.Allows(authz.ActionReadModelFinance, *f.Product) {
		return nil, kernel.ErrForbidden
	}
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
		if !sc.Allows(authz.ActionReadModelFinance, r.ProductID) {
			continue
		}
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
	return kernel.CloneValue(out), nil
}

// SaveAllocationRule добавляет версию правила аллокации.
func (m *MemStore) SaveAllocationRule(_ context.Context, sc authz.Scope, r AllocationRule) error {
	r = kernel.CloneValue(r)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alloc = append(m.alloc, r)
	return nil
}

// AllocationRules возвращает правила аллокации в порядке даты действия.
func (m *MemStore) AllocationRules(_ context.Context, sc authz.Scope) ([]AllocationRule, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AllocationRule, 0)
	for _, rule := range m.alloc {
		visible := true
		for id := range rule.Shares {
			if !sc.Allows(authz.ActionReadModelFinance, id) {
				visible = false
			}
		}
		if !sc.Allows(authz.ActionReadModelFinance, rule.HubProductID) {
			visible = false
		}
		for _, id := range rule.Consumers {
			if !sc.Allows(authz.ActionReadModelFinance, id) {
				visible = false
			}
		}
		if visible {
			out = append(out, rule)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return kernel.CloneValue(out), nil
}

// SaveBundleRule добавляет версию правила атрибуции бандла.
func (m *MemStore) SaveBundleRule(_ context.Context, sc authz.Scope, r BundleRule) error {
	r = kernel.CloneValue(r)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bundles = append(m.bundles, r)
	return nil
}

// BundleRules возвращает правила атрибуции бандлов.
func (m *MemStore) BundleRules(_ context.Context, sc authz.Scope) ([]BundleRule, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BundleRule, 0)
	for _, rule := range m.bundles {
		visible := true
		for id := range rule.Shares {
			if !sc.Allows(authz.ActionReadModelFinance, id) {
				visible = false
			}
		}
		if visible {
			out = append(out, rule)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return kernel.CloneValue(out), nil
}

// SaveTeam сохраняет команду.
func (m *MemStore) SaveTeam(_ context.Context, sc authz.Scope, t Team) error {
	t = kernel.CloneValue(t)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.teams[t.ID] = t
	return nil
}

// Teams возвращает команды.
func (m *MemStore) Teams(_ context.Context, sc authz.Scope) ([]Team, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Team, 0, len(m.teams))
	for _, t := range m.teams {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return kernel.CloneValue(out), nil
}

// SaveTeamShares заменяет доли команд в периодах, к которым относятся переданные записи.
func (m *MemStore) SaveTeamShares(_ context.Context, sc authz.Scope, shares []TeamShare) error {
	shares = kernel.CloneValue(shares)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
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
func (m *MemStore) TeamShares(_ context.Context, sc authz.Scope, p Period) ([]TeamShare, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TeamShare, 0, len(m.shares))
	for _, s := range m.shares {
		if !sc.Allows(authz.ActionReadModelFinance, s.ProductID) {
			continue
		}
		if !p.IsZero() && s.Period != p {
			continue
		}
		out = append(out, s)
	}
	return kernel.CloneValue(out), nil
}

// ClosePeriod закрывает период.
func (m *MemStore) ClosePeriod(_ context.Context, sc authz.Scope, p Period, _ string) error {
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed[p.String()] = p
	return nil
}

// ClosedPeriods возвращает закрытые периоды.
func (m *MemStore) ClosedPeriods(_ context.Context, sc authz.Scope) ([]Period, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Period, 0, len(m.closed))
	for _, p := range m.closed {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return kernel.CloneValue(out), nil
}

// SaveScenario сохраняет сценарий.
func (m *MemStore) SaveScenario(_ context.Context, sc authz.Scope, s Scenario) error {
	s = kernel.CloneValue(s)
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scenarios[s.ID] = s
	return nil
}

// Scenario возвращает сценарий.
func (m *MemStore) Scenario(_ context.Context, sc authz.Scope, id kernel.ID) (Scenario, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return Scenario{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.scenarios[id]
	if !ok {
		return Scenario{}, kernel.NotFound("scenario", id)
	}
	if !scenarioVisible(sc, s) {
		return Scenario{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(s), nil
}

// Scenarios возвращает сценарии.
func (m *MemStore) Scenarios(_ context.Context, sc authz.Scope) ([]Scenario, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Scenario, 0, len(m.scenarios))
	for _, s := range m.scenarios {
		if scenarioVisible(sc, s) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return kernel.CloneValue(out), nil
}

func scenarioVisible(sc authz.Scope, s Scenario) bool {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates {
		return false
	}
	if len(s.Products) == 0 && !sc.SeesAllProducts() {
		return false
	}
	for _, id := range s.Products {
		if !sc.Allows(authz.ActionReadModelFinance, id) {
			return false
		}
	}
	for _, o := range s.Overrides {
		if !sc.Allows(authz.ActionReadModelFinance, o.ProductID) {
			return false
		}
	}
	return true
}
