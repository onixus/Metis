// Package analytics — конструктор дашбордов платформы (ТЗ 3.10, DA-05).
//
// По ADR-0007 внешний BI не встраивается: дашборд — это сохранённый набор панелей,
// каждая из которых ссылается на известный срез API платформы. Данные отдают те же
// сервисы и тот же authz.Scope, поэтому второй контур доступа к финансам не возникает.
package analytics

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Source — срез данных платформы, на который ссылается панель.
type Source string

// Известные срезы. Список закрыт: панель не может обратиться к произвольному адресу.
const (
	SourcePortfolioPnL   Source = "economics.pnl.portfolio"
	SourceProductPnL     Source = "economics.pnl.product"
	SourceTeamMatrix     Source = "economics.matrix"
	SourceMetric         Source = "economics.metric"
	SourceImportBatches  Source = "economics.batches"
	SourceWinLoss        Source = "marketing.winloss"
	SourceTracks         Source = "compliance.tracks"
	SourceCertifiedVers  Source = "compliance.versions"
	SourceCommitments    Source = "commitments.list"
	SourceRoadmapLaunch  Source = "roadmap.launch_calendar"
	SourceDeliveryMetric Source = "delivery.feature_metrics"
	SourceDecisions      Source = "decisions.list"
)

var knownSources = map[Source]bool{
	SourcePortfolioPnL: true, SourceProductPnL: true, SourceTeamMatrix: true, SourceMetric: true,
	SourceImportBatches: true, SourceWinLoss: true, SourceTracks: true, SourceCertifiedVers: true,
	SourceCommitments: true, SourceRoadmapLaunch: true, SourceDeliveryMetric: true, SourceDecisions: true,
}

