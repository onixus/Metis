-- +goose Up
CREATE SCHEMA IF NOT EXISTS portfoliograph;

-- Время — timestamptz (UTC), плановые даты — date, деньги — bigint + код валюты (инварианты 6, 7).

CREATE TABLE portfoliograph.products (
    id              uuid        PRIMARY KEY,
    key             text        NOT NULL UNIQUE,
    name            text        NOT NULL,
    type            text        NOT NULL,
    owner           text        NOT NULL DEFAULT '',
    lifecycle       text        NOT NULL,
    ssdlc_certified boolean     NOT NULL DEFAULT false,
    hub_manual      boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);

CREATE TABLE portfoliograph.capabilities (
    id         uuid PRIMARY KEY,
    product_id uuid NOT NULL REFERENCES portfoliograph.products (id),
    name       text NOT NULL
);
CREATE INDEX capabilities_product_idx ON portfoliograph.capabilities (product_id);

CREATE TABLE portfoliograph.features (
    id                 uuid        PRIMARY KEY,
    product_id         uuid        NOT NULL REFERENCES portfoliograph.products (id),
    capability_id      uuid        NULL REFERENCES portfoliograph.capabilities (id),
    name               text        NOT NULL,
    status             text        NOT NULL,
    own_value_amount   bigint      NOT NULL DEFAULT 0,
    own_value_currency text        NOT NULL DEFAULT '',
    planned_date       date        NULL,
    affected           boolean     NOT NULL DEFAULT false,
    affected_by        uuid        NULL,
    implied_date       date        NULL,
    external_key       text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL
);
CREATE INDEX features_product_idx ON portfoliograph.features (product_id);
CREATE INDEX features_capability_idx ON portfoliograph.features (capability_id);

CREATE TABLE portfoliograph.requirements (
    id         uuid PRIMARY KEY,
    product_id uuid NOT NULL REFERENCES portfoliograph.products (id),
    feature_id uuid NOT NULL REFERENCES portfoliograph.features (id),
    text       text NOT NULL
);
CREATE INDEX requirements_product_idx ON portfoliograph.requirements (product_id);
CREATE INDEX requirements_feature_idx ON portfoliograph.requirements (feature_id);

CREATE TABLE portfoliograph.contracts (
    id                   uuid        PRIMARY KEY,
    name                 text        NOT NULL,
    provider_product_id  uuid        NOT NULL REFERENCES portfoliograph.products (id),
    consumer_product_id  uuid        NOT NULL REFERENCES portfoliograph.products (id),
    provider_feature_ids uuid[]      NOT NULL DEFAULT '{}',
    consumer_feature_ids uuid[]      NOT NULL DEFAULT '{}',
    interface_version    text        NOT NULL DEFAULT '',
    owner                text        NOT NULL DEFAULT '',
    status               text        NOT NULL,
    criticality          text        NOT NULL,
    compatibility        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    signal_value_amount  bigint      NOT NULL DEFAULT 0,
    signal_value_currency text       NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL
);
CREATE INDEX contracts_provider_idx ON portfoliograph.contracts (provider_product_id);
CREATE INDEX contracts_consumer_idx ON portfoliograph.contracts (consumer_product_id);

CREATE TABLE portfoliograph.links (
    id              uuid        PRIMARY KEY,
    type            text        NOT NULL,
    from_product_id uuid        NOT NULL REFERENCES portfoliograph.products (id),
    to_product_id   uuid        NOT NULL REFERENCES portfoliograph.products (id),
    from_feature_id uuid        NULL REFERENCES portfoliograph.features (id),
    to_feature_id   uuid        NULL REFERENCES portfoliograph.features (id),
    criticality     text        NOT NULL,
    contract_id     uuid        NULL REFERENCES portfoliograph.contracts (id),
    created_at      timestamptz NOT NULL
);
CREATE INDEX links_from_product_idx ON portfoliograph.links (from_product_id);
CREATE INDEX links_to_product_idx ON portfoliograph.links (to_product_id);
CREATE INDEX links_from_feature_idx ON portfoliograph.links (from_feature_id);
CREATE INDEX links_to_feature_idx ON portfoliograph.links (to_feature_id);

-- Одна строка настроек портфеля; коэффициенты — строки decimal (без float, инвариант 6).
CREATE TABLE portfoliograph.settings (
    id           smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    coefficients jsonb       NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Результат rollup (PG-07).
CREATE TABLE portfoliograph.feature_values (
    feature_id             uuid        PRIMARY KEY REFERENCES portfoliograph.features (id),
    product_id             uuid        NOT NULL REFERENCES portfoliograph.products (id),
    own_value_amount       bigint      NOT NULL,
    own_value_currency     text        NOT NULL DEFAULT '',
    derived_value_amount   bigint      NOT NULL,
    derived_value_currency text        NOT NULL DEFAULT '',
    total_value_amount     bigint      NOT NULL,
    total_value_currency   text        NOT NULL DEFAULT '',
    computed_at            timestamptz NOT NULL
);
CREATE INDEX feature_values_product_idx ON portfoliograph.feature_values (product_id);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA portfoliograph TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA portfoliograph TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE portfoliograph.feature_values;
DROP TABLE portfoliograph.links;
DROP TABLE portfoliograph.contracts;
DROP TABLE portfoliograph.requirements;
DROP TABLE portfoliograph.features;
DROP TABLE portfoliograph.capabilities;
DROP TABLE portfoliograph.products;
DROP TABLE portfoliograph.settings;
DROP SCHEMA portfoliograph;
