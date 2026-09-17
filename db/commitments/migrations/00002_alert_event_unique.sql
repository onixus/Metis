-- +goose Up
-- CT-03: одно событие поднимает по обязательству не больше одного алерта. Список алертов
-- append-only (DELETE запрещён триггером), поэтому дубль при повторной доставке события остался бы
-- навсегда и исказил счётчик открытых алертов. Алерты без события (event_id IS NULL) не ограничены.
CREATE UNIQUE INDEX alerts_commitment_event_uniq
    ON commitments.alerts (commitment_id, event_id)
    WHERE event_id IS NOT NULL;

-- +goose Down
DROP INDEX commitments.alerts_commitment_event_uniq;
