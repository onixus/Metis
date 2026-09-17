-- name: UpsertSignal :exec
INSERT INTO signals.signals (id, product_id, source, text, external_key, account_id, deal_id, version, segment,
  weight_amount, weight_currency, account_arr_amount, account_arr_currency, blocks_deal, status, due_date,
  feature_id, contract_id, hypothesis_id, merged_into, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
ON CONFLICT (id) DO UPDATE SET
  product_id = EXCLUDED.product_id, source = EXCLUDED.source, text = EXCLUDED.text, external_key = EXCLUDED.external_key,
  account_id = EXCLUDED.account_id, deal_id = EXCLUDED.deal_id, version = EXCLUDED.version, segment = EXCLUDED.segment,
  weight_amount = EXCLUDED.weight_amount, weight_currency = EXCLUDED.weight_currency,
  account_arr_amount = EXCLUDED.account_arr_amount, account_arr_currency = EXCLUDED.account_arr_currency,
  blocks_deal = EXCLUDED.blocks_deal, status = EXCLUDED.status, due_date = EXCLUDED.due_date,
  feature_id = EXCLUDED.feature_id, contract_id = EXCLUDED.contract_id, hypothesis_id = EXCLUDED.hypothesis_id,
  merged_into = EXCLUDED.merged_into, created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetSignal :one
SELECT * FROM signals.signals WHERE id = $1;

-- name: GetSignalByExternalKey :one
SELECT * FROM signals.signals WHERE external_key = $1 AND external_key <> '' ORDER BY created_at, id LIMIT 1;

-- name: ListSignals :many
SELECT * FROM signals.signals
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
  AND (sqlc.narg('feature_id')::uuid IS NULL OR feature_id = sqlc.narg('feature_id')::uuid)
  AND (sqlc.narg('contract_id')::uuid IS NULL OR contract_id = sqlc.narg('contract_id')::uuid)
  AND (sqlc.narg('hypothesis_id')::uuid IS NULL OR hypothesis_id = sqlc.narg('hypothesis_id')::uuid)
  AND (cardinality(@statuses::text[]) = 0 OR status = ANY(@statuses::text[]))
ORDER BY created_at, id;
