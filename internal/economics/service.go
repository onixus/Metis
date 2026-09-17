package economics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// Config — ключи полей, из которых собираются стандартные отчёты. Ключи настраиваются,
// потому что состав статей у заказчика свой (TODO(question-30)).
type Config struct {
	RevenueField          string
	BundleRevenueField    string
	CertifiedRevenueField string
	FeatureRevenueField   string
	PayrollField          string
	DirectCostField       string
	MarketingField        string
	FeatureCostField      string
	BranchCostField       string
	TrackCostField        string
	BudgetField           string
	Currency              string
}

// DefaultConfig — ключи полей по умолчанию.
func DefaultConfig() Config {
	return Config{
		RevenueField:          "revenue",
		BundleRevenueField:    "bundle_revenue",
		CertifiedRevenueField: "certified_revenue",
		FeatureRevenueField:   "feature_revenue",
		PayrollField:          "payroll",
		DirectCostField:       "direct_costs",
		MarketingField:        "marketing",
		FeatureCostField:      "feature_cost",
		BranchCostField:       "branch_cost",
		TrackCostField:        "track_cost",
		BudgetField:           "budget",
		Currency:              "RUB",
	}
}

// Auditor — порт журнала аудита: просмотр и экспорт финансовых данных, изменение правил (AD-04).
type Auditor interface {
	FinanceAccess(ctx context.Context, actor, action, object string, productID kernel.ID, details map[string]any) error
}

// TrackCosts — порт модуля compliance: затраты трека сертификации продукта (EC-06).
type TrackCosts interface {
	TrackCost(ctx context.Context, sc authz.Scope, productID kernel.ID) (kernel.Money, error)
}

// Service — публичный интерфейс модуля экономики.
type Service struct {
	store  Store
	engine *formula.Engine
	pub    kernel.Publisher
	clock  kernel.Clock
	cfg    Config

	audit       Auditor
	tracks      TrackCosts
	commitments CommitmentsPort
	tracksPort  TracksPort
	reader      ports.FinanceImport
	products    ProductDirectory

	mu       sync.Mutex
	compiled map[string]*formula.Program
}

// NewService создаёт сервис экономики.
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock, cfg Config) (*Service, error) {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	eng, err := formula.New()
	if err != nil {
		return nil, fmt.Errorf("движок формул: %w", err)
	}
	if cfg.Currency == "" {
		cfg = DefaultConfig()
	}
	return &Service{store: store, engine: eng, pub: pub, clock: clock, cfg: cfg, compiled: map[string]*formula.Program{}}, nil
}

// WithAuditor подключает журнал аудита.
func (s *Service) WithAuditor(a Auditor) *Service { s.audit = a; return s }

// WithTrackCosts подключает источник затрат треков сертификации (EC-06).
func (s *Service) WithTrackCosts(t TrackCosts) *Service { s.tracks = t; return s }

// Config возвращает настройки ключей полей.
func (s *Service) Config() Config { return s.cfg }

func (s *Service) emit(ctx context.Context, typ string, aggregate, product kernel.ID, actor string, payload any) error {
	if s.pub == nil {
		return nil
	}
	ev, err := kernel.NewEvent(s.clock, typ, aggregate, product, actor, payload)
	if err != nil {
		return err
	}
	if err := s.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	return nil
}

func (s *Service) logAccess(ctx context.Context, sc authz.Scope, action, object string, product kernel.ID, details map[string]any) {
	if s.audit == nil {
		return
	}
	_ = s.audit.FinanceAccess(ctx, sc.Subject(), action, object, product, details)
}

// ---- Доступ ----

// requireRead проверяет право читать финансовые данные продукта (NF-S02).
// Нулевой продукт означает портфельный срез: он доступен тем, кто видит все продукты.
func (s *Service) requireRead(sc authz.Scope, product kernel.ID, level authz.FinanceLevel) error {
	if err := sc.Require(authz.ActionReadFinance, product); err != nil {
		return err
	}
	if sc.Finance() < level {
		return fmt.Errorf("%w: недостаточный уровень доступа к финансовым данным", kernel.ErrForbidden)
	}
	if product == kernel.NilID && !sc.SeesAllProducts() {
		return fmt.Errorf("%w: портфельный срез доступен при доступе ко всем продуктам", kernel.ErrForbidden)
	}
	return nil
}

