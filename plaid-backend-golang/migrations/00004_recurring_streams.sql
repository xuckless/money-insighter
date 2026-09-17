-- +goose Up

-- Plaid's recurring transaction streams (/transactions/recurring/get), an
-- optional add-on enabled with PLAIDSYNC_RECURRING_ENABLED. Each refresh
-- replaces the item's streams: rows Plaid no longer returns get removed_at
-- and a returning stream clears it. Amounts keep Plaid's sign convention
-- (positive is money out), which Plaid applies to streams as well: outflow
-- streams are positive and inflow streams negative.
CREATE TABLE plaid_recurring_streams (
    stream_id                TEXT PRIMARY KEY,
    item_id                  TEXT NOT NULL REFERENCES plaid_items(item_id),
    account_id               TEXT NOT NULL REFERENCES plaid_accounts(account_id),
    direction                TEXT NOT NULL,     -- inflow | outflow
    description              TEXT NOT NULL,
    merchant_name            TEXT,
    pfc_primary              TEXT,
    pfc_detailed             TEXT,
    frequency                TEXT NOT NULL,     -- WEEKLY | BIWEEKLY | SEMI_MONTHLY | MONTHLY | ANNUALLY | UNKNOWN
    first_date               DATE NOT NULL,
    last_date                DATE NOT NULL,
    predicted_next_date      DATE,
    average_amount           NUMERIC,
    last_amount              NUMERIC,
    iso_currency_code        TEXT,
    unofficial_currency_code TEXT,
    is_active                BOOLEAN NOT NULL,
    status                   TEXT NOT NULL,     -- MATURE | EARLY_DETECTION | TOMBSTONED | UNKNOWN
    transaction_ids          TEXT[] NOT NULL DEFAULT '{}',
    raw                      JSONB NOT NULL,    -- full Plaid stream object
    removed_at               TIMESTAMPTZ,
    first_seen_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT plaid_recurring_streams_direction_check CHECK (direction IN ('inflow','outflow'))
);
CREATE INDEX plaid_recurring_streams_item_idx ON plaid_recurring_streams (item_id);
CREATE TRIGGER plaid_recurring_streams_set_updated_at BEFORE UPDATE ON plaid_recurring_streams FOR EACH ROW EXECUTE FUNCTION plaidsync_set_updated_at();

-- The outcome of the item's last recurring refresh. It never affects
-- status or last_error_*: the add-on failing (commonly PRODUCT_NOT_ENABLED
-- in Production) must not make a healthy item look broken.
ALTER TABLE plaid_items
    ADD COLUMN recurring_checked_at   TIMESTAMPTZ,   -- last attempt, whatever its outcome
    ADD COLUMN recurring_refreshed_at TIMESTAMPTZ,   -- last successful refresh
    ADD COLUMN recurring_error_code   TEXT,
    ADD COLUMN recurring_error_message TEXT;

-- +goose Down

ALTER TABLE plaid_items
    DROP COLUMN recurring_error_message,
    DROP COLUMN recurring_error_code,
    DROP COLUMN recurring_refreshed_at,
    DROP COLUMN recurring_checked_at;
DROP TRIGGER plaid_recurring_streams_set_updated_at ON plaid_recurring_streams;
DROP INDEX plaid_recurring_streams_item_idx;
DROP TABLE plaid_recurring_streams;
