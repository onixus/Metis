-- name: UpsertCommitment :exec
INSERT INTO commitments.commitments (id, product_id, kind, subtype, counterparty, subject, due_date, basis, owner, status, feature_id, release_id, renewal_item_id, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, kind = EXCLUDED.kind, subtype = EXCLUDED.subtype,
  counterparty = EXCLUDED.counterparty, subject = EXCLUDED.subject, due_date = EXCLUDED.due_date, basis = EXCLUDED.basis,
  owner = EXCLUDED.owner, status = EXCLUDED.status, feature_id = EXCLUDED.feature_id, release_id = EXCLUDED.release_id,
  renewal_item_id = EXCLUDED.renewal_item_id, created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetCommitment :one
SELECT * FROM commitments.commitments WHERE id = $1;

-- name: ListCommitments :many
SELECT * FROM commitments.commitments
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
  AND (@kind::text = '' OR kind = @kind::text)
  AND (@subtype::text = '' OR subtype = @subtype::text)
  AND (sqlc.narg('feature_id')::uuid IS NULL OR feature_id = sqlc.narg('feature_id')::uuid)
  AND (sqlc.narg('release_id')::uuid IS NULL OR release_id = sqlc.narg('release_id')::uuid)
  AND (sqlc.narg('due_before')::date IS NULL OR due_date <= sqlc.narg('due_before')::date)
  AND (cardinality(@statuses::text[]) = 0 OR status = ANY(@statuses::text[]))
ORDER BY created_at, id;

-- name: InsertAlert :exec
INSERT INTO commitments.alerts (id, commitment_id, product_id, kind, message, event_id, new_date, due_date, raised_at, acknowledged, acknowledged_by, acknowledged_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetAlert :one
SELECT * FROM commitments.alerts WHERE id = $1;

-- name: ListAlerts :many
SELECT * FROM commitments.alerts WHERE product_id = $1 AND (NOT @only_open::bool OR NOT acknowledged) ORDER BY seq;

-- name: AcknowledgeAlert :execrows
UPDATE commitments.alerts SET acknowledged = $2, acknowledged_by = $3, acknowledged_at = $4 WHERE id = $1;

-- name: EventProcessed :one
SELECT EXISTS (SELECT 1 FROM commitments.processed_events WHERE event_id = $1);

-- name: MarkEventProcessed :exec
INSERT INTO commitments.processed_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING;

-- name: GetSettings :one
SELECT lead_months FROM commitments.settings WHERE id = 1;

-- name: UpsertSettings :exec
INSERT INTO commitments.settings (id, lead_months, updated_at) VALUES (1, $1, $2)
ON CONFLICT (id) DO UPDATE SET lead_months = EXCLUDED.lead_months, updated_at = EXCLUDED.updated_at;