// Sources возвращает список срезов, доступных конструктору.
func Sources() []Source {
	out := make([]Source, 0, len(knownSources))
	for s := range knownSources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Kind — вид панели.
type Kind string

// Виды панелей.
const (
	KindTable Kind = "table"
	KindBar   Kind = "bar"
	KindLine  Kind = "line"
	KindStat  Kind = "stat"
)

func (k Kind) valid() bool {
	switch k {
	case KindTable, KindBar, KindLine, KindStat:
		return true
	}
	return false
}

// Panel — панель дашборда: срез, вид и параметры отбора.
type Panel struct {
	Key    string            `json:"key"`
	Title  string            `json:"title"`
	Source Source            `json:"source"`
	Kind   Kind              `json:"kind"`
	Params map[string]string `json:"params,omitempty"`
	Width  int               `json:"width,omitempty"` // 1…12 колонок сетки
}

// Dashboard — сохранённое определение дашборда (DA-05).
type Dashboard struct {
	ID kernel.ID `json:"id"`
	// ProductID — дашборд продукта; нулевое значение — портфельный дашборд.
	ProductID kernel.ID `json:"product_id,omitempty"`
	Name      string    `json:"name"`
	Panels    []Panel   `json:"panels"`
	Owner     string    `json:"owner"`
	// Shared — дашборд виден всем, кому доступен продукт; иначе только автору.
	Shared    bool      `json:"shared"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store — хранилище дашбордов.
type Store interface {
	Save(ctx context.Context, sc authz.Scope, d Dashboard) error
	Get(ctx context.Context, sc authz.Scope, id kernel.ID) (Dashboard, error)
	GetForWrite(ctx context.Context, sc authz.Scope, id kernel.ID) (Dashboard, error)
	List(ctx context.Context, sc authz.Scope) ([]Dashboard, error)
	Delete(ctx context.Context, sc authz.Scope, id kernel.ID) error
}

// MemStore — хранилище в памяти.
type MemStore struct {
	mu    sync.RWMutex
	items map[kernel.ID]Dashboard
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore { return &MemStore{items: map[kernel.ID]Dashboard{}} }

var _ Store = (*MemStore)(nil)

// Save сохраняет дашборд.
func (m *MemStore) Save(_ context.Context, sc authz.Scope, d Dashboard) error {
	d = kernel.CloneValue(d)
	if !dashboardWritable(sc, d) {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.items[d.ID]; ok {
		if !dashboardWritable(sc, old) || old.ProductID != d.ProductID || old.Owner != d.Owner {
			return kernel.ErrForbidden
		}
	}
	m.items[d.ID] = cloneDashboard(d)
	return nil
}

// Get возвращает дашборд.
func (m *MemStore) Get(_ context.Context, sc authz.Scope, id kernel.ID) (Dashboard, error) {
	return m.get(sc, id, dashboardVisible)
}

// GetForWrite returns detached metadata only to an authorized editor. It does
// not change ordinary Get/List visibility or reserve a write lock.
func (m *MemStore) GetForWrite(_ context.Context, sc authz.Scope, id kernel.ID) (Dashboard, error) {
	return m.get(sc, id, dashboardWritable)
}

func (m *MemStore) get(sc authz.Scope, id kernel.ID, allowed func(authz.Scope, Dashboard) bool) (Dashboard, error) {
	if !sc.Valid() {
		return Dashboard{}, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.items[id]
	if !ok {
		return Dashboard{}, kernel.NotFound("dashboard", id)
	}
	if !allowed(sc, d) {
		return Dashboard{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(cloneDashboard(d)), nil
}

// List возвращает дашборды в порядке названия.
func (m *MemStore) List(_ context.Context, sc authz.Scope) ([]Dashboard, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Dashboard, 0, len(m.items))
	for _, d := range m.items {
		if dashboardVisible(sc, d) {
			out = append(out, cloneDashboard(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return kernel.CloneValue(out), nil
}

// Delete удаляет дашборд.
func (m *MemStore) Delete(_ context.Context, sc authz.Scope, id kernel.ID) error {
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[id]; !ok {
		return kernel.NotFound("dashboard", id)
	}
	if !dashboardWritable(sc, m.items[id]) {
		return kernel.ErrForbidden
	}
	delete(m.items, id)
	return nil
}

// Service — публичный интерфейс конструктора дашбордов.
type Service struct {
	store Store
	clock kernel.Clock
}

// NewService создаёт сервис.
func NewService(store Store, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{store: store, clock: clock}
}

// Input — определение дашборда.
type Input struct {
	ID        kernel.ID
	ProductID kernel.ID
	Name      string
	Panels    []Panel
	Shared    bool
}

// Save создаёт или изменяет дашборд (DA-05).
func (s *Service) Save(ctx context.Context, sc authz.Scope, in Input) (Dashboard, error) {
	if err := sc.Require(authz.ActionWriteDashboard, in.ProductID); err != nil {
		return Dashboard{}, err
	}
	if in.ProductID == kernel.NilID && !sc.SeesAllProducts() {
		return Dashboard{}, fmt.Errorf("%w: портфельный дашборд собирает тот, кто видит все продукты", kernel.ErrForbidden)
	}
	if strings.TrimSpace(in.Name) == "" {
		return Dashboard{}, kernel.Invalid("name", "название обязательно")
	}
	if len(in.Panels) == 0 {
		return Dashboard{}, kernel.Invalid("panels", "нужна хотя бы одна панель")
	}
	keys := map[string]bool{}
	for i, p := range in.Panels {
		if strings.TrimSpace(p.Key) == "" {
			return Dashboard{}, kernel.Invalid("panels", "у панели нет ключа")
		}
		if keys[p.Key] {
			return Dashboard{}, kernel.Invalid("panels", fmt.Sprintf("ключ панели %q повторяется", p.Key))
		}
		keys[p.Key] = true
		if !knownSources[p.Source] {
			return Dashboard{}, kernel.Invalid("source", fmt.Sprintf("неизвестный срез %q", p.Source))
		}
		if !p.Kind.valid() {
			return Dashboard{}, kernel.Invalid("kind", "допустимы table, bar, line, stat")
		}
		if p.Width < 0 || p.Width > 12 {
			return Dashboard{}, kernel.Invalid("width", "ширина панели — от 1 до 12 колонок")
		}
		if p.Width == 0 {
			in.Panels[i].Width = 6
		}
	}
	now := s.clock.Now()
	d := Dashboard{ID: in.ID, ProductID: in.ProductID, Name: in.Name, Panels: in.Panels,
		Owner: sc.Subject(), Shared: in.Shared, UpdatedAt: now}
	if d.ID == kernel.NilID {
		d.ID, d.CreatedAt = kernel.NewID(), now
	} else {
		prev, err := s.store.GetForWrite(ctx, sc, d.ID)
		if err != nil {
			return Dashboard{}, err
		}
		if prev.Owner != sc.Subject() && !sc.HasRole(authz.RoleAdmin) {
			return Dashboard{}, fmt.Errorf("%w: чужой дашборд меняет только автор или администратор", kernel.ErrForbidden)
		}
		d.CreatedAt, d.Owner = prev.CreatedAt, prev.Owner
	}
	if err := s.store.Save(ctx, sc, d); err != nil {
		return Dashboard{}, fmt.Errorf("save dashboard: %w", err)
	}
	return d, nil
}

// List возвращает дашборды, доступные субъекту: общие по доступным продуктам и собственные.
func (s *Service) List(ctx context.Context, sc authz.Scope) ([]Dashboard, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	all, err := s.store.List(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	out := make([]Dashboard, 0, len(all))
	for _, d := range all {
		if !dashboardVisible(sc, d) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

// Get возвращает дашборд, если он доступен субъекту.
func (s *Service) Get(ctx context.Context, sc authz.Scope, id kernel.ID) (Dashboard, error) {
	d, err := s.store.Get(ctx, sc, id)
	if err != nil {
		return Dashboard{}, err
	}
	if !dashboardVisible(sc, d) {
		return Dashboard{}, kernel.ErrForbidden
	}
	return d, nil
}

// Delete удаляет дашборд: автор или администратор.
func (s *Service) Delete(ctx context.Context, sc authz.Scope, id kernel.ID) error {
	if err := s.store.Delete(ctx, sc, id); err != nil {
		return fmt.Errorf("delete dashboard: %w", err)
	}
	return nil
}

func dashboardVisible(sc authz.Scope, d Dashboard) bool {
	if !sc.Valid() {
		return false
	}
	if d.ProductID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, d.ProductID) {
		return false
	}
	if d.ProductID == kernel.NilID && !sc.SeesAllProducts() {
		return false
	}
	if d.Owner == sc.Subject() {
		return true
	}
	if !d.Shared {
		return false
	}
	if d.ProductID == kernel.NilID {
		return sc.SeesAllProducts()
	}
	return sc.Allows(authz.ActionReadStrategic, d.ProductID)
}

func dashboardWritable(sc authz.Scope, d Dashboard) bool {
	if !sc.Allows(authz.ActionWriteDashboard, d.ProductID) {
		return false
	}
	if d.ProductID == kernel.NilID {
		if !sc.SeesAllProducts() {
			return false
		}
	} else if !sc.Allows(authz.ActionReadStrategic, d.ProductID) {
		return false
	}
	return d.Owner == sc.Subject() || sc.HasRole(authz.RoleAdmin)
}

func cloneDashboard(d Dashboard) Dashboard {
	d.Panels = append([]Panel(nil), d.Panels...)
	for i := range d.Panels {
		d.Panels[i].Params = maps.Clone(d.Panels[i].Params)
	}
	return d
}
