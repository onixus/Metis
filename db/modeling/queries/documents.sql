-- name: GetDocument :one
SELECT body FROM modeling.documents WHERE kind = $1 AND key = $2 ORDER BY revision DESC LIMIT 1;

-- name: ListDocuments :many
SELECT DISTINCT ON (key) key, body FROM modeling.documents WHERE kind = $1 ORDER BY key, revision DESC;

-- name: AppendDocument :exec
INSERT INTO modeling.documents(kind, key, revision, product_id, body)
SELECT $1, $2, COALESCE(MAX(revision), 0) + 1, $3, $4 FROM modeling.documents WHERE kind = $1 AND key = $2;

-- name: ListFacts :many
SELECT f.body FROM modeling.documents f
JOIN modeling.documents b ON b.kind = 'batch' AND b.key = f.body->>'batch_id'
WHERE f.kind = 'fact' AND b.body->>'status' = 'applied'
AND (sqlc.arg(all_products)::boolean OR f.product_id = ANY(sqlc.arg(products)::uuid[]))
AND CASE WHEN sqlc.arg(explicit_version)::boolean
THEN (f.body->>'data_version')::bigint = sqlc.arg(data_version)::bigint
ELSE (f.body->>'data_version')::bigint = (
 SELECT max((latest.body->>'data_version')::bigint) FROM modeling.documents latest
 WHERE latest.kind = 'batch' AND latest.body->>'status' = 'applied'
 AND latest.body->>'period' = f.body->>'period'
) END
ORDER BY f.key;
