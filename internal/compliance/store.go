package compliance

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// TrackFilter — условия выборки треков. Пустое поле — без ограничения.
type TrackFilter struct {
	ProductID kernel.ID
	ReleaseID kernel.ID
}

func (f TrackFilter) matches(t Track) bool {
	if f.ProductID != kernel.NilID && t.ProductID != f.ProductID {
		return false
	}
	if f.ReleaseID != kernel.NilID && t.ReleaseID != f.ReleaseID {
		return false
	}
	return true
}

// Store — хранилище каталога, треков, оценок влияния и baseline. Авторизация — в Service.
// Журнал доказательств — отдельный EvidenceStore (только INSERT).
type Store interface {
	SaveRequirementSet(ctx context.Context, rs RequirementSet) error
	RequirementSet(ctx context.Context, id kernel.ID) (RequirementSet, error)
	// RequirementSets возвращает наборы по коду (пустой код — все) по возрастанию версии.
	RequirementSets(ctx context.Context, code string) ([]RequirementSet, error)

	SaveTemplate(ctx context.Context, t TrackTemplate) error
	Template(ctx context.Context, id kernel.ID) (TrackTemplate, error)
	// Templates возвращает шаблоны типа продукта (пустой тип — все) в порядке сохранения.
	Templates(ctx context.Context, pt portfoliograph.ProductType) ([]TrackTemplate, error)

	SaveTrack(ctx context.Context, t Track) error
	Track(ctx context.Context, id kernel.ID) (Track, error)
	Tracks(ctx context.Context, f TrackFilter) ([]Track, error)

	// AppendImpact добавляет оценку в историю (append-only).
	AppendImpact(ctx context.Context, a ImpactAssessment) error
	// ImpactHistory — оценки фичи в порядке добавления.
	ImpactHistory(ctx context.Context, featureID kernel.ID) ([]ImpactAssessment, error)

	SaveBaseline(ctx context.Context, b CertifiedBaseline) error
	Baseline(ctx context.Context, id kernel.ID) (CertifiedBaseline, error)
	// Baselines возвращает baseline продукта (NilID — все) в порядке сохранения.
	Baselines(ctx context.Context, productID kernel.ID) ([]CertifiedBaseline, error)
}

// MemStore — хранилище в памяти для тестов и стендов без БД.
type MemStore struct {
	mu        sync.RWMutex
	sets      []RequirementSet
	templates []TrackTemplate
	tracks    []Track
	impacts   []ImpactAssessment
	baselines []CertifiedBaseline
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{} }

// SaveRequirementSet создаёт или обновляет набор.
func (m *MemStore) SaveRequirementSet(_ context.Context, rs RequirementSet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs.Items = append([]RequirementItem(nil), rs.Items...)
	for i := range m.sets {
		if m.sets[i].ID == rs.ID {
			m.sets[i] = rs
			return nil
		}
	}
	m.sets = append(m.sets, rs)
	return nil
}

// RequirementSet возвращает набор по идентификатору.
func (m *MemStore) RequirementSet(_ context.Context, id kernel.ID) (RequirementSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rs := range m.sets {
		if rs.ID == id {
			return cloneSet(rs), nil
		}
	}
	return RequirementSet{}, kernel.NotFound("requirement_set", id)
}

// RequirementSets возвращает наборы по коду.
func (m *MemStore) RequirementSets(_ context.Context, code string) ([]RequirementSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RequirementSet, 0, len(m.sets))
	for _, rs := range m.sets {
		if code == "" || rs.Code == code {
			out = append(out, cloneSet(rs))
		}
	}
	return out, nil
}

func cloneSet(rs RequirementSet) RequirementSet {
	rs.Items = append([]RequirementItem(nil), rs.Items...)
	return rs
}

