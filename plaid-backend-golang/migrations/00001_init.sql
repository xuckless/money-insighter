-- +goose Up

CREATE TABLE plaid_items (
    item_id                  TEXT PRIMARY KEY,
    institution_id           TEXT,
    institution_name         TEXT,
    encrypted_access_token   BYTEA,             -- AES-256-GCM blob: nonce(12) || ciphertext || tag. NULL once the item is removed.
    key_version              INTEGER,
    cursor                   TEXT,              -- /transactions/sync cursor after the last COMPLETE pagination; NULL = never synced
    status                   TEXT NOT NULL DEFAULT 'active',
    last_error_code          TEXT,
    last_error_type          TEXT,
    last_error_message       TEXT,
    last_error_at            TIMESTAMPTZ,
    last_successful_sync_at  TIMESTAMPTZ,
    consent_expires_at       TIMESTAMPTZ,
    raw                      JSONB,             -- last /item/get payload (contains no secrets)
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT plaid_items_status_check CHECK (status IN ('active','login_required','pending_expiration','permission_revoked','error','removed')),
    CONSTRAINT plaid_items_credential_check CHECK ((status = 'removed') = (encrypted_access_token IS NULL)),
    CONSTRAINT plaid_items_key_version_check CHECK ((encrypted_access_token IS NULL) = (key_version IS NULL))
);

