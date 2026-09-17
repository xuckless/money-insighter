-- +goose Up

-- One row per account per day with the balances as they stood after the
-- day's last sync. plaid_accounts only holds the latest balances, so this
-- is the only record of how they moved: a net worth or cash balance chart
-- reads it. A trigger fills it, which keeps every writer of plaid_accounts
-- (today only the sync batch) recording history without knowing about it.
-- The day is the database session's current date.
CREATE TABLE account_balance_snapshots (
    account_id               TEXT NOT NULL REFERENCES plaid_accounts(account_id),
    day                      DATE NOT NULL,
    current_balance          NUMERIC,
    available_balance        NUMERIC,
    credit_limit             NUMERIC,
    iso_currency_code        TEXT,
    unofficial_currency_code TEXT,
    recorded_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, day)
);
CREATE INDEX account_balance_snapshots_day_idx ON account_balance_snapshots (day);

-- +goose StatementBegin
CREATE FUNCTION plaidsync_snapshot_balance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO account_balance_snapshots (
        account_id, day, current_balance, available_balance, credit_limit,
        iso_currency_code, unofficial_currency_code, recorded_at
    )
    VALUES (
        NEW.account_id, current_date, NEW.current_balance, NEW.available_balance, NEW.credit_limit,
        NEW.iso_currency_code, NEW.unofficial_currency_code, clock_timestamp()
    )
    ON CONFLICT (account_id, day) DO UPDATE SET
        current_balance          = EXCLUDED.current_balance,
        available_balance        = EXCLUDED.available_balance,
        credit_limit             = EXCLUDED.credit_limit,
        iso_currency_code        = EXCLUDED.iso_currency_code,
        unofficial_currency_code = EXCLUDED.unofficial_currency_code,
        recorded_at              = EXCLUDED.recorded_at;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER plaid_accounts_snapshot_balance
    AFTER INSERT OR UPDATE OF current_balance, available_balance, credit_limit, iso_currency_code, unofficial_currency_code
    ON plaid_accounts FOR EACH ROW EXECUTE FUNCTION plaidsync_snapshot_balance();

-- Accounts that exist before this migration get today's row now, so a
-- chart has a first point without waiting for the next sync.
INSERT INTO account_balance_snapshots (
    account_id, day, current_balance, available_balance, credit_limit,
    iso_currency_code, unofficial_currency_code
)
SELECT account_id, current_date, current_balance, available_balance, credit_limit,
       iso_currency_code, unofficial_currency_code
FROM plaid_accounts;

-- +goose Down

DROP TRIGGER plaid_accounts_snapshot_balance ON plaid_accounts;
DROP FUNCTION plaidsync_snapshot_balance();
DROP INDEX account_balance_snapshots_day_idx;
DROP TABLE account_balance_snapshots;
