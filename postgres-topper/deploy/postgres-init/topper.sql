-- Creates the Postgres role and schema the topper connects as. Run as the
-- plaidsync superuser with the password supplied as a psql variable:
--
--   psql -v ON_ERROR_STOP=1 -v topper_password='...' -U plaidsync -d plaidsync -f topper.sql
--
-- Idempotent: re-running updates the password and re-applies the grants.
-- In the production stack deploy/postgres-init/01-topper.sh runs it on the
-- first boot of an empty volume; for an existing volume run it by hand (see
-- README, "Existing volumes").
--
-- What the topper role can do afterwards:
--   - SELECT from every table in public (plaidsync's), now and in future
--   - anything in schema topper, which it owns: its own migrations create
--     the consumer tables there
--   - nothing else: no INSERT/UPDATE/DELETE on plaidsync's tables, so the
--     read-only rule is enforced by Postgres, not only by the topper.

SELECT format('CREATE ROLE topper LOGIN PASSWORD %L', :'topper_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'topper')
\gexec

ALTER ROLE topper PASSWORD :'topper_password';

CREATE SCHEMA IF NOT EXISTS topper AUTHORIZATION topper;

GRANT USAGE ON SCHEMA public TO topper;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO topper;
ALTER DEFAULT PRIVILEGES FOR ROLE plaidsync IN SCHEMA public GRANT SELECT ON TABLES TO topper;
