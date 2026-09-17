package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

// ---- discovery: DS-01…DS-04, SG-04, AD-03 ----

func (s *Server) requireDiscovery() error {
	if s.d.Discovery == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

func strsPtr(in []string) *[]string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	return &out
}

func strsOf(in *[]string) []string {
	if in == nil {
		return nil
	}
	return *in
}

func idsPtr(in []kernel.ID) *[]openapi_types.UUID {
	if len(in) == 0 {
		return nil
	}
	return ptr(ids(in))
}

func toHypothesis(h discovery.Hypothesis) gen.Hypothesis {
	out := gen.Hypothesis{
		Id: h.ID, ProductId: h.ProductID, Title: h.Title, Statement: h.Statement, Assumptions: strsPtr(h.Assumptions),
		ConfirmationCriterion: h.ConfirmationCriterion, Status: string(h.Status), Resolution: strPtr(h.Resolution),
		FeatureId: idPtr(h.FeatureID), CreatedBy: h.CreatedBy, CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
	}
	if len(h.CustomFields) > 0 {
		cf := map[string]any(h.CustomFields)
		out.CustomFields = &cf
	}
	return out
}

func toHypothesisInput(id, productID kernel.ID, in gen.HypothesisInput) discovery.HypothesisInput {
	out := discovery.HypothesisInput{
		ID: id, ProductID: productID, Title: in.Title, Statement: in.Statement, Assumptions: strsOf(in.Assumptions),
		ConfirmationCriterion: in.ConfirmationCriterion, FeatureID: idOrNil(in.FeatureId),
	}
	if in.CustomFields != nil {
		out.CustomFields = *in.CustomFields
	}
	return out
}

// ListHypotheses — DS-01.
func (s *Server) ListHypotheses(ctx context.Context, req gen.ListHypothesesRequestObject) (gen.ListHypothesesResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	f := discovery.HypothesisFilter{FeatureID: idOrNil(req.Params.FeatureId)}
	for _, st := range strsOf(req.Params.Status) {
		f.Statuses = append(f.Statuses, discovery.HypothesisStatus(st))
	}
	list, err := s.d.Discovery.Hypotheses(ctx, scope(ctx), req.ProductId, f)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListHypotheses200JSONResponse, 0, len(list))
	for _, h := range list {
		out = append(out, toHypothesis(h))
	}
	return out, nil
}