func (s *Service) requireWrite(sc authz.Scope, product kernel.ID) error {
	return sc.Require(authz.ActionWriteFinance, product)
}

// ---- Поля (EC-08) ----

// FieldInput — описание поля.
type FieldInput struct {
	Key           string
	Name          string
	Type          FieldType
	Currency      string
	Dimensions    []formula.Dimension
	Source        FieldSource
	EffectiveFrom kernel.Date
}

// SaveField создаёт поле или добавляет новую версию его описания (EC-08, EC-11).
func (s *Service) SaveField(ctx context.Context, sc authz.Scope, in FieldInput) (Field, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return Field{}, err
	}
	if err := validKey(in.Key); err != nil {
		return Field{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return Field{}, kernel.Invalid("name", "название обязательно")
	}
	if !in.Type.valid() {
		return Field{}, kernel.Invalid("type", "допустимы money, number, percent, date, reference")
	}
	if !in.Source.valid() {
		return Field{}, kernel.Invalid("source", "допустимы import, manual, calculated")
	}
	if in.Type == FieldMoney && strings.TrimSpace(in.Currency) == "" {
		in.Currency = s.cfg.Currency
	}
	for _, d := range in.Dimensions {
		if !d.Valid() {
			return Field{}, kernel.Invalid("dimensions", fmt.Sprintf("неизвестное измерение %q", d))
		}
	}
	f, err := s.store.Field(ctx, in.Key)
	switch {
	case err == nil:
	case kernel.IsNotFound(err):
		f = Field{ID: kernel.NewID(), Key: in.Key}
	default:
		return Field{}, err
	}
	now := s.clock.Now()
	eff := effectiveFrom(in.EffectiveFrom, len(f.Versions), now)
	if last := f.Latest(); last.Version > 0 && eff.Before(last.EffectiveFrom) {
		return Field{}, kernel.Invalid("effective_from", "дата действия раньше предыдущей версии")
	}
	f.Versions = append(f.Versions, FieldVersion{
		Version: len(f.Versions) + 1, EffectiveFrom: eff, Name: in.Name, Type: in.Type,
		Currency: in.Currency, Dimensions: in.Dimensions, Source: in.Source, Actor: sc.Subject(), At: now,
	})
	if err := s.store.SaveField(ctx, f); err != nil {
		return Field{}, fmt.Errorf("save field: %w", err)
	}
	s.logAccess(ctx, sc, "write", "economics.field:"+f.Key, kernel.NilID, map[string]any{"version": len(f.Versions)})
	if err := s.emit(ctx, EventFieldSaved, f.ID, kernel.NilID, sc.Subject(), f.Latest()); err != nil {
		return Field{}, err
	}
	return f, nil
}

// Fields возвращает поля.
func (s *Service) Fields(ctx context.Context, sc authz.Scope) ([]Field, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Fields(ctx)
}

// ---- Показатели (EC-09, EC-10, EC-11) ----

// MetricInput — описание расчётного показателя.
type MetricInput struct {
	Key           string
	Name          string
	Expression    string
	Currency      string
	EffectiveFrom kernel.Date
}

