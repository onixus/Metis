package httpapi

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

// ---- commitments: CT-01…CT-04 ----

func (s *Server) requireCommitments() error {
	if s.d.Commitments == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

func toCommitment(c commitments.Commitment) gen.Commitment {
	out := gen.Commitment{
		Id: c.ID, ProductId: c.ProductID, Kind: gen.CommitmentKind(c.Kind), Counterparty: c.Counterparty, Subject: c.Subject,
		DueDate: openapi_types.Date{Time: c.DueDate.Time()}, Basis: c.Basis, Owner: c.Owner, Status: gen.CommitmentStatus(c.Status),
		FeatureId: idPtr(c.FeatureID), ReleaseId: idPtr(c.ReleaseID), RenewalItemId: idPtr(c.RenewalItemID),
		CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.Subtype != "" {
		out.Subtype = ptr(gen.CommitmentSubtype(c.Subtype))
	}
	return out
}

func toCommitmentInput(in gen.CommitmentInput) commitments.Input {
	out := commitments.Input{
		Kind: commitments.Kind(in.Kind), Counterparty: in.Counterparty, Subject: in.Subject, DueDate: kernel.DateFromTime(in.DueDate.Time),
		Basis: in.Basis, Owner: in.Owner, FeatureID: idOrNil(in.FeatureId), ReleaseID: idOrNil(in.ReleaseId),
	}
	if in.Subtype != nil {
		out.Subtype = commitments.Subtype(*in.Subtype)
	}
	return out
}

func toAlert(a commitments.Alert) gen.CommitmentAlert {
	out := gen.CommitmentAlert{
		Id: a.ID, CommitmentId: a.CommitmentID, ProductId: a.ProductID, Kind: gen.CommitmentAlertKind(a.Kind), Message: a.Message,
		EventId: a.EventID, NewDate: datePtr(a.NewDate), DueDate: datePtr(a.DueDate), RaisedAt: a.RaisedAt, Acknowledged: a.Acknowledged,
		AcknowledgedBy: strPtr(a.AcknowledgedBy),
	}
	if !a.AcknowledgedAt.IsZero() {
		out.AcknowledgedAt = ptr(a.AcknowledgedAt)
	}
	return out
}

// ListCommitments — CT-01, CT-02.
func (s *Server) ListCommitments(ctx context.Context, req gen.ListCommitmentsRequestObject) (gen.ListCommitmentsResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	f := commitments.Filter{ProductID: req.ProductId}
	if req.Params.Kind != nil {
		f.Kind = commitments.Kind(*req.Params.Kind)
	}
	if req.Params.Status != nil {
		for _, st := range *req.Params.Status {
			f.Statuses = append(f.Statuses, commitments.Status(st))
		}
	}
	list, err := s.d.Commitments.List(ctx, scope(ctx), f)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListCommitments200JSONResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toCommitment(c))
	}
	return out, nil
}

// CreateCommitment — CT-01, CT-02.
func (s *Server) CreateCommitment(ctx context.Context, req gen.CreateCommitmentRequestObject) (gen.CreateCommitmentResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	c, err := s.d.Commitments.Create(ctx, scope(ctx), req.ProductId, toCommitmentInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateCommitment201JSONResponse(toCommitment(c)), nil
}

// GetCommitment — обязательство.
func (s *Server) GetCommitment(ctx context.Context, req gen.GetCommitmentRequestObject) (gen.GetCommitmentResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	c, err := s.d.Commitments.Get(ctx, scope(ctx), req.CommitmentId)
	if err != nil {
		return nil, err
	}
	return gen.GetCommitment200JSONResponse(toCommitment(c)), nil
}

// UpdateCommitment — изменение активного обязательства.
func (s *Server) UpdateCommitment(ctx context.Context, req gen.UpdateCommitmentRequestObject) (gen.UpdateCommitmentResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	c, err := s.d.Commitments.Update(ctx, scope(ctx), req.CommitmentId, toCommitmentInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateCommitment200JSONResponse(toCommitment(c)), nil
}

// FulfilCommitment — исполнено.
func (s *Server) FulfilCommitment(ctx context.Context, req gen.FulfilCommitmentRequestObject) (gen.FulfilCommitmentResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	c, err := s.d.Commitments.Fulfil(ctx, scope(ctx), req.CommitmentId)
	if err != nil {
		return nil, err
	}
	return gen.FulfilCommitment200JSONResponse(toCommitment(c)), nil
}

// CancelCommitment — отменено.
func (s *Server) CancelCommitment(ctx context.Context, req gen.CancelCommitmentRequestObject) (gen.CancelCommitmentResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	c, err := s.d.Commitments.Cancel(ctx, scope(ctx), req.CommitmentId)
	if err != nil {
		return nil, err
	}
	return gen.CancelCommitment200JSONResponse(toCommitment(c)), nil
}

// EnsureRenewals — CT-04.
func (s *Server) EnsureRenewals(ctx context.Context, req gen.EnsureRenewalsRequestObject) (gen.EnsureRenewalsResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	now := kernel.DateFromTime(time.Now().UTC())
	if req.Body != nil && req.Body.Now != nil {
		now = kernel.DateFromTime(req.Body.Now.Time)
	}
	list, err := s.d.Commitments.EnsureRenewals(ctx, scope(ctx), now)
	if err != nil {
		return nil, err
	}
	out := make(gen.EnsureRenewals200JSONResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toCommitment(c))
	}
	return out, nil
}

// ListCommitmentAlerts — CT-03.
func (s *Server) ListCommitmentAlerts(ctx context.Context, req gen.ListCommitmentAlertsRequestObject) (gen.ListCommitmentAlertsResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	list, err := s.d.Commitments.Alerts(ctx, scope(ctx), req.ProductId, boolOr(req.Params.Open))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListCommitmentAlerts200JSONResponse, 0, len(list))
	for _, a := range list {
		out = append(out, toAlert(a))
	}
	return out, nil
}

// AcknowledgeCommitmentAlert — CT-03.
func (s *Server) AcknowledgeCommitmentAlert(ctx context.Context, req gen.AcknowledgeCommitmentAlertRequestObject) (gen.AcknowledgeCommitmentAlertResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	a, err := s.d.Commitments.AcknowledgeAlert(ctx, scope(ctx), req.AlertId)
	if err != nil {
		return nil, err
	}
	return gen.AcknowledgeCommitmentAlert200JSONResponse(toAlert(a)), nil
}

// GetCommitmentSettings — CT-04. Чтение доступно любому аутентифицированному субъекту.
func (s *Server) GetCommitmentSettings(ctx context.Context, _ gen.GetCommitmentSettingsRequestObject) (gen.GetCommitmentSettingsResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	if !scope(ctx).Valid() {
		return nil, kernel.ErrForbidden
	}
	st, err := s.d.Commitments.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetCommitmentSettings200JSONResponse{LeadMonths: st.LeadMonths}, nil
}

// UpdateCommitmentSettings — CT-04 (admin).
func (s *Server) UpdateCommitmentSettings(ctx context.Context, req gen.UpdateCommitmentSettingsRequestObject) (gen.UpdateCommitmentSettingsResponseObject, error) {
	if err := s.requireCommitments(); err != nil {
		return nil, err
	}
	if err := s.d.Commitments.UpdateSettings(ctx, scope(ctx), commitments.Settings{LeadMonths: req.Body.LeadMonths}); err != nil {
		return nil, err
	}
	st, err := s.d.Commitments.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.UpdateCommitmentSettings200JSONResponse{LeadMonths: st.LeadMonths}, nil
}
