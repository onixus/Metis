-- +goose Up
-- CM-03/PR-05/CM-08: изменяемые настройки compliance должны переживать рестарт API.
-- Singleton хранится JSONB: схема Settings остаётся доменной, миграции не дублируют набор ключей.
CREATE TABLE compliance.settings (
    id smallint PRIMARY KEY CHECK (id = 1),
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE compliance.settings;