// SaveMetric создаёт показатель или добавляет версию формулы. Формула компилируется
// при сохранении; формула, создающая цикл, отклоняется с показом пути (EC-10).
func (s *Service) SaveMetric(ctx context.Context, sc authz.Scope, in MetricInput) (Metric, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return Metric{}, err
	}
	if err := validKey(in.Key); err != nil {
		return Metric{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return Metric{}, kernel.Invalid("name", "название обязательно")
	}
	prog, err := s.engine.Compile(in.Expression)
	if err != nil {
		return Metric{}, fmt.Errorf("%w: %w", kernel.ErrValidation, err)
	}
	if err := s.checkCycle(ctx, in.Key, prog.Refs()); err != nil {
		return Metric{}, err
	}
	m, err := s.store.Metric(ctx, in.Key)
	switch {
	case err == nil:
	case kernel.IsNotFound(err):
		m = Metric{ID: kernel.NewID(), Key: in.Key}
	default:
		return Metric{}, err
	}
	now := s.clock.Now()
	eff := effectiveFrom(in.EffectiveFrom, len(m.Versions), now)
	if last := m.Latest(); last.Version > 0 && eff.Before(last.EffectiveFrom) {
		return Metric{}, kernel.Invalid("effective_from", "дата действия раньше предыдущей версии")
	}
	currency := in.Currency
	if currency == "" {
		currency = s.cfg.Currency
	}
	m.Versions = append(m.Versions, MetricVersion{
		Version: len(m.Versions) + 1, EffectiveFrom: eff, Name: in.Name, Expression: in.Expression,
		Refs: prog.Refs(), Currency: currency, Actor: sc.Subject(), At: now,
	})
	if err := s.store.SaveMetric(ctx, m); err != nil {
		return Metric{}, fmt.Errorf("save metric: %w", err)
	}
	s.cache(in.Expression, prog)
	s.logAccess(ctx, sc, "write", "economics.metric:"+m.Key, kernel.NilID, map[string]any{"version": len(m.Versions), "expression": in.Expression})
	if err := s.emit(ctx, EventMetricSaved, m.ID, kernel.NilID, sc.Subject(), m.Latest()); err != nil {
		return Metric{}, err
	}
	return m, nil
}

// Metrics возвращает показатели.
func (s *Service) Metrics(ctx context.Context, sc authz.Scope) ([]Metric, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Metrics(ctx)
}

func (s *Service) cache(expr string, p *formula.Program) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compiled[expr] = p
}

func (s *Service) program(expr string) (*formula.Program, error) {
	s.mu.Lock()
	p, ok := s.compiled[expr]
	s.mu.Unlock()
	if ok {
		return p, nil
	}
	p, err := s.engine.Compile(expr)
	if err != nil {
		return nil, err
	}
	s.cache(expr, p)
	return p, nil
}

// effectiveFrom выбирает дату действия версии. Первая версия действует с начала времён:
// иначе расчёт закрытых периодов остался бы без описания полей и формул. Следующие версии
// по умолчанию действуют с сегодняшнего дня (EC-11).
func effectiveFrom(given kernel.Date, existing int, now time.Time) kernel.Date {
	if !given.IsZero() {
		return given
	}
	if existing == 0 {
		return kernel.Date{}
	}
	return kernel.DateFromTime(now)
}

func validKey(key string) error {
	k := strings.TrimSpace(key)
	if k == "" {
		return kernel.Invalid("key", "ключ обязателен")
	}
	for _, r := range k {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return kernel.Invalid("key", "ключ состоит из строчных латинских букв, цифр и подчёркивания")
		}
	}
	return nil
}

// ---- Вычисление (EC-09, EC-10) ----

// Slice — срез вычисления показателя.
type Slice struct {
	ProductID kernel.ID
	TeamID    kernel.ID
	Period    Period
	Item      string
}

func (sl Slice) formulaSlice() formula.Slice {
	out := formula.Slice{Period: sl.Period.String(), Item: sl.Item}
	if sl.ProductID != kernel.NilID {
		out.Product = sl.ProductID.String()
	}
	if sl.TeamID != kernel.NilID {
		out.Team = sl.TeamID.String()
	}
	return out
}

// Value вычисляет показатель в срезе.
func (s *Service) Value(ctx context.Context, sc authz.Scope, key string, sl Slice) (decimal.Decimal, error) {
	if err := s.requireRead(sc, sl.ProductID, authz.FinanceAggregates); err != nil {
		return decimal.Zero, err
	}
	src := s.source(ctx, nil)
	v, err := src.Metric(key, sl.formulaSlice())
	if err != nil {
		return decimal.Zero, err
	}
	s.logAccess(ctx, sc, "read", "economics.metric:"+key, sl.ProductID, map[string]any{"period": sl.Period.String()})
	return v, nil
}

