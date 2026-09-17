-- name: InsertOutbox :exec
INSERT INTO kernel.outbox (id, event_type, aggregate_id, product_id, occurred_at, actor, payload, next_attempt_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8);

-- name: ClaimOutbox :many
SELECT id, event_type, aggregate_id, product_id, occurred_at, actor, payload, attempts, next_attempt_at, last_error, created_at
FROM kernel.outbox
WHERE next_attempt_at <= $1
ORDER BY next_attempt_at, created_at
LIMIT $2
FOR UPDATE SKIP LOCKED;

-- name: DeleteOutbox :exec
DELETE FROM kernel.outbox WHERE id = $1;

-- name: RetryOutbox :exec
UPDATE kernel.outbox
SET attempts = $2, next_attempt_at = $3, last_error = $4
WHERE id = $1;

-- name: MoveOutboxToDLQ :exec
INSERT INTO kernel.outbox_dlq (id, event_type, aggregate_id, product_id, occurred_at, actor, payload, attempts, error, created_at, failed_at)
SELECT o.id, o.event_type, o.aggregate_id, o.product_id, o.occurred_at, o.actor, o.payload, $2, $3, o.created_at, $4
FROM kernel.outbox o WHERE o.id = $1;

-- name: CountDLQ :one
SELECT count(*) FROM kernel.outbox_dlq;

-- name: ListDLQ :many
SELECT id, event_type, aggregate_id, product_id, occurred_at, actor, payload, attempts, error, created_at, failed_at
FROM kernel.outbox_dlq
ORDER BY failed_at DESC
LIMIT $1;

-- name: RequeueFromDLQ :execrows
INSERT INTO kernel.outbox (id, event_type, aggregate_id, product_id, occurred_at, actor, payload, attempts, next_attempt_at, last_error, created_at)
SELECT d.id, d.event_type, d.aggregate_id, d.product_id, d.occurred_at, d.actor, d.payload, 0, $2, d.error, d.created_at
FROM kernel.outbox_dlq d WHERE d.id = $1;

-- name: DeleteDLQ :exec
DELETE FROM kernel.outbox_dlq WHERE id = $1;
