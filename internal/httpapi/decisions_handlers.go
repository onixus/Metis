package httpapi

import (
	"context"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

// ---- decisions: DA-01 ----

func (s *Server) requireDecisions() error {
	if s.d.Decisions == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

func toDecision(r decisions.DecisionRecord) gen.Decision {
	out := gen.Decision{
		Id: r.ID, ProductId: idPtr(r.ProductID), Title: r.Title, Context: r.Context, ChosenKey: strPtr(r.ChosenKey), Rationale: strPtr(r.Rationale),
		ExpectedEffect: strPtr(r.ExpectedEffect), ReviewDate: datePtr(r.ReviewDate), Status: gen.DecisionStatus(r.Status), SupersededBy: idPtr(r.SupersededBy),
		PageId: strPtr(r.PageID), Author: r.Author, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if len(r.Snapshot) > 0 {
		snap := map[string]any(r.Snapshot)
		out.Snapshot = &snap
	}
	opts := make([]gen.DecisionOption, 0, len(r.Options))
	for _, o := range r.Options {
		opts = append(opts, gen.DecisionOption{Key: o.Key, Title: o.Title, Description: strPtr(o.Description)})
	}
	out.Options = &opts
	if len(r.Links) > 0 {
		links := make([]gen.DecisionLink, 0, len(r.Links))
		for _, l := range r.Links {
			links = append(links, gen.DecisionLink{Kind: gen.DecisionLinkKind(l.Kind), Id: l.ID})
		}
		out.Links = &links
	}
	return out
}

func toDecisionInput(in gen.DecisionInput) decisions.Input {
	out := decisions.Input{
		ProductID: idOrNil(in.ProductId), Title: in.Title, Context: in.Context, ChosenKey: strOrEmpty(in.ChosenKey), Rationale: strOrEmpty(in.Rationale),
		ExpectedEffect: strOrEmpty(in.ExpectedEffect), ReviewDate: dateOf(in.ReviewDate),
	}
	if in.Snapshot != nil {
		out.Snapshot = *in.Snapshot
	}
	if in.Options != nil {
		for _, o := range *in.Options {
			out.Options = append(out.Options, decisions.Option{Key: o.Key, Title: o.Title, Description: strOrEmpty(o.Description)})
		}
	}
	if in.Links != nil {
		for _, l := range *in.Links {
			out.Links = append(out.Links, decisions.Link{Kind: decisions.LinkKind(l.Kind), ID: l.Id})
		}
	}
	return out
}

// ListDecisions — DA-01.
func (s *Server) ListDecisions(ctx context.Context, req gen.ListDecisionsRequestObject) (gen.ListDecisionsResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	var st decisions.Status
	if req.Params.Status != nil {
		st = decisions.Status(*req.Params.Status)
	}
	list, err := s.d.Decisions.List(ctx, scope(ctx), idOrNil(req.Params.ProductId), st)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListDecisions200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, toDecision(r))
	}
	return out, nil
}

// CreateDecision — DA-01.
func (s *Server) CreateDecision(ctx context.Context, req gen.CreateDecisionRequestObject) (gen.CreateDecisionResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	r, err := s.d.Decisions.Create(ctx, scope(ctx), toDecisionInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateDecision201JSONResponse(toDecision(r)), nil
}

// ListDecisionsFor — DS-04.
func (s *Server) ListDecisionsFor(ctx context.Context, req gen.ListDecisionsForRequestObject) (gen.ListDecisionsForResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	list, err := s.d.Decisions.DecisionsFor(ctx, scope(ctx), req.Kind, req.Id)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListDecisionsFor200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, gen.DecisionRef{Id: r.ID, Title: r.Title})
	}
	return out, nil
}

// GetDecision — решение.
func (s *Server) GetDecision(ctx context.Context, req gen.GetDecisionRequestObject) (gen.GetDecisionResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	r, err := s.d.Decisions.Get(ctx, scope(ctx), req.DecisionId)
	if err != nil {
		return nil, err
	}
	return gen.GetDecision200JSONResponse(toDecision(r)), nil
}

// UpdateDecision — DA-01.
func (s *Server) UpdateDecision(ctx context.Context, req gen.UpdateDecisionRequestObject) (gen.UpdateDecisionResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	r, err := s.d.Decisions.Update(ctx, scope(ctx), req.DecisionId, toDecisionInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateDecision200JSONResponse(toDecision(r)), nil
}

// AcceptDecision — DA-01.
func (s *Server) AcceptDecision(ctx context.Context, req gen.AcceptDecisionRequestObject) (gen.AcceptDecisionResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	r, err := s.d.Decisions.Accept(ctx, scope(ctx), req.DecisionId)
	if err != nil {
		return nil, err
	}
	return gen.AcceptDecision200JSONResponse(toDecision(r)), nil
}

// RejectDecision — DA-01.
func (s *Server) RejectDecision(ctx context.Context, req gen.RejectDecisionRequestObject) (gen.RejectDecisionResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	r, err := s.d.Decisions.Reject(ctx, scope(ctx), req.DecisionId)
	if err != nil {
		return nil, err
	}
	return gen.RejectDecision200JSONResponse(toDecision(r)), nil
}

// SupersedeDecision — DA-01.
func (s *Server) SupersedeDecision(ctx context.Context, req gen.SupersedeDecisionRequestObject) (gen.SupersedeDecisionResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	r, err := s.d.Decisions.Supersede(ctx, scope(ctx), req.DecisionId, req.Body.By)
	if err != nil {
		return nil, err
	}
	return gen.SupersedeDecision200JSONResponse(toDecision(r)), nil
}

// RequestDecisionPage — страница ADR через outbox (DA-01, ТЗ 4.3). Без адаптера базы знаний — 503.
//
// Пустое KnowledgeSpace означает, что адаптер не настроен и обработчик публикации не
// зарегистрирован: событие ушло бы в outbox, воркер подтвердил бы его без обработчиков и удалил —
// страница не создалась бы никогда. Поэтому 503 возвращается независимо от тела запроса;
// space_key из тела допустим только как переопределение пространства при настроенном адаптере.
func (s *Server) RequestDecisionPage(ctx context.Context, req gen.RequestDecisionPageRequestObject) (gen.RequestDecisionPageResponseObject, error) {
	if err := s.requireDecisions(); err != nil {
		return nil, err
	}
	if s.d.KnowledgeSpace == "" {
		return nil, kernel.ErrUnavailable
	}
	space := s.d.KnowledgeSpace
	if req.Body != nil && req.Body.SpaceKey != nil && *req.Body.SpaceKey != "" {
		space = *req.Body.SpaceKey
	}
	if err := s.d.Decisions.RequestPage(ctx, scope(ctx), req.DecisionId, space); err != nil {
		return nil, err
	}
	return gen.RequestDecisionPage202Response{}, nil
}
