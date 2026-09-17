package httpapi

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/signals"
)

// ---- signals ----

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toSignal(s signals.Signal) gen.Signal {
	return gen.Signal{
		Id: s.ID, ProductId: s.ProductID, Source: gen.SignalSource(s.Source), Text: s.Text, ExternalKey: strPtr(s.ExternalKey),
		AccountId: strPtr(s.AccountID), DealId: strPtr(s.DealID), Version: strPtr(s.Version), Segment: strPtr(s.Segment),
		Weight: money(s.Weight), AccountArr: money(s.AccountARR), BlocksDeal: s.BlocksDeal, Status: gen.SignalStatus(s.Status),
		DueDate: datePtr(s.DueDate), FeatureId: idPtr(s.FeatureID), ContractId: idPtr(s.ContractID),
		CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func moneyOf(m *gen.Money) kernel.Money {
	if m == nil {
		return kernel.Money{}
	}
	return kernel.Money{Amount: m.Amount, Currency: m.Currency}
}

func (s *Server) requireSignals() error {
	if s.d.Signals == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// ListSignals — сигналы продукта.
func (s *Server) ListSignals(ctx context.Context, req gen.ListSignalsRequestObject) (gen.ListSignalsResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	f := signals.Filter{FeatureID: idOrNil(req.Params.FeatureId)}
	if req.Params.Status != nil {
		for _, st := range *req.Params.Status {
			f.Statuses = append(f.Statuses, signals.Status(st))
		}
	}
	list, err := s.d.Signals.Signals(ctx, scope(ctx), req.ProductId, f)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListSignals200JSONResponse, 0, len(list))
	for _, sg := range list {
		out = append(out, toSignal(sg))
	}
	return out, nil
}

// IngestSignal — SG-01.
func (s *Server) IngestSignal(ctx context.Context, req gen.IngestSignalRequestObject) (gen.IngestSignalResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	b := req.Body
	sg, err := s.d.Signals.Ingest(ctx, scope(ctx), signals.IngestInput{
		ProductID: req.ProductId, Source: signals.Source(b.Source), Text: b.Text, ExternalKey: strOrEmpty(b.ExternalKey),
		AccountID: strOrEmpty(b.AccountId), DealID: strOrEmpty(b.DealId), Version: strOrEmpty(b.Version), Segment: strOrEmpty(b.Segment),
		DealAmount: moneyOf(b.DealAmount), AccountARR: moneyOf(b.AccountArr), BlocksDeal: boolOr(b.BlocksDeal),
	})
	if err != nil {
		return nil, err
	}
	return gen.IngestSignal201JSONResponse(toSignal(sg)), nil
}

// TriageQueue — SG-03.
func (s *Server) TriageQueue(ctx context.Context, req gen.TriageQueueRequestObject) (gen.TriageQueueResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	list, err := s.d.Signals.TriageQueue(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.TriageQueue200JSONResponse, 0, len(list))
	for _, sg := range list {
		out = append(out, toSignal(sg))
	}
	return out, nil
}

// ImportSignalsFromCRM — SG-01 через порт CRM.
func (s *Server) ImportSignalsFromCRM(ctx context.Context, _ gen.ImportSignalsFromCRMRequestObject) (gen.ImportSignalsFromCRMResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	if s.d.CRM == nil {
		return nil, kernel.ErrUnavailable
	}
	res, err := s.d.Signals.ImportFromCRM(ctx, scope(ctx), s.d.CRM)
	if err != nil {
		return nil, err
	}
	out := gen.ImportResult{Imported: len(res.Imported), Skipped: map[string]string{}}
	for k, e := range res.Skipped {
		out.Skipped[k] = e.Error()
	}
	return gen.ImportSignalsFromCRM200JSONResponse(out), nil
}

// GetSignal — сигнал.
func (s *Server) GetSignal(ctx context.Context, req gen.GetSignalRequestObject) (gen.GetSignalResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	sg, err := s.d.Signals.Signal(ctx, scope(ctx), req.SignalId)
	if err != nil {
		return nil, err
	}
	return gen.GetSignal200JSONResponse(toSignal(sg)), nil
}

// TriageSignal — SG-03.
func (s *Server) TriageSignal(ctx context.Context, req gen.TriageSignalRequestObject) (gen.TriageSignalResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	sg, err := s.d.Signals.Triage(ctx, scope(ctx), req.SignalId, signals.TriageInput{Status: signals.Status(req.Body.Status), DueDate: dateOf(req.Body.DueDate)})
	if err != nil {
		return nil, err
	}
	return gen.TriageSignal200JSONResponse(toSignal(sg)), nil
}

// LinkSignal — SG-05.
func (s *Server) LinkSignal(ctx context.Context, req gen.LinkSignalRequestObject) (gen.LinkSignalResponseObject, error) {
	if err := s.requireSignals(); err != nil {
		return nil, err
	}
	var (
		sg  signals.Signal
		err error
	)
	switch {
	case req.Body.FeatureId != nil && req.Body.ContractId != nil:
		return nil, kernel.Invalid("link", "укажите фичу или контракт, не оба")
	case req.Body.FeatureId != nil:
		sg, err = s.d.Signals.LinkToFeature(ctx, scope(ctx), req.SignalId, *req.Body.FeatureId)
	case req.Body.ContractId != nil:
		sg, err = s.d.Signals.LinkToContract(ctx, scope(ctx), req.SignalId, *req.Body.ContractId)
	default:
		return nil, kernel.Invalid("link", "укажите фичу или контракт")
	}
	if err != nil {
		return nil, err
	}
	return gen.LinkSignal200JSONResponse(toSignal(sg)), nil
}

// ---- prioritization ----

func toModel(m prioritization.ScoringModel) gen.ScoringModel {
	inputs := m.Inputs
	if inputs == nil {
		inputs = []string{}
	}
	return gen.ScoringModel{Id: m.ID, ProductId: idPtr(m.ProductID), Name: m.Name, Type: gen.ScoringModelType(m.Type), Formula: m.Formula, Inputs: inputs, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

func toModelInput(in gen.ScoringModelInput) prioritization.ModelInput {
	out := prioritization.ModelInput{ProductID: idOrNil(in.ProductId), Name: in.Name, Type: prioritization.ModelType(in.Type), Formula: strOrEmpty(in.Formula)}
	if in.Inputs != nil {
		out.Inputs = *in.Inputs
	}
	return out
}

func toScore(r prioritization.ScoreResult) gen.ScoreResult {
	comps := make([]gen.ScoreComponent, 0, len(r.Components))
	for _, c := range r.Components {
		comps = append(comps, gen.ScoreComponent{Name: c.Name, Value: c.Value.String(), System: c.System})
	}
	return gen.ScoreResult{ModelId: r.ModelID, FeatureId: r.FeatureID, ProductId: r.ProductID, Score: r.Score.String(), Components: comps, Explanation: r.Explanation}
}

func (s *Server) requirePrioritization() error {
	if s.d.Prioritization == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// ListScoringModels — PR-01.
func (s *Server) ListScoringModels(ctx context.Context, _ gen.ListScoringModelsRequestObject) (gen.ListScoringModelsResponseObject, error) {
	if err := s.requirePrioritization(); err != nil {
		return nil, err
	}
	ms, err := s.d.Prioritization.Models(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListScoringModels200JSONResponse, 0, len(ms))
	for _, m := range ms {
		out = append(out, toModel(m))
	}
	return out, nil
}

// CreateScoringModel — PR-01.
func (s *Server) CreateScoringModel(ctx context.Context, req gen.CreateScoringModelRequestObject) (gen.CreateScoringModelResponseObject, error) {
	if err := s.requirePrioritization(); err != nil {
		return nil, err
	}
	m, err := s.d.Prioritization.CreateModel(ctx, scope(ctx), toModelInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateScoringModel201JSONResponse(toModel(m)), nil
}

// UpdateScoringModel — PR-01.
func (s *Server) UpdateScoringModel(ctx context.Context, req gen.UpdateScoringModelRequestObject) (gen.UpdateScoringModelResponseObject, error) {
	if err := s.requirePrioritization(); err != nil {
		return nil, err
	}
	m, err := s.d.Prioritization.UpdateModel(ctx, scope(ctx), req.ModelId, toModelInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateScoringModel200JSONResponse(toModel(m)), nil
}

// SetFeatureScoreInputs — входные переменные фичи; возвращает скор.
func (s *Server) SetFeatureScoreInputs(ctx context.Context, req gen.SetFeatureScoreInputsRequestObject) (gen.SetFeatureScoreInputsResponseObject, error) {
	if err := s.requirePrioritization(); err != nil {
		return nil, err
	}
	f, err := s.d.Portfolio.Feature(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	values := map[string]decimal.Decimal{}
	for k, v := range req.Body.Values {
		d, err := decimal.NewFromString(v)
		if err != nil {
			return nil, kernel.Invalid("values", "значение "+k+" не число")
		}
		values[k] = d
	}
	if _, err := s.d.Prioritization.SetFeatureInputs(ctx, scope(ctx), req.ModelId, f.ProductID, req.FeatureId, values); err != nil {
		return nil, err
	}
	r, err := s.d.Prioritization.Score(ctx, scope(ctx), req.ModelId, req.FeatureId)
	if err != nil {
		return nil, err
	}
	return gen.SetFeatureScoreInputs200JSONResponse(toScore(r)), nil
}

// GetRanking — PR-01…PR-03.
func (s *Server) GetRanking(ctx context.Context, req gen.GetRankingRequestObject) (gen.GetRankingResponseObject, error) {
	if err := s.requirePrioritization(); err != nil {
		return nil, err
	}
	rs, err := s.d.Prioritization.Ranking(ctx, scope(ctx), req.ModelId, req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetRanking200JSONResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, toScore(r))
	}
	return out, nil
}

// ---- roadmap ----

func toItem(it roadmap.RoadmapItem) gen.RoadmapItem {
	return gen.RoadmapItem{
		Id: it.ID, ProductId: it.ProductID, FeatureId: idPtr(it.FeatureID), Title: it.Title, Bucket: gen.RoadmapItemBucket(it.Bucket),
		StartDate: datePtr(it.StartDate), EndDate: datePtr(it.EndDate), ReleaseId: idPtr(it.ReleaseID),
		Audience: gen.RoadmapItemAudience(it.Audience), Status: gen.RoadmapItemStatus(it.Status), CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
	}
}

func toSafe(it roadmap.SalesSafeItem) gen.SalesSafeItem {
	return gen.SalesSafeItem{Id: it.ID, ProductId: it.ProductID, Title: it.Title, Bucket: gen.SalesSafeItemBucket(it.Bucket), StartDate: datePtr(it.StartDate), EndDate: datePtr(it.EndDate), ReleaseId: idPtr(it.ReleaseID)}
}

func toItems(in []roadmap.RoadmapItem) *[]gen.RoadmapItem {
	if in == nil {
		return nil
	}
	out := make([]gen.RoadmapItem, 0, len(in))
	for _, it := range in {
		out = append(out, toItem(it))
	}
	return &out
}

func toSafes(in []roadmap.SalesSafeItem) *[]gen.SalesSafeItem {
	if in == nil {
		return nil
	}
	out := make([]gen.SalesSafeItem, 0, len(in))
	for _, it := range in {
		out = append(out, toSafe(it))
	}
	return &out
}

func toGroup(g roadmap.BucketGroup) gen.RoadmapGroup {
	return gen.RoadmapGroup{Items: toItems(g.Items), SalesSafe: toSafes(g.SalesSafe)}
}

func toRelease(r roadmap.Release) gen.Release {
	return gen.Release{Id: r.ID, ProductId: r.ProductID, Name: r.Name, Version: r.Version, PlannedDate: datePtr(r.PlannedDate), Status: gen.ReleaseStatus(r.Status), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func toItemInput(in gen.RoadmapItemInput) roadmap.ItemInput {
	out := roadmap.ItemInput{FeatureID: idOrNil(in.FeatureId), Title: strOrEmpty(in.Title), StartDate: dateOf(in.StartDate), EndDate: dateOf(in.EndDate), ReleaseID: idOrNil(in.ReleaseId)}
	if in.Bucket != nil {
		out.Bucket = roadmap.Bucket(*in.Bucket)
	}
	if in.Audience != nil {
		out.Audience = authz.Audience(*in.Audience)
	}
	if in.Status != nil {
		out.Status = roadmap.ItemStatus(*in.Status)
	}
	return out
}

func (s *Server) requireRoadmap() error {
	if s.d.Roadmap == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// ListRoadmapItems — элементы (внутренняя аудитория).
func (s *Server) ListRoadmapItems(ctx context.Context, req gen.ListRoadmapItemsRequestObject) (gen.ListRoadmapItemsResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	items, err := s.d.Roadmap.Items(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListRoadmapItems200JSONResponse, 0, len(items))
	for _, it := range items {
		out = append(out, toItem(it))
	}
	return out, nil
}

// CreateRoadmapItem — создание элемента.
func (s *Server) CreateRoadmapItem(ctx context.Context, req gen.CreateRoadmapItemRequestObject) (gen.CreateRoadmapItemResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	it, err := s.d.Roadmap.CreateItem(ctx, scope(ctx), req.ProductId, toItemInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateRoadmapItem201JSONResponse(toItem(it)), nil
}

// GetRoadmapTimeline — RM-01/RM-02.
func (s *Server) GetRoadmapTimeline(ctx context.Context, req gen.GetRoadmapTimelineRequestObject) (gen.GetRoadmapTimelineResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	t, err := s.d.Roadmap.Timeline(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	return gen.GetRoadmapTimeline200JSONResponse{ProductId: t.ProductID, Audience: gen.RoadmapTimelineAudience(t.Audience), Items: toItems(t.Items), SalesSafe: toSafes(t.SalesSafe)}, nil
}

// GetRoadmapNowNextLater — RM-01.
func (s *Server) GetRoadmapNowNextLater(ctx context.Context, req gen.GetRoadmapNowNextLaterRequestObject) (gen.GetRoadmapNowNextLaterResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	v, err := s.d.Roadmap.NowNextLater(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	return gen.GetRoadmapNowNextLater200JSONResponse{ProductId: v.ProductID, Audience: gen.RoadmapNowNextLaterAudience(v.Audience), Now: toGroup(v.Now), Next: toGroup(v.Next), Later: toGroup(v.Later)}, nil
}

// GetRoadmapByRelease — RM-01.
func (s *Server) GetRoadmapByRelease(ctx context.Context, req gen.GetRoadmapByReleaseRequestObject) (gen.GetRoadmapByReleaseResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	v, err := s.d.Roadmap.ByRelease(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := gen.RoadmapByRelease{ProductId: v.ProductID, Audience: gen.RoadmapByReleaseAudience(v.Audience), Releases: []gen.ReleaseGroup{}, Unassigned: toGroup(v.Unassigned)}
	for _, g := range v.Releases {
		out.Releases = append(out.Releases, gen.ReleaseGroup{Release: toRelease(g.Release), Items: toItems(g.Items), SalesSafe: toSafes(g.SalesSafe)})
	}
	return gen.GetRoadmapByRelease200JSONResponse(out), nil
}

// ListReleases — релизы.
func (s *Server) ListReleases(ctx context.Context, req gen.ListReleasesRequestObject) (gen.ListReleasesResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	rs, err := s.d.Roadmap.Releases(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListReleases200JSONResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, toRelease(r))
	}
	return out, nil
}

// CreateRelease — релиз.
func (s *Server) CreateRelease(ctx context.Context, req gen.CreateReleaseRequestObject) (gen.CreateReleaseResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	in := roadmap.ReleaseInput{Name: req.Body.Name, Version: req.Body.Version, PlannedDate: dateOf(req.Body.PlannedDate)}
	if req.Body.Status != nil {
		in.Status = roadmap.ReleaseStatus(*req.Body.Status)
	}
	r, err := s.d.Roadmap.CreateRelease(ctx, scope(ctx), req.ProductId, in)
	if err != nil {
		return nil, err
	}
	return gen.CreateRelease201JSONResponse(toRelease(r)), nil
}

// UpdateRoadmapItem — изменение без дат.
func (s *Server) UpdateRoadmapItem(ctx context.Context, req gen.UpdateRoadmapItemRequestObject) (gen.UpdateRoadmapItemResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	it, err := s.d.Roadmap.UpdateItem(ctx, scope(ctx), req.ItemId, toItemInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateRoadmapItem200JSONResponse(toItem(it)), nil
}

// ChangeRoadmapItemDates — RM-03.
func (s *Server) ChangeRoadmapItemDates(ctx context.Context, req gen.ChangeRoadmapItemDatesRequestObject) (gen.ChangeRoadmapItemDatesResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	it, err := s.d.Roadmap.ChangeDates(ctx, scope(ctx), req.ItemId, dateOf(req.Body.StartDate), dateOf(req.Body.EndDate), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return gen.ChangeRoadmapItemDates200JSONResponse(toItem(it)), nil
}

// GetRoadmapItemHistory — RM-03.
func (s *Server) GetRoadmapItemHistory(ctx context.Context, req gen.GetRoadmapItemHistoryRequestObject) (gen.GetRoadmapItemHistoryResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	hs, err := s.d.Roadmap.DateHistory(ctx, scope(ctx), req.ItemId)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetRoadmapItemHistory200JSONResponse, 0, len(hs))
	for _, h := range hs {
		out = append(out, gen.DateChange{Id: h.ID, ItemId: h.ItemID, ProductId: h.ProductID, OldStart: datePtr(h.OldStart), OldEnd: datePtr(h.OldEnd), NewStart: datePtr(h.NewStart), NewEnd: datePtr(h.NewEnd), Reason: h.Reason, Actor: h.Actor, At: h.At, EventId: idPtr(h.EventID)})
	}
	return out, nil
}
