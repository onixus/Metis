-- name: UpsertModel :exec
INSERT INTO prioritization.models (id, product_id, name, type, formula, inputs, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, name = EXCLUDED.name, type = EXCLUDED.type,
  formula = EXCLUDED.formula, inputs = EXCLUDED.inputs, updated_at = EXCLUDED.updated_at;

-- name: GetModel :one
SELECT * FROM prioritization.models WHERE id = $1;

-- name: ListModels :many
SELECT * FROM prioritization.models ORDER BY created_at, id;

-- name: UpsertInputs :exec
INSERT INTO prioritization.feature_inputs (model_id, feature_id, product_id, values, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (model_id, feature_id) DO UPDATE SET product_id = EXCLUDED.product_id, values = EXCLUDED.values,
  updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by;

-- name: GetInputs :one
SELECT * FROM prioritization.feature_inputs WHERE model_id = $1 AND feature_id = $2;

-- name: ListInputsByProduct :many
SELECT * FROM prioritization.feature_inputs WHERE model_id = $1 AND product_id = $2 ORDER BY feature_id;

-- name: UpsertFlags :exec
INSERT INTO prioritization.feature_flags (feature_id, product_id, regulatory_mandatory, reason, set_by, set_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (feature_id) DO UPDATE SET product_id = EXCLUDED.product_id, regulatory_mandatory = EXCLUDED.regulatory_mandatory,
  reason = EXCLUDED.reason, set_by = EXCLUDED.set_by, set_at = EXCLUDED.set_at;

-- name: GetFlags :one
SELECT * FROM prioritization.feature_flags WHERE feature_id = $1;

-- name: UpsertDevCost :exec
INSERT INTO prioritization.dev_costs (feature_id, product_id, amount, currency) VALUES ($1, $2, $3, $4)
ON CONFLICT (feature_id) DO UPDATE SET product_id = EXCLUDED.product_id, amount = EXCLUDED.amount, currency = EXCLUDED.currency;

-- name: GetDevCost :one
SELECT * FROM prioritization.dev_costs WHERE feature_id = $1;
