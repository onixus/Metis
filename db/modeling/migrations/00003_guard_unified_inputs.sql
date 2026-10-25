-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION modeling.guard_unified_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.kind = 'batch' AND NEW.body->>'status' = 'applied') OR NEW.kind IN ('fact','closed') THEN
  IF EXISTS (SELECT 1 FROM modeling.documents u WHERE u.kind='unification' AND u.body->>'period'=NEW.body->>'period') THEN
   RAISE EXCEPTION 'unified period accepts financial inputs only through the canonical book' USING ERRCODE='23514';
  END IF;
 ELSIF NEW.kind='shares' THEN
  IF EXISTS (SELECT 1 FROM jsonb_array_elements(NEW.body) sh JOIN modeling.documents u ON u.kind='unification' AND u.body->>'period'=sh->>'period') THEN
   RAISE EXCEPTION 'unified allocation belongs to canonical book' USING ERRCODE='23514';
  END IF;
 ELSIF NEW.kind IN ('allocation','bundle') THEN
  IF EXISTS (SELECT 1 FROM modeling.documents u WHERE u.kind='unification' AND COALESCE(NEW.body->>'effective_from','') <= (u.body->>'period') || '-31') THEN
   RAISE EXCEPTION 'unified period allocation belongs to canonical book' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER guard_unified_inputs BEFORE INSERT ON modeling.documents FOR EACH ROW EXECUTE FUNCTION modeling.guard_unified_inputs();

-- +goose Down
-- Preserve the append-only canonical-owner guard on downgrade.
SELECT 1;
