// Package pgstore — prioritization.Store на PostgreSQL (схема prioritization).
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/prioritization/internal/db"
)

// Store — хранилище моделей оценки, входов, флагов и стоимости разработки.
type Store struct {
	db *pgdb.DB
}

var _ prioritization.Store = (*Store)(nil)

// New создаёт хранилище на схеме prioritization.
func New(d *pgdb.DB) *Store { return &Store{db: d} }

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// SaveModel создаёт или обновляет модель. ProductID == NilID хранится как NULL (портфельная модель).
func (s *Store) SaveModel(ctx context.Context, m prioritization.ScoringModel) error {
	err := s.q(ctx).UpsertModel(ctx, db.UpsertModelParams{
		ID: m.ID, ProductID: pgdb.NullID(m.ProductID), Name: m.Name, Type: string(m.Type), Formula: m.Formula,
		Inputs: pgdb.Strings(m.Inputs), CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("prioritization model %s: %w", m.ID, pgdb.MapError(err))
	}
	return nil
}

// Model возвращает модель.
func (s *Store) Model(ctx context.Context, id kernel.ID) (prioritization.ScoringModel, error) {
	row, err := s.q(ctx).GetModel(ctx, id)
	if err != nil {
		return prioritization.ScoringModel{}, fmt.Errorf("prioritization model %s: %w", id, pgdb.MapError(err))
	}
	return modelFromRow(row), nil
}

// Models возвращает все модели в порядке создания.
func (s *Store) Models(ctx context.Context) ([]prioritization.ScoringModel, error) {
	rows, err := s.q(ctx).ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("prioritization models: %w", pgdb.MapError(err))
	}
	out := make([]prioritization.ScoringModel, 0, len(rows))
	for _, r := range rows {
		out = append(out, modelFromRow(r))
	}
	return out, nil
}

func modelFromRow(r db.PrioritizationModel) prioritization.ScoringModel {
	return prioritization.ScoringModel{
		ID: r.ID, ProductID: r.ProductID.UUID, Name: r.Name, Type: prioritization.ModelType(r.Type), Formula: r.Formula,
		Inputs: pgdb.NilIfEmptyStrings(r.Inputs), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// SaveInputs сохраняет входы фичи; значения — строки decimal (инвариант 6).
func (s *Store) SaveInputs(ctx context.Context, in prioritization.FeatureScoreInput) error {
	vals := in.Values
	if vals == nil {
		vals = map[string]decimal.Decimal{}
	}
	raw, err := json.Marshal(vals)
	if err != nil {
		return fmt.Errorf("prioritization inputs %s: %w", in.FeatureID, err)
	}
	err = s.q(ctx).UpsertInputs(ctx, db.UpsertInputsParams{
		ModelID: in.ModelID, FeatureID: in.FeatureID, ProductID: in.ProductID, Values: raw,
		UpdatedAt: in.UpdatedAt.UTC(), UpdatedBy: in.UpdatedBy,
	})
	if err != nil {
		return fmt.Errorf("prioritization inputs %s: %w", in.FeatureID, pgdb.MapError(err))
	}
	return nil
}

// Inputs возвращает входы фичи по модели.
func (s *Store) Inputs(ctx context.Context, modelID, featureID kernel.ID) (prioritization.FeatureScoreInput, error) {
	row, err := s.q(ctx).GetInputs(ctx, db.GetInputsParams{ModelID: modelID, FeatureID: featureID})
	if err != nil {
		return prioritization.FeatureScoreInput{}, fmt.Errorf("prioritization inputs %s: %w", featureID, pgdb.MapError(err))
	}
	return inputsFromRow(row)
}

// InputsByProduct возвращает входы всех фич продукта по модели.
func (s *Store) InputsByProduct(ctx context.Context, modelID, productID kernel.ID) ([]prioritization.FeatureScoreInput, error) {
	rows, err := s.q(ctx).ListInputsByProduct(ctx, db.ListInputsByProductParams{ModelID: modelID, ProductID: productID})
	if err != nil {
		return nil, fmt.Errorf("prioritization inputs by product %s: %w", productID, pgdb.MapError(err))
	}
	out := make([]prioritization.FeatureScoreInput, 0, len(rows))
	for _, r := range rows {
		in, err := inputsFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

func inputsFromRow(r db.PrioritizationFeatureInput) (prioritization.FeatureScoreInput, error) {
	vals := map[string]decimal.Decimal{}
	if len(r.Values) > 0 {
		if err := json.Unmarshal(r.Values, &vals); err != nil {
			return prioritization.FeatureScoreInput{}, fmt.Errorf("prioritization inputs %s: values: %w", r.FeatureID, err)
		}
	}
	return prioritization.FeatureScoreInput{
		ModelID: r.ModelID, FeatureID: r.FeatureID, ProductID: r.ProductID, Values: vals,
		UpdatedAt: r.UpdatedAt.UTC(), UpdatedBy: r.UpdatedBy,
	}, nil
}

// SaveFlags сохраняет флаги фичи (PR-04).
func (s *Store) SaveFlags(ctx context.Context, f prioritization.FeatureFlags) error {
	err := s.q(ctx).UpsertFlags(ctx, db.UpsertFlagsParams{
		FeatureID: f.FeatureID, ProductID: f.ProductID, RegulatoryMandatory: f.RegulatoryMandatory,
		Reason: f.Reason, SetBy: f.SetBy, SetAt: f.SetAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("prioritization flags %s: %w", f.FeatureID, pgdb.MapError(err))
	}
	return nil
}

// Flags возвращает флаги фичи; kernel.ErrNotFound, если не задавались.
func (s *Store) Flags(ctx context.Context, featureID kernel.ID) (prioritization.FeatureFlags, error) {
	r, err := s.q(ctx).GetFlags(ctx, featureID)
	if err != nil {
		return prioritization.FeatureFlags{}, fmt.Errorf("prioritization flags %s: %w", featureID, pgdb.MapError(err))
	}
	return prioritization.FeatureFlags{
		FeatureID: r.FeatureID, ProductID: r.ProductID, RegulatoryMandatory: r.RegulatoryMandatory,
		Reason: r.Reason, SetBy: r.SetBy, SetAt: r.SetAt.UTC(),
	}, nil
}

// SaveDevCost сохраняет стоимость разработки фичи (PR-05).
func (s *Store) SaveDevCost(ctx context.Context, productID, featureID kernel.ID, cost kernel.Money) error {
	err := s.q(ctx).UpsertDevCost(ctx, db.UpsertDevCostParams{FeatureID: featureID, ProductID: productID, Amount: cost.Amount, Currency: cost.Currency})
	if err != nil {
		return fmt.Errorf("prioritization dev cost %s: %w", featureID, pgdb.MapError(err))
	}
	return nil
}

// DevCost возвращает стоимость разработки; kernel.ErrNotFound, если не задана.
func (s *Store) DevCost(ctx context.Context, featureID kernel.ID) (kernel.Money, error) {
	r, err := s.q(ctx).GetDevCost(ctx, featureID)
	if err != nil {
		return kernel.Money{}, fmt.Errorf("prioritization dev cost %s: %w", featureID, pgdb.MapError(err))
	}
	return kernel.Money{Amount: r.Amount, Currency: r.Currency}, nil
}
