-- Роль миграций владеет схемами; роль приложения не имеет UPDATE/DELETE на журналы (инвариант 8).
CREATE EXTENSION IF NOT EXISTS vector;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    CREATE ROLE metis_app LOGIN PASSWORD 'metis-dev-only';
  END IF;
END $$;
GRANT CONNECT ON DATABASE metis TO metis_app;
