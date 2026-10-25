package portfoliograph

import (
	"fmt"
	"sort"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// ContractOwnsLink checks both declared sides and actual owners of feature endpoints.
func ContractOwnsLink(c IntegrationContract, l Link, products map[kernel.ID]kernel.ID) bool {
	if !contractBindingMatches(c, l) {
		return false
	}
	return !l.IsFeatureLevel() || (products[l.FromFeatureID] == l.FromProductID && products[l.ToFeatureID] == l.ToProductID)
}

func contractBindingMatches(c IntegrationContract, l Link) bool {
	return c.ID != kernel.NilID && l.ContractID == c.ID && l.Type == LinkIntegration && l.FromProductID == c.ConsumerProductID && l.ToProductID == c.ProviderProductID && ((l.FromFeatureID == kernel.NilID) == (l.ToFeatureID == kernel.NilID))
}

func featureProducts(features []Feature) map[kernel.ID]kernel.ID {
	products := make(map[kernel.ID]kernel.ID, len(features))
	for _, f := range features {
		products[f.ID] = f.ProductID
	}
	return products
}

func (g *Graph) ownsContractLink(c IntegrationContract, l Link) bool {
	products := map[kernel.ID]kernel.ID{}
	for _, id := range []kernel.ID{l.FromFeatureID, l.ToFeatureID} {
		if f := g.features[id]; f != nil {
			products[id] = f.ProductID
		}
	}
	return ContractOwnsLink(c, l, products)
}

// ContractFeatureUpdates derives only date projections from trusted stored plans.
// Stores invoke it under the same lock/transaction as the contract replacement.
func ContractFeatureUpdates(features []Feature, existing []Link, old IntegrationContract, links []Link, at time.Time) ([]Feature, error) {
	g := NewGraph()
	for _, f := range features {
		g.putFeature(f)
	}
	products := featureProducts(features)
	roots := map[kernel.ID]bool{}
	for _, l := range existing {
		if ContractOwnsLink(old, l, products) {
			if l.IsFeatureLevel() {
				roots[l.FromFeatureID] = true
			}
			continue
		}
		g.putLink(l)
	}
	for _, l := range links {
		if l.IsFeatureLevel() {
			from, to := g.features[l.FromFeatureID], g.features[l.ToFeatureID]
			if from == nil || to == nil || from.ProductID != l.FromProductID || to.ProductID != l.ToProductID {
				return nil, kernel.Invalid("feature_ids", "фичи должны принадлежать сторонам связи")
			}
			roots[l.FromFeatureID] = true
		}
		g.putLink(l)
	}
	dates, via, err := g.effectiveDates(kernel.NilID, kernel.Date{})
	if err != nil {
		return nil, fmt.Errorf("contract constraints: %w", err)
	}
	queue := make([]kernel.ID, 0, len(roots))
	for id := range roots {
		queue = append(queue, id)
	}
	for i := 0; i < len(queue); i++ {
		for _, consumer := range g.in[queue[i]] {
			if !roots[consumer] {
				roots[consumer] = true
				queue = append(queue, consumer)
			}
		}
	}
	updates := make([]Feature, 0)
	for id := range roots {
		f := g.features[id]
		if f == nil {
			continue
		}
		affected := dates[id].After(f.PlannedDate)
		implied, source := kernel.Date{}, kernel.NilID
		if affected {
			implied, source = dates[id], via[id]
		}
		if f.Affected == affected && f.ImpliedDate == implied && f.AffectedBy == source {
			continue
		}
		updated := *f
		updated.Affected, updated.ImpliedDate, updated.AffectedBy = affected, implied, source
		updated.UpdatedAt = at
		updates = append(updates, updated)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].ID.String() < updates[j].ID.String() })
	return updates, nil
}
