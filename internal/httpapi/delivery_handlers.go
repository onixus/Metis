package httpapi

import (
	"context"
	"errors"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

func (s *Server) GetProductDelivery(ctx context.Context, req gen.GetProductDeliveryRequestObject) (gen.GetProductDeliveryResponseObject, error) {
	if s.d.Delivery == nil {
		return nil, kernel.ErrUnavailable
	}
	mappings, err := s.d.Delivery.Mappings(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	sprints, state, err := s.d.Delivery.SprintStatuses(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	if mappings == nil {
		mappings = []delivery.Mapping{}
	}
	if sprints == nil {
		sprints = []delivery.SprintStatus{}
	}
	metrics := []delivery.FeatureMetrics{}
	for _, m := range mappings {
		metric, err := s.d.Delivery.FeatureMetrics(ctx, scope(ctx), m.FeatureID)
		if errors.Is(err, kernel.ErrNotFound) {
			continue
		} // привязка ожидает первой сверки
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	return gen.GetProductDelivery200JSONResponse{Mappings: mappings, Sprints: sprints, Metrics: metrics, Sync: state, Enabled: s.d.DeliveryEnabled}, nil
}

func (s *Server) MapDeliveryFeature(ctx context.Context, req gen.MapDeliveryFeatureRequestObject) (gen.MapDeliveryFeatureResponseObject, error) {
	if s.d.Delivery == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	m, err := s.d.Delivery.MapFeature(ctx, scope(ctx), req.FeatureId, req.Body.EpicKey, req.Body.Project)
	if err != nil {
		return nil, err
	}
	return gen.MapDeliveryFeature200JSONResponse(m), nil
}

func (s *Server) RequestDeliveryEpic(ctx context.Context, req gen.RequestDeliveryEpicRequestObject) (gen.RequestDeliveryEpicResponseObject, error) {
	if s.d.Delivery == nil || !s.d.DeliveryEnabled {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	if err := s.d.Delivery.CreateEpicForFeature(ctx, scope(ctx), req.FeatureId, req.Body.Project); err != nil {
		return nil, err
	}
	return gen.RequestDeliveryEpic202Response{}, nil
}

func (s *Server) GetDeliveryConnector(ctx context.Context, _ gen.GetDeliveryConnectorRequestObject) (gen.GetDeliveryConnectorResponseObject, error) {
	if s.d.Delivery == nil {
		return nil, kernel.ErrUnavailable
	}
	status, err := s.d.Delivery.ConnectorStatus(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	return gen.GetDeliveryConnector200JSONResponse(status), nil
}

func (s *Server) SetDeliveryMapping(ctx context.Context, req gen.SetDeliveryMappingRequestObject) (gen.SetDeliveryMappingResponseObject, error) {
	if s.d.Delivery == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	if err := s.d.Delivery.SetFieldMapping(ctx, scope(ctx), *req.Body); err != nil {
		return nil, err
	}
	return gen.SetDeliveryMapping204Response{}, nil
}
