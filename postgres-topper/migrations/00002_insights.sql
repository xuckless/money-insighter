-- Insights for the desktop app: the category mapping from Plaid's personal
-- finance categories to the app's fixed category set, and the four tables
-- the user writes through the API (budgets, per-transaction overrides,
-- merchant rules, preferences). The tables are listed in
-- TOPPER_WRITE_TABLES by the desktop app.
--
-- Category ids are fixed. Spending categories: groceries, dining,
-- shopping, transport, utilities, housing, subscriptions, health,
-- entertainment, travel, loans, other. Non-spending: income, transfer.
-- desktop/src/shared/categories.ts holds the same list with labels and
-- colours; keep the two in step.

-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION topper.is_category(c TEXT) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT c IN (
        'groceries', 'dining', 'shopping', 'transport', 'utilities', 'housing',
        'subscriptions', 'health', 'entertainment', 'travel', 'loans', 'other',
        'income', 'transfer'
    )
$$;
-- +goose StatementEnd

-- plaid_category maps a transaction's Plaid category to an app category.
-- It matches on prefixes of the detailed code so both PFC v1 and v2
-- spellings land in the same place, and an unknown or missing category is
-- 'other'. Credit card payments are transfers: the spending already
-- counted when the card was used.
-- +goose StatementBegin
CREATE FUNCTION topper.plaid_category(pfc_primary TEXT, pfc_detailed TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN pfc_detailed LIKE 'FOOD_AND_DRINK_GROCERIES%' THEN 'groceries'
        WHEN pfc_primary = 'FOOD_AND_DRINK' THEN 'dining'
        WHEN pfc_detailed LIKE 'RENT_AND_UTILITIES_RENT%' THEN 'housing'
        WHEN pfc_detailed LIKE 'LOAN_PAYMENTS_MORTGAGE%' THEN 'housing'
        WHEN pfc_primary = 'RENT_AND_UTILITIES' THEN 'utilities'
        WHEN pfc_detailed LIKE 'LOAN_PAYMENTS_CREDIT_CARD%' THEN 'transfer'
        WHEN pfc_primary = 'LOAN_PAYMENTS' THEN 'loans'
        WHEN pfc_detailed LIKE 'ENTERTAINMENT_TV_AND_MOVIES%' THEN 'subscriptions'
        WHEN pfc_detailed LIKE 'ENTERTAINMENT_MUSIC_AND_AUDIO%' THEN 'subscriptions'
        WHEN pfc_primary = 'ENTERTAINMENT' THEN 'entertainment'
        WHEN pfc_primary IN ('GENERAL_MERCHANDISE', 'HOME_IMPROVEMENT') THEN 'shopping'
        WHEN pfc_primary = 'TRANSPORTATION' THEN 'transport'
        WHEN pfc_primary = 'TRAVEL' THEN 'travel'
        WHEN pfc_primary IN ('MEDICAL', 'PERSONAL_CARE') THEN 'health'
        WHEN pfc_primary = 'INCOME' THEN 'income'
        WHEN pfc_primary IN ('TRANSFER_IN', 'TRANSFER_OUT', 'LOAN_DISBURSEMENTS') THEN 'transfer'
        ELSE 'other'
    END
$$;
-- +goose StatementEnd

-- merchant_key is how a merchant rule recognises a transaction: the
-- merchant name Plaid resolved, or the raw description when it resolved
-- none, lowercased with runs of whitespace collapsed.
-- +goose StatementBegin
CREATE FUNCTION topper.merchant_key(merchant_name TEXT, name TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT lower(regexp_replace(btrim(COALESCE(NULLIF(btrim(merchant_name), ''), name)), '\s+', ' ', 'g'))
$$;
-- +goose StatementEnd

-- A monthly budget per spending category. The total budget is the sum.
CREATE TABLE topper.budgets (
    category       TEXT PRIMARY KEY,
    monthly_amount NUMERIC NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT budgets_category_check CHECK (topper.is_category(category) AND category NOT IN ('income', 'transfer')),
    CONSTRAINT budgets_amount_check CHECK (monthly_amount >= 0)
);
CREATE TRIGGER budgets_set_updated_at BEFORE UPDATE ON topper.budgets
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

-- The user's category for one transaction. No foreign key: the topper
-- never constrains plaidsync's tables, and an override for a transaction
-- Plaid later removes is harmless.
CREATE TABLE topper.category_overrides (
    transaction_id TEXT PRIMARY KEY,
    category       TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT category_overrides_category_check CHECK (topper.is_category(category))
);
CREATE TRIGGER category_overrides_set_updated_at BEFORE UPDATE ON topper.category_overrides
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

-- The user's category for every transaction of a merchant, keyed by
-- topper.merchant_key.
CREATE TABLE topper.merchant_rules (
    merchant_key TEXT PRIMARY KEY,
    category     TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT merchant_rules_category_check CHECK (topper.is_category(category)),
    CONSTRAINT merchant_rules_key_check CHECK (merchant_key <> '')
);
CREATE TRIGGER merchant_rules_set_updated_at BEFORE UPDATE ON topper.merchant_rules
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

-- Small app settings that belong with the data rather than the app's
-- config file, such as the cash cushion kept out of "safe to spend".
CREATE TABLE topper.preferences (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER preferences_set_updated_at BEFORE UPDATE ON topper.preferences
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

-- +goose Down

DROP TABLE topper.preferences;
DROP TABLE topper.merchant_rules;
DROP TABLE topper.category_overrides;
DROP TABLE topper.budgets;
DROP FUNCTION topper.merchant_key(TEXT, TEXT);
DROP FUNCTION topper.plaid_category(TEXT, TEXT);
DROP FUNCTION topper.is_category(TEXT);
