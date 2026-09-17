-- name: LastRecord :one
SELECT seq, at, actor, action, object_type, object_id, product_id, details, prev_hash, hash
FROM audit.records ORDER BY seq DESC LIMIT 1;

-- name: InsertRecord :exec
INSERT INTO audit.records (seq, at, actor, action, object_type, object_id, product_id, details, prev_hash, hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: RecordsAfter :many
SELECT seq, at, actor, action, object_type, object_id, product_id, details, prev_hash, hash
FROM audit.records WHERE seq > $1 ORDER BY seq LIMIT $2;
