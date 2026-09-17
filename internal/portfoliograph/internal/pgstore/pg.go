// Package pgstore — реализация portfoliograph.Store на PostgreSQL (схема portfoliograph).
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/portfoliograph/internal/db"
)

// PG — хранилище графа портфеля.
type PG struct {
	db    *pgdb.DB
	clock kernel.Clock
}

// New создаёт хранилище.
func New(d *pgdb.DB, clock kernel.Clock) *PG {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &PG{db: d, clock: clock}
}

func (s *PG) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Load читает весь граф одной транзакцией.
func (s *PG) Load(ctx context.Context) (portfoliograph.Snapshot, error) {
	var snap portfoliograph.Snapshot
	err := s.db.Transact(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		products, err := q.ListProducts(ctx)
		if err != nil {
			return fmt.Errorf("products: %w", err)
		}
		for _, p := range products {
			snap.Products = append(snap.Products, portfoliograph.Product{
				ID: p.ID, Key: p.Key, Name: p.Name, Type: portfoliograph.ProductType(p.Type), Owner: p.Owner,
				Lifecycle: portfoliograph.Lifecycle(p.Lifecycle), SSDLCCertified: p.SsdlcCertified, HubManual: p.HubManual,
				CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
			})
		}
		caps, err := q.ListCapabilities(ctx)
		if err != nil {
			return fmt.Errorf("capabilities: %w", err)
		}
		for _, c := range caps {
			snap.Capabilities = append(snap.Capabilities, portfoliograph.Capability{ID: c.ID, ProductID: c.ProductID, Name: c.Name})
		}
		features, err := q.ListFeatures(ctx)
		if err != nil {
			return fmt.Errorf("features: %w", err)
		}
		for _, f := range features {
			snap.Features = append(snap.Features, portfoliograph.Feature{
				ID: f.ID, ProductID: f.ProductID, CapabilityID: f.CapabilityID.UUID, Name: f.Name,
				Status:      portfoliograph.FeatureStatus(f.Status),
				OwnValue:    kernel.Money{Amount: f.OwnValueAmount, Currency: f.OwnValueCurrency},
				PlannedDate: fromDate(f.PlannedDate), Affected: f.Affected, AffectedBy: f.AffectedBy.UUID,
				ImpliedDate: fromDate(f.ImpliedDate), ExternalKey: f.ExternalKey,
				CreatedAt: f.CreatedAt.UTC(), UpdatedAt: f.UpdatedAt.UTC(),
			})
		}
		reqs, err := q.ListRequirements(ctx)
		if err != nil {
			return fmt.Errorf("requirements: %w", err)
		}
		for _, r := range reqs {
			snap.Requirements = append(snap.Requirements, portfoliograph.Requirement{ID: r.ID, ProductID: r.ProductID, FeatureID: r.FeatureID, Text: r.Text})
		}
		links, err := q.ListLinks(ctx)
		if err != nil {
			return fmt.Errorf("links: %w", err)
		}
		for _, l := range links {
			snap.Links = append(snap.Links, portfoliograph.Link{
				ID: l.ID, Type: portfoliograph.LinkType(l.Type), FromProductID: l.FromProductID, ToProductID: l.ToProductID,
				FromFeatureID: l.FromFeatureID.UUID, ToFeatureID: l.ToFeatureID.UUID,
				Criticality: portfoliograph.Criticality(l.Criticality), ContractID: l.ContractID.UUID, CreatedAt: l.CreatedAt.UTC(),
			})
		}
		contracts, err := q.ListContracts(ctx)
		if err != nil {
			return fmt.Errorf("contracts: %w", err)
		}
		for _, c := range contracts {
			var compat []portfoliograph.VersionPair
			if len(c.Compatibility) > 0 {
				if err := json.Unmarshal(c.Compatibility, &compat); err != nil {
					return fmt.Errorf("contract %s compatibility: %w", c.ID, err)
				}
			}
			snap.Contracts = append(snap.Contracts, portfoliograph.IntegrationContract{
				ID: c.ID, Name: c.Name, ProviderProductID: c.ProviderProductID, ConsumerProductID: c.ConsumerProductID,
				ProviderFeatureIDs: c.ProviderFeatureIds, ConsumerFeatureIDs: c.ConsumerFeatureIds,
				InterfaceVersion: c.InterfaceVersion, Owner: c.Owner, Status: portfoliograph.ContractStatus(c.Status),
				Criticality: portfoliograph.Criticality(c.Criticality), Compatibility: compat,
				SignalValue: kernel.Money{Amount: c.SignalValueAmount, Currency: c.SignalValueCurrency},
				CreatedAt:   c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
			})
		}
		raw, err := q.GetSettings(ctx)
		switch {
		case err == nil:
			var coefs map[portfoliograph.Criticality]decimal.Decimal
			if err := json.Unmarshal(raw, &coefs); err != nil {
				return fmt.Errorf("settings: %w", err)
			}
			snap.Settings = portfoliograph.Settings{Coefficients: coefs}
		case kernel.IsNotFound(pgdb.MapError(err)):
			snap.Settings = portfoliograph.DefaultSettings()
		default:
			return fmt.Errorf("settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return portfoliograph.Snapshot{}, fmt.Errorf("portfoliograph load: %w", err)
	}
	return snap, nil
}

// SaveProduct сохраняет продукт (upsert).
func (s *PG) SaveProduct(ctx context.Context, p portfoliograph.Product) error {
	err := s.q(ctx).UpsertProduct(ctx, db.UpsertProductParams{
		ID: p.ID, Key: p.Key, Name: p.Name, Type: string(p.Type), Owner: p.Owner, Lifecycle: string(p.Lifecycle),
		SsdlcCertified: p.SSDLCCertified, HubManual: p.HubManual, CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	})
	return wrap("product", p.ID, err)
}

// SaveCapability сохраняет возможность.
func (s *PG) SaveCapability(ctx context.Context, c portfoliograph.Capability) error {
	return wrap("capability", c.ID, s.q(ctx).UpsertCapability(ctx, db.UpsertCapabilityParams{ID: c.ID, ProductID: c.ProductID, Name: c.Name}))
}

// SaveFeature сохраняет фичу.
func (s *PG) SaveFeature(ctx context.Context, f portfoliograph.Feature) error {
	err := s.q(ctx).UpsertFeature(ctx, db.UpsertFeatureParams{
		ID: f.ID, ProductID: f.ProductID, CapabilityID: nullID(f.CapabilityID), Name: f.Name, Status: string(f.Status),
		OwnValueAmount: f.OwnValue.Amount, OwnValueCurrency: f.OwnValue.Currency,
		PlannedDate: toDate(f.PlannedDate), Affected: f.Affected, AffectedBy: nullID(f.AffectedBy),
		ImpliedDate: toDate(f.ImpliedDate), ExternalKey: f.ExternalKey,
		CreatedAt: f.CreatedAt.UTC(), UpdatedAt: f.UpdatedAt.UTC(),
	})
	return wrap("feature", f.ID, err)
}

// SaveRequirement сохраняет требование.
func (s *PG) SaveRequirement(ctx context.Context, r portfoliograph.Requirement) error {
	return wrap("requirement", r.ID, s.q(ctx).UpsertRequirement(ctx, db.UpsertRequirementParams{ID: r.ID, ProductID: r.ProductID, FeatureID: r.FeatureID, Text: r.Text}))
}

// SaveLink сохраняет связь.
func (s *PG) SaveLink(ctx context.Context, l portfoliograph.Link) error {
	err := s.q(ctx).UpsertLink(ctx, db.UpsertLinkParams{
		ID: l.ID, Type: string(l.Type), FromProductID: l.FromProductID, ToProductID: l.ToProductID,
		FromFeatureID: nullID(l.FromFeatureID), ToFeatureID: nullID(l.ToFeatureID),
		Criticality: string(l.Criticality), ContractID: nullID(l.ContractID), CreatedAt: l.CreatedAt.UTC(),
	})
	return wrap("link", l.ID, err)
}

// DeleteLink удаляет связь; kernel.ErrNotFound, если её нет.
func (s *PG) DeleteLink(ctx context.Context, id kernel.ID) error {
	n, err := s.q(ctx).DeleteLink(ctx, id)
	if err != nil {
		return wrap("link", id, err)
	}
	if n == 0 {
		return kernel.NotFound("link", id)
	}
	return nil
}

// SaveContract сохраняет контракт.
func (s *PG) SaveContract(ctx context.Context, c portfoliograph.IntegrationContract) error {
	compat := c.Compatibility
	if compat == nil {
		compat = []portfoliograph.VersionPair{}
	}
	raw, err := json.Marshal(compat)
	if err != nil {
		return fmt.Errorf("portfoliograph contract %s: compatibility: %w", c.ID, err)
	}
	err = s.q(ctx).UpsertContract(ctx, db.UpsertContractParams{
		ID: c.ID, Name: c.Name, ProviderProductID: c.ProviderProductID, ConsumerProductID: c.ConsumerProductID,
		ProviderFeatureIds: orEmpty(c.ProviderFeatureIDs), ConsumerFeatureIds: orEmpty(c.ConsumerFeatureIDs),
		InterfaceVersion: c.InterfaceVersion, Owner: c.Owner, Status: string(c.Status), Criticality: string(c.Criticality),
		Compatibility: raw, SignalValueAmount: c.SignalValue.Amount, SignalValueCurrency: c.SignalValue.Currency,
		CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
	})
	return wrap("contract", c.ID, err)
}

// SaveSettings сохраняет коэффициенты как строки decimal (инвариант 6).
func (s *PG) SaveSettings(ctx context.Context, st portfoliograph.Settings) error {
	coefs := st.Coefficients
	if coefs == nil {
		coefs = portfoliograph.DefaultSettings().Coefficients
	}
	raw, err := json.Marshal(coefs)
	if err != nil {
		return fmt.Errorf("portfoliograph settings: %w", err)
	}
	if err := s.q(ctx).UpsertSettings(ctx, db.UpsertSettingsParams{Coefficients: raw, UpdatedAt: s.clock.Now().UTC()}); err != nil {
		return fmt.Errorf("portfoliograph settings: %w", pgdb.MapError(err))
	}
	return nil
}

// SaveRollup сохраняет результат rollup одной транзакцией.
func (s *PG) SaveRollup(ctx context.Context, values []portfoliograph.FeatureValue) error {
	return s.db.Transact(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		for _, v := range values {
			err := q.UpsertFeatureValue(ctx, db.UpsertFeatureValueParams{
				FeatureID: v.FeatureID, ProductID: v.ProductID,
				OwnValueAmount: v.OwnValue.Amount, OwnValueCurrency: v.OwnValue.Currency,
				DerivedValueAmount: v.DerivedValue.Amount, DerivedValueCurrency: v.DerivedValue.Currency,
				TotalValueAmount: v.TotalValue.Amount, TotalValueCurrency: v.TotalValue.Currency,
				ComputedAt: v.ComputedAt.UTC(),
			})
			if err != nil {
				return wrap("feature_value", v.FeatureID, err)
			}
		}
		return nil
	})
}

// Rollup возвращает сохранённый rollup.
func (s *PG) Rollup(ctx context.Context) ([]portfoliograph.FeatureValue, error) {
	rows, err := s.q(ctx).ListFeatureValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("portfoliograph rollup: %w", err)
	}
	out := make([]portfoliograph.FeatureValue, 0, len(rows))
	for _, r := range rows {
		out = append(out, portfoliograph.FeatureValue{
			FeatureID: r.FeatureID, ProductID: r.ProductID,
			OwnValue:     kernel.Money{Amount: r.OwnValueAmount, Currency: r.OwnValueCurrency},
			DerivedValue: kernel.Money{Amount: r.DerivedValueAmount, Currency: r.DerivedValueCurrency},
			TotalValue:   kernel.Money{Amount: r.TotalValueAmount, Currency: r.TotalValueCurrency},
			ComputedAt:   r.ComputedAt.UTC(),
		})
	}
	return out, nil
}

func wrap(entity string, id kernel.ID, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("portfoliograph %s %s: %w", entity, id, pgdb.MapError(err))
}

func nullID(id kernel.ID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != kernel.NilID} }

func orEmpty(ids []kernel.ID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}

func toDate(d kernel.Date) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.Time(), Valid: true}
}

func fromDate(d pgtype.Date) kernel.Date {
	if !d.Valid {
		return kernel.Date{}
	}
	return kernel.DateFromTime(time.Date(d.Time.Year(), d.Time.Month(), d.Time.Day(), 0, 0, 0, 0, time.UTC))
}
