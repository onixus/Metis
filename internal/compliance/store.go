package compliance

import (
	"context"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"

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
	Settings(ctx context.Context, sc authz.Scope) (Settings, error)
	SaveSettings(ctx context.Context, sc authz.Scope, settings Settings) error

	SaveRequirementSet(ctx context.Context, sc authz.Scope, rs RequirementSet) error
	RequirementSet(ctx context.Context, sc authz.Scope, id kernel.ID) (RequirementSet, error)
	// RequirementSets возвращает наборы по коду (пустой код — все) по возрастанию версии.
	RequirementSets(ctx context.Context, sc authz.Scope, code string) ([]RequirementSet, error)

	SaveTemplate(ctx context.Context, sc authz.Scope, t TrackTemplate) error
	Template(ctx context.Context, sc authz.Scope, id kernel.ID) (TrackTemplate, error)
	// Templates возвращает шаблоны типа продукта (пустой тип — все) в порядке сохранения.
	Templates(ctx context.Context, sc authz.Scope, pt portfoliograph.ProductType) ([]TrackTemplate, error)

	SaveTrack(ctx context.Context, sc authz.Scope, t Track) error
	Track(ctx context.Context, sc authz.Scope, id kernel.ID) (Track, error)
	Tracks(ctx context.Context, sc authz.Scope, f TrackFilter) ([]Track, error)

	// AppendImpact добавляет оценку в историю (append-only).
	AppendImpact(ctx context.Context, sc authz.Scope, a ImpactAssessment) error
	// ImpactHistory — оценки фичи в порядке добавления.
	ImpactHistory(ctx context.Context, sc authz.Scope, featureID kernel.ID) ([]ImpactAssessment, error)

	SaveBaseline(ctx context.Context, sc authz.Scope, b CertifiedBaseline) error
	Baseline(ctx context.Context, sc authz.Scope, id kernel.ID) (CertifiedBaseline, error)
	// Baselines возвращает baseline продукта (NilID — все) в порядке сохранения.
	Baselines(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]CertifiedBaseline, error)
	// BaselinesWithComponent возвращает baseline, в составе которых есть компонент с таким
	// ключом; версия компонента проверяется вызывающим (CM-08).
	BaselinesWithComponent(ctx context.Context, sc authz.Scope, componentKey string) ([]CertifiedBaseline, error)
}

// MemStore — хранилище в памяти для тестов и стендов без БД.
type MemStore struct {
	mu        sync.RWMutex
	settings  *Settings
	sets      []RequirementSet
	templates []TrackTemplate
	tracks    []Track
	impacts   []ImpactAssessment
	baselines []CertifiedBaseline
}

func NewMemStore() *MemStore { return &MemStore{} }