// Explain раскрывает значение показателя до формулы и исходных строк импорта (EC-10).
// Требует полного доступа к финансовым данным: в объяснении видны отдельные строки.
func (s *Service) Explain(ctx context.Context, sc authz.Scope, key string, sl Slice) (Explanation, error) {
	if err := s.requireRead(sc, sl.ProductID, authz.FinanceFull); err != nil {
		return Explanation{}, err
	}
	src := s.source(ctx, nil)
	ex, err := src.explainMetric(key, sl.formulaSlice(), 0)
	if err != nil {
		return Explanation{}, err
	}
	s.logAccess(ctx, sc, "read", "economics.explain:"+key, sl.ProductID, map[string]any{"period": sl.Period.String()})
	return ex, nil
}

// CompareVersions считает показатель по двум версиям формулы (EC-11).
func (s *Service) CompareVersions(ctx context.Context, sc authz.Scope, key string, sl Slice, a, b int) (decimal.Decimal, decimal.Decimal, error) {
	if err := s.requireRead(sc, sl.ProductID, authz.FinanceFull); err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	m, err := s.store.Metric(ctx, key)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	pick := func(v int) (MetricVersion, error) {
		for _, mv := range m.Versions {
			if mv.Version == v {
				return mv, nil
			}
		}
		return MetricVersion{}, fmt.Errorf("%w: версия %d показателя %q", kernel.ErrNotFound, v, key)
	}
	va, err := pick(a)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	vb, err := pick(b)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	src := s.source(ctx, nil)
	ra, err := src.evalExpression(va.Expression, sl.formulaSlice())
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	rb, err := src.evalExpression(vb.Expression, sl.formulaSlice())
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	s.logAccess(ctx, sc, "read", "economics.compare:"+key, sl.ProductID, map[string]any{"a": a, "b": b})
	return ra, rb, nil
}

// ---- Закрытые периоды (EC-11) ----

// ClosePeriod закрывает период: дальнейший пересчёт возможен только явным действием.
func (s *Service) ClosePeriod(ctx context.Context, sc authz.Scope, p Period) error {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return err
	}
	if p.IsZero() {
		return kernel.Invalid("period", "период обязателен")
	}
	if err := s.store.ClosePeriod(ctx, p, sc.Subject()); err != nil {
		return fmt.Errorf("close period: %w", err)
	}
	s.logAccess(ctx, sc, "write", "economics.period:"+p.String(), kernel.NilID, map[string]any{"action": "close"})
	return s.emit(ctx, EventPeriodClosed, kernel.NewID(), kernel.NilID, sc.Subject(), map[string]string{"period": p.String()})
}

// PeriodClosed сообщает, закрыт ли период.
func (s *Service) PeriodClosed(ctx context.Context, p Period) (bool, error) {
	closed, err := s.store.ClosedPeriods(ctx)
	if err != nil {
		return false, fmt.Errorf("closed periods: %w", err)
	}
	for _, c := range closed {
		if c == p {
			return true, nil
		}
	}
	return false, nil
}

