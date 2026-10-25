package portfoliograph

import (
	"context"
	"sort"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Capabilities lists the private capability hierarchy of a product (PG-02).
func (s *Service) Capabilities(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]Capability, error) {
	if err := sc.Require(authz.ActionReadPrivate, productID); err != nil {
		return nil, err
	}
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.g.products[productID]; !ok {
		return nil, kernel.NotFound("product", productID)
	}
	out := make([]Capability, 0)
	for _, c := range s.g.capabilities {
		if c.ProductID == productID {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out, nil
}

// Requirements lists private requirements belonging to a feature (PG-02).
func (s *Service) Requirements(ctx context.Context, sc authz.Scope, featureID kernel.ID) ([]Requirement, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.g.features[featureID]
	if !ok {
		return nil, kernel.NotFound("feature", featureID)
	}
	if err := sc.Require(authz.ActionReadPrivate, f.ProductID); err != nil {
		return nil, err
	}
	out := make([]Requirement, 0)
	for _, r := range s.g.requirements {
		if r.FeatureID == featureID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out, nil
}
