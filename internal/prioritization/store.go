package prioritization

import (
	"context"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Store — хранилище моделей и входов. Авторизация выполняется в Service до вызова.
type Store interface {
	SaveModel(ctx context.Context, sc authz.Scope, m ScoringModel) error
	Model(ctx context.Context, sc authz.Scope, id kernel.ID) (ScoringModel, error)
	Models(ctx context.Context, sc authz.Scope) ([]ScoringModel, error)
	SaveInputs(ctx context.Context, sc authz.Scope, in FeatureScoreInput) error
	Inputs(ctx context.Context, sc authz.Scope, modelID, featureID kernel.ID) (FeatureScoreInput, error)
	InputsByProduct(ctx context.Context, sc authz.Scope, modelID, productID kernel.ID) ([]FeatureScoreInput, error)
	// Флаги фичи (PR-04): Flags возвращает ErrNotFound, если флаги не задавались.
	SaveFlags(ctx context.Context, sc authz.Scope, f FeatureFlags) error
	Flags(ctx context.Context, sc authz.Scope, featureID kernel.ID) (FeatureFlags, error)
	// Стоимость разработки (PR-05): DevCost возвращает ErrNotFound, если не задана.
	SaveDevCost(ctx context.Context, sc authz.Scope, productID, featureID kernel.ID, cost kernel.Money) error
	DevCost(ctx context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error)
}

type inputKey struct{ model, feature kernel.ID }

// MemStore — хранилище в памяти.
type MemStore struct {
	mu     sync.Mutex
	models map[kernel.ID]ScoringModel
	order  []kernel.ID
	inputs map[inputKey]FeatureScoreInput
	flags  map[kernel.ID]FeatureFlags
	costs  map[kernel.ID]storedCost
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore {
	return &MemStore{models: map[kernel.ID]ScoringModel{}, inputs: map[inputKey]FeatureScoreInput{},
		flags: map[kernel.ID]FeatureFlags{}, costs: map[kernel.ID]storedCost{}}
}

// SaveFlags сохраняет флаги фичи.
func (m *MemStore) SaveFlags(_ context.Context, sc authz.Scope, f FeatureFlags) error {
	f = kernel.CloneValue(f)
	if f.ProductID == kernel.NilID {
		return kernel.ErrForbidden
	}
	if !sc.Allows(authz.ActionWritePriority, f.ProductID) || (f.ProductID != kernel.NilID && sc.Product(f.ProductID) < authz.AccessPrivate) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.flags[f.FeatureID]; ok && old.ProductID != f.ProductID {
		return kernel.ErrForbidden
	}
	m.flags[f.FeatureID] = f
	return nil
}

// Flags возвращает флаги фичи.
func (m *MemStore) Flags(_ context.Context, sc authz.Scope, featureID kernel.ID) (FeatureFlags, error) {
	if !sc.Valid() {
		return FeatureFlags{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flags[featureID]
	if !ok {
		return FeatureFlags{}, kernel.NotFound("feature flags", featureID)
	}
	if !sc.Allows(authz.ActionReadStrategic, f.ProductID) {
		return FeatureFlags{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(f), nil
}

// SaveDevCost сохраняет стоимость разработки фичи.
func (m *MemStore) SaveDevCost(_ context.Context, sc authz.Scope, productID, featureID kernel.ID, cost kernel.Money) error {
	cost = kernel.CloneValue(cost)
	if productID == kernel.NilID {
		return kernel.ErrForbidden
	}
	if !sc.Allows(authz.ActionWritePriority, productID) || (productID != kernel.NilID && sc.Product(productID) < authz.AccessPrivate) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.costs[featureID]; ok && old.ProductID != productID {
		return kernel.ErrForbidden
	}
	m.costs[featureID] = storedCost{ProductID: productID, Money: cost}
	return nil
}

// DevCost возвращает стоимость разработки фичи.
func (m *MemStore) DevCost(_ context.Context, sc authz.Scope, featureID kernel.ID) (kernel.Money, error) {
	if !sc.Valid() {
		return kernel.Money{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.costs[featureID]
	if !ok {
		return kernel.Money{}, kernel.NotFound("dev cost", featureID)
	}
	if !sc.Allows(authz.ActionReadStrategic, c.ProductID) {
		return kernel.Money{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(c.Money), nil
}

// SaveModel сохраняет модель.
func (m *MemStore) SaveModel(_ context.Context, sc authz.Scope, sm ScoringModel) error {
	sm = kernel.CloneValue(sm)
	if !sc.Allows(authz.ActionWritePriority, sm.ProductID) || (sm.ProductID != kernel.NilID && sc.Product(sm.ProductID) < authz.AccessPrivate) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.models[sm.ID]; ok && old.ProductID != sm.ProductID {
		return kernel.ErrForbidden
	}
	if _, ok := m.models[sm.ID]; !ok {
		m.order = append(m.order, sm.ID)
	}
	sm.Inputs = append([]string(nil), sm.Inputs...)
	m.models[sm.ID] = sm
	return nil
}

// Model возвращает модель.
func (m *MemStore) Model(_ context.Context, sc authz.Scope, id kernel.ID) (ScoringModel, error) {
	if !sc.Valid() {
		return ScoringModel{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sm, ok := m.models[id]
	if !ok {
		return ScoringModel{}, kernel.NotFound("scoring model", id)
	}
	if sm.ProductID != kernel.NilID && !sc.Allows(authz.ActionReadStrategic, sm.ProductID) {
		return ScoringModel{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(sm), nil
}

// Models возвращает все модели в порядке создания.
func (m *MemStore) Models(_ context.Context, sc authz.Scope) ([]ScoringModel, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ScoringModel, 0, len(m.order))
	for _, id := range m.order {
		v := m.models[id]
		if v.ProductID == kernel.NilID || sc.Allows(authz.ActionReadStrategic, v.ProductID) {
			v.Inputs = append([]string(nil), v.Inputs...)
			out = append(out, v)
		}
	}
	return kernel.CloneValue(out), nil
}

// SaveInputs сохраняет входы фичи.
func (m *MemStore) SaveInputs(_ context.Context, sc authz.Scope, in FeatureScoreInput) error {
	in = kernel.CloneValue(in)
	if in.ProductID == kernel.NilID {
		return kernel.ErrForbidden
	}
	if !sc.Allows(authz.ActionWritePriority, in.ProductID) || (in.ProductID != kernel.NilID && sc.Product(in.ProductID) < authz.AccessPrivate) {
		return kernel.ErrForbidden
	}
	if !sc.Valid() {
		return kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.inputs[inputKey{in.ModelID, in.FeatureID}]; ok && old.ProductID != in.ProductID {
		return kernel.ErrForbidden
	}
	m.inputs[inputKey{in.ModelID, in.FeatureID}] = copyInputs(in)
	return nil
}

// Inputs возвращает входы фичи.
func (m *MemStore) Inputs(_ context.Context, sc authz.Scope, modelID, featureID kernel.ID) (FeatureScoreInput, error) {
	if !sc.Valid() {
		return FeatureScoreInput{}, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.inputs[inputKey{modelID, featureID}]
	if !ok {
		return FeatureScoreInput{}, kernel.NotFound("feature score input", featureID)
	}
	if !sc.Allows(authz.ActionReadStrategic, in.ProductID) {
		return FeatureScoreInput{}, kernel.ErrForbidden
	}
	return kernel.CloneValue(copyInputs(in)), nil
}

// InputsByProduct возвращает входы всех фич продукта по модели.
func (m *MemStore) InputsByProduct(_ context.Context, sc authz.Scope, modelID, productID kernel.ID) ([]FeatureScoreInput, error) {
	if !sc.Allows(authz.ActionReadStrategic, productID) {
		return nil, kernel.ErrForbidden
	}
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FeatureScoreInput
	for _, in := range m.inputs {
		if in.ModelID == modelID && in.ProductID == productID {
			out = append(out, copyInputs(in))
		}
	}
	return kernel.CloneValue(out), nil
}

func copyInputs(in FeatureScoreInput) FeatureScoreInput {
	vals := make(map[string]decimal.Decimal, len(in.Values))
	for k, v := range in.Values {
		vals[k] = v
	}
	in.Values = vals
	return in
}

type storedCost struct {
	ProductID kernel.ID
	Money     kernel.Money
}