// CreateHypothesis — DS-01.
func (s *Server) CreateHypothesis(ctx context.Context, req gen.CreateHypothesisRequestObject) (gen.CreateHypothesisResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	h, err := s.d.Discovery.SaveHypothesis(ctx, scope(ctx), toHypothesisInput(kernel.NilID, req.ProductId, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateHypothesis201JSONResponse(toHypothesis(h)), nil
}

// GetHypothesis — гипотеза.
func (s *Server) GetHypothesis(ctx context.Context, req gen.GetHypothesisRequestObject) (gen.GetHypothesisResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	h, err := s.d.Discovery.Hypothesis(ctx, scope(ctx), req.HypothesisId)
	if err != nil {
		return nil, err
	}
	return gen.GetHypothesis200JSONResponse(toHypothesis(h)), nil
}

// UpdateHypothesis — DS-01 (продукт берётся из существующей гипотезы).
func (s *Server) UpdateHypothesis(ctx context.Context, req gen.UpdateHypothesisRequestObject) (gen.UpdateHypothesisResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	cur, err := s.d.Discovery.Hypothesis(ctx, scope(ctx), req.HypothesisId)
	if err != nil {
		return nil, err
	}
	h, err := s.d.Discovery.SaveHypothesis(ctx, scope(ctx), toHypothesisInput(cur.ID, cur.ProductID, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateHypothesis200JSONResponse(toHypothesis(h)), nil
}

// ChangeHypothesisStatus — DS-01, AD-03.
func (s *Server) ChangeHypothesisStatus(ctx context.Context, req gen.ChangeHypothesisStatusRequestObject) (gen.ChangeHypothesisStatusResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	h, err := s.d.Discovery.ChangeHypothesisStatus(ctx, scope(ctx), req.HypothesisId, discovery.StatusChange{Status: discovery.HypothesisStatus(req.Body.Status), Resolution: strOrEmpty(req.Body.Resolution)})
	if err != nil {
		return nil, err
	}
	return gen.ChangeHypothesisStatus200JSONResponse(toHypothesis(h)), nil
}

// ---- интервью (DS-02) ----

func toInterview(i discovery.Interview) gen.Interview {
	return gen.Interview{
		Id: i.ID, ProductId: i.ProductID, AccountId: strPtr(i.AccountID), Segment: strPtr(i.Segment), Date: openapi_types.Date{Time: i.Date.Time()},
		Participants: strsPtr(i.Participants), Notes: strPtr(i.Notes), HypothesisIds: idsPtr(i.HypothesisIDs),
		CreatedBy: i.CreatedBy, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

func toInterviewInput(id, productID kernel.ID, in gen.InterviewInput) discovery.InterviewInput {
	return discovery.InterviewInput{
		ID: id, ProductID: productID, AccountID: strOrEmpty(in.AccountId), Segment: strOrEmpty(in.Segment), Date: kernel.DateFromTime(in.Date.Time),
		Participants: strsOf(in.Participants), Notes: strOrEmpty(in.Notes), HypothesisIDs: kids(in.HypothesisIds),
	}
}

// ListInterviews — DS-02.
func (s *Server) ListInterviews(ctx context.Context, req gen.ListInterviewsRequestObject) (gen.ListInterviewsResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	list, err := s.d.Discovery.Interviews(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListInterviews200JSONResponse, 0, len(list))
	for _, i := range list {
		out = append(out, toInterview(i))
	}
	return out, nil
}

// CreateInterview — DS-02.
func (s *Server) CreateInterview(ctx context.Context, req gen.CreateInterviewRequestObject) (gen.CreateInterviewResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	i, err := s.d.Discovery.SaveInterview(ctx, scope(ctx), toInterviewInput(kernel.NilID, req.ProductId, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateInterview201JSONResponse(toInterview(i)), nil
}

// GetInterview — интервью.
func (s *Server) GetInterview(ctx context.Context, req gen.GetInterviewRequestObject) (gen.GetInterviewResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	i, err := s.d.Discovery.Interview(ctx, scope(ctx), req.InterviewId)
	if err != nil {
		return nil, err
	}
	return gen.GetInterview200JSONResponse(toInterview(i)), nil
}

// UpdateInterview — DS-02.
func (s *Server) UpdateInterview(ctx context.Context, req gen.UpdateInterviewRequestObject) (gen.UpdateInterviewResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	cur, err := s.d.Discovery.Interview(ctx, scope(ctx), req.InterviewId)
	if err != nil {
		return nil, err
	}
	i, err := s.d.Discovery.SaveInterview(ctx, scope(ctx), toInterviewInput(cur.ID, cur.ProductID, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateInterview200JSONResponse(toInterview(i)), nil
}

// ---- инсайты (DS-02) ----

func toInsight(i discovery.Insight) gen.Insight {
	return gen.Insight{
		Id: i.ID, ProductId: i.ProductID, Text: i.Text, InterviewId: idPtr(i.InterviewID), HypothesisIds: idsPtr(i.HypothesisIDs),
		SignalIds: idsPtr(i.SignalIDs), Confidence: gen.InsightConfidence(i.Confidence), CreatedBy: i.CreatedBy, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

func toInsightInput(id, productID kernel.ID, in gen.InsightInput) discovery.InsightInput {
	return discovery.InsightInput{
		ID: id, ProductID: productID, Text: in.Text, InterviewID: idOrNil(in.InterviewId), HypothesisIDs: kids(in.HypothesisIds),
		SignalIDs: kids(in.SignalIds), Confidence: discovery.Confidence(in.Confidence),
	}
}

// ListInsights — DS-02.
func (s *Server) ListInsights(ctx context.Context, req gen.ListInsightsRequestObject) (gen.ListInsightsResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	f := discovery.InsightFilter{InterviewID: idOrNil(req.Params.InterviewId), HypothesisID: idOrNil(req.Params.HypothesisId), SignalID: idOrNil(req.Params.SignalId)}
	list, err := s.d.Discovery.Insights(ctx, scope(ctx), req.ProductId, f)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListInsights200JSONResponse, 0, len(list))
	for _, i := range list {
		out = append(out, toInsight(i))
	}
	return out, nil
}

// CreateInsight — DS-02.
func (s *Server) CreateInsight(ctx context.Context, req gen.CreateInsightRequestObject) (gen.CreateInsightResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	i, err := s.d.Discovery.SaveInsight(ctx, scope(ctx), toInsightInput(kernel.NilID, req.ProductId, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateInsight201JSONResponse(toInsight(i)), nil
}

// GetInsight — инсайт.
func (s *Server) GetInsight(ctx context.Context, req gen.GetInsightRequestObject) (gen.GetInsightResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	i, err := s.d.Discovery.Insight(ctx, scope(ctx), req.InsightId)
	if err != nil {
		return nil, err
	}
	return gen.GetInsight200JSONResponse(toInsight(i)), nil
}

// UpdateInsight — DS-02.
func (s *Server) UpdateInsight(ctx context.Context, req gen.UpdateInsightRequestObject) (gen.UpdateInsightResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	cur, err := s.d.Discovery.Insight(ctx, scope(ctx), req.InsightId)
	if err != nil {
		return nil, err
	}
	i, err := s.d.Discovery.SaveInsight(ctx, scope(ctx), toInsightInput(cur.ID, cur.ProductID, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateInsight200JSONResponse(toInsight(i)), nil
}

// ---- evidence (DS-03) ----

func toEvidence(e discovery.Evidence) gen.Evidence {
	return gen.Evidence{
		Id: e.ID, ProductId: e.ProductID, Source: e.Source, SourceRef: strPtr(e.SourceRef), Date: openapi_types.Date{Time: e.Date.Time()},
		Trust: gen.EvidenceTrust(e.Trust), Verification: gen.EvidenceVerification(e.Verification), Sha256: strPtr(e.SHA256),
		HypothesisId: idPtr(e.HypothesisID), InsightId: idPtr(e.InsightID), FeatureId: idPtr(e.FeatureID),
		CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

func toEvidenceInput(id, productID kernel.ID, in gen.EvidenceInput) discovery.EvidenceInput {
	out := discovery.EvidenceInput{
		ID: id, ProductID: productID, Source: in.Source, SourceRef: strOrEmpty(in.SourceRef), Date: kernel.DateFromTime(in.Date.Time),
		Trust: discovery.Confidence(in.Trust), SHA256: strOrEmpty(in.Sha256),
		HypothesisID: idOrNil(in.HypothesisId), InsightID: idOrNil(in.InsightId), FeatureID: idOrNil(in.FeatureId),
	}
	if in.Verification != nil {
		out.Verification = discovery.Verification(*in.Verification)
	}
	return out
}

// ListEvidence — DS-03.
func (s *Server) ListEvidence(ctx context.Context, req gen.ListEvidenceRequestObject) (gen.ListEvidenceResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	f := discovery.EvidenceFilter{HypothesisID: idOrNil(req.Params.HypothesisId), InsightID: idOrNil(req.Params.InsightId), FeatureID: idOrNil(req.Params.FeatureId)}
	if req.Params.Verification != nil {
		f.Verification = discovery.Verification(*req.Params.Verification)
	}
	list, err := s.d.Discovery.EvidenceList(ctx, scope(ctx), req.ProductId, f)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListEvidence200JSONResponse, 0, len(list))
	for _, e := range list {
		out = append(out, toEvidence(e))
	}
	return out, nil
}

// CreateEvidence — DS-03.
func (s *Server) CreateEvidence(ctx context.Context, req gen.CreateEvidenceRequestObject) (gen.CreateEvidenceResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	e, err := s.d.Discovery.SaveEvidence(ctx, scope(ctx), toEvidenceInput(kernel.NilID, req.ProductId, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateEvidence201JSONResponse(toEvidence(e)), nil
}

// GetEvidence — evidence.
func (s *Server) GetEvidence(ctx context.Context, req gen.GetEvidenceRequestObject) (gen.GetEvidenceResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	e, err := s.d.Discovery.Evidence(ctx, scope(ctx), req.EvidenceId)
	if err != nil {
		return nil, err
	}
	return gen.GetEvidence200JSONResponse(toEvidence(e)), nil
}

// UpdateEvidence — DS-03.
func (s *Server) UpdateEvidence(ctx context.Context, req gen.UpdateEvidenceRequestObject) (gen.UpdateEvidenceResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	cur, err := s.d.Discovery.Evidence(ctx, scope(ctx), req.EvidenceId)
	if err != nil {
		return nil, err
	}
	e, err := s.d.Discovery.SaveEvidence(ctx, scope(ctx), toEvidenceInput(cur.ID, cur.ProductID, *req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateEvidence200JSONResponse(toEvidence(e)), nil
}

// ---- трассировка (DS-04) ----

func toTraceRef(r discovery.TraceRef) gen.TraceRef {
	return gen.TraceRef{Kind: gen.TraceRefKind(r.Kind), Id: r.ID}
}

// GetTrace — DS-04.
func (s *Server) GetTrace(ctx context.Context, req gen.GetTraceRequestObject) (gen.GetTraceResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	g, err := s.d.Discovery.Trace(ctx, scope(ctx), discovery.TraceKind(req.Kind), req.Id)
	if err != nil {
		return nil, err
	}
	out := gen.TraceGraph{Root: toTraceRef(g.Root), Nodes: make([]gen.TraceNode, 0, len(g.Nodes)), Edges: make([]gen.TraceEdge, 0, len(g.Edges))}
	for _, n := range g.Nodes {
		out.Nodes = append(out.Nodes, gen.TraceNode{Kind: gen.TraceNodeKind(n.Kind), Id: n.ID, ProductId: idPtr(n.ProductID), Title: n.Title})
	}
	for _, e := range g.Edges {
		out.Edges = append(out.Edges, gen.TraceEdge{From: toTraceRef(e.From), To: toTraceRef(e.To)})
	}
	return gen.GetTrace200JSONResponse(out), nil
}

// ---- похожие сигналы и слияние (SG-04) ----

// GetSimilarSignals — SG-04.
func (s *Server) GetSimilarSignals(ctx context.Context, req gen.GetSimilarSignalsRequestObject) (gen.GetSimilarSignalsResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	limit := 10
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	list, err := s.d.Discovery.SimilarSignals(ctx, scope(ctx), req.SignalId, limit)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetSimilarSignals200JSONResponse, 0, len(list))
	for _, m := range list {
		out = append(out, gen.SimilarSignal{Signal: toSignal(m.Signal), Score: m.Score})
	}
	return out, nil
}

// MergeSignals — SG-04.
func (s *Server) MergeSignals(ctx context.Context, req gen.MergeSignalsRequestObject) (gen.MergeSignalsResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	dups := make([]kernel.ID, 0, len(req.Body.DuplicateIds))
	dups = append(dups, req.Body.DuplicateIds...)
	if err := s.d.Discovery.MergeSignals(ctx, scope(ctx), req.SignalId, dups); err != nil {
		return nil, err
	}
	return gen.MergeSignals204Response{}, nil
}

// ---- кастомные поля и статусы (AD-03) ----

func toFieldDef(d discovery.CustomFieldDef) gen.CustomFieldDef {
	return gen.CustomFieldDef{Id: d.ID, Entity: gen.CustomFieldDefEntity(d.Entity), Key: d.Key, Label: d.Label, Type: gen.CustomFieldDefType(d.Type), Options: strsPtr(d.Options), Required: ptr(d.Required)}
}

func toStatusDef(d discovery.CustomStatusDef) gen.CustomStatusDef {
	return gen.CustomStatusDef{Entity: gen.CustomStatusDefEntity(d.Entity), Key: d.Key, Label: d.Label, Category: d.Category}
}

// ListCustomFields — AD-03.
func (s *Server) ListCustomFields(ctx context.Context, req gen.ListCustomFieldsRequestObject) (gen.ListCustomFieldsResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	defs, err := s.d.Discovery.CustomFields(ctx, scope(ctx), discovery.Entity(req.Params.Entity))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListCustomFields200JSONResponse, 0, len(defs))
	for _, d := range defs {
		out = append(out, toFieldDef(d))
	}
	return out, nil
}

// DefineCustomField — AD-03.
func (s *Server) DefineCustomField(ctx context.Context, req gen.DefineCustomFieldRequestObject) (gen.DefineCustomFieldResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	b := req.Body
	d, err := s.d.Discovery.DefineCustomField(ctx, scope(ctx), discovery.CustomFieldDef{
		Entity: discovery.Entity(b.Entity), Key: b.Key, Label: b.Label, Type: discovery.FieldType(b.Type), Options: strsOf(b.Options), Required: boolOr(b.Required),
	})
	if err != nil {
		return nil, err
	}
	return gen.DefineCustomField200JSONResponse(toFieldDef(d)), nil
}

// ListCustomStatuses — AD-03.
func (s *Server) ListCustomStatuses(ctx context.Context, req gen.ListCustomStatusesRequestObject) (gen.ListCustomStatusesResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	defs, err := s.d.Discovery.CustomStatuses(ctx, scope(ctx), discovery.Entity(req.Params.Entity))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListCustomStatuses200JSONResponse, 0, len(defs))
	for _, d := range defs {
		out = append(out, toStatusDef(d))
	}
	return out, nil
}

// DefineCustomStatus — AD-03.
func (s *Server) DefineCustomStatus(ctx context.Context, req gen.DefineCustomStatusRequestObject) (gen.DefineCustomStatusResponseObject, error) {
	if err := s.requireDiscovery(); err != nil {
		return nil, err
	}
	b := req.Body
	d, err := s.d.Discovery.DefineCustomStatus(ctx, scope(ctx), discovery.CustomStatusDef{Entity: discovery.Entity(b.Entity), Key: b.Key, Label: b.Label, Category: b.Category})
	if err != nil {
		return nil, err
	}
	return gen.DefineCustomStatus200JSONResponse(toStatusDef(d)), nil
}
