-- +goose Up
CREATE SCHEMA IF NOT EXISTS delivery;

-- Привязки фич к эпикам и релизов к версиям трекера (DL-01).
CREATE TABLE delivery.mappings (
    feature_id uuid        PRIMARY KEY,
    product_id uuid        NOT NULL,
    epic_key   text        NOT NULL,
    project    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL
);
CREATE INDEX mappings_epic_idx ON delivery.mappings (epic_key);
CREATE INDEX mappings_product_idx ON delivery.mappings (product_id);

CREATE TABLE delivery.release_mappings (
    release_id  uuid        PRIMARY KEY,
    product_id  uuid        NOT NULL,
    project     text        NOT NULL DEFAULT '',
    fix_version text        NOT NULL,
    created_at  timestamptz NOT NULL
);
CREATE INDEX release_mappings_product_idx ON delivery.release_mappings (product_id);

-- Проекция эпика (только чтение полей трекера, инвариант 4).
CREATE TABLE delivery.epics (
    feature_id      uuid        PRIMARY KEY,
    product_id      uuid        NOT NULL,
    epic_key        text        NOT NULL,
    summary         text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT '',
    due_date        date        NULL,
    fix_versions    text[]      NOT NULL DEFAULT '{}',
    issues          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    initial_scope   text[]      NOT NULL DEFAULT '{}',
    first_seen_at   timestamptz NULL,
    synced_at       timestamptz NULL,
    source_event_id text        NOT NULL DEFAULT ''
);
CREATE INDEX epics_product_idx ON delivery.epics (product_id);

-- Спринты продукта (DL-02): набор заменяется целиком при синхронизации; position — порядок.
CREATE TABLE delivery.sprints (
    product_id   uuid        NOT NULL,
    position     bigint      NOT NULL,
    board        text        NOT NULL DEFAULT '',
    sprint_id    text        NOT NULL DEFAULT '',
    name         text        NOT NULL DEFAULT '',
    goal         text        NOT NULL DEFAULT '',
    state        text        NOT NULL DEFAULT '',
    start_date   date        NULL,
    end_date     date        NULL,
    issues       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    total        bigint      NOT NULL DEFAULT 0,
    done         bigint      NOT NULL DEFAULT 0,
    carried_over text[]      NOT NULL DEFAULT '{}',
    synced_at    timestamptz NULL,
    PRIMARY KEY (product_id, position)
);

-- Состояние синхронизации (NF-R05, AD-05): одна строка.
CREATE TABLE delivery.sync_state (
    id              smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_success_at timestamptz NULL,
    last_attempt_at timestamptz NULL,
    last_error      text        NOT NULL DEFAULT '',
    lag_ns          bigint      NOT NULL DEFAULT 0,
    stale           boolean     NOT NULL DEFAULT true
);

-- Маппинг полей и статусов трекера (AD-05): одна строка.
CREATE TABLE delivery.field_mapping (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    mapping    jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Обработанные события трекера по внешнему ключу (ТЗ 4.2).
CREATE TABLE delivery.processed_events (
    external_id  text        PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA delivery TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA delivery TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE delivery.processed_events;
DROP TABLE delivery.field_mapping;
DROP TABLE delivery.sync_state;
DROP TABLE delivery.sprints;
DROP TABLE delivery.epics;
DROP TABLE delivery.release_mappings;
DROP TABLE delivery.mappings;
DROP SCHEMA delivery;
