package signals

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

// Graph — нужная сигналам часть публичного интерфейса portfoliograph (инвариант 1).
// Реализуется *portfoliograph.Service.
type Graph interface {
	Feature(ctx context.Context, sc authz.Scope, id kernel.ID) (portfoliograph.Feature, error)
	Contract(ctx context.Context, sc authz.Scope, id kernel.ID) (portfoliograph.IntegrationContract, kernel.Date, error)
	ProductIDByKey(ctx context.Context, key string) (kernel.ID, error)
	SetFeatureOwnValue(ctx context.Context, sc authz.Scope, id kernel.ID, v kernel.Money) error
	SetContractSignalValue(ctx context.Context, sc authz.Scope, id kernel.ID, v kernel.Money) error
}

var _ Graph = (*portfoliograph.Service)(nil)

// Service — публичный интерфейс модуля сигналов.
type Service struct {
	store Store
	graph Graph
	pub   kernel.Publisher
	clock kernel.Clock
	// DefaultTriageDays — срок разбора по умолчанию от даты приёма (SG-03).
	// TODO(question-08): норматив срока разбора не задан в ТЗ; 14 дней.
	DefaultTriageDays int
}

// NewService создаёт сервис.
func NewService(store Store, graph Graph, pub kernel.Publisher, clock kernel.Clock) *Service {
	return &Service{store: store, graph: graph, pub: pub, clock: clock, DefaultTriageDays: 14}
}

func (s *Service) emit(ctx context.Context, typ string, sig Signal, actor string) error {
	ev, err := kernel.NewEvent(s.clock, typ, sig.ID, sig.ProductID, actor, sig)
	if err != nil {
		return err
	}
	if err := s.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish %s: %w", typ, err)
	}
	return nil
}

// IngestInput — данные сигнала при ручном вводе, из service desk или импорта (SG-01, SG-02).
type IngestInput struct {
	ProductID   kernel.ID
	Source      Source
	Text        string
	ExternalKey string
	AccountID   string
	DealID      string
	Version     string
	Segment     string
	// DealAmount — сумма сделки; AccountARR — ARR аккаунта. Вес = DealAmount, иначе AccountARR.
	DealAmount kernel.Money
	AccountARR kernel.Money
	BlocksDeal bool
	DueDate    kernel.Date
}

func (in IngestInput) validate() error {
	if in.ProductID == kernel.NilID {
		return kernel.Invalid("product_id", "обязателен")
	}
	if !ValidSource(in.Source) {
		return kernel.Invalid("source", fmt.Sprintf("неизвестный источник %q", in.Source))
	}
	if strings.TrimSpace(in.Text) == "" {
		return kernel.Invalid("text", "обязателен")
	}
	if in.DealID != "" && in.AccountID == "" {
		return kernel.Invalid("account_id", "сделка без аккаунта")
	}
	if in.DealAmount.Amount < 0 || in.AccountARR.Amount < 0 {
		return kernel.Invalid("weight", "отрицательная сумма")
	}
	if in.BlocksDeal && in.DealID == "" {
		return kernel.Invalid("blocks_deal", "флаг без сделки")
	}
	return nil
}

func (in IngestInput) weight() kernel.Money {
	if in.DealID != "" && !in.DealAmount.IsZero() {
		return in.DealAmount
	}
	return in.AccountARR
}

