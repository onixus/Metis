package discovery

import (
	"context"
	"slices"
	"sync"

	"github.com/onixus/metis/internal/kernel"
)

// HypothesisFilter — условия выборки гипотез. Пустое поле — без ограничения.
type HypothesisFilter struct {
	ProductID kernel.ID
	FeatureID kernel.ID
	Statuses  []HypothesisStatus
}

// InsightFilter — условия выборки инсайтов.
type InsightFilter struct {
	ProductID    kernel.ID
	InterviewID  kernel.ID
	HypothesisID kernel.ID
	SignalID     kernel.ID
}

// EvidenceFilter — условия выборки evidence.
type EvidenceFilter struct {
	ProductID    kernel.ID
	HypothesisID kernel.ID
	InsightID    kernel.ID
	FeatureID    kernel.ID
	Verification Verification
}

// Store — хранилище discovery. Реализации: память (тесты, стенд), PostgreSQL (следующая волна).
// Авторизация выполняется в Service до вызова хранилища.
type Store interface {
	SaveHypothesis(ctx context.Context, h Hypothesis) error
	Hypothesis(ctx context.Context, id kernel.ID) (Hypothesis, error)
	Hypotheses(ctx context.Context, f HypothesisFilter) ([]Hypothesis, error)

	SaveInterview(ctx context.Context, i Interview) error
	Interview(ctx context.Context, id kernel.ID) (Interview, error)
	Interviews(ctx context.Context, productID kernel.ID) ([]Interview, error)

	SaveInsight(ctx context.Context, i Insight) error
	Insight(ctx context.Context, id kernel.ID) (Insight, error)
	Insights(ctx context.Context, f InsightFilter) ([]Insight, error)

	SaveEvidence(ctx context.Context, e Evidence) error
	Evidence(ctx context.Context, id kernel.ID) (Evidence, error)
	EvidenceList(ctx context.Context, f EvidenceFilter) ([]Evidence, error)

	// Настройки AD-03: определения общие для портфеля (без product_id).
	SaveFieldDef(ctx context.Context, d CustomFieldDef) error
	FieldDefs(ctx context.Context, e Entity) ([]CustomFieldDef, error)
	SaveStatusDef(ctx context.Context, d CustomStatusDef) error
	StatusDefs(ctx context.Context, e Entity) ([]CustomStatusDef, error)
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu         sync.Mutex
	hypotheses []Hypothesis
	interviews []Interview
	insights   []Insight
	evidence   []Evidence
	fields     []CustomFieldDef
	statuses   []CustomStatusDef
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{} }

// upsert заменяет элемент с тем же ключом или добавляет новый.
func upsert[T any](items []T, v T, same func(a, b T) bool) []T {
	for i := range items {
		if same(items[i], v) {
			items[i] = v
			return items
		}
	}
	return append(items, v)
}

// SaveHypothesis создаёт или обновляет гипотезу.
func (m *MemStore) SaveHypothesis(_ context.Context, h Hypothesis) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hypotheses = upsert(m.hypotheses, h, func(a, b Hypothesis) bool { return a.ID == b.ID })
	return nil
}

// Hypothesis возвращает гипотезу по идентификатору.
func (m *MemStore) Hypothesis(_ context.Context, id kernel.ID) (Hypothesis, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.hypotheses {
		if h.ID == id {
			return h, nil
		}
	}
	return Hypothesis{}, kernel.NotFound("hypothesis", id)
}

// Hypotheses возвращает гипотезы по фильтру в порядке сохранения.
func (m *MemStore) Hypotheses(_ context.Context, f HypothesisFilter) ([]Hypothesis, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Hypothesis, 0, len(m.hypotheses))
	for _, h := range m.hypotheses {
		if f.ProductID != kernel.NilID && h.ProductID != f.ProductID {
			continue
		}
		if f.FeatureID != kernel.NilID && h.FeatureID != f.FeatureID {
			continue
		}
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, h.Status) {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// SaveInterview создаёт или обновляет интервью.
func (m *MemStore) SaveInterview(_ context.Context, i Interview) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.interviews = upsert(m.interviews, i, func(a, b Interview) bool { return a.ID == b.ID })
	return nil
}

// Interview возвращает интервью по идентификатору.
func (m *MemStore) Interview(_ context.Context, id kernel.ID) (Interview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.interviews {
		if i.ID == id {
			return i, nil
		}
	}
	return Interview{}, kernel.NotFound("interview", id)
}

