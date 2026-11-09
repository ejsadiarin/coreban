-- ==============================================================================
-- coreban - one-time bootstrap (NOT a goose migration)
-- ==============================================================================
--
-- Creates the coreban schema, the runtime role, and its grants. Run ONCE per
-- environment as neondb_owner over the DIRECT (non-pooler) endpoint, before
-- the first `make migrate-up`:
--
--   psql "$MIGRATION_URL" -f internal/db/bootstrap.sql
--
-- Roles are cluster-level and must exist before migrations run as the owner,
-- so they live here rather than in the goose chain (same split as the
-- gateway's and corefinance's bootstrap.sql).
-- ==============================================================================

CREATE SCHEMA IF NOT EXISTS coreban AUTHORIZATION neondb_owner;

-- Runtime role: DML-only on its own schema; never the owner.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'coreban_role') THEN
    CREATE ROLE coreban_role LOGIN PASSWORD 'REPLACE_ME';
  END IF;
END
$$;

GRANT USAGE ON SCHEMA coreban TO coreban_role;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA coreban TO coreban_role;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA coreban TO coreban_role;

ALTER DEFAULT PRIVILEGES FOR ROLE neondb_owner IN SCHEMA coreban
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO coreban_role;
ALTER DEFAULT PRIVILEGES FOR ROLE neondb_owner IN SCHEMA coreban
  GRANT USAGE, SELECT ON SEQUENCES TO coreban_role;

-- Pooler-safe schema scoping: the pooler strips per-connection options.
ALTER ROLE coreban_role SET search_path = coreban;
