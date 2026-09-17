package discovery

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/signals"
)

// Service — публичный интерфейс модуля discovery.
type Service struct {
	store Store
	pub   kernel.Publisher
	clock kernel.Clock

	signals   SignalReader
	merger    SignalMerger
	linker    SignalLinker
	features  FeatureReader
	decisions DecisionLinks
	index     SimilarityIndex
}

// Option настраивает порты сервиса. Ненастроенный порт — операция, которой он нужен,
// возвращает kernel.ErrUnavailable; трассировка без порта решений идёт без решений.
type Option func(*Service)

// WithSignals подключает чтение сигналов (DS-02, DS-04, SG-04).
func WithSignals(r SignalReader) Option { return func(s *Service) { s.signals = r } }

// WithSignalMerger подключает слияние сигналов (SG-04).
func WithSignalMerger(m SignalMerger) Option { return func(s *Service) { s.merger = m } }

// WithSignalLinker подключает привязку сигнала к гипотезе (DS-01).
func WithSignalLinker(l SignalLinker) Option { return func(s *Service) { s.linker = l } }

// WithFeatures подключает чтение фич (DS-01, DS-03, DS-04).
func WithFeatures(f FeatureReader) Option { return func(s *Service) { s.features = f } }

// WithDecisions подключает решения (DS-04).
func WithDecisions(d DecisionLinks) Option { return func(s *Service) { s.decisions = d } }

// WithIndex подключает индекс похожести (SG-04).
func WithIndex(ix SimilarityIndex) Option { return func(s *Service) { s.index = ix } }

