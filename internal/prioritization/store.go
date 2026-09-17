package prioritization

import (
	"context"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
)

// Store — хранилище моделей и входов. Авторизация выполняется в Service до вызова.
type Store interface {
	SaveModel(ctx context.Context, m ScoringModel) error
	Model(ctx context.Context, id kernel.ID) (ScoringModel, error)
	Models(ctx context.Context) ([]ScoringModel, error)
	SaveInputs(ctx context.Context, in FeatureScoreInput) error
	Inputs(ctx context.Context, modelID, featureID kernel.ID) (FeatureScoreInput, error)
	InputsByProduct(ctx context.Context, modelID, productID kernel.ID) ([]FeatureScoreInput, error)
}

type inputKey struct{ model, feature kernel.ID }

// MemStore — хранилище в памяти.
type MemStore struct {
	mu     sync.Mutex
	models map[kernel.ID]ScoringModel
	order  []kernel.ID
	inputs map[inputKey]FeatureScoreInput
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore {
	return &MemStore{models: map[kernel.ID]ScoringModel{}, inputs: map[inputKey]FeatureScoreInput{}}
}

// SaveModel сохраняет модель.
func (m *MemStore) SaveModel(_ context.Context, sm ScoringModel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.models[sm.ID]; !ok {
		m.order = append(m.order, sm.ID)
	}
	sm.Inputs = append([]string(nil), sm.Inputs...)
	m.models[sm.ID] = sm
	return nil
}

// Model возвращает модель.
func (m *MemStore) Model(_ context.Context, id kernel.ID) (ScoringModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sm, ok := m.models[id]
	if !ok {
		return ScoringModel{}, kernel.NotFound("scoring model", id)
	}
	return sm, nil
}

// Models возвращает все модели в порядке создания.
func (m *MemStore) Models(context.Context) ([]ScoringModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ScoringModel, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.models[id])
	}
	return out, nil
}

// SaveInputs сохраняет входы фичи.
func (m *MemStore) SaveInputs(_ context.Context, in FeatureScoreInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs[inputKey{in.ModelID, in.FeatureID}] = copyInputs(in)
	return nil
}

// Inputs возвращает входы фичи.
func (m *MemStore) Inputs(_ context.Context, modelID, featureID kernel.ID) (FeatureScoreInput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.inputs[inputKey{modelID, featureID}]
	if !ok {
		return FeatureScoreInput{}, kernel.NotFound("feature score input", featureID)
	}
	return copyInputs(in), nil
}

// InputsByProduct возвращает входы всех фич продукта по модели.
func (m *MemStore) InputsByProduct(_ context.Context, modelID, productID kernel.ID) ([]FeatureScoreInput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FeatureScoreInput
	for _, in := range m.inputs {
		if in.ModelID == modelID && in.ProductID == productID {
			out = append(out, copyInputs(in))
		}
	}
	return out, nil
}

func copyInputs(in FeatureScoreInput) FeatureScoreInput {
	vals := make(map[string]decimal.Decimal, len(in.Values))
	for k, v := range in.Values {
		vals[k] = v
	}
	in.Values = vals
	return in
}
