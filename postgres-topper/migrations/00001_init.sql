-- Topper scaffolding. Consumer tables arrive in later migrations; each one
-- should attach the trigger below to maintain its updated_at column:
--
--   CREATE TRIGGER <table>_set_updated_at BEFORE UPDATE ON topper.<table>
--       FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();
--
-- The topper schema itself is created by deploy/postgres-init/topper.sql,
-- which runs as the plaidsync superuser; the topper role owns the schema
-- and therefore everything created here.

-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION topper.set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END $$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION topper.set_updated_at();
