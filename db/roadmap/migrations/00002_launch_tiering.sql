-- +goose Up
-- RM-06: уровень запуска и дата запуска элемента roadmap. Уровень определяет объём поддержки
-- запуска маркетингом; календарь запусков строится по дате запуска.
ALTER TABLE roadmap.items ADD COLUMN launch_tier text NOT NULL DEFAULT '';
ALTER TABLE roadmap.items ADD COLUMN launch_date date NULL;
CREATE INDEX items_launch_idx ON roadmap.items (launch_date) WHERE launch_tier <> '';

-- +goose Down
DROP INDEX roadmap.items_launch_idx;
ALTER TABLE roadmap.items DROP COLUMN launch_date;
ALTER TABLE roadmap.items DROP COLUMN launch_tier;