// Ingest принимает сигнал (SG-01). Право: запись сигналов продукта. Существующий ExternalKey
// обновляет сигнал, не создавая дубль (ТЗ 4.2); привязка и статус при этом сохраняются.
func (s *Service) Ingest(ctx context.Context, sc authz.Scope, in IngestInput) (Signal, error) {
	if err := in.validate(); err != nil {
		return Signal{}, err
	}
	if err := sc.Require(authz.ActionWriteSignals, in.ProductID); err != nil {
		return Signal{}, err
	}
	now := s.clock.Now()
	sig := Signal{
		ID:          kernel.NewID(),
		ProductID:   in.ProductID,
		Source:      in.Source,
		Text:        strings.TrimSpace(in.Text),
		ExternalKey: in.ExternalKey,
		AccountID:   in.AccountID,
		DealID:      in.DealID,
		Version:     in.Version,
		Segment:     in.Segment,
		Weight:      in.weight(),
		AccountARR:  in.AccountARR,
		BlocksDeal:  in.BlocksDeal,
		Status:      StatusNew,
		DueDate:     in.DueDate,
		CreatedBy:   sc.Subject(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if sig.DueDate.IsZero() {
		sig.DueDate = kernel.DateFromTime(now).AddDays(s.DefaultTriageDays)
	}
	if in.ExternalKey != "" {
		prev, err := s.store.GetByExternalKey(ctx, in.ExternalKey)
		switch {
		case err == nil:
			if prev.ProductID != in.ProductID {
				return Signal{}, fmt.Errorf("%w: сигнал %s уже относится к другому продукту", kernel.ErrConflict, in.ExternalKey)
			}
			sig.ID, sig.Status, sig.DueDate = prev.ID, prev.Status, prev.DueDate
			sig.FeatureID, sig.ContractID, sig.HypothesisID = prev.FeatureID, prev.ContractID, prev.HypothesisID
			sig.CreatedBy, sig.CreatedAt = prev.CreatedBy, prev.CreatedAt
		case !errors.Is(err, kernel.ErrNotFound):
			return Signal{}, fmt.Errorf("lookup signal: %w", err)
		}
	}
	if err := s.store.Save(ctx, sig); err != nil {
		return Signal{}, fmt.Errorf("save signal: %w", err)
	}
	if err := s.emit(ctx, EventSignalIngested, sig, sc.Subject()); err != nil {
		return Signal{}, err
	}
	if sig.IsLinked() {
		if err := s.recomputeTarget(ctx, sc, sig); err != nil {
			return Signal{}, err
		}
	}
	return sig, nil
}

// ImportResult — итог импорта из CRM.
type ImportResult struct {
	Imported []Signal
	// Skipped — сделки без продукта в портфеле или без прав; ключ — id сделки.
	Skipped map[string]error
}

// ImportFromCRM читает сделки через порт CRM и создаёт сигнал на каждый запрос фичи из сделки
// (SG-01). Импорт идемпотентен по ключу «crm:deal:<id>:<n>». Сделки продуктов, на которые нет
// права записи, пропускаются с указанием причины.
func (s *Service) ImportFromCRM(ctx context.Context, sc authz.Scope, crm ports.CRM) (ImportResult, error) {
	res := ImportResult{Skipped: map[string]error{}}
	if !sc.Valid() {
		return res, kernel.ErrForbidden
	}
	accounts, err := crm.Accounts(ctx)
	if err != nil {
		return res, fmt.Errorf("crm accounts: %w", err)
	}
	byID := make(map[string]ports.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ExternalID] = a
	}
	deals, err := crm.Deals(ctx)
	if err != nil {
		return res, fmt.Errorf("crm deals: %w", err)
	}
	for _, d := range deals {
		productID, err := s.graph.ProductIDByKey(ctx, d.ProductKey)
		if err != nil {
			res.Skipped[d.ExternalID] = err
			continue
		}
		if err := sc.Require(authz.ActionWriteSignals, productID); err != nil {
			res.Skipped[d.ExternalID] = err
			continue
		}
		acc := byID[d.AccountID]
		requests := d.RequestedFeatures
		if len(requests) == 0 && strings.TrimSpace(d.Notes) != "" {
			requests = []string{d.Notes}
		}
		for i, text := range requests {
			in := IngestInput{
				ProductID:   productID,
				Source:      SourceCRM,
				Text:        text,
				ExternalKey: fmt.Sprintf("crm:deal:%s:%d", d.ExternalID, i),
				AccountID:   d.AccountID,
				DealID:      d.ExternalID,
				Version:     d.Version,
				Segment:     acc.Segment,
				DealAmount:  d.Amount,
				AccountARR:  acc.ARR,
				BlocksDeal:  d.BlocksOnFeatures,
			}
			sig, err := s.Ingest(ctx, sc, in)
			if err != nil {
				return res, fmt.Errorf("сделка %s: %w", d.ExternalID, err)
			}
			res.Imported = append(res.Imported, sig)
		}
	}
	return res, nil
}

// Signal возвращает сигнал (приватный контур продукта).
func (s *Service) Signal(ctx context.Context, sc authz.Scope, id kernel.ID) (Signal, error) {
	sig, err := s.store.Get(ctx, id)
	if err != nil {
		return Signal{}, err
	}
	if err := sc.Require(authz.ActionReadPrivate, sig.ProductID); err != nil {
		return Signal{}, err
	}
	return sig, nil
}

// Signals возвращает сигналы продукта по фильтру (приватный контур).
func (s *Service) Signals(ctx context.Context, sc authz.Scope, productID kernel.ID, f Filter) ([]Signal, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	f.ProductID = productID
	return s.store.List(ctx, f)
}

// TriageQueue — очередь разбора продукта: сигналы new и in_review по сроку разбора,
// затем по дате приёма (SG-03).
func (s *Service) TriageQueue(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Signal, error) {
	out, err := s.Signals(ctx, sc, productID, Filter{Statuses: []Status{StatusNew, StatusInReview}})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.DueDate.IsZero() != b.DueDate.IsZero():
			return !a.DueDate.IsZero()
		case a.DueDate != b.DueDate:
			return a.DueDate.Before(b.DueDate)
		default:
			return a.CreatedAt.Before(b.CreatedAt)
		}
	})
	return out, nil
}