// NewService создаёт сервис.
func NewService(store Store, pub kernel.Publisher, clock kernel.Clock, opts ...Option) *Service {
	s := &Service{store: store, pub: pub, clock: clock}
	for _, o := range opts {
		o(s)
	}
	return s
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

func unavailable(port string) error {
	return fmt.Errorf("%w: порт %s не подключён", kernel.ErrUnavailable, port)
}

// checkFeature проверяет, что фича существует и принадлежит продукту.
func (s *Service) checkFeature(ctx context.Context, sc authz.Scope, featureID, productID kernel.ID) error {
	if featureID == kernel.NilID {
		return nil
	}
	if s.features == nil {
		return unavailable("features")
	}
	f, err := s.features.Feature(ctx, sc, featureID)
	if err != nil {
		return fmt.Errorf("feature: %w", err)
	}
	if f.ProductID != productID {
		return kernel.Invalid("feature_id", "фича другого продукта")
	}
	return nil
}

// checkHypotheses проверяет, что гипотезы существуют и принадлежат продукту.
func (s *Service) checkHypotheses(ctx context.Context, ids []kernel.ID, productID kernel.ID) error {
	for _, id := range ids {
		h, err := s.store.Hypothesis(ctx, id)
		if err != nil {
			return err
		}
		if h.ProductID != productID {
			return kernel.Invalid("hypothesis_ids", fmt.Sprintf("гипотеза %s другого продукта", id))
		}
	}
	return nil
}

// checkSignals проверяет, что сигналы существуют и принадлежат продукту.
func (s *Service) checkSignals(ctx context.Context, sc authz.Scope, ids []kernel.ID, productID kernel.ID) error {
	if len(ids) == 0 {
		return nil
	}
	if s.signals == nil {
		return unavailable("signals")
	}
	for _, id := range ids {
		sig, err := s.signals.Signal(ctx, sc, id)
		if err != nil {
			return fmt.Errorf("signal: %w", err)
		}
		if sig.ProductID != productID {
			return kernel.Invalid("signal_ids", fmt.Sprintf("сигнал %s другого продукта", id))
		}
	}
	return nil
}

func dedupe(ids []kernel.ID) []kernel.ID {
	out := make([]kernel.ID, 0, len(ids))
	for _, id := range ids {
		if id != kernel.NilID && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ---- Гипотезы (DS-01) ----

// HypothesisInput — данные гипотезы. ID пустой — создание; иначе обновление (продукт не меняется).
// Статус задаётся только через ChangeHypothesisStatus.
type HypothesisInput struct {
	ID                    kernel.ID
	ProductID             kernel.ID
	Title                 string
	Statement             string
	Assumptions           []string
	ConfirmationCriterion string
	FeatureID             kernel.ID
	CustomFields          map[string]any
}

func (in HypothesisInput) validate() error {
	if in.ProductID == kernel.NilID {
		return kernel.Invalid("product_id", "обязателен")
	}
	if strings.TrimSpace(in.Title) == "" {
		return kernel.Invalid("title", "обязателен")
	}
	if strings.TrimSpace(in.Statement) == "" {
		return kernel.Invalid("statement", "обязательна")
	}
	if strings.TrimSpace(in.ConfirmationCriterion) == "" {
		return kernel.Invalid("confirmation_criterion", "обязателен")
	}
	return nil
}

// SaveHypothesis создаёт или обновляет гипотезу (DS-01). Право: запись discovery продукта.
// Значения кастомных полей проверяются по определениям (AD-03).
func (s *Service) SaveHypothesis(ctx context.Context, sc authz.Scope, in HypothesisInput) (Hypothesis, error) {
	if err := in.validate(); err != nil {
		return Hypothesis{}, err
	}
	if err := sc.Require(authz.ActionWriteDiscovery, in.ProductID); err != nil {
		return Hypothesis{}, err
	}
	if err := s.ValidateCustomFields(ctx, EntityHypothesis, in.CustomFields); err != nil {
		return Hypothesis{}, err
	}
	if err := s.checkFeature(ctx, sc, in.FeatureID, in.ProductID); err != nil {
		return Hypothesis{}, err
	}
	now := s.clock.Now()
	h := Hypothesis{
		ID:                    kernel.NewID(),
		ProductID:             in.ProductID,
		Title:                 strings.TrimSpace(in.Title),
		Statement:             strings.TrimSpace(in.Statement),
		Assumptions:           cleanStrings(in.Assumptions),
		ConfirmationCriterion: strings.TrimSpace(in.ConfirmationCriterion),
		Status:                HypothesisDraft,
		FeatureID:             in.FeatureID,
		CustomFields:          in.CustomFields,
		CreatedBy:             sc.Subject(),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if in.ID != kernel.NilID {
		prev, err := s.store.Hypothesis(ctx, in.ID)
		if err != nil {
			return Hypothesis{}, err
		}
		if prev.ProductID != in.ProductID {
			return Hypothesis{}, kernel.Invalid("product_id", "продукт гипотезы не меняется")
		}
		h.ID, h.Status, h.Resolution = prev.ID, prev.Status, prev.Resolution
		h.CreatedBy, h.CreatedAt = prev.CreatedBy, prev.CreatedAt
	}
	if err := s.store.SaveHypothesis(ctx, h); err != nil {
		return Hypothesis{}, fmt.Errorf("save hypothesis: %w", err)
	}
	if err := s.emit(ctx, EventHypothesisSaved, h.ID, h.ProductID, sc.Subject(), h); err != nil {
		return Hypothesis{}, err
	}
	return h, nil
}

// StatusChange — смена статуса гипотезы. Resolution обязательна для категорий confirmed и rejected.
type StatusChange struct {
	Status     HypothesisStatus
	Resolution string
}

// allowedTransition — переходы между категориями статусов (DS-01). confirmed и rejected
// возвращаются в draft (переоткрытие) — наименее необратимый вариант.
func allowedTransition(from, to HypothesisStatus) bool {
	switch from {
	case HypothesisDraft:
		return to == HypothesisTesting || to == HypothesisRejected
	case HypothesisTesting:
		return to == HypothesisConfirmed || to == HypothesisRejected || to == HypothesisDraft
	case HypothesisConfirmed, HypothesisRejected:
		return to == HypothesisDraft
	}
	return false
}

// ChangeHypothesisStatus переводит гипотезу в новый статус (DS-01). Переход проверяется по
// категориям: draft → testing|rejected, testing → confirmed|rejected|draft, confirmed|rejected → draft.
// Смена ключа внутри одной категории (встроенный ↔ пользовательский) разрешена.
func (s *Service) ChangeHypothesisStatus(ctx context.Context, sc authz.Scope, id kernel.ID, ch StatusChange) (Hypothesis, error) {
	h, err := s.store.Hypothesis(ctx, id)
	if err != nil {
		return Hypothesis{}, err
	}
	if err := sc.Require(authz.ActionWriteDiscovery, h.ProductID); err != nil {
		return Hypothesis{}, err
	}
	to, err := s.hypothesisCategory(ctx, ch.Status)
	if err != nil {
		return Hypothesis{}, err
	}
	from, err := s.hypothesisCategory(ctx, h.Status)
	if err != nil {
		return Hypothesis{}, err
	}
	if from != to && !allowedTransition(from, to) {
		return Hypothesis{}, fmt.Errorf("%w: переход %s → %s не допускается", kernel.ErrConflict, h.Status, ch.Status)
	}
	resolution := strings.TrimSpace(ch.Resolution)
	if (to == HypothesisConfirmed || to == HypothesisRejected) && resolution == "" {
		return Hypothesis{}, kernel.Invalid("resolution", "причина обязательна для подтверждения и отклонения")
	}
	h.Status, h.Resolution, h.UpdatedAt = ch.Status, resolution, s.clock.Now()
	if err := s.store.SaveHypothesis(ctx, h); err != nil {
		return Hypothesis{}, fmt.Errorf("save hypothesis: %w", err)
	}
	if err := s.emit(ctx, EventHypothesisSaved, h.ID, h.ProductID, sc.Subject(), h); err != nil {
		return Hypothesis{}, err
	}
	return h, nil
}

// Hypothesis возвращает гипотезу (приватный контур продукта).
func (s *Service) Hypothesis(ctx context.Context, sc authz.Scope, id kernel.ID) (Hypothesis, error) {
	h, err := s.store.Hypothesis(ctx, id)
	if err != nil {
		return Hypothesis{}, err
	}
	if err := sc.Require(authz.ActionReadPrivate, h.ProductID); err != nil {
		return Hypothesis{}, err
	}
	return h, nil
}

// Hypotheses возвращает гипотезы продукта по фильтру (приватный контур).
func (s *Service) Hypotheses(ctx context.Context, sc authz.Scope, productID kernel.ID, f HypothesisFilter) ([]Hypothesis, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	f.ProductID = productID
	out, err := s.store.Hypotheses(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("list hypotheses: %w", err)
	}
	return out, nil
}

// LinkSignalToHypothesis привязывает сигнал к гипотезе того же продукта (DS-01, SG-05).
// Права проверяет модуль signals (запись сигналов); гипотеза должна быть видима субъекту.
func (s *Service) LinkSignalToHypothesis(ctx context.Context, sc authz.Scope, signalID, hypothesisID kernel.ID) (signals.Signal, error) {
	if s.signals == nil || s.linker == nil {
		return signals.Signal{}, unavailable("signals")
	}
	h, err := s.Hypothesis(ctx, sc, hypothesisID)
	if err != nil {
		return signals.Signal{}, err
	}
	sig, err := s.signals.Signal(ctx, sc, signalID)
	if err != nil {
		return signals.Signal{}, fmt.Errorf("signal: %w", err)
	}
	if sig.ProductID != h.ProductID {
		return signals.Signal{}, kernel.Invalid("hypothesis_id", "гипотеза другого продукта")
	}
	sig, err = s.linker.LinkToHypothesis(ctx, sc, signalID, hypothesisID)
	if err != nil {
		return signals.Signal{}, fmt.Errorf("link signal: %w", err)
	}
	return sig, nil
}

// ---- Интервью (DS-02) ----

// InterviewInput — данные интервью. ID пустой — создание.
type InterviewInput struct {
	ID            kernel.ID
	ProductID     kernel.ID
	AccountID     string
	Segment       string
	Date          kernel.Date
	Participants  []string
	Notes         string
	HypothesisIDs []kernel.ID
}

// SaveInterview создаёт или обновляет интервью (DS-02). Право: запись discovery продукта.
func (s *Service) SaveInterview(ctx context.Context, sc authz.Scope, in InterviewInput) (Interview, error) {
	if in.ProductID == kernel.NilID {
		return Interview{}, kernel.Invalid("product_id", "обязателен")
	}
	if in.Date.IsZero() {
		return Interview{}, kernel.Invalid("date", "обязательна")
	}
	if err := sc.Require(authz.ActionWriteDiscovery, in.ProductID); err != nil {
		return Interview{}, err
	}
	hyps := dedupe(in.HypothesisIDs)
	if err := s.checkHypotheses(ctx, hyps, in.ProductID); err != nil {
		return Interview{}, err
	}
	now := s.clock.Now()
	i := Interview{
		ID:            kernel.NewID(),
		ProductID:     in.ProductID,
		AccountID:     strings.TrimSpace(in.AccountID),
		Segment:       strings.TrimSpace(in.Segment),
		Date:          in.Date,
		Participants:  cleanStrings(in.Participants),
		Notes:         strings.TrimSpace(in.Notes),
		HypothesisIDs: hyps,
		CreatedBy:     sc.Subject(),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if in.ID != kernel.NilID {
		prev, err := s.store.Interview(ctx, in.ID)
		if err != nil {
			return Interview{}, err
		}
		if prev.ProductID != in.ProductID {
			return Interview{}, kernel.Invalid("product_id", "продукт интервью не меняется")
		}
		i.ID, i.CreatedBy, i.CreatedAt = prev.ID, prev.CreatedBy, prev.CreatedAt
	}
	if err := s.store.SaveInterview(ctx, i); err != nil {
		return Interview{}, fmt.Errorf("save interview: %w", err)
	}
	if err := s.emit(ctx, EventInterviewSaved, i.ID, i.ProductID, sc.Subject(), i); err != nil {
		return Interview{}, err
	}
	return i, nil
}

// Interview возвращает интервью (приватный контур продукта).
func (s *Service) Interview(ctx context.Context, sc authz.Scope, id kernel.ID) (Interview, error) {
	i, err := s.store.Interview(ctx, id)
	if err != nil {
		return Interview{}, err
	}
	if err := sc.Require(authz.ActionReadPrivate, i.ProductID); err != nil {
		return Interview{}, err
	}
	return i, nil
}

// Interviews возвращает интервью продукта (приватный контур).
func (s *Service) Interviews(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Interview, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	out, err := s.store.Interviews(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list interviews: %w", err)
	}
	return out, nil
}

// ---- Инсайты (DS-02) ----

// InsightInput — данные инсайта. ID пустой — создание.
type InsightInput struct {
	ID            kernel.ID
	ProductID     kernel.ID
	Text          string
	InterviewID   kernel.ID
	HypothesisIDs []kernel.ID
	SignalIDs     []kernel.ID
	Confidence    Confidence
}

// SaveInsight создаёт или обновляет инсайт (DS-02). Привязки — к интервью, гипотезам и
// сигналам того же продукта. Право: запись discovery продукта.
func (s *Service) SaveInsight(ctx context.Context, sc authz.Scope, in InsightInput) (Insight, error) {
	if in.ProductID == kernel.NilID {
		return Insight{}, kernel.Invalid("product_id", "обязателен")
	}
	if strings.TrimSpace(in.Text) == "" {
		return Insight{}, kernel.Invalid("text", "обязателен")
	}
	if !ValidConfidence(in.Confidence) {
		return Insight{}, kernel.Invalid("confidence", "ожидается low|medium|high")
	}
	if err := sc.Require(authz.ActionWriteDiscovery, in.ProductID); err != nil {
		return Insight{}, err
	}
	if in.InterviewID != kernel.NilID {
		iv, err := s.store.Interview(ctx, in.InterviewID)
		if err != nil {
			return Insight{}, err
		}
		if iv.ProductID != in.ProductID {
			return Insight{}, kernel.Invalid("interview_id", "интервью другого продукта")
		}
	}
	hyps, sigs := dedupe(in.HypothesisIDs), dedupe(in.SignalIDs)
	if err := s.checkHypotheses(ctx, hyps, in.ProductID); err != nil {
		return Insight{}, err
	}
	if err := s.checkSignals(ctx, sc, sigs, in.ProductID); err != nil {
		return Insight{}, err
	}
	now := s.clock.Now()
	i := Insight{
		ID:            kernel.NewID(),
		ProductID:     in.ProductID,
		Text:          strings.TrimSpace(in.Text),
		InterviewID:   in.InterviewID,
		HypothesisIDs: hyps,
		SignalIDs:     sigs,
		Confidence:    in.Confidence,
		CreatedBy:     sc.Subject(),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if in.ID != kernel.NilID {
		prev, err := s.store.Insight(ctx, in.ID)
		if err != nil {
			return Insight{}, err
		}
		if prev.ProductID != in.ProductID {
			return Insight{}, kernel.Invalid("product_id", "продукт инсайта не меняется")
		}
		i.ID, i.CreatedBy, i.CreatedAt = prev.ID, prev.CreatedBy, prev.CreatedAt
	}
	if err := s.store.SaveInsight(ctx, i); err != nil {
		return Insight{}, fmt.Errorf("save insight: %w", err)
	}
	if err := s.emit(ctx, EventInsightSaved, i.ID, i.ProductID, sc.Subject(), i); err != nil {
		return Insight{}, err
	}
	return i, nil
}

// Insight возвращает инсайт (приватный контур продукта).
func (s *Service) Insight(ctx context.Context, sc authz.Scope, id kernel.ID) (Insight, error) {
	i, err := s.store.Insight(ctx, id)
	if err != nil {
		return Insight{}, err
	}
	if err := sc.Require(authz.ActionReadPrivate, i.ProductID); err != nil {
		return Insight{}, err
	}
	return i, nil
}

// Insights возвращает инсайты продукта по фильтру (приватный контур).
func (s *Service) Insights(ctx context.Context, sc authz.Scope, productID kernel.ID, f InsightFilter) ([]Insight, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	f.ProductID = productID
	out, err := s.store.Insights(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("list insights: %w", err)
	}
	return out, nil
}

// ---- Evidence (DS-03) ----

// EvidenceInput — данные evidence. ID пустой — создание. Verification пустая — unverified.
type EvidenceInput struct {
	ID           kernel.ID
	ProductID    kernel.ID
	Source       string
	SourceRef    string
	Date         kernel.Date
	Trust        Confidence
	Verification Verification
	SHA256       string
	HypothesisID kernel.ID
	InsightID    kernel.ID
	FeatureID    kernel.ID
}

func (in EvidenceInput) validate() error {
	if in.ProductID == kernel.NilID {
		return kernel.Invalid("product_id", "обязателен")
	}
	if strings.TrimSpace(in.Source) == "" {
		return kernel.Invalid("source", "обязателен")
	}
	if in.Date.IsZero() {
		return kernel.Invalid("date", "обязательна")
	}
	if !ValidConfidence(in.Trust) {
		return kernel.Invalid("trust", "ожидается low|medium|high")
	}
	if in.Verification != "" && !ValidVerification(in.Verification) {
		return kernel.Invalid("verification", "ожидается unverified|verified|rejected")
	}
	if h := strings.TrimSpace(in.SHA256); h != "" {
		if b, err := hex.DecodeString(h); err != nil || len(b) != 32 {
			return kernel.Invalid("sha256", "ожидается 64 hex-символа")
		}
	}
	return nil
}

// SaveEvidence создаёт или обновляет evidence (DS-03). Статус проверки по умолчанию — unverified
// (проверка — задел под AI-04). Привязки — к гипотезе, инсайту и фиче того же продукта.
// Право: запись discovery продукта.
func (s *Service) SaveEvidence(ctx context.Context, sc authz.Scope, in EvidenceInput) (Evidence, error) {
	if err := in.validate(); err != nil {
		return Evidence{}, err
	}
	if err := sc.Require(authz.ActionWriteDiscovery, in.ProductID); err != nil {
		return Evidence{}, err
	}
	if in.HypothesisID != kernel.NilID {
		if err := s.checkHypotheses(ctx, []kernel.ID{in.HypothesisID}, in.ProductID); err != nil {
			return Evidence{}, err
		}
	}
	if in.InsightID != kernel.NilID {
		i, err := s.store.Insight(ctx, in.InsightID)
		if err != nil {
			return Evidence{}, err
		}
		if i.ProductID != in.ProductID {
			return Evidence{}, kernel.Invalid("insight_id", "инсайт другого продукта")
		}
	}
	if err := s.checkFeature(ctx, sc, in.FeatureID, in.ProductID); err != nil {
		return Evidence{}, err
	}
	now := s.clock.Now()
	e := Evidence{
		ID:           kernel.NewID(),
		ProductID:    in.ProductID,
		Source:       strings.TrimSpace(in.Source),
		SourceRef:    strings.TrimSpace(in.SourceRef),
		Date:         in.Date,
		Trust:        in.Trust,
		Verification: in.Verification,
		SHA256:       strings.ToLower(strings.TrimSpace(in.SHA256)),
		HypothesisID: in.HypothesisID,
		InsightID:    in.InsightID,
		FeatureID:    in.FeatureID,
		CreatedBy:    sc.Subject(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if e.Verification == "" {
		e.Verification = VerificationUnverified
	}
	if in.ID != kernel.NilID {
		prev, err := s.store.Evidence(ctx, in.ID)
		if err != nil {
			return Evidence{}, err
		}
		if prev.ProductID != in.ProductID {
			return Evidence{}, kernel.Invalid("product_id", "продукт evidence не меняется")
		}
		e.ID, e.CreatedBy, e.CreatedAt = prev.ID, prev.CreatedBy, prev.CreatedAt
	}
	if err := s.store.SaveEvidence(ctx, e); err != nil {
		return Evidence{}, fmt.Errorf("save evidence: %w", err)
	}
	if err := s.emit(ctx, EventEvidenceSaved, e.ID, e.ProductID, sc.Subject(), e); err != nil {
		return Evidence{}, err
	}
	return e, nil
}

// Evidence возвращает evidence (приватный контур продукта).
func (s *Service) Evidence(ctx context.Context, sc authz.Scope, id kernel.ID) (Evidence, error) {
	e, err := s.store.Evidence(ctx, id)
	if err != nil {
		return Evidence{}, err
	}
	if err := sc.Require(authz.ActionReadPrivate, e.ProductID); err != nil {
		return Evidence{}, err
	}
	return e, nil
}

// EvidenceList возвращает evidence продукта по фильтру (приватный контур).
func (s *Service) EvidenceList(ctx context.Context, sc authz.Scope, productID kernel.ID, f EvidenceFilter) ([]Evidence, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	f.ProductID = productID
	out, err := s.store.EvidenceList(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	return out, nil
}

// ---- Похожие сигналы и слияние (SG-04) ----

// SimilarSignal — похожий сигнал с оценкой близости.
type SimilarSignal struct {
	Signal signals.Signal `json:"signal"`
	Score  float64        `json:"score"`
}

// SimilarSignals возвращает до limit сигналов того же продукта, похожих на заданный, по индексу
// (SG-04). Слитые сигналы и сам сигнал исключаются. Право: приватный контур продукта сигнала.
func (s *Service) SimilarSignals(ctx context.Context, sc authz.Scope, signalID kernel.ID, limit int) ([]SimilarSignal, error) {
	if s.signals == nil {
		return nil, unavailable("signals")
	}
	if s.index == nil {
		return nil, unavailable("index")
	}
	if limit <= 0 {
		return nil, kernel.Invalid("limit", "ожидается положительное число")
	}
	sig, err := s.signals.Signal(ctx, sc, signalID)
	if err != nil {
		return nil, fmt.Errorf("signal: %w", err)
	}
	// Запрашиваем с запасом: сам сигнал и слитые дубликаты будут отброшены.
	matches, err := s.index.Similar(ctx, signals.IndexKindSignal, sig.ProductID, sig.Text, limit*2+1)
	if err != nil {
		return nil, fmt.Errorf("similar: %w", err)
	}
	out := make([]SimilarSignal, 0, limit)
	for _, m := range matches {
		if m.ID == signalID {
			continue
		}
		cand, err := s.signals.Signal(ctx, sc, m.ID)
		if err != nil {
			if kernel.IsNotFound(err) {
				continue // индекс отстал от хранилища
			}
			return nil, fmt.Errorf("signal: %w", err)
		}
		if cand.Status == signals.StatusMerged {
			continue
		}
		out = append(out, SimilarSignal{Signal: cand, Score: m.Score})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// MergeSignals сливает дубликаты в целевой сигнал через порт signals (SG-04).
// Права и инварианты слияния проверяет модуль signals.
func (s *Service) MergeSignals(ctx context.Context, sc authz.Scope, targetID kernel.ID, dupIDs []kernel.ID) error {
	if s.merger == nil {
		return unavailable("signals")
	}
	if err := s.merger.Merge(ctx, sc, targetID, dedupe(dupIDs)); err != nil {
		return fmt.Errorf("merge signals: %w", err)
	}
	return nil
}