// Settings returns a detached copy; absence is resolved by the service defaults.
func (m *MemStore) Settings(_ context.Context, sc authz.Scope) (Settings, error) {
	if !sc.Valid() {
		return Settings{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.settings == nil {
		return Settings{}, kernel.ErrNotFound
	}
	return kernel.CloneValue(cloneSettings(*m.settings)), nil
}

// SaveSettings persists validated module settings without retaining caller-owned maps.
func (m *MemStore) SaveSettings(_ context.Context, sc authz.Scope, st Settings) error {
	st = kernel.CloneValue(st)
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return err
	}
	if err := st.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := cloneSettings(st)
	m.settings = &copy
	return nil
}

func cloneSettings(st Settings) Settings {
	out := st
	out.CostByClass = make(map[ImpactClass]kernel.Money, len(st.CostByClass))
	for k, v := range st.CostByClass {
		out.CostByClass[k] = v
	}
	out.VulnerabilityFixDays = make(map[Severity]int, len(st.VulnerabilityFixDays))
	for k, v := range st.VulnerabilityFixDays {
		out.VulnerabilityFixDays[k] = v
	}
	return out
}

// SaveRequirementSet создаёт или обновляет набор.
func (m *MemStore) SaveRequirementSet(_ context.Context, sc authz.Scope, rs RequirementSet) error {
	rs = kernel.CloneValue(rs)
	if err := RequireCatalog(sc); err != nil {
		return err
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
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
func (m *MemStore) RequirementSet(_ context.Context, sc authz.Scope, id kernel.ID) (RequirementSet, error) {
	if !sc.Valid() {
		return RequirementSet{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, rs := range m.sets {
		if rs.ID == id {
			return kernel.CloneValue(cloneSet(rs)), nil
		}
	}
	return RequirementSet{}, kernel.NotFound("requirement_set", id)
}

// RequirementSets возвращает наборы по коду.
func (m *MemStore) RequirementSets(_ context.Context, sc authz.Scope, code string) ([]RequirementSet, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RequirementSet, 0, len(m.sets))
	for _, rs := range m.sets {
		if code == "" || rs.Code == code {
			out = append(out, cloneSet(rs))
		}
	}
	return kernel.CloneValue(out), nil
}

func cloneSet(rs RequirementSet) RequirementSet {
	rs.Items = append([]RequirementItem(nil), rs.Items...)
	return rs
}

// SaveTemplate создаёт или обновляет шаблон.
func (m *MemStore) SaveTemplate(_ context.Context, sc authz.Scope, t TrackTemplate) error {
	t = kernel.CloneValue(t)
	if err := RequireCatalog(sc); err != nil {
		return err
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
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
func (m *MemStore) Template(_ context.Context, sc authz.Scope, id kernel.ID) (TrackTemplate, error) {
	if !sc.Valid() {
		return TrackTemplate{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.templates {
		if t.ID == id {
			return kernel.CloneValue(cloneTemplate(t)), nil
		}
	}
	return TrackTemplate{}, kernel.NotFound("track_template", id)
}

// Templates возвращает шаблоны типа продукта.
func (m *MemStore) Templates(_ context.Context, sc authz.Scope, pt portfoliograph.ProductType) ([]TrackTemplate, error) {
	pt = kernel.CloneValue(pt)
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TrackTemplate, 0, len(m.templates))
	for _, t := range m.templates {
		if pt == "" || t.ProductType == pt {
			out = append(out, cloneTemplate(t))
		}
	}
	return kernel.CloneValue(out), nil
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
func (m *MemStore) SaveTrack(_ context.Context, sc authz.Scope, t Track) error {
	t = kernel.CloneValue(t)
	if t.ProductID == kernel.NilID || !sc.Allows(authz.ActionReadStrategic, t.ProductID) || !sc.Allows(authz.ActionWriteCompliance, t.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t = cloneTrack(t)
	for i := range m.tracks {
		if m.tracks[i].ID == t.ID {
			if m.tracks[i].ProductID != t.ProductID {
				return kernel.ErrForbidden
			}
			m.tracks[i] = t
			return nil
		}
	}
	m.tracks = append(m.tracks, t)
	return nil
}

// Track возвращает трек.
func (m *MemStore) Track(_ context.Context, sc authz.Scope, id kernel.ID) (Track, error) {
	if !sc.Valid() {
		return Track{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tracks {
		if t.ID == id {
			if !sc.Allows(authz.ActionReadStrategic, t.ProductID) {
				return Track{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(cloneTrack(t)), nil
		}
	}
	return Track{}, kernel.NotFound("track", id)
}

// Tracks возвращает треки по фильтру в порядке сохранения.
func (m *MemStore) Tracks(_ context.Context, sc authz.Scope, f TrackFilter) ([]Track, error) {
	f = kernel.CloneValue(f)
	if f.ProductID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, f.ProductID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Track, 0, len(m.tracks))
	for _, t := range m.tracks {
		if !sc.Allows(authz.ActionReadStrategic, t.ProductID) {
			continue
		}
		if f.matches(t) {
			out = append(out, cloneTrack(t))
		}
	}
	return kernel.CloneValue(out), nil
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
func (m *MemStore) AppendImpact(_ context.Context, sc authz.Scope, a ImpactAssessment) error {
	a = kernel.CloneValue(a)
	if a.ProductID == kernel.NilID || !sc.Allows(authz.ActionReadStrategic, a.ProductID) || !sc.Allows(authz.ActionWriteCompliance, a.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.impacts = append(m.impacts, a)
	return nil
}

// ImpactHistory возвращает оценки фичи.
func (m *MemStore) ImpactHistory(_ context.Context, sc authz.Scope, featureID kernel.ID) ([]ImpactAssessment, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ImpactAssessment, 0)
	for _, a := range m.impacts {
		if !sc.Allows(authz.ActionReadStrategic, a.ProductID) {
			continue
		}
		if a.FeatureID == featureID {
			out = append(out, a)
		}
	}
	return kernel.CloneValue(out), nil
}

// SaveBaseline создаёт или обновляет baseline.
func (m *MemStore) SaveBaseline(_ context.Context, sc authz.Scope, b CertifiedBaseline) error {
	b = kernel.CloneValue(b)
	if b.ProductID == kernel.NilID || !sc.Allows(authz.ActionReadStrategic, b.ProductID) || !sc.Allows(authz.ActionWriteCompliance, b.ProductID) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.baselines {
		if m.baselines[i].ID == b.ID {
			if m.baselines[i].ProductID != b.ProductID {
				return kernel.ErrForbidden
			}
			m.baselines[i] = b
			return nil
		}
	}
	m.baselines = append(m.baselines, b)
	return nil
}

// Baseline возвращает baseline.
func (m *MemStore) Baseline(_ context.Context, sc authz.Scope, id kernel.ID) (CertifiedBaseline, error) {
	if !sc.Valid() {
		return CertifiedBaseline{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.baselines {
		if b.ID == id {
			if !sc.Allows(authz.ActionReadStrategic, b.ProductID) {
				return CertifiedBaseline{}, kernel.ErrForbidden
			}
			return kernel.CloneValue(b), nil
		}
	}
	return CertifiedBaseline{}, kernel.NotFound("certified_baseline", id)
}

// Baselines возвращает baseline продукта.
func (m *MemStore) Baselines(_ context.Context, sc authz.Scope, productID kernel.ID) ([]CertifiedBaseline, error) {
	if productID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, productID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CertifiedBaseline, 0, len(m.baselines))
	for _, b := range m.baselines {
		if !sc.Allows(authz.ActionReadStrategic, b.ProductID) {
			continue
		}
		if productID == kernel.NilID || b.ProductID == productID {
			out = append(out, b)
		}
	}
	return kernel.CloneValue(out), nil
}

// BaselinesWithComponent возвращает baseline, содержащие компонент с таким ключом (CM-08).
func (m *MemStore) BaselinesWithComponent(_ context.Context, sc authz.Scope, componentKey string) ([]CertifiedBaseline, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CertifiedBaseline, 0)
	for _, b := range m.baselines {
		if !sc.Allows(authz.ActionReadStrategic, b.ProductID) {
			continue
		}
		if b.HasComponent(Component{Key: componentKey}) {
			out = append(out, b)
		}
	}
	return kernel.CloneValue(out), nil
}
