-- +goose Up
CREATE SCHEMA IF NOT EXISTS discovery;

-- Гипотезы (DS-01, AD-03).
CREATE TABLE discovery.hypotheses (
    id                     uuid        PRIMARY KEY,
    product_id             uuid        NOT NULL,
    title                  text        NOT NULL,
    statement              text        NOT NULL DEFAULT '',
    assumptions            text[]      NOT NULL DEFAULT '{}',
    confirmation_criterion text        NOT NULL DEFAULT '',
    status                 text        NOT NULL,
    resolution             text        NOT NULL DEFAULT '',
    feature_id             uuid        NULL,
    custom_fields          jsonb       NULL,
    created_by             text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL,
    updated_at             timestamptz NOT NULL
);
CREATE INDEX hypotheses_product_idx ON discovery.hypotheses (product_id);
CREATE INDEX hypotheses_feature_idx ON discovery.hypotheses (feature_id);

-- Интервью (DS-02). Дата — без времени (инвариант 7).
CREATE TABLE discovery.interviews (
    id             uuid        PRIMARY KEY,
    product_id     uuid        NOT NULL,
    account_id     text        NOT NULL DEFAULT '',
    segment        text        NOT NULL DEFAULT '',
    date           date        NULL,
    participants   text[]      NOT NULL DEFAULT '{}',
    notes          text        NOT NULL DEFAULT '',
    hypothesis_ids uuid[]      NOT NULL DEFAULT '{}',
    created_by     text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL
);
CREATE INDEX interviews_product_idx ON discovery.interviews (product_id);

-- Инсайты (DS-02, DS-04).
CREATE TABLE discovery.insights (
    id             uuid        PRIMARY KEY,
    product_id     uuid        NOT NULL,
    text           text        NOT NULL,
    interview_id   uuid        NULL,
    hypothesis_ids uuid[]      NOT NULL DEFAULT '{}',
    signal_ids     uuid[]      NOT NULL DEFAULT '{}',
    confidence     text        NOT NULL,
    created_by     text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL
);
CREATE INDEX insights_product_idx ON discovery.insights (product_id);
CREATE INDEX insights_interview_idx ON discovery.insights (interview_id);
CREATE INDEX insights_hypotheses_idx ON discovery.insights USING gin (hypothesis_ids);
CREATE INDEX insights_signals_idx ON discovery.insights USING gin (signal_ids);

-- Evidence (DS-03).
CREATE TABLE discovery.evidence (
    id            uuid        PRIMARY KEY,
    product_id    uuid        NOT NULL,
    source        text        NOT NULL,
    source_ref    text        NOT NULL DEFAULT '',
    date          date        NULL,
    trust         text        NOT NULL,
    verification  text        NOT NULL,
    sha256        text        NOT NULL DEFAULT '',
    hypothesis_id uuid        NULL,
    insight_id    uuid        NULL,
    feature_id    uuid        NULL,
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);
CREATE INDEX evidence_product_idx ON discovery.evidence (product_id);
CREATE INDEX evidence_hypothesis_idx ON discovery.evidence (hypothesis_id);
CREATE INDEX evidence_insight_idx ON discovery.evidence (insight_id);
CREATE INDEX evidence_feature_idx ON discovery.evidence (feature_id);

-- Кастомные поля и статусы (AD-03): портфельные настройки, без product_id.
CREATE TABLE discovery.field_defs (
    seq      bigint  GENERATED ALWAYS AS IDENTITY,
    id       uuid    NOT NULL UNIQUE,
    entity   text    NOT NULL,
    key      text    NOT NULL,
    label    text    NOT NULL,
    type     text    NOT NULL,
    options  text[]  NOT NULL DEFAULT '{}',
    required boolean NOT NULL DEFAULT false,
    PRIMARY KEY (entity, key)
);

CREATE TABLE discovery.status_defs (
    seq      bigint GENERATED ALWAYS AS IDENTITY,
    entity   text   NOT NULL,
    key      text   NOT NULL,
    label    text   NOT NULL,
    category text   NOT NULL,
    PRIMARY KEY (entity, key)
);

-- Индекс похожести (SG-04): полнотекстовый поиск PostgreSQL, конфигурация 'russian'.
-- Колонка embedding vector(N) не добавлена: модели эмбеддингов на этапе 2 нет (AI-10 — этап 5,
-- docs/questions.md №24).
CREATE TABLE discovery.embeddings (
    kind       text      NOT NULL,
    id         uuid      NOT NULL,
    product_id uuid      NOT NULL,
    text       text      NOT NULL,
    tsv        tsvector  GENERATED ALWAYS AS (to_tsvector('russian', text)) STORED,
    PRIMARY KEY (kind, id)
);
CREATE INDEX embeddings_bucket_idx ON discovery.embeddings (kind, product_id);
CREATE INDEX embeddings_tsv_idx ON discovery.embeddings USING gin (tsv);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA discovery TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA discovery TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE discovery.embeddings;
DROP TABLE discovery.status_defs;
DROP TABLE discovery.field_defs;
DROP TABLE discovery.evidence;
DROP TABLE discovery.insights;
DROP TABLE discovery.interviews;
DROP TABLE discovery.hypotheses;
DROP SCHEMA discovery;