// Recalculate пересчитывает показатели, затронутые изменением поля или показателя (EC-10).
// Закрытый период пересчитывается только при force (EC-11).
func (s *Service) Recalculate(ctx context.Context, sc authz.Scope, changed formula.Ref, sl Slice, force bool) (map[string]decimal.Decimal, error) {
	if err := s.requireWrite(sc, sl.ProductID); err != nil {
		return nil, err
	}
	closed, err := s.PeriodClosed(ctx, sl.Period)
	if err != nil {
		return nil, err
	}
	if closed && !force {
		return nil, fmt.Errorf("%w: период %s закрыт; пересчёт — только явным действием", kernel.ErrConflict, sl.Period)
	}
	keys, err := s.Affected(ctx, changed)
	if err != nil {
		return nil, err
	}
	src := s.source(ctx, nil)
	out := make(map[string]decimal.Decimal, len(keys))
	for _, k := range keys {
		v, err := src.Metric(k, sl.formulaSlice())
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	if closed {
		s.logAccess(ctx, sc, "write", "economics.period:"+sl.Period.String(), sl.ProductID,
			map[string]any{"action": "recalculate_closed", "changed": changed.Key})
		if err := s.emit(ctx, EventPeriodRecalced, kernel.NewID(), sl.ProductID, sc.Subject(),
			map[string]string{"period": sl.Period.String(), "changed": changed.Key}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---- Команды и доли (EC-12) ----

// SaveTeam создаёт команду.
func (s *Service) SaveTeam(ctx context.Context, sc authz.Scope, key, name string) (Team, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return Team{}, err
	}
	if err := validKey(key); err != nil {
		return Team{}, err
	}
	if strings.TrimSpace(name) == "" {
		return Team{}, kernel.Invalid("name", "название обязательно")
	}
	teams, err := s.store.Teams(ctx)
	if err != nil {
		return Team{}, fmt.Errorf("teams: %w", err)
	}
	for _, t := range teams {
		if t.Key == key {
			return t, nil
		}
	}
	t := Team{ID: kernel.NewID(), Key: key, Name: name}
	if err := s.store.SaveTeam(ctx, t); err != nil {
		return Team{}, fmt.Errorf("save team: %w", err)
	}
	return t, nil
}

// Teams возвращает команды.
func (s *Service) Teams(ctx context.Context, sc authz.Scope) ([]Team, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.Teams(ctx)
}

// SetTeamShares задаёт доли команды по продуктам за период вручную (EC-12).
// Сумма долей команды в периоде должна равняться единице.
func (s *Service) SetTeamShares(ctx context.Context, sc authz.Scope, teamID kernel.ID, p Period, shares map[kernel.ID]decimal.Decimal) error {
	return s.setTeamShares(ctx, sc, teamID, p, shares, "manual")
}

// SetWorklogShares задаёт доли команды по продуктам из списаний времени трекера (DL-05, EC-12).
// Источник помечается как worklogs: видно, откуда взялась база распределения.
func (s *Service) SetWorklogShares(ctx context.Context, sc authz.Scope, teamID kernel.ID, p Period, shares map[kernel.ID]decimal.Decimal) error {
	return s.setTeamShares(ctx, sc, teamID, p, shares, "worklogs")
}

func (s *Service) setTeamShares(ctx context.Context, sc authz.Scope, teamID kernel.ID, p Period, shares map[kernel.ID]decimal.Decimal, source string) error {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return err
	}
	if p.IsZero() {
		return kernel.Invalid("period", "период обязателен")
	}
	if err := checkShares(shares); err != nil {
		return err
	}
	out := make([]TeamShare, 0, len(shares))
	for productID, share := range shares {
		out = append(out, TeamShare{TeamID: teamID, ProductID: productID, Period: p, Share: share, Source: source})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProductID.String() < out[j].ProductID.String() })
	if err := s.store.SaveTeamShares(ctx, out); err != nil {
		return fmt.Errorf("save team shares: %w", err)
	}
	return nil
}

// TeamShares возвращает доли команд за период.
func (s *Service) TeamShares(ctx context.Context, sc authz.Scope, p Period) ([]TeamShare, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.TeamShares(ctx, p)
}

func checkShares(shares map[kernel.ID]decimal.Decimal) error {
	if len(shares) == 0 {
		return kernel.Invalid("shares", "доли обязательны")
	}
	total := decimal.Zero
	for id, v := range shares {
		if id == kernel.NilID {
			return kernel.Invalid("shares", "пустой идентификатор продукта")
		}
		if v.IsNegative() {
			return kernel.Invalid("shares", "доля не может быть отрицательной")
		}
		total = total.Add(v)
	}
	if !total.Equal(decimal.NewFromInt(1)) {
		return kernel.Invalid("shares", fmt.Sprintf("сумма долей должна равняться 1, получено %s", total))
	}
	return nil
}

// ---- Правила аллокации и бандлов (EC-02, EC-04) ----

// AllocationInput — правило аллокации затрат хаба.
type AllocationInput struct {
	HubProductID  kernel.ID
	Basis         AllocationBasis
	Shares        map[kernel.ID]decimal.Decimal
	Consumers     []kernel.ID
	EffectiveFrom kernel.Date
}

// SaveAllocationRule добавляет версию правила аллокации (EC-02). Применённые версии не изменяются.
func (s *Service) SaveAllocationRule(ctx context.Context, sc authz.Scope, in AllocationInput) (AllocationRule, error) {
	if err := s.requireWrite(sc, in.HubProductID); err != nil {
		return AllocationRule{}, err
	}
	if in.HubProductID == kernel.NilID {
		return AllocationRule{}, kernel.Invalid("hub_product_id", "продукт-хаб обязателен")
	}
	if !in.Basis.valid() {
		return AllocationRule{}, kernel.Invalid("basis", "допустимы manual, revenue, worklogs")
	}
	if in.Basis == BasisManual {
		if err := checkShares(in.Shares); err != nil {
			return AllocationRule{}, err
		}
		if _, ok := in.Shares[in.HubProductID]; ok {
			return AllocationRule{}, kernel.Invalid("shares", "хаб не распределяет затраты на себя")
		}
	}
	rules, err := s.store.AllocationRules(ctx)
	if err != nil {
		return AllocationRule{}, fmt.Errorf("allocation rules: %w", err)
	}
	version := 0
	var last AllocationRule
	for _, r := range rules {
		if r.HubProductID == in.HubProductID && r.Version > version {
			version, last = r.Version, r
		}
	}
	now := s.clock.Now()
	eff := effectiveFrom(in.EffectiveFrom, version, now)
	if version > 0 && eff.Before(last.EffectiveFrom) {
		return AllocationRule{}, kernel.Invalid("effective_from", "дата действия раньше предыдущей версии")
	}
	r := AllocationRule{
		ID: kernel.NewID(), HubProductID: in.HubProductID, Basis: in.Basis, Shares: in.Shares,
		Consumers: in.Consumers, Version: version + 1, EffectiveFrom: eff, Actor: sc.Subject(), At: now,
	}
	if err := s.store.SaveAllocationRule(ctx, r); err != nil {
		return AllocationRule{}, fmt.Errorf("save allocation rule: %w", err)
	}
	s.logAccess(ctx, sc, "write", "economics.allocation", in.HubProductID, map[string]any{"version": r.Version, "basis": string(r.Basis)})
	if err := s.emit(ctx, EventRuleSaved, r.ID, r.HubProductID, sc.Subject(), r); err != nil {
		return AllocationRule{}, err
	}
	return r, nil
}

// AllocationRules возвращает правила аллокации.
func (s *Service) AllocationRules(ctx context.Context, sc authz.Scope) ([]AllocationRule, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.AllocationRules(ctx)
}

// BundleInput — правило атрибуции выручки бандла.
type BundleInput struct {
	BundleKey     string
	Shares        map[kernel.ID]decimal.Decimal
	EffectiveFrom kernel.Date
}

// SaveBundleRule добавляет версию правила атрибуции бандла (EC-04).
func (s *Service) SaveBundleRule(ctx context.Context, sc authz.Scope, in BundleInput) (BundleRule, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return BundleRule{}, err
	}
	if strings.TrimSpace(in.BundleKey) == "" {
		return BundleRule{}, kernel.Invalid("bundle_key", "ключ бандла обязателен")
	}
	if err := checkShares(in.Shares); err != nil {
		return BundleRule{}, err
	}
	rules, err := s.store.BundleRules(ctx)
	if err != nil {
		return BundleRule{}, fmt.Errorf("bundle rules: %w", err)
	}
	version := 0
	for _, r := range rules {
		if r.BundleKey == in.BundleKey && r.Version > version {
			version = r.Version
		}
	}
	now := s.clock.Now()
	eff := effectiveFrom(in.EffectiveFrom, version, now)
	r := BundleRule{ID: kernel.NewID(), BundleKey: in.BundleKey, Shares: in.Shares,
		Version: version + 1, EffectiveFrom: eff, Actor: sc.Subject(), At: now}
	if err := s.store.SaveBundleRule(ctx, r); err != nil {
		return BundleRule{}, fmt.Errorf("save bundle rule: %w", err)
	}
	s.logAccess(ctx, sc, "write", "economics.bundle:"+r.BundleKey, kernel.NilID, map[string]any{"version": r.Version})
	return r, nil
}

