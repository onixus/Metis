-- name: GetSnapshot :one
SELECT body FROM economics.snapshots
WHERE product_id = $1 AND period = $2 AND (sqlc.arg(requested_version)::integer = 0 OR version = sqlc.arg(requested_version)::integer)
ORDER BY version DESC LIMIT 1;

-- name: ListSnapshots :many
SELECT body FROM economics.snapshots WHERE product_id = $1 AND period = $2 ORDER BY version;

-- name: AppendSnapshot :execrows
INSERT INTO economics.snapshots (id, product_id, period, version, body, created_at)
SELECT sqlc.arg(id), sqlc.arg(product_id), sqlc.arg(period), sqlc.arg(version), sqlc.arg(body), sqlc.arg(created_at)
WHERE (SELECT COALESCE(MAX(version),0) FROM economics.snapshots WHERE product_id=sqlc.arg(product_id) AND period=sqlc.arg(period)) = sqlc.arg(expected_version)::integer;

-- name: UpsertImportTemplate :exec
INSERT INTO economics.import_templates(product_id,name,body) VALUES ($1,$2,$3)
ON CONFLICT(product_id,name) DO UPDATE SET body=EXCLUDED.body;

-- name: ListImportTemplates :many
SELECT body FROM economics.import_templates WHERE product_id=$1 ORDER BY name;
