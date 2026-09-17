package compliance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// GraphReader — нужная compliance часть публичного интерфейса portfoliograph (инвариант 1).
// Реализуется *portfoliograph.Service.
//
// TODO(question-23): Feature требует приватного чтения продукта; роли compliance для CM-06/CM-07
// нужен приватный доступ к продукту фичи.
type GraphReader interface {
	Links(ctx context.Context, sc authz.Scope) ([]portfoliograph.Link, error)
	Feature(ctx context.Context, sc authz.Scope, id kernel.ID) (portfoliograph.Feature, error)
	Product(ctx context.Context, sc authz.Scope, id kernel.ID) (portfoliograph.Product, error)
}

var _ GraphReader = (*portfoliograph.Service)(nil)

// Service — публичный интерфейс модуля compliance.
type Service struct {
	store    Store
	evidence EvidenceStore
	graph    GraphReader
	pub      kernel.Publisher
	clock    kernel.Clock

	mu       sync.RWMutex
	settings Settings
}

// NewService создаёт сервис с настройками по умолчанию.
func NewService(store Store, evidence EvidenceStore, graph GraphReader, pub kernel.Publisher, clock kernel.Clock) *Service {
	return &Service{store: store, evidence: evidence, graph: graph, pub: pub, clock: clock, settings: DefaultSettings()}
}

func (s *Service) emit(ctx context.Context, typ string, aggregate, product kernel.ID, actor string, payload any) error {
	ev, err := kernel.NewEvent(s.clock, typ, aggregate, product, actor, payload)
	if err != nil {
		return err
	}
	if err := s.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	return nil
}

// requireCatalog — право вести каталог наборов требований и шаблонов: администратор
// настроек или роль compliance.
func requireCatalog(sc authz.Scope) error {
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	// Сервисный Scope (seed, воркер) наравне с admin — как и в остальных действиях записи authz.
	if sc.Allows(authz.ActionAdminSettings, kernel.NilID) || sc.HasRole(authz.RoleCompliance) || sc.HasRole(authz.RoleService) {
		return nil
	}
	return kernel.ErrForbidden
}

// ---------- Настройки ----------

// Settings возвращает настройки модуля.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, nil
}

// UpdateSettings меняет настройки. Право: администрирование настроек.
func (s *Service) UpdateSettings(ctx context.Context, sc authz.Scope, st Settings) error {
	if err := sc.Require(authz.ActionAdminSettings, kernel.NilID); err != nil {
		return err
	}
	if err := st.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = st
	return nil
}

// ---------- CM-01: каталог наборов требований ----------

// RequirementSetInput — данные новой версии набора.
type RequirementSetInput struct {
	Code        string
	ProductType portfoliograph.ProductType
	Items       []RequirementItem
}

func (in RequirementSetInput) validate() error {
	if strings.TrimSpace(in.Code) == "" {
		return kernel.Invalid("code", "обязателен")
	}
	if in.ProductType == "" {
		return kernel.Invalid("product_type", "обязателен")
	}
	if len(in.Items) == 0 {
		return kernel.Invalid("items", "набор без требований")
	}
	seen := map[string]struct{}{}
	for _, it := range in.Items {
		if strings.TrimSpace(it.Key) == "" || strings.TrimSpace(it.Text) == "" {
			return kernel.Invalid("items", "ключ и текст требования обязательны")
		}
		if _, dup := seen[it.Key]; dup {
			return kernel.Invalid("items", "повтор ключа "+it.Key)
		}
		seen[it.Key] = struct{}{}
	}
	return nil
}

