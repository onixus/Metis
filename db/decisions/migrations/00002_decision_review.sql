-- +goose Up
-- DA-06: ревизия решения. Ожидаемый эффект получает измеримую часть (показатель экономики,
-- целевое значение, период), а результат ревизии — отдельный jsonb: факт на дату ревизии,
-- отклонение и вердикт. Текстовое поле expected_effect остаётся как есть (DA-01).
ALTER TABLE decisions.records ADD COLUMN effect_metric text NOT NULL DEFAULT '';
ALTER TABLE decisions.records ADD COLUMN effect_value text NOT NULL DEFAULT '';
ALTER TABLE decisions.records ADD COLUMN effect_period text NOT NULL DEFAULT '';
ALTER TABLE decisions.records ADD COLUMN review jsonb NULL;

-- +goose Down
ALTER TABLE decisions.records DROP COLUMN review;
ALTER TABLE decisions.records DROP COLUMN effect_period;
ALTER TABLE decisions.records DROP COLUMN effect_value;
ALTER TABLE decisions.records DROP COLUMN effect_metric;