// BundleRules возвращает правила атрибуции бандлов.
func (s *Service) BundleRules(ctx context.Context, sc authz.Scope) ([]BundleRule, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceAggregates); err != nil {
		return nil, err
	}
	return s.store.BundleRules(ctx)
}

// ---- Строки данных ----

// Facts возвращает строки данных. Требует полного доступа к финансам (NF-S02).
func (s *Service) Facts(ctx context.Context, sc authz.Scope, f FactFilter) ([]FactRow, error) {
	product := kernel.NilID
	if f.Product != nil {
		product = *f.Product
	}
	if err := s.requireRead(sc, product, authz.FinanceFull); err != nil {
		return nil, err
	}
	rows, err := s.store.Facts(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	s.logAccess(ctx, sc, "read", "economics.facts", product, map[string]any{"field": f.FieldKey, "rows": len(rows)})
	return rows, nil
}

// SetManualValue записывает значение поля ручного ввода отдельной загрузкой (EC-08).
func (s *Service) SetManualValue(ctx context.Context, sc authz.Scope, key string, sl Slice, value decimal.Decimal) (FactRow, error) {
	if err := s.requireWrite(sc, sl.ProductID); err != nil {
		return FactRow{}, err
	}
	f, err := s.store.Field(ctx, key)
	if err != nil {
		return FactRow{}, err
	}
	v := f.Latest()
	if v.Source != SourceManual {
		return FactRow{}, kernel.Invalid("field", "поле не принимает ручной ввод")
	}
	if sl.Period.IsZero() {
		return FactRow{}, kernel.Invalid("period", "период обязателен")
	}
	closed, err := s.PeriodClosed(ctx, sl.Period)
	if err != nil {
		return FactRow{}, err
	}
	if closed {
		return FactRow{}, fmt.Errorf("%w: период %s закрыт", kernel.ErrConflict, sl.Period)
	}
	now := s.clock.Now()
	version, err := s.nextDataVersion(ctx, sl.Period)
	if err != nil {
		return FactRow{}, err
	}
	batch := ImportBatch{ID: kernel.NewID(), Period: sl.Period, DataVersion: version, Status: BatchApplied,
		FileName: "ручной ввод", Rows: 1, Actor: sc.Subject(), At: now}
	row := FactRow{ID: kernel.NewID(), BatchID: batch.ID, FieldKey: key, ProductID: sl.ProductID, TeamID: sl.TeamID,
		Period: sl.Period, Item: sl.Item, Value: value, DataVersion: version}
	// Строки прежней версии переносятся до записи загрузки: иначе действующей
	// версией периода уже считалась бы новая, ещё пустая.
	carried, err := s.carriedRows(ctx, sl.Period, version, batch.ID, key, sl)
	if err != nil {
		return FactRow{}, err
	}
	if err := s.store.SaveBatch(ctx, batch); err != nil {
		return FactRow{}, fmt.Errorf("save batch: %w", err)
	}
	if err := s.store.AppendFacts(ctx, append([]FactRow{row}, carried...)); err != nil {
		return FactRow{}, fmt.Errorf("append facts: %w", err)
	}
	s.logAccess(ctx, sc, "write", "economics.value:"+key, sl.ProductID, map[string]any{"period": sl.Period.String()})
	return row, nil
}

// carriedRows переносит действующие строки периода в новую версию данных, кроме заменяемой.
func (s *Service) carriedRows(ctx context.Context, p Period, version int, batchID kernel.ID, replacedKey string, sl Slice) ([]FactRow, error) {
	prev, err := s.store.Facts(ctx, FactFilter{Period: &p})
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	carried := make([]FactRow, 0, len(prev))
	for _, r := range prev {
		if r.FieldKey == replacedKey && r.ProductID == sl.ProductID && r.TeamID == sl.TeamID && r.Item == sl.Item {
			continue
		}
		r.ID, r.BatchID, r.DataVersion = kernel.NewID(), batchID, version
		carried = append(carried, r)
	}
	return carried, nil
}

func (s *Service) nextDataVersion(ctx context.Context, p Period) (int, error) {
	batches, err := s.store.Batches(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("batches: %w", err)
	}
	version := 0
	for _, b := range batches {
		if b.Status == BatchApplied && b.DataVersion > version {
			version = b.DataVersion
		}
	}
	return version + 1, nil
}

// money собирает денежную сумму из decimal минорных единиц.
func (s *Service) money(v decimal.Decimal) kernel.Money {
	return kernel.Money{Amount: v.RoundBank(0).IntPart(), Currency: s.cfg.Currency}
}
