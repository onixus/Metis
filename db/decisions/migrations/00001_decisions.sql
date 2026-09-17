-- +goose Up
CREATE SCHEMA IF NOT EXISTS decisions;

-- Decision Records (DA-01). product_id NULL — портфельное решение.
CREATE TABLE decisions.records (
    id              uuid        PRIMARY KEY,
    product_id      uuid        NULL,
    title           text        NOT NULL,
    context         text        NOT NULL DEFAULT '',
    snapshot        jsonb       NULL,
    options         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    chosen_key      text        NOT NULL DEFAULT '',
    rationale       text        NOT NULL DEFAULT '',
    expected_effect text        NOT NULL DEFAULT '',
    review_date     date        NULL,
    status          text        NOT NULL,
    superseded_by   uuid        NULL,
    links           jsonb       NOT NULL DEFAULT '[]'::jsonb,
    page_id         text        NOT NULL DEFAULT '',
    author          text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE INDEX records_product_idx ON decisions.records (product_id);
CREATE INDEX records_links_idx ON decisions.records USING gin (links jsonb_path_ops);

-- Обработанные события (идемпотентность обработчиков по Event.ID).
CREATE TABLE decisions.processed_events (
    event_id     uuid        PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA decisions TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA decisions TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE decisions.processed_events;
DROP TABLE decisions.records;
DROP SCHEMA decisions;