CREATE TABLE plaid_accounts (
    account_id               TEXT PRIMARY KEY,
    item_id                  TEXT NOT NULL REFERENCES plaid_items(item_id),
    name                     TEXT NOT NULL,
    official_name            TEXT,
    mask                     TEXT,
    type                     TEXT NOT NULL,
    subtype                  TEXT,
    current_balance          NUMERIC(14,2),
    available_balance        NUMERIC(14,2),
    credit_limit             NUMERIC(14,2),     -- Plaid's "limit"; renamed because LIMIT is reserved
    iso_currency_code        TEXT,
    unofficial_currency_code TEXT,
    balance_last_updated_at  TIMESTAMPTZ,
    raw                      JSONB NOT NULL,
    first_seen_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    missing_since            TIMESTAMPTZ,       -- set when a sync response no longer lists this account; cleared when it reappears
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX plaid_accounts_item_id_idx ON plaid_accounts (item_id);

CREATE TABLE transactions (
    transaction_id           TEXT PRIMARY KEY,
    account_id               TEXT NOT NULL REFERENCES plaid_accounts(account_id),
    item_id                  TEXT NOT NULL REFERENCES plaid_items(item_id),
    amount                   NUMERIC(14,2) NOT NULL,   -- Plaid sign convention: positive = money out
    iso_currency_code        TEXT,
    unofficial_currency_code TEXT,
    date                     DATE NOT NULL,
    authorized_date          DATE,
    datetime                 TIMESTAMPTZ,
    authorized_datetime      TIMESTAMPTZ,
    name                     TEXT NOT NULL,
    merchant_name            TEXT,
    merchant_entity_id       TEXT,
    pending                  BOOLEAN NOT NULL,
    pending_transaction_id   TEXT,              -- set by Plaid on the POSTED row, pointing at the pending row it replaces
    superseded_by            TEXT,              -- set by us on the PENDING row, pointing at the posted row (no FK: rows arrive in any order)
    superseded_at            TIMESTAMPTZ,
    pfc_primary              TEXT,
    pfc_detailed             TEXT,
    pfc_confidence           TEXT,
    payment_channel          TEXT,
    transaction_code         TEXT,
    raw                      JSONB NOT NULL,    -- full Plaid transaction object
    removed_at               TIMESTAMPTZ,       -- soft delete; never DELETE
    first_seen_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX transactions_item_date_idx ON transactions (item_id, date DESC);
CREATE INDEX transactions_account_date_idx ON transactions (account_id, date DESC);
CREATE INDEX transactions_pending_txn_idx ON transactions (pending_transaction_id) WHERE pending_transaction_id IS NOT NULL;

CREATE TABLE sync_jobs (
    job_id        UUID PRIMARY KEY,
    item_id       TEXT NOT NULL REFERENCES plaid_items(item_id),
    kind          TEXT NOT NULL,   -- initial | manual | webhook | scheduled
    state         TEXT NOT NULL,   -- queued | running | succeeded | failed | skipped
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    error_code    TEXT,
    error_message TEXT,
    CONSTRAINT sync_jobs_kind_check CHECK (kind IN ('initial','manual','webhook','scheduled')),
    CONSTRAINT sync_jobs_state_check CHECK (state IN ('queued','running','succeeded','failed','skipped'))
);
CREATE INDEX sync_jobs_item_created_idx ON sync_jobs (item_id, created_at DESC);

CREATE TABLE sync_runs (
    run_id           BIGSERIAL PRIMARY KEY,
    item_id          TEXT NOT NULL REFERENCES plaid_items(item_id),
    job_id           UUID REFERENCES sync_jobs(job_id),
    trigger          TEXT NOT NULL,   -- initial | manual | webhook | scheduled
    started_at       TIMESTAMPTZ NOT NULL,
    finished_at      TIMESTAMPTZ NOT NULL,
    cursor_before    TEXT,
    cursor_after     TEXT,
    pages            INTEGER NOT NULL DEFAULT 0,
    added            INTEGER NOT NULL DEFAULT 0,   -- as reported by Plaid
    modified         INTEGER NOT NULL DEFAULT 0,   -- as reported by Plaid
    removed          INTEGER NOT NULL DEFAULT 0,   -- as reported by Plaid
    inserted         INTEGER NOT NULL DEFAULT 0,   -- rows actually inserted
    updated          INTEGER NOT NULL DEFAULT 0,   -- rows actually updated
    superseded       INTEGER NOT NULL DEFAULT 0,   -- pending rows linked to a posted successor this run
    accounts_seen    INTEGER NOT NULL DEFAULT 0,
    accounts_missing INTEGER NOT NULL DEFAULT 0,
    outcome          TEXT NOT NULL,   -- success | retryable_error | needs_reauth | fatal | locked | canceled | error
    error_code       TEXT,
    error_type       TEXT,
    error_message    TEXT,
    request_id       TEXT,
    CONSTRAINT sync_runs_outcome_check CHECK (outcome IN ('success','retryable_error','needs_reauth','fatal','locked','canceled','error'))
);
CREATE INDEX sync_runs_item_started_idx ON sync_runs (item_id, started_at DESC);

-- updated_at maintenance
-- +goose StatementBegin
CREATE FUNCTION plaidsync_set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END $$;
-- +goose StatementEnd
CREATE TRIGGER plaid_items_set_updated_at BEFORE UPDATE ON plaid_items FOR EACH ROW EXECUTE FUNCTION plaidsync_set_updated_at();
CREATE TRIGGER plaid_accounts_set_updated_at BEFORE UPDATE ON plaid_accounts FOR EACH ROW EXECUTE FUNCTION plaidsync_set_updated_at();
CREATE TRIGGER transactions_set_updated_at BEFORE UPDATE ON transactions FOR EACH ROW EXECUTE FUNCTION plaidsync_set_updated_at();

-- +goose Down

DROP TRIGGER transactions_set_updated_at ON transactions;
DROP TRIGGER plaid_accounts_set_updated_at ON plaid_accounts;
DROP TRIGGER plaid_items_set_updated_at ON plaid_items;
DROP FUNCTION plaidsync_set_updated_at();

DROP INDEX sync_runs_item_started_idx;
DROP TABLE sync_runs;

DROP INDEX sync_jobs_item_created_idx;
DROP TABLE sync_jobs;

DROP INDEX transactions_pending_txn_idx;
DROP INDEX transactions_account_date_idx;
DROP INDEX transactions_item_date_idx;
DROP TABLE transactions;

DROP INDEX plaid_accounts_item_id_idx;
DROP TABLE plaid_accounts;

DROP TABLE plaid_items;
