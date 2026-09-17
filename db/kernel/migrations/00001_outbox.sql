-- +goose Up
CREATE SCHEMA IF NOT EXISTS kernel;

-- Transactional outbox (ADR-0002, NF-R06). Запись создаётся в той же транзакции, что и доменное изменение.
CREATE TABLE kernel.outbox (
    id              uuid        PRIMARY KEY,
    event_type      text        NOT NULL,
    aggregate_id    uuid        NOT NULL,
    product_id      uuid        NULL,
    occurred_at     timestamptz NOT NULL,
    actor           text        NOT NULL DEFAULT '',
    payload         jsonb       NOT NULL DEFAULT 'null'::jsonb,
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text        NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_next_attempt_idx ON kernel.outbox (next_attempt_at);
CREATE INDEX outbox_product_idx ON kernel.outbox (product_id);

-- Очередь недоставленных: событие после max_attempts с текстом последней ошибки.
CREATE TABLE kernel.outbox_dlq (
    id              uuid        PRIMARY KEY,
    event_type      text        NOT NULL,
    aggregate_id    uuid        NOT NULL,
    product_id      uuid        NULL,
    occurred_at     timestamptz NOT NULL,
    actor           text        NOT NULL DEFAULT '',
    payload         jsonb       NOT NULL DEFAULT 'null'::jsonb,
    attempts        integer     NOT NULL,
    error           text        NOT NULL,
    created_at      timestamptz NOT NULL,
    failed_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_dlq_failed_idx ON kernel.outbox_dlq (failed_at);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA kernel TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON kernel.outbox, kernel.outbox_dlq TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE kernel.outbox_dlq;
DROP TABLE kernel.outbox;
DROP SCHEMA kernel;