// SaveTemplate создаёт или обновляет шаблон.
func (m *MemStore) SaveTemplate(_ context.Context, t TrackTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t = cloneTemplate(t)
	for i := range m.templates {
		if m.templates[i].ID == t.ID {
			m.templates[i] = t
			return nil
		}
	}
	m.templates = append(m.templates, t)
	return nil
}

// Template возвращает шаблон.
func (m *MemStore) Template(_ context.Context, id kernel.ID) (TrackTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.templates {
		if t.ID == id {
			return cloneTemplate(t), nil
		}
	}
	return TrackTemplate{}, kernel.NotFound("track_template", id)
}

// Templates возвращает шаблоны типа продукта.
func (m *MemStore) Templates(_ context.Context, pt portfoliograph.ProductType) ([]TrackTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TrackTemplate, 0, len(m.templates))
	for _, t := range m.templates {
		if pt == "" || t.ProductType == pt {
			out = append(out, cloneTemplate(t))
		}
	}
	return out, nil
}

func cloneTemplate(t TrackTemplate) TrackTemplate {
	gates := make([]GateTemplate, len(t.Gates))
	for i, g := range t.Gates {
		g.Checklist = append([]string(nil), g.Checklist...)
		gates[i] = g
	}
	t.Gates = gates
	return t
}

// SaveTrack создаёт или обновляет трек.
func (m *MemStore) SaveTrack(_ context.Context, t Track) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t = cloneTrack(t)
	for i := range m.tracks {
		if m.tracks[i].ID == t.ID {
			m.tracks[i] = t
			return nil
		}
	}
	m.tracks = append(m.tracks, t)
	return nil
}

// Track возвращает трек.
func (m *MemStore) Track(_ context.Context, id kernel.ID) (Track, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tracks {
		if t.ID == id {
			return cloneTrack(t), nil
		}
	}
	return Track{}, kernel.NotFound("track", id)
}

// Tracks возвращает треки по фильтру в порядке сохранения.
func (m *MemStore) Tracks(_ context.Context, f TrackFilter) ([]Track, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Track, 0, len(m.tracks))
	for _, t := range m.tracks {
		if f.matches(t) {
			out = append(out, cloneTrack(t))
		}
	}
	return out, nil
}

func cloneTrack(t Track) Track {
	gates := make([]Gate, len(t.Gates))
	for i, g := range t.Gates {
		g.Checklist = append([]ChecklistItem(nil), g.Checklist...)
		gates[i] = g
	}
	t.Gates = gates
	return t
}

// AppendImpact добавляет оценку класса влияния.
func (m *MemStore) AppendImpact(_ context.Context, a ImpactAssessment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.impacts = append(m.impacts, a)
	return nil
}

// ImpactHistory возвращает оценки фичи.
func (m *MemStore) ImpactHistory(_ context.Context, featureID kernel.ID) ([]ImpactAssessment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ImpactAssessment, 0)
	for _, a := range m.impacts {
		if a.FeatureID == featureID {
			out = append(out, a)
		}
	}
	return out, nil
}

// SaveBaseline создаёт или обновляет baseline.
func (m *MemStore) SaveBaseline(_ context.Context, b CertifiedBaseline) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.baselines {
		if m.baselines[i].ID == b.ID {
			m.baselines[i] = b
			return nil
		}
	}
	m.baselines = append(m.baselines, b)
	return nil
}

// Baseline возвращает baseline.
func (m *MemStore) Baseline(_ context.Context, id kernel.ID) (CertifiedBaseline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.baselines {
		if b.ID == id {
			return b, nil
		}
	}
	return CertifiedBaseline{}, kernel.NotFound("certified_baseline", id)
}

// Baselines возвращает baseline продукта.
func (m *MemStore) Baselines(_ context.Context, productID kernel.ID) ([]CertifiedBaseline, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CertifiedBaseline, 0, len(m.baselines))
	for _, b := range m.baselines {
		if productID == kernel.NilID || b.ProductID == productID {
			out = append(out, b)
		}
	}
	return out, nil
}
