-- +goose Up
CREATE SCHEMA IF NOT EXISTS prioritization;

-- Модели оценки (PR-01). product_id NULL — портфельная модель для всех продуктов.
CREATE TABLE prioritization.models (
    id         uuid        PRIMARY KEY,
    product_id uuid        NULL,
    name       text        NOT NULL,
    type       text        NOT NULL,
    formula    text        NOT NULL DEFAULT '',
    inputs     text[]      NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX models_product_idx ON prioritization.models (product_id);

-- Входы фичи по модели; значения — строки decimal (без float, инвариант 6).
CREATE TABLE prioritization.feature_inputs (
    model_id   uuid        NOT NULL REFERENCES prioritization.models (id) ON DELETE CASCADE,
    feature_id uuid        NOT NULL,
    product_id uuid        NOT NULL,
    values     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL,
    updated_by text        NOT NULL DEFAULT '',
    PRIMARY KEY (model_id, feature_id)
);
CREATE INDEX feature_inputs_product_idx ON prioritization.feature_inputs (model_id, product_id);

-- Флаги фичи (PR-04).
CREATE TABLE prioritization.feature_flags (
    feature_id           uuid        PRIMARY KEY,
    product_id           uuid        NOT NULL,
    regulatory_mandatory boolean     NOT NULL DEFAULT false,
    reason               text        NOT NULL DEFAULT '',
    set_by               text        NOT NULL DEFAULT '',
    set_at               timestamptz NOT NULL
);
CREATE INDEX feature_flags_product_idx ON prioritization.feature_flags (product_id);

-- Стоимость разработки (PR-05).
CREATE TABLE prioritization.dev_costs (
    feature_id uuid   PRIMARY KEY,
    product_id uuid   NOT NULL,
    amount     bigint NOT NULL DEFAULT 0,
    currency   text   NOT NULL DEFAULT ''
);
CREATE INDEX dev_costs_product_idx ON prioritization.dev_costs (product_id);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA prioritization TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA prioritization TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE prioritization.dev_costs;
DROP TABLE prioritization.feature_flags;
DROP TABLE prioritization.feature_inputs;
DROP TABLE prioritization.models;
DROP SCHEMA prioritization;