// TriageInput — решение по разбору (SG-03).
type TriageInput struct {
	Status  Status
	DueDate kernel.Date // пустая — срок не меняется
}

// Triage меняет статус разбора и срок. Допустимые статусы: new, in_review, rejected.
// linked выставляется только привязкой; merged — слиянием (этап 2).
func (s *Service) Triage(ctx context.Context, sc authz.Scope, id kernel.ID, in TriageInput) (Signal, error) {
	sig, err := s.store.Get(ctx, id)
	if err != nil {
		return Signal{}, err
	}
	if err := sc.Require(authz.ActionWriteSignals, sig.ProductID); err != nil {
		return Signal{}, err
	}
	switch in.Status {
	case StatusNew, StatusInReview, StatusRejected:
	case StatusLinked:
		return Signal{}, kernel.Invalid("status", "статус linked выставляется привязкой к фиче или контракту")
	case StatusMerged:
		return Signal{}, kernel.Invalid("status", "слияние сигналов — этап 2")
	default:
		return Signal{}, kernel.Invalid("status", fmt.Sprintf("неизвестный статус %q", in.Status))
	}
	if sig.Status == StatusMerged {
		return Signal{}, fmt.Errorf("%w: слитый сигнал не разбирается", kernel.ErrConflict)
	}
	wasLinked := sig.IsLinked()
	prev := sig
	sig.Status = in.Status
	if !in.DueDate.IsZero() {
		sig.DueDate = in.DueDate
	}
	if in.Status == StatusRejected {
		sig.FeatureID, sig.ContractID, sig.HypothesisID = kernel.NilID, kernel.NilID, kernel.NilID
	}
	sig.UpdatedAt = s.clock.Now()
	if err := s.store.Save(ctx, sig); err != nil {
		return Signal{}, fmt.Errorf("save signal: %w", err)
	}
	if err := s.emit(ctx, EventSignalTriaged, sig, sc.Subject()); err != nil {
		return Signal{}, err
	}
	if wasLinked && in.Status == StatusRejected {
		if err := s.recomputeTarget(ctx, sc, prev); err != nil {
			return Signal{}, err
		}
	}
	return sig, nil
}

// LinkToFeature привязывает сигнал к фиче своего продукта и пересчитывает собственную
// ценность фичи как сумму весов привязанных сигналов (SG-05, ТЗ 2.4).
func (s *Service) LinkToFeature(ctx context.Context, sc authz.Scope, id, featureID kernel.ID) (Signal, error) {
	sig, err := s.store.Get(ctx, id)
	if err != nil {
		return Signal{}, err
	}
	if err := sc.Require(authz.ActionWriteSignals, sig.ProductID); err != nil {
		return Signal{}, err
	}
	f, err := s.graph.Feature(ctx, sc, featureID)
	if err != nil {
		return Signal{}, fmt.Errorf("feature: %w", err)
	}
	if f.ProductID != sig.ProductID {
		return Signal{}, kernel.Invalid("feature_id", "фича другого продукта")
	}
	prev := sig
	sig.FeatureID, sig.ContractID, sig.HypothesisID = featureID, kernel.NilID, kernel.NilID
	return s.link(ctx, sc, prev, sig)
}

// LinkToContract привязывает сигнал к контракту, одной из сторон которого является продукт
// сигнала, и пересчитывает ценность контракта как сумму весов привязанных сигналов (SG-05, ТЗ 2.4).
func (s *Service) LinkToContract(ctx context.Context, sc authz.Scope, id, contractID kernel.ID) (Signal, error) {
	sig, err := s.store.Get(ctx, id)
	if err != nil {
		return Signal{}, err
	}
	if err := sc.Require(authz.ActionWriteSignals, sig.ProductID); err != nil {
		return Signal{}, err
	}
	c, _, err := s.graph.Contract(ctx, sc, contractID)
	if err != nil {
		return Signal{}, fmt.Errorf("contract: %w", err)
	}
	if c.ProviderProductID != sig.ProductID && c.ConsumerProductID != sig.ProductID {
		return Signal{}, kernel.Invalid("contract_id", "продукт сигнала не участвует в контракте")
	}
	prev := sig
	sig.FeatureID, sig.ContractID, sig.HypothesisID = kernel.NilID, contractID, kernel.NilID
	return s.link(ctx, sc, prev, sig)
}

