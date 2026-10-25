package httpapi

import (
	"context"
	"errors"

	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

func (s *Server) GetEconomicsUnificationStatus(ctx context.Context, req gen.GetEconomicsUnificationStatusRequestObject) (gen.GetEconomicsUnificationStatusResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	p, err := modeling.ParsePeriod(req.Period)
	if err != nil {
		return nil, err
	}
	a, err := s.d.Modeling.UnificationHistory(ctx, scope(ctx), p)
	if errors.Is(err, kernel.ErrNotFound) {
		return gen.GetEconomicsUnificationStatus200JSONResponse{Active: false, Period: req.Period}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetEconomicsUnificationStatus200JSONResponse{Active: a.BookProductID == req.ProductId, Period: req.Period, Digest: &a.Digest, BookProductId: &a.BookProductID, FieldKeys: &a.FieldKeys, MetricKeys: &a.MetricKeys}, nil
}

func unificationInput(product kernel.ID, period string, body *gen.HistoryUnificationRequest) (modeling.HistoryUnificationInput, error) {
	if body == nil {
		return modeling.HistoryUnificationInput{}, kernel.Invalid("body", "обязательно")
	}
	p, err := modeling.ParsePeriod(period)
	if err != nil {
		return modeling.HistoryUnificationInput{}, err
	}
	return modeling.HistoryUnificationInput{BookProductID: product, Period: p, ExpectedBookVersion: body.ExpectedBookVersion, ExpectedModelVersion: body.ExpectedModelVersion, Digest: strOrEmpty(body.Digest)}, nil
}

func (s *Server) PreviewEconomicsUnification(ctx context.Context, req gen.PreviewEconomicsUnificationRequestObject) (gen.PreviewEconomicsUnificationResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	in, err := unificationInput(req.ProductId, req.Period, req.Body)
	if err != nil {
		return nil, err
	}
	result, err := s.d.Modeling.PreviewUnification(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.PreviewEconomicsUnification200JSONResponse(result), nil
}

func (s *Server) ApplyEconomicsUnification(ctx context.Context, req gen.ApplyEconomicsUnificationRequestObject) (gen.ApplyEconomicsUnificationResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	in, err := unificationInput(req.ProductId, req.Period, req.Body)
	if err != nil {
		return nil, err
	}
	if len(in.Digest) != 64 {
		return nil, kernel.Invalid("digest", "нужна контрольная сумма подтверждённого предпросмотра")
	}
	result, err := s.d.Modeling.ApplyUnification(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.ApplyEconomicsUnification200JSONResponse(result), nil
}

func (s *Server) GetEconomicsUnificationArchive(ctx context.Context, req gen.GetEconomicsUnificationArchiveRequestObject) (gen.GetEconomicsUnificationArchiveResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	p, err := modeling.ParsePeriod(req.Period)
	if err != nil {
		return nil, err
	}
	result, err := s.d.Modeling.UnificationHistory(ctx, scope(ctx), p)
	if err != nil {
		return nil, err
	}
	if result.BookProductID != req.ProductId {
		return nil, kernel.ErrNotFound
	}
	return gen.GetEconomicsUnificationArchive200JSONResponse(result), nil
}