// CreateRequirementSet создаёт новую версию набора в статусе draft: версия = максимальная
// по коду + 1; существующие версии не изменяются (CM-01).
func (s *Service) CreateRequirementSet(ctx context.Context, sc authz.Scope, in RequirementSetInput) (RequirementSet, error) {
	if err := requireCatalog(sc); err != nil {
		return RequirementSet{}, err
	}
	if err := in.validate(); err != nil {
		return RequirementSet{}, err
	}
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	existing, err := s.store.RequirementSets(ctx, code)
	if err != nil {
		return RequirementSet{}, fmt.Errorf("list requirement sets: %w", err)
	}
	version := 1
	for _, rs := range existing {
		if rs.ProductType != in.ProductType {
			return RequirementSet{}, fmt.Errorf("%w: набор %s привязан к типу продукта %s", kernel.ErrConflict, code, rs.ProductType)
		}
		if rs.Version >= version {
			version = rs.Version + 1
		}
	}
	now := s.clock.Now()
	rs := RequirementSet{
		ID: kernel.NewID(), Code: code, Version: version, ProductType: in.ProductType,
		Items: in.Items, Status: RequirementSetDraft, CreatedBy: sc.Subject(), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.SaveRequirementSet(ctx, rs); err != nil {
		return RequirementSet{}, fmt.Errorf("save requirement set: %w", err)
	}
	return rs, nil
}

// SetRequirementSetStatus переводит набор: draft → published → retired. Назад — нельзя.
func (s *Service) SetRequirementSetStatus(ctx context.Context, sc authz.Scope, id kernel.ID, st RequirementSetStatus) (RequirementSet, error) {
	if err := requireCatalog(sc); err != nil {
		return RequirementSet{}, err
	}
	rs, err := s.store.RequirementSet(ctx, id)
	if err != nil {
		return RequirementSet{}, err
	}
	allowed := (rs.Status == RequirementSetDraft && st == RequirementSetPublished) ||
		(rs.Status == RequirementSetPublished && st == RequirementSetRetired)
	if !allowed {
		return RequirementSet{}, fmt.Errorf("%w: переход %s → %s недопустим", kernel.ErrConflict, rs.Status, st)
	}
	rs.Status, rs.UpdatedAt = st, s.clock.Now()
	if err := s.store.SaveRequirementSet(ctx, rs); err != nil {
		return RequirementSet{}, fmt.Errorf("save requirement set: %w", err)
	}
	return rs, nil
}

// RequirementSet возвращает набор. Право: любой аутентифицированный субъект.
func (s *Service) RequirementSet(ctx context.Context, sc authz.Scope, id kernel.ID) (RequirementSet, error) {
	if !sc.Valid() {
		return RequirementSet{}, kernel.ErrForbidden
	}
	return s.store.RequirementSet(ctx, id)
}

// RequirementSets возвращает версии набора по коду (пустой код — весь каталог).
func (s *Service) RequirementSets(ctx context.Context, sc authz.Scope, code string) ([]RequirementSet, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	out, err := s.store.RequirementSets(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if err != nil {
		return nil, fmt.Errorf("list requirement sets: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// publishedSet — последняя опубликованная версия набора по коду для типа продукта; NilID, если нет.
func (s *Service) publishedSet(ctx context.Context, code string, pt portfoliograph.ProductType) (kernel.ID, error) {
	if code == "" {
		return kernel.NilID, nil
	}
	sets, err := s.store.RequirementSets(ctx, code)
	if err != nil {
		return kernel.NilID, fmt.Errorf("list requirement sets: %w", err)
	}
	var best RequirementSet
	for _, rs := range sets {
		if rs.Status == RequirementSetPublished && rs.ProductType == pt && rs.Version > best.Version {
			best = rs
		}
	}
	return best.ID, nil
}

// ---------- CM-02: шаблоны треков ----------

func validateTemplate(t TrackTemplate) error {
	if t.ProductType == "" {
		return kernel.Invalid("product_type", "обязателен")
	}
	if strings.TrimSpace(t.Name) == "" {
		return kernel.Invalid("name", "обязателен")
	}
	if len(t.Gates) == 0 {
		return kernel.Invalid("gates", "шаблон без гейтов")
	}
	keys := map[string]struct{}{}
	orders := map[int]struct{}{}
	for _, g := range t.Gates {
		if strings.TrimSpace(g.Key) == "" || strings.TrimSpace(g.Name) == "" {
			return kernel.Invalid("gates", "ключ и название гейта обязательны")
		}
		if !ValidGateKind(g.Kind) {
			return kernel.Invalid("gates", fmt.Sprintf("неизвестная категория %q гейта %s", g.Kind, g.Key))
		}
		if g.Order <= 0 {
			return kernel.Invalid("gates", "порядок гейта "+g.Key+" должен быть положительным")
		}
		if _, dup := keys[g.Key]; dup {
			return kernel.Invalid("gates", "повтор ключа "+g.Key)
		}
		if _, dup := orders[g.Order]; dup {
			return kernel.Invalid("gates", fmt.Sprintf("повтор порядка %d", g.Order))
		}
		keys[g.Key], orders[g.Order] = struct{}{}, struct{}{}
	}
	return nil
}

// SaveTemplate создаёт (ID пуст) или обновляет шаблон (CM-02). Право: каталог.
func (s *Service) SaveTemplate(ctx context.Context, sc authz.Scope, t TrackTemplate) (TrackTemplate, error) {
	if err := requireCatalog(sc); err != nil {
		return TrackTemplate{}, err
	}
	if err := validateTemplate(t); err != nil {
		return TrackTemplate{}, err
	}
	now := s.clock.Now()
	if t.ID == kernel.NilID {
		t.ID, t.CreatedAt = kernel.NewID(), now
	} else {
		prev, err := s.store.Template(ctx, t.ID)
		if err != nil {
			return TrackTemplate{}, err
		}
		t.CreatedAt = prev.CreatedAt
	}
	t.UpdatedAt = now
	sort.SliceStable(t.Gates, func(i, j int) bool { return t.Gates[i].Order < t.Gates[j].Order })
	if err := s.store.SaveTemplate(ctx, t); err != nil {
		return TrackTemplate{}, fmt.Errorf("save template: %w", err)
	}
	return t, nil
}

// Template возвращает шаблон.
func (s *Service) Template(ctx context.Context, sc authz.Scope, id kernel.ID) (TrackTemplate, error) {
	if !sc.Valid() {
		return TrackTemplate{}, kernel.ErrForbidden
	}
	return s.store.Template(ctx, id)
}

// Templates возвращает шаблоны типа продукта (пустой тип — все).
func (s *Service) Templates(ctx context.Context, sc authz.Scope, pt portfoliograph.ProductType) ([]TrackTemplate, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	out, err := s.store.Templates(ctx, pt)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	return out, nil
}

// ---------- CM-03: треки и гейты ----------

// TrackInput — данные нового трека.
type TrackInput struct {
	ProductID kernel.ID
	ReleaseID kernel.ID
	Version   string
	// TemplateID — шаблон; пустой — первый шаблон типа продукта.
	TemplateID kernel.ID
}

// StartTrack запускает трек сертификации версии из шаблона по типу продукта (CM-03).
// На релиз допускается один трек.
func (s *Service) StartTrack(ctx context.Context, sc authz.Scope, in TrackInput) (Track, error) {
	if in.ProductID == kernel.NilID {
		return Track{}, kernel.Invalid("product_id", "обязателен")
	}
	if in.ReleaseID == kernel.NilID {
		return Track{}, kernel.Invalid("release_id", "обязателен")
	}
	if strings.TrimSpace(in.Version) == "" {
		return Track{}, kernel.Invalid("version", "обязательна")
	}
	if err := sc.Require(authz.ActionWriteCompliance, in.ProductID); err != nil {
		return Track{}, err
	}
	product, err := s.graph.Product(ctx, sc, in.ProductID)
	if err != nil {
		return Track{}, fmt.Errorf("product: %w", err)
	}
	existing, err := s.store.Tracks(ctx, TrackFilter{ReleaseID: in.ReleaseID})
	if err != nil {
		return Track{}, fmt.Errorf("list tracks: %w", err)
	}
	if len(existing) > 0 {
		return Track{}, fmt.Errorf("%w: у релиза %s уже есть трек", kernel.ErrConflict, in.ReleaseID)
	}
	var tpl TrackTemplate
	if in.TemplateID != kernel.NilID {
		tpl, err = s.store.Template(ctx, in.TemplateID)
		if err != nil {
			return Track{}, err
		}
		if tpl.ProductType != product.Type {
			return Track{}, kernel.Invalid("template_id", "шаблон другого типа продукта")
		}
	} else {
		tpls, err := s.store.Templates(ctx, product.Type)
		if err != nil {
			return Track{}, fmt.Errorf("list templates: %w", err)
		}
		if len(tpls) == 0 {
			return Track{}, fmt.Errorf("%w: шаблон трека для типа продукта %s", kernel.ErrNotFound, product.Type)
		}
		tpl = tpls[0]
	}
	now := s.clock.Now()
	t := Track{
		ID: kernel.NewID(), ProductID: in.ProductID, ReleaseID: in.ReleaseID, Version: strings.TrimSpace(in.Version),
		TemplateID: tpl.ID, Status: TrackActive, CreatedBy: sc.Subject(), CreatedAt: now, UpdatedAt: now,
	}
	for _, g := range tpl.Gates {
		gate := Gate{
			ID: kernel.NewID(), Key: g.Key, Name: g.Name, Kind: g.Kind, Order: g.Order,
			ParallelGroup: g.ParallelGroup, RequirementSetCode: g.RequirementSetCode, Status: GatePending,
			Checklist: make([]ChecklistItem, 0, len(g.Checklist)),
		}
		for _, key := range g.Checklist {
			gate.Checklist = append(gate.Checklist, ChecklistItem{Key: key, Text: key})
		}
		t.Gates = append(t.Gates, gate)
	}
	sort.SliceStable(t.Gates, func(i, j int) bool { return t.Gates[i].Order < t.Gates[j].Order })
	if err := s.store.SaveTrack(ctx, t); err != nil {
		return Track{}, fmt.Errorf("save track: %w", err)
	}
	if err := s.emit(ctx, EventTrackStarted, t.ID, t.ProductID, sc.Subject(), t); err != nil {
		return Track{}, err
	}
	return t, nil
}

// Track возвращает трек. Право: стратегический срез продукта (статус compliance — часть среза).
func (s *Service) Track(ctx context.Context, sc authz.Scope, id kernel.ID) (Track, error) {
	t, err := s.store.Track(ctx, id)
	if err != nil {
		return Track{}, err
	}
	if err := sc.Require(authz.ActionReadStrategic, t.ProductID); err != nil {
		return Track{}, err
	}
	return t, nil
}

// Tracks возвращает треки продукта.
func (s *Service) Tracks(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Track, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	out, err := s.store.Tracks(ctx, TrackFilter{ProductID: productID})
	if err != nil {
		return nil, fmt.Errorf("list tracks: %w", err)
	}
	return out, nil
}

// loadForWrite читает трек и проверяет право записи и активность.
func (s *Service) loadForWrite(ctx context.Context, sc authz.Scope, trackID kernel.ID) (Track, error) {
	t, err := s.store.Track(ctx, trackID)
	if err != nil {
		return Track{}, err
	}
	if err := sc.Require(authz.ActionWriteCompliance, t.ProductID); err != nil {
		return Track{}, err
	}
	if t.Status != TrackActive {
		return Track{}, fmt.Errorf("%w: трек в статусе %s", kernel.ErrConflict, t.Status)
	}
	return t, nil
}

func gateIndex(t Track, gateID kernel.ID) (int, error) {
	for i := range t.Gates {
		if t.Gates[i].ID == gateID {
			return i, nil
		}
	}
	return 0, kernel.NotFound("gate", gateID)
}

// GateUpdate — изменяемые атрибуты гейта; пустое поле не меняется.
type GateUpdate struct {
	Owner   string
	DueDate kernel.Date
	Cost    kernel.Money
}

// UpdateGate меняет владельца, срок и затраты гейта (CM-03).
func (s *Service) UpdateGate(ctx context.Context, sc authz.Scope, trackID, gateID kernel.ID, in GateUpdate) (Track, error) {
	t, err := s.loadForWrite(ctx, sc, trackID)
	if err != nil {
		return Track{}, err
	}
	i, err := gateIndex(t, gateID)
	if err != nil {
		return Track{}, err
	}
	if in.Cost.Amount < 0 {
		return Track{}, kernel.Invalid("cost", "отрицательная сумма")
	}
	g := &t.Gates[i]
	if in.Owner != "" {
		g.Owner = in.Owner
	}
	if !in.DueDate.IsZero() {
		g.DueDate = in.DueDate
	}
	if !in.Cost.IsZero() {
		g.Cost = in.Cost
	}
	t.UpdatedAt = s.clock.Now()
	if err := s.store.SaveTrack(ctx, t); err != nil {
		return Track{}, fmt.Errorf("save track: %w", err)
	}
	return t, nil
}

// CheckItem закрывает пункт чек-листа гейта доказательством из журнала (CM-03, CM-04).
// Доказательство должно относиться к этому гейту и не быть отклонённым.
func (s *Service) CheckItem(ctx context.Context, sc authz.Scope, trackID, gateID kernel.ID, key string, evidenceID kernel.ID) (Track, error) {
	t, err := s.loadForWrite(ctx, sc, trackID)
	if err != nil {
		return Track{}, err
	}
	i, err := gateIndex(t, gateID)
	if err != nil {
		return Track{}, err
	}
	g := &t.Gates[i]
	if g.Status == GatePassed || g.Status == GateFailed {
		return Track{}, fmt.Errorf("%w: гейт %s в статусе %s", kernel.ErrConflict, g.Key, g.Status)
	}
	if evidenceID == kernel.NilID {
		return Track{}, kernel.Invalid("evidence_id", "обязателен")
	}
	ev, err := s.latestEvidence(ctx, evidenceID)
	if err != nil {
		return Track{}, err
	}
	if ev.TrackID != trackID || ev.GateID != gateID {
		return Track{}, kernel.Invalid("evidence_id", "доказательство другого гейта")
	}
	if ev.Status == EvidenceRejected {
		return Track{}, fmt.Errorf("%w: доказательство отклонено", kernel.ErrConflict)
	}
	found := false
	for j := range g.Checklist {
		if g.Checklist[j].Key == key {
			g.Checklist[j].Done, g.Checklist[j].EvidenceID = true, evidenceID
			found = true
			break
		}
	}
	if !found {
		return Track{}, kernel.Invalid("key", fmt.Sprintf("пункт %q не найден в чек-листе гейта %s", key, g.Key))
	}
	if g.Status == GatePending {
		g.Status = GateInProgress
	}
	t.UpdatedAt = s.clock.Now()
	if err := s.store.SaveTrack(ctx, t); err != nil {
		return Track{}, fmt.Errorf("save track: %w", err)
	}
	return t, nil
}

// blockers возвращает ключи непройденных гейтов, которые должны предшествовать гейту g:
// гейты с меньшим Order, кроме гейтов других параллельных веток.
func blockers(t Track, g Gate) []string {
	var out []string
	for _, o := range t.Gates {
		if o.ID == g.ID || o.Order >= g.Order || o.Status == GatePassed {
			continue
		}
		if g.ParallelGroup != "" && o.ParallelGroup != "" && o.ParallelGroup != g.ParallelGroup {
			continue
		}
		out = append(out, o.Key)
	}
	return out
}

// PassGate проходит гейт: все пункты чек-листа закрыты и предшествующие гейты пройдены (CM-03).
// Прохождение гейта «сертификат» переводит трек в certified и создаёт CertifiedBaseline (CM-07).
func (s *Service) PassGate(ctx context.Context, sc authz.Scope, trackID, gateID kernel.ID) (Track, error) {
	t, err := s.loadForWrite(ctx, sc, trackID)
	if err != nil {
		return Track{}, err
	}
	i, err := gateIndex(t, gateID)
	if err != nil {
		return Track{}, err
	}
	g := &t.Gates[i]
	if g.Status == GatePassed {
		return Track{}, fmt.Errorf("%w: гейт %s уже пройден", kernel.ErrConflict, g.Key)
	}
	if g.Status == GateFailed {
		return Track{}, fmt.Errorf("%w: гейт %s провален", kernel.ErrConflict, g.Key)
	}
	if !g.ChecklistDone() {
		return Track{}, fmt.Errorf("%w: чек-лист гейта %s не закрыт", kernel.ErrConflict, g.Key)
	}
	if b := blockers(t, *g); len(b) > 0 {
		return Track{}, fmt.Errorf("%w: не пройдены предшествующие гейты %s", kernel.ErrConflict, strings.Join(b, ", "))
	}
	now := s.clock.Now()
	g.Status, g.PassedAt = GatePassed, now
	t.UpdatedAt = now
	var baseline CertifiedBaseline
	certified := g.Key == GateKeyCertificate
	if certified {
		baseline, err = s.newBaseline(ctx, sc, t, *g)
		if err != nil {
			return Track{}, err
		}
		t.Status, t.BaselineID = TrackCertified, baseline.ID
	}
	if err := s.store.SaveTrack(ctx, t); err != nil {
		return Track{}, fmt.Errorf("save track: %w", err)
	}
	if err := s.emit(ctx, EventGatePassed, t.ID, t.ProductID, sc.Subject(), *g); err != nil {
		return Track{}, err
	}
	if certified {
		if err := s.store.SaveBaseline(ctx, baseline); err != nil {
			return Track{}, fmt.Errorf("save baseline: %w", err)
		}
		if err := s.emit(ctx, EventBaselineCreated, baseline.ID, baseline.ProductID, sc.Subject(), baseline); err != nil {
			return Track{}, err
		}
	}
	return t, nil
}

func (s *Service) newBaseline(ctx context.Context, sc authz.Scope, t Track, g Gate) (CertifiedBaseline, error) {
	product, err := s.graph.Product(ctx, sc, t.ProductID)
	if err != nil {
		return CertifiedBaseline{}, fmt.Errorf("product: %w", err)
	}
	setID, err := s.publishedSet(ctx, g.RequirementSetCode, product.Type)
	if err != nil {
		return CertifiedBaseline{}, err
	}
	s.mu.RLock()
	years := s.settings.BaselineLifetimeYears
	s.mu.RUnlock()
	now := s.clock.Now()
	today := kernel.DateFromTime(now)
	id := kernel.NewID()
	return CertifiedBaseline{
		ID: id, ProductID: t.ProductID, TrackID: t.ID, Version: t.Version, RequirementSetID: setID,
		CertificateNo: "SYN-" + strings.ToUpper(product.Key) + "-" + id.String()[:8],
		CertifiedAt:   today,
		EOL:           kernel.DateFromTime(today.Time().AddDate(years, 0, 0)),
		CreatedAt:     now,
	}, nil
}

// FailGate проваливает гейт; трек переходит в failed (CM-03).
func (s *Service) FailGate(ctx context.Context, sc authz.Scope, trackID, gateID kernel.ID, reason string) (Track, error) {
	t, err := s.loadForWrite(ctx, sc, trackID)
	if err != nil {
		return Track{}, err
	}
	i, err := gateIndex(t, gateID)
	if err != nil {
		return Track{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return Track{}, kernel.Invalid("reason", "обязательна")
	}
	g := &t.Gates[i]
	if g.Status == GatePassed {
		return Track{}, fmt.Errorf("%w: гейт %s уже пройден", kernel.ErrConflict, g.Key)
	}
	now := s.clock.Now()
	g.Status = GateFailed
	t.Status, t.UpdatedAt = TrackFailed, now
	if err := s.store.SaveTrack(ctx, t); err != nil {
		return Track{}, fmt.Errorf("save track: %w", err)
	}
	payload := struct {
		Gate   Gate   `json:"gate"`
		Reason string `json:"reason"`
	}{*g, strings.TrimSpace(reason)}
	if err := s.emit(ctx, EventGateFailed, t.ID, t.ProductID, sc.Subject(), payload); err != nil {
		return Track{}, err
	}
	return t, nil
}

// ---------- CM-04: журнал доказательств ----------

// EvidenceInput — новое доказательство.
type EvidenceInput struct {
	TrackID kernel.ID
	GateID  kernel.ID
	URL     string
	SHA256  string
	Comment string
}

// AppendEvidence добавляет доказательство в журнал в статусе submitted (CM-04).
func (s *Service) AppendEvidence(ctx context.Context, sc authz.Scope, in EvidenceInput) (EvidenceItem, error) {
	if strings.TrimSpace(in.URL) == "" {
		return EvidenceItem{}, kernel.Invalid("url", "обязательна")
	}
	sha := strings.ToLower(strings.TrimSpace(in.SHA256))
	if !ValidSHA256(sha) {
		return EvidenceItem{}, kernel.Invalid("sha256", "ожидается hex SHA-256 из 64 символов")
	}
	t, err := s.loadForWrite(ctx, sc, in.TrackID)
	if err != nil {
		return EvidenceItem{}, err
	}
	if _, err := gateIndex(t, in.GateID); err != nil {
		return EvidenceItem{}, err
	}
	e := EvidenceItem{
		ID: kernel.NewID(), ProductID: t.ProductID, TrackID: t.ID, GateID: in.GateID,
		URL: strings.TrimSpace(in.URL), SHA256: sha, Status: EvidenceSubmitted, Comment: strings.TrimSpace(in.Comment),
		Actor: sc.Subject(), At: s.clock.Now().UTC(),
	}
	e, err = appendEvidence(ctx, s.evidence, e)
	if err != nil {
		return EvidenceItem{}, err
	}
	if err := s.emit(ctx, EventEvidenceAppended, e.ID, e.ProductID, sc.Subject(), e); err != nil {
		return EvidenceItem{}, err
	}
	return e, nil
}

// SetEvidenceStatus меняет статус доказательства новой записью журнала (Supersedes = Seq прежней).
func (s *Service) SetEvidenceStatus(ctx context.Context, sc authz.Scope, evidenceID kernel.ID, st EvidenceStatus, comment string) (EvidenceItem, error) {
	if !ValidEvidenceStatus(st) {
		return EvidenceItem{}, kernel.Invalid("status", fmt.Sprintf("неизвестный статус %q", st))
	}
	prev, err := s.latestEvidence(ctx, evidenceID)
	if err != nil {
		return EvidenceItem{}, err
	}
	if err := sc.Require(authz.ActionWriteCompliance, prev.ProductID); err != nil {
		return EvidenceItem{}, err
	}
	if prev.Status == st {
		return EvidenceItem{}, fmt.Errorf("%w: доказательство уже в статусе %s", kernel.ErrConflict, st)
	}
	e := prev
	e.Status, e.Comment, e.Supersedes = st, strings.TrimSpace(comment), prev.Seq
	e.Actor, e.At = sc.Subject(), s.clock.Now().UTC()
	e.PrevHash, e.Hash = "", ""
	e, err = appendEvidence(ctx, s.evidence, e)
	if err != nil {
		return EvidenceItem{}, err
	}
	if err := s.emit(ctx, EventEvidenceAppended, e.ID, e.ProductID, sc.Subject(), e); err != nil {
		return EvidenceItem{}, err
	}
	return e, nil
}

// latestEvidence — актуальная запись доказательства (последняя по Seq с этим ID).
func (s *Service) latestEvidence(ctx context.Context, id kernel.ID) (EvidenceItem, error) {
	var found EvidenceItem
	ok := false
	err := s.evidence.Walk(ctx, func(e EvidenceItem) error {
		if e.ID == id {
			found, ok = e, true
		}
		return nil
	})
	if err != nil {
		return EvidenceItem{}, fmt.Errorf("evidence walk: %w", err)
	}
	if !ok {
		return EvidenceItem{}, kernel.NotFound("evidence", id)
	}
	return found, nil
}

// Evidence возвращает актуальные записи доказательств трека по возрастанию Seq.
func (s *Service) Evidence(ctx context.Context, sc authz.Scope, trackID kernel.ID) ([]EvidenceItem, error) {
	t, err := s.store.Track(ctx, trackID)
	if err != nil {
		return nil, err
	}
	if err := sc.Require(authz.ActionReadStrategic, t.ProductID); err != nil {
		return nil, err
	}
	latest := map[kernel.ID]EvidenceItem{}
	err = s.evidence.Walk(ctx, func(e EvidenceItem) error {
		if e.TrackID == trackID {
			latest[e.ID] = e
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("evidence walk: %w", err)
	}
	out := make([]EvidenceItem, 0, len(latest))
	for _, e := range latest {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// VerifyEvidence проверяет целостность журнала доказательств. Право: чтение аудита.
func (s *Service) VerifyEvidence(ctx context.Context, sc authz.Scope) (VerifyResult, error) {
	if err := sc.Require(authz.ActionReadAudit, kernel.NilID); err != nil {
		return VerifyResult{}, err
	}
	return VerifyEvidenceLog(ctx, s.evidence)
}

// ---------- CM-05: готовность релиза ----------

// ReleaseReadiness сообщает, готов ли релиз к сертификации: у трека релиза все гейты категории
// ssdlc пройдены с закрытыми чек-листами (CM-05). Порт для roadmap.
func (s *Service) ReleaseReadiness(ctx context.Context, sc authz.Scope, releaseID kernel.ID) (Readiness, error) {
	if !sc.Valid() {
		return Readiness{}, kernel.ErrForbidden
	}
	tracks, err := s.store.Tracks(ctx, TrackFilter{ReleaseID: releaseID})
	if err != nil {
		return Readiness{}, fmt.Errorf("list tracks: %w", err)
	}
	if len(tracks) == 0 {
		return Readiness{Ready: false, OpenItems: []string{"трек сертификации не запущен"}}, nil
	}
	t := tracks[0]
	if err := sc.Require(authz.ActionReadStrategic, t.ProductID); err != nil {
		return Readiness{}, err
	}
	res := Readiness{Ready: true, OpenItems: []string{}}
	if t.Status == TrackFailed {
		res.Ready = false
		res.OpenItems = append(res.OpenItems, "трек провален")
	}
	hasSSDLC := false
	for _, g := range t.Gates {
		if g.Kind != GateKindSSDLC {
			continue
		}
		hasSSDLC = true
		for _, it := range g.Checklist {
			if !it.Done {
				res.Ready = false
				res.OpenItems = append(res.OpenItems, g.Key+"/"+it.Key)
			}
		}
		if g.Status != GatePassed {
			res.Ready = false
			res.OpenItems = append(res.OpenItems, g.Key+": гейт не пройден")
		}
	}
	if !hasSSDLC {
		res.Ready = false
		res.OpenItems = append(res.OpenItems, "в треке нет гейтов SSDLC")
	}
	return res, nil
}

// ---------- CM-06: класс влияния ----------

// SetImpactClass фиксирует класс влияния фичи с обоснованием; автор — субъект (CM-06).
// История оценок хранится целиком; действует последняя.
func (s *Service) SetImpactClass(ctx context.Context, sc authz.Scope, featureID, productID kernel.ID, class ImpactClass, justification string) (ImpactAssessment, error) {
	if !ValidImpactClass(class) {
		return ImpactAssessment{}, kernel.Invalid("class", fmt.Sprintf("неизвестный класс %q", class))
	}
	if strings.TrimSpace(justification) == "" {
		return ImpactAssessment{}, kernel.Invalid("justification", "обосновение обязательно")
	}
	if err := sc.Require(authz.ActionWriteCompliance, productID); err != nil {
		return ImpactAssessment{}, err
	}
	f, err := s.graph.Feature(ctx, sc, featureID)
	if err != nil {
		return ImpactAssessment{}, fmt.Errorf("feature: %w", err)
	}
	if f.ProductID != productID {
		return ImpactAssessment{}, kernel.Invalid("product_id", "фича другого продукта")
	}
	a := ImpactAssessment{
		ID: kernel.NewID(), FeatureID: featureID, ProductID: productID, Class: class,
		Justification: strings.TrimSpace(justification), Author: sc.Subject(), At: s.clock.Now(),
	}
	if err := s.store.AppendImpact(ctx, a); err != nil {
		return ImpactAssessment{}, fmt.Errorf("append impact: %w", err)
	}
	if err := s.emit(ctx, EventImpactSet, featureID, productID, sc.Subject(), a); err != nil {
		return ImpactAssessment{}, err
	}
	return a, nil
}

// ImpactClass возвращает действующую оценку фичи; kernel.ErrNotFound, если оценок нет.
func (s *Service) ImpactClass(ctx context.Context, sc authz.Scope, featureID kernel.ID) (ImpactAssessment, error) {
	hist, err := s.store.ImpactHistory(ctx, featureID)
	if err != nil {
		return ImpactAssessment{}, fmt.Errorf("impact history: %w", err)
	}
	if len(hist) == 0 {
		return ImpactAssessment{}, kernel.NotFound("impact_assessment", featureID)
	}
	last := hist[len(hist)-1]
	if err := sc.Require(authz.ActionReadStrategic, last.ProductID); err != nil {
		return ImpactAssessment{}, err
	}
	return last, nil
}

// ImpactHistory возвращает все оценки фичи в порядке добавления.
func (s *Service) ImpactHistory(ctx context.Context, sc authz.Scope, featureID kernel.ID) ([]ImpactAssessment, error) {
	hist, err := s.store.ImpactHistory(ctx, featureID)
	if err != nil {
		return nil, fmt.Errorf("impact history: %w", err)
	}
	if len(hist) == 0 {
		return []ImpactAssessment{}, nil
	}
	if err := sc.Require(authz.ActionReadStrategic, hist[0].ProductID); err != nil {
		return nil, err
	}
	return hist, nil
}

// ConfirmationCost — стоимость подтверждения изменения для фичи по её классу влияния (PR-05):
// CostByClass[class], со скидкой CertifiedProcessDiscount, если процессы РБПО продукта
// сертифицированы. Без оценки класс считается none. Порт для prioritization.
func (s *Service) ConfirmationCost(ctx context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error) {
	f, err := s.graph.Feature(ctx, sc, featureID)
	if err != nil {
		return kernel.Money{}, fmt.Errorf("feature: %w", err)
	}
	if err := sc.Require(authz.ActionReadStrategic, f.ProductID); err != nil {
		return kernel.Money{}, err
	}
	class := ImpactNone
	a, err := s.ImpactClass(ctx, sc, featureID)
	switch {
	case err == nil:
		class = a.Class
	case errors.Is(err, kernel.ErrNotFound):
	default:
		return kernel.Money{}, err
	}
	product, err := s.graph.Product(ctx, sc, f.ProductID)
	if err != nil {
		return kernel.Money{}, fmt.Errorf("product: %w", err)
	}
	s.mu.RLock()
	st := s.settings
	s.mu.RUnlock()
	cost := st.CostByClass[class]
	if product.SSDLCCertified {
		cost = cost.MulCoef(decimal.NewFromInt(1).Sub(st.CertifiedProcessDiscount))
	}
	return cost, nil
}

// ---------- CM-07: сертифицированные конфигурации ----------

// Baselines возвращает baseline продукта.
func (s *Service) Baselines(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]CertifiedBaseline, error) {
	if err := sc.Require(authz.ActionReadStrategic, productID); err != nil {
		return nil, err
	}
	out, err := s.store.Baselines(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list baselines: %w", err)
	}
	return out, nil
}

// AffectedBaselines — baseline, затронутые изменением фичи (CM-07): собственного продукта и
// продуктов, достижимых по связям «вхождение в поставку» (компонент → дистрибутив) и
// «общий компонент» (в обе стороны), транзитивно. Path — продукты от продукта фичи до продукта
// baseline. Procedure — упрощённое подтверждение при SSDLCCertified продукта baseline.
func (s *Service) AffectedBaselines(ctx context.Context, sc authz.Scope, featureID kernel.ID) ([]AffectedBaseline, error) {
	f, err := s.graph.Feature(ctx, sc, featureID)
	if err != nil {
		return nil, fmt.Errorf("feature: %w", err)
	}
	if err := sc.Require(authz.ActionReadStrategic, f.ProductID); err != nil {
		return nil, err
	}
	links, err := s.graph.Links(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("links: %w", err)
	}
	// adj: продукт → продукты, чьи baseline затрагивает его изменение.
	adj := map[kernel.ID][]kernel.ID{}
	for _, l := range links {
		switch l.Type {
		case portfoliograph.LinkBundled:
			// From (дистрибутив) зависит от To (компонент): изменение компонента затрагивает дистрибутив.
			adj[l.ToProductID] = append(adj[l.ToProductID], l.FromProductID)
		case portfoliograph.LinkSharedComponent:
			adj[l.FromProductID] = append(adj[l.FromProductID], l.ToProductID)
			adj[l.ToProductID] = append(adj[l.ToProductID], l.FromProductID)
		}
	}
	paths := map[kernel.ID][]kernel.ID{f.ProductID: {f.ProductID}}
	order := []kernel.ID{f.ProductID}
	for head := 0; head < len(order); head++ {
		cur := order[head]
		for _, next := range adj[cur] {
			if _, seen := paths[next]; seen {
				continue
			}
			paths[next] = append(append([]kernel.ID(nil), paths[cur]...), next)
			order = append(order, next)
		}
	}
	out := []AffectedBaseline{}
	for _, pid := range order {
		if !sc.Allows(authz.ActionReadStrategic, pid) {
			continue
		}
		bls, err := s.store.Baselines(ctx, pid)
		if err != nil {
			return nil, fmt.Errorf("list baselines: %w", err)
		}
		if len(bls) == 0 {
			continue
		}
		product, err := s.graph.Product(ctx, sc, pid)
		if err != nil {
			return nil, fmt.Errorf("product: %w", err)
		}
		proc := ProcedureFull
		if product.SSDLCCertified {
			proc = ProcedureSimplified
		}
		for _, b := range bls {
			out = append(out, AffectedBaseline{Baseline: b, Path: paths[pid], Procedure: proc})
		}
	}
	return out, nil
}