func (s *Service) link(ctx context.Context, sc authz.Scope, prev, sig Signal) (Signal, error) {
	if sig.Status == StatusMerged {
		return Signal{}, fmt.Errorf("%w: слитый сигнал не привязывается", kernel.ErrConflict)
	}
	sig.Status = StatusLinked
	sig.UpdatedAt = s.clock.Now()
	if err := s.store.Save(ctx, sig); err != nil {
		return Signal{}, fmt.Errorf("save signal: %w", err)
	}
	if err := s.emit(ctx, EventSignalLinked, sig, sc.Subject()); err != nil {
		return Signal{}, err
	}
	if prev.IsLinked() && (prev.FeatureID != sig.FeatureID || prev.ContractID != sig.ContractID) {
		if err := s.recomputeTarget(ctx, sc, prev); err != nil {
			return Signal{}, err
		}
	}
	if err := s.recomputeTarget(ctx, sc, sig); err != nil {
		return Signal{}, err
	}
	return sig, nil
}

// recomputeTarget передаёт в portfoliograph сумму весов сигналов, привязанных к цели сигнала.
func (s *Service) recomputeTarget(ctx context.Context, sc authz.Scope, sig Signal) error {
	switch {
	case sig.FeatureID != kernel.NilID:
		total, err := s.sumWeights(ctx, Filter{FeatureID: sig.FeatureID})
		if err != nil {
			return err
		}
		if err := s.graph.SetFeatureOwnValue(ctx, sc, sig.FeatureID, total); err != nil {
			return fmt.Errorf("feature value: %w", err)
		}
	case sig.ContractID != kernel.NilID:
		total, err := s.sumWeights(ctx, Filter{ContractID: sig.ContractID})
		if err != nil {
			return err
		}
		if err := s.graph.SetContractSignalValue(ctx, sc, sig.ContractID, total); err != nil {
			return fmt.Errorf("contract value: %w", err)
		}
	}
	return nil
}

func (s *Service) sumWeights(ctx context.Context, f Filter) (kernel.Money, error) {
	f.Statuses = []Status{StatusLinked}
	list, err := s.store.List(ctx, f)
	if err != nil {
		return kernel.Money{}, fmt.Errorf("list signals: %w", err)
	}
	var total kernel.Money
	for _, sig := range list {
		if total, err = total.Add(sig.Weight); err != nil {
			return kernel.Money{}, err
		}
	}
	return total, nil
}

// ARRByFeature — сумма ARR аккаунтов, запросивших фичу; каждый аккаунт учитывается один раз
// (для prioritization, PR-01). Право: приватный контур продукта фичи.
func (s *Service) ARRByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error) {
	f, err := s.graph.Feature(ctx, sc, featureID)
	if err != nil {
		return kernel.Money{}, fmt.Errorf("feature: %w", err)
	}
	if err := sc.Require(authz.ActionReadPrivate, f.ProductID); err != nil {
		return kernel.Money{}, err
	}
	list, err := s.store.List(ctx, Filter{FeatureID: featureID, Statuses: []Status{StatusLinked}})
	if err != nil {
		return kernel.Money{}, fmt.Errorf("list signals: %w", err)
	}
	var total kernel.Money
	seen := map[string]struct{}{}
	for _, sig := range list {
		if sig.AccountID == "" {
			continue
		}
		if _, ok := seen[sig.AccountID]; ok {
			continue
		}
		seen[sig.AccountID] = struct{}{}
		if total, err = total.Add(sig.AccountARR); err != nil {
			return kernel.Money{}, err
		}
	}
	return total, nil
}

// BlockedDealsByFeature — сумма сделок, которые не закроются без фичи (BlocksDeal);
// каждая сделка учитывается один раз (для prioritization, PR-01).
func (s *Service) BlockedDealsByFeature(ctx context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error) {
	f, err := s.graph.Feature(ctx, sc, featureID)
	if err != nil {
		return kernel.Money{}, fmt.Errorf("feature: %w", err)
	}
	if err := sc.Require(authz.ActionReadPrivate, f.ProductID); err != nil {
		return kernel.Money{}, err
	}
	list, err := s.store.List(ctx, Filter{FeatureID: featureID, Statuses: []Status{StatusLinked}})
	if err != nil {
		return kernel.Money{}, fmt.Errorf("list signals: %w", err)
	}
	var total kernel.Money
	seen := map[string]struct{}{}
	for _, sig := range list {
		if !sig.BlocksDeal || sig.DealID == "" {
			continue
		}
		if _, ok := seen[sig.DealID]; ok {
			continue
		}
		seen[sig.DealID] = struct{}{}
		if total, err = total.Add(sig.Weight); err != nil {
			return kernel.Money{}, err
		}
	}
	return total, nil
}
