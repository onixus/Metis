package delivery

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// WorklogShare — доля продукта или фичи в списаниях времени (DL-05).
type WorklogShare struct {
	ProductID kernel.ID       `json:"product_id"`
	FeatureID kernel.ID       `json:"feature_id,omitempty"`
	EpicKey   string          `json:"epic_key,omitempty"`
	Seconds   int64           `json:"seconds"`
	Share     decimal.Decimal `json:"share"`
}

// WorklogReport — база распределения затрат по продуктам и фичам (DL-05).
// Затраты в деньгах считает модуль экономики: здесь только доли времени.
type WorklogReport struct {
	From         time.Time      `json:"from"`
	To           time.Time      `json:"to"`
	TotalSeconds int64          `json:"total_seconds"`
	ByProduct    []WorklogShare `json:"by_product"`
	ByFeature    []WorklogShare `json:"by_feature"`
	Sync         SyncState      `json:"sync"`
}

// WorklogCostBase собирает списания времени по эпикам, привязанным к фичам, и превращает их
// в доли по продуктам и фичам (DL-05). Списания принадлежат трекеру и читаются только на чтение.
func (s *Service) WorklogCostBase(ctx context.Context, sc authz.Scope, from, to time.Time) (WorklogReport, error) {
	if !sc.Valid() {
		return WorklogReport{}, kernel.ErrForbidden
	}
	if s.tracker == nil {
		return WorklogReport{}, fmt.Errorf("%w: адаптер трекера выключен", kernel.ErrUnavailable)
	}
	mappings, err := s.store.Mappings(ctx)
	if err != nil {
		return WorklogReport{}, fmt.Errorf("mappings: %w", err)
	}
	type target struct {
		productID kernel.ID
		featureID kernel.ID
		epicKey   string
	}
	byIssue := map[string]target{}
	keys := make([]string, 0, len(mappings))
	for _, m := range mappings {
		if !sc.Allows(authz.ActionReadStrategic, m.ProductID) {
			continue
		}
		epic, err := s.store.EpicByFeature(ctx, m.FeatureID)
		if err != nil {
			if kernel.IsNotFound(err) {
				continue
			}
			return WorklogReport{}, fmt.Errorf("epic: %w", err)
		}
		t := target{productID: m.ProductID, featureID: m.FeatureID, epicKey: m.EpicKey}
		byIssue[m.EpicKey] = t
		keys = append(keys, m.EpicKey)
		for _, i := range epic.Issues {
			byIssue[i.Key] = t
			keys = append(keys, i.Key)
		}
	}
	sort.Strings(keys)
	report := WorklogReport{From: from.UTC(), To: to.UTC()}
	if len(keys) == 0 {
		return report, nil
	}
	logs, err := s.tracker.Worklogs(ctx, keys, from)
	if err != nil {
		return WorklogReport{}, fmt.Errorf("%w: worklogs: %w", kernel.ErrUnavailable, err)
	}
	products := map[kernel.ID]int64{}
	features := map[kernel.ID]*WorklogShare{}
	for _, l := range logs {
		if !to.IsZero() && l.Started.After(to) {
			continue
		}
		t, ok := byIssue[l.IssueKey]
		if !ok {
			continue // списание вне фич платформы
		}
		seconds := int64(l.Spent / time.Second)
		if seconds <= 0 {
			continue
		}
		report.TotalSeconds += seconds
		products[t.productID] += seconds
		f, ok := features[t.featureID]
		if !ok {
			f = &WorklogShare{ProductID: t.productID, FeatureID: t.featureID, EpicKey: t.epicKey}
			features[t.featureID] = f
		}
		f.Seconds += seconds
	}
	total := decimal.NewFromInt(report.TotalSeconds)
	share := func(seconds int64) decimal.Decimal {
		if total.IsZero() {
			return decimal.Zero
		}
		return decimal.NewFromInt(seconds).DivRound(total, 10)
	}
	for id, seconds := range products {
		report.ByProduct = append(report.ByProduct, WorklogShare{ProductID: id, Seconds: seconds, Share: share(seconds)})
	}
	for _, f := range features {
		f.Share = share(f.Seconds)
		report.ByFeature = append(report.ByFeature, *f)
	}
	sort.Slice(report.ByProduct, func(i, j int) bool { return report.ByProduct[i].Seconds > report.ByProduct[j].Seconds })
	sort.Slice(report.ByFeature, func(i, j int) bool { return report.ByFeature[i].Seconds > report.ByFeature[j].Seconds })
	state, err := s.syncState(ctx)
	if err != nil {
		return WorklogReport{}, err
	}
	report.Sync = state
	return report, nil
}

// ProductShares возвращает доли продуктов в виде, пригодном для правил аллокации экономики (EC-02, EC-12).
func (r WorklogReport) ProductShares() map[kernel.ID]decimal.Decimal {
	out := make(map[kernel.ID]decimal.Decimal, len(r.ByProduct))
	for _, s := range r.ByProduct {
		out[s.ProductID] = s.Share
	}
	return out
}
