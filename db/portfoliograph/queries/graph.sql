-- name: ListProducts :many
SELECT * FROM portfoliograph.products ORDER BY id;

-- name: UpsertProduct :exec
INSERT INTO portfoliograph.products (id, key, name, type, owner, lifecycle, ssdlc_certified, hub_manual, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET
  key = EXCLUDED.key, name = EXCLUDED.name, type = EXCLUDED.type, owner = EXCLUDED.owner,
  lifecycle = EXCLUDED.lifecycle, ssdlc_certified = EXCLUDED.ssdlc_certified,
  hub_manual = EXCLUDED.hub_manual, updated_at = EXCLUDED.updated_at;

-- name: ListCapabilities :many
SELECT * FROM portfoliograph.capabilities ORDER BY id;

-- name: UpsertCapability :exec
INSERT INTO portfoliograph.capabilities (id, product_id, name)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, name = EXCLUDED.name;

-- name: ListFeatures :many
SELECT * FROM portfoliograph.features ORDER BY id;

-- name: UpsertFeature :exec
INSERT INTO portfoliograph.features (id, product_id, capability_id, name, status, own_value_amount, own_value_currency,
  planned_date, affected, affected_by, implied_date, external_key, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (id) DO UPDATE SET
  product_id = EXCLUDED.product_id, capability_id = EXCLUDED.capability_id, name = EXCLUDED.name,
  status = EXCLUDED.status, own_value_amount = EXCLUDED.own_value_amount, own_value_currency = EXCLUDED.own_value_currency,
  planned_date = EXCLUDED.planned_date, affected = EXCLUDED.affected, affected_by = EXCLUDED.affected_by,
  implied_date = EXCLUDED.implied_date, external_key = EXCLUDED.external_key, updated_at = EXCLUDED.updated_at;

-- name: ListRequirements :many
SELECT * FROM portfoliograph.requirements ORDER BY id;

-- name: UpsertRequirement :exec
INSERT INTO portfoliograph.requirements (id, product_id, feature_id, text)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, feature_id = EXCLUDED.feature_id, text = EXCLUDED.text;

-- name: ListLinks :many
SELECT * FROM portfoliograph.links ORDER BY id;

-- name: UpsertLink :exec
INSERT INTO portfoliograph.links (id, type, from_product_id, to_product_id, from_feature_id, to_feature_id, criticality, contract_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
  type = EXCLUDED.type, from_product_id = EXCLUDED.from_product_id, to_product_id = EXCLUDED.to_product_id,
  from_feature_id = EXCLUDED.from_feature_id, to_feature_id = EXCLUDED.to_feature_id,
  criticality = EXCLUDED.criticality, contract_id = EXCLUDED.contract_id;

-- name: DeleteLink :execrows
DELETE FROM portfoliograph.links WHERE id = $1;

-- name: ListContracts :many
SELECT * FROM portfoliograph.contracts ORDER BY id;

-- name: UpsertContract :exec
INSERT INTO portfoliograph.contracts (id, name, provider_product_id, consumer_product_id, provider_feature_ids, consumer_feature_ids,
  interface_version, owner, status, criticality, compatibility, signal_value_amount, signal_value_currency, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name, provider_product_id = EXCLUDED.provider_product_id, consumer_product_id = EXCLUDED.consumer_product_id,
  provider_feature_ids = EXCLUDED.provider_feature_ids, consumer_feature_ids = EXCLUDED.consumer_feature_ids,
  interface_version = EXCLUDED.interface_version, owner = EXCLUDED.owner, status = EXCLUDED.status,
  criticality = EXCLUDED.criticality, compatibility = EXCLUDED.compatibility,
  signal_value_amount = EXCLUDED.signal_value_amount, signal_value_currency = EXCLUDED.signal_value_currency,
  updated_at = EXCLUDED.updated_at;

-- name: GetSettings :one
SELECT coefficients FROM portfoliograph.settings WHERE id = 1;

-- name: UpsertSettings :exec
INSERT INTO portfoliograph.settings (id, coefficients, updated_at) VALUES (1, $1, $2)
ON CONFLICT (id) DO UPDATE SET coefficients = EXCLUDED.coefficients, updated_at = EXCLUDED.updated_at;

-- name: UpsertFeatureValue :exec
INSERT INTO portfoliograph.feature_values (feature_id, product_id, own_value_amount, own_value_currency,
  derived_value_amount, derived_value_currency, total_value_amount, total_value_currency, computed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (feature_id) DO UPDATE SET
  product_id = EXCLUDED.product_id, own_value_amount = EXCLUDED.own_value_amount, own_value_currency = EXCLUDED.own_value_currency,
  derived_value_amount = EXCLUDED.derived_value_amount, derived_value_currency = EXCLUDED.derived_value_currency,
  total_value_amount = EXCLUDED.total_value_amount, total_value_currency = EXCLUDED.total_value_currency,
  computed_at = EXCLUDED.computed_at;

-- name: ListFeatureValues :many
SELECT * FROM portfoliograph.feature_values ORDER BY feature_id;

-- name: DeleteFeatureValuesByProduct :exec
DELETE FROM portfoliograph.feature_values WHERE product_id = $1;

-- name: DeleteLinksByProduct :exec
DELETE FROM portfoliograph.links WHERE from_product_id = $1 OR to_product_id = $1;

-- name: DeleteRequirementsByProduct :exec
DELETE FROM portfoliograph.requirements WHERE product_id = $1;

-- name: DeleteFeaturesByProduct :exec
DELETE FROM portfoliograph.features WHERE product_id = $1;

-- name: DeleteCapabilitiesByProduct :exec
DELETE FROM portfoliograph.capabilities WHERE product_id = $1;

-- name: DeleteProduct :execrows
DELETE FROM portfoliograph.products WHERE id = $1;
