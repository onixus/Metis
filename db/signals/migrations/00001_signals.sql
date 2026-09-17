-- +goose Up
CREATE SCHEMA IF NOT EXISTS signals;

-- Сигналы (SG-01…SG-05, SG-04 слияние, DS-01 привязка к гипотезе).
-- Время — timestamptz (UTC), плановые даты — date, деньги — bigint + код валюты (инварианты 6, 7).
CREATE TABLE signals.signals (
    id                   uuid        PRIMARY KEY,
    product_id           uuid        NOT NULL,
    source               text        NOT NULL,
    text                 text        NOT NULL,
    external_key         text        NOT NULL DEFAULT '',
    account_id           text        NOT NULL DEFAULT '',
    deal_id              text        NOT NULL DEFAULT '',
    version              text        NOT NULL DEFAULT '',
    segment              text        NOT NULL DEFAULT '',
    weight_amount        bigint      NOT NULL DEFAULT 0,
    weight_currency      text        NOT NULL DEFAULT '',
    account_arr_amount   bigint      NOT NULL DEFAULT 0,
    account_arr_currency text        NOT NULL DEFAULT '',
    blocks_deal          boolean     NOT NULL DEFAULT false,
    status               text        NOT NULL,
    due_date             date        NULL,
    feature_id           uuid        NULL,
    contract_id          uuid        NULL,
    hypothesis_id        uuid        NULL,
    merged_into          uuid        NULL,
    created_by           text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL
);
CREATE INDEX signals_product_idx ON signals.signals (product_id);
CREATE INDEX signals_feature_idx ON signals.signals (feature_id);
CREATE INDEX signals_contract_idx ON signals.signals (contract_id);
CREATE INDEX signals_hypothesis_idx ON signals.signals (hypothesis_id);
CREATE INDEX signals_external_key_idx ON signals.signals (external_key) WHERE external_key <> '';

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA signals TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA signals TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE signals.signals;
DROP SCHEMA signals;
