-- +goose Up
CREATE SCHEMA IF NOT EXISTS roadmap;

-- Релизы (RM-04, RM-05). Матрица совместимости вычисляется при чтении и не хранится.
CREATE TABLE roadmap.releases (
    id              uuid        PRIMARY KEY,
    product_id      uuid        NOT NULL,
    name            text        NOT NULL,
    version         text        NOT NULL DEFAULT '',
    planned_date    date        NULL,
    status          text        NOT NULL,
    branch          text        NOT NULL DEFAULT 'evolving',
    base_release_id uuid        NULL,
    feature_ids     uuid[]      NOT NULL DEFAULT '{}',
    release_notes   text        NOT NULL DEFAULT '',
    eol             date        NULL,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE INDEX releases_product_idx ON roadmap.releases (product_id);

-- Элементы roadmap (RM-01…RM-04, CT-04).
CREATE TABLE roadmap.items (
    id            uuid        PRIMARY KEY,
    product_id    uuid        NOT NULL,
    feature_id    uuid        NULL,
    title         text        NOT NULL,
    bucket        text        NOT NULL,
    start_date    date        NULL,
    end_date      date        NULL,
    release_id    uuid        NULL,
    audience      text        NOT NULL DEFAULT '',
    status        text        NOT NULL,
    kind          text        NOT NULL DEFAULT 'feature',
    commitment_id uuid        NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);
CREATE INDEX items_product_idx ON roadmap.items (product_id);
CREATE INDEX items_feature_idx ON roadmap.items (feature_id);
CREATE INDEX items_release_idx ON roadmap.items (release_id);
CREATE INDEX items_commitment_idx ON roadmap.items (commitment_id);

-- История дат (RM-03): только INSERT (инвариант 8).
CREATE TABLE roadmap.date_history (
    seq        bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id         uuid        NOT NULL UNIQUE,
    item_id    uuid        NOT NULL,
    product_id uuid        NOT NULL,
    old_start  date        NULL,
    old_end    date        NULL,
    new_start  date        NULL,
    new_end    date        NULL,
    reason     text        NOT NULL,
    actor      text        NOT NULL DEFAULT '',
    at         timestamptz NOT NULL,
    event_id   uuid        NULL
);
CREATE INDEX date_history_item_idx ON roadmap.date_history (item_id, seq);

-- +goose StatementBegin
CREATE FUNCTION roadmap.reject_change() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION '%.%: % запрещён (журнал только для INSERT)', TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP
    USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER date_history_immutable
  BEFORE UPDATE OR DELETE ON roadmap.date_history
  FOR EACH ROW EXECUTE FUNCTION roadmap.reject_change();

-- Обработанные события (идемпотентность обработчиков по Event.ID).
CREATE TABLE roadmap.processed_events (
    event_id     uuid        PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA roadmap TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA roadmap TO metis_app;
    REVOKE UPDATE, DELETE ON roadmap.date_history FROM metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE roadmap.processed_events;
DROP TRIGGER date_history_immutable ON roadmap.date_history;
DROP FUNCTION roadmap.reject_change();
DROP TABLE roadmap.date_history;
DROP TABLE roadmap.items;
DROP TABLE roadmap.releases;
DROP SCHEMA roadmap;