// Interviews возвращает интервью продукта в порядке сохранения.
func (m *MemStore) Interviews(_ context.Context, productID kernel.ID) ([]Interview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Interview, 0, len(m.interviews))
	for _, i := range m.interviews {
		if productID == kernel.NilID || i.ProductID == productID {
			out = append(out, i)
		}
	}
	return out, nil
}

// SaveInsight создаёт или обновляет инсайт.
func (m *MemStore) SaveInsight(_ context.Context, i Insight) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insights = upsert(m.insights, i, func(a, b Insight) bool { return a.ID == b.ID })
	return nil
}

// Insight возвращает инсайт по идентификатору.
func (m *MemStore) Insight(_ context.Context, id kernel.ID) (Insight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.insights {
		if i.ID == id {
			return i, nil
		}
	}
	return Insight{}, kernel.NotFound("insight", id)
}

// Insights возвращает инсайты по фильтру в порядке сохранения.
func (m *MemStore) Insights(_ context.Context, f InsightFilter) ([]Insight, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Insight, 0, len(m.insights))
	for _, i := range m.insights {
		if f.ProductID != kernel.NilID && i.ProductID != f.ProductID {
			continue
		}
		if f.InterviewID != kernel.NilID && i.InterviewID != f.InterviewID {
			continue
		}
		if f.HypothesisID != kernel.NilID && !slices.Contains(i.HypothesisIDs, f.HypothesisID) {
			continue
		}
		if f.SignalID != kernel.NilID && !slices.Contains(i.SignalIDs, f.SignalID) {
			continue
		}
		out = append(out, i)
	}
	return out, nil
}

// SaveEvidence создаёт или обновляет evidence.
func (m *MemStore) SaveEvidence(_ context.Context, e Evidence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evidence = upsert(m.evidence, e, func(a, b Evidence) bool { return a.ID == b.ID })
	return nil
}

// Evidence возвращает evidence по идентификатору.
func (m *MemStore) Evidence(_ context.Context, id kernel.ID) (Evidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.evidence {
		if e.ID == id {
			return e, nil
		}
	}
	return Evidence{}, kernel.NotFound("evidence", id)
}

// EvidenceList возвращает evidence по фильтру в порядке сохранения.
func (m *MemStore) EvidenceList(_ context.Context, f EvidenceFilter) ([]Evidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Evidence, 0, len(m.evidence))
	for _, e := range m.evidence {
		if f.ProductID != kernel.NilID && e.ProductID != f.ProductID {
			continue
		}
		if f.HypothesisID != kernel.NilID && e.HypothesisID != f.HypothesisID {
			continue
		}
		if f.InsightID != kernel.NilID && e.InsightID != f.InsightID {
			continue
		}
		if f.FeatureID != kernel.NilID && e.FeatureID != f.FeatureID {
			continue
		}
		if f.Verification != "" && e.Verification != f.Verification {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// SaveFieldDef создаёт или обновляет определение поля; ключ уникален в пределах сущности.
func (m *MemStore) SaveFieldDef(_ context.Context, d CustomFieldDef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields = upsert(m.fields, d, func(a, b CustomFieldDef) bool { return a.Entity == b.Entity && a.Key == b.Key })
	return nil
}

// FieldDefs возвращает определения полей сущности в порядке создания.
func (m *MemStore) FieldDefs(_ context.Context, e Entity) ([]CustomFieldDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CustomFieldDef, 0, len(m.fields))
	for _, d := range m.fields {
		if d.Entity == e {
			out = append(out, d)
		}
	}
	return out, nil
}

// SaveStatusDef создаёт или обновляет пользовательский статус.
func (m *MemStore) SaveStatusDef(_ context.Context, d CustomStatusDef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses = upsert(m.statuses, d, func(a, b CustomStatusDef) bool { return a.Entity == b.Entity && a.Key == b.Key })
	return nil
}

// StatusDefs возвращает пользовательские статусы сущности в порядке создания.
func (m *MemStore) StatusDefs(_ context.Context, e Entity) ([]CustomStatusDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CustomStatusDef, 0, len(m.statuses))
	for _, d := range m.statuses {
		if d.Entity == e {
			out = append(out, d)
		}
	}
	return out, nil
}
