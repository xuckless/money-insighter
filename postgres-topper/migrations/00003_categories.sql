-- Categories become data. The fixed list in 00002 (topper.is_category) is
-- replaced by topper.categories, seeded with the built-in set and open to
-- rows the user adds. Budgets, overrides and merchant rules reference it
-- by foreign key; a category in use is merged into another by the app
-- before it is deleted.
--
-- kind says what a category means to the screens:
--   spending  everyday money out, paced day by day and budgeted
--   bill      fixed costs that land on a schedule (rent, utilities,
--             subscriptions, loans); budgeted, not paced
--   income    money in
--   transfer  money moved between the user's own accounts
--
-- The migration also adds the tables behind recurring payments the user
-- adds by hand (topper.recurring_entries) and detected or Plaid streams the
-- user has marked as not recurring (topper.recurring_hidden).
--
-- desktop/src/shared/categories.ts holds the same seed with the same ids;
-- keep the two in step.

-- +goose Up

CREATE TABLE topper.categories (
    id         TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    color      TEXT NOT NULL,
    icon       TEXT NOT NULL DEFAULT 'tag',
    kind       TEXT NOT NULL,
    builtin    BOOLEAN NOT NULL DEFAULT false,
    sort_order INTEGER NOT NULL DEFAULT 1000,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT categories_id_check CHECK (id ~ '^[a-z0-9_]{1,40}$'),
    CONSTRAINT categories_label_check CHECK (btrim(label) <> ''),
    CONSTRAINT categories_color_check CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
    CONSTRAINT categories_kind_check CHECK (kind IN ('spending', 'bill', 'income', 'transfer'))
);
CREATE TRIGGER categories_set_updated_at BEFORE UPDATE ON topper.categories
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

INSERT INTO topper.categories (id, label, color, icon, kind, builtin, sort_order) VALUES
    ('groceries',     'Groceries',          '#5E7F5A', 'shopping-basket', 'spending', true, 10),
    ('dining',        'Dining & takeout',   '#C06A45', 'utensils',        'spending', true, 20),
    ('shopping',      'Shopping',           '#86587C', 'shopping-bag',    'spending', true, 30),
    ('transport',     'Transport',          '#4F6D8A', 'car',             'spending', true, 40),
    ('health',        'Health & fitness',   '#A1566B', 'heart-pulse',     'spending', true, 50),
    ('medical',       'Medical',            '#B5443A', 'stethoscope',     'spending', true, 60),
    ('personal_care', 'Personal care',      '#8A6A9E', 'sparkles',        'spending', true, 70),
    ('entertainment', 'Entertainment',      '#6B6FA3', 'clapperboard',    'spending', true, 80),
    ('travel',        'Travel',             '#3E7F9C', 'plane',           'spending', true, 90),
    ('education',     'Education',          '#2F6F8F', 'graduation-cap',  'spending', true, 100),
    ('kids_pets',     'Kids & pets',        '#B0782A', 'paw-print',       'spending', true, 110),
    ('gifts',         'Gifts & donations',  '#9C4F7C', 'gift',            'spending', true, 120),
    ('other',         'Other',              '#8C8174', 'circle-dashed',   'spending', true, 900),
    ('housing',       'Rent & housing',     '#7A5B45', 'house',           'bill',     true, 200),
    ('utilities',     'Utilities & phone',  '#A67A1F', 'plug-zap',        'bill',     true, 210),
    ('subscriptions', 'Subscriptions',      '#3F7F7A', 'repeat',          'bill',     true, 220),
    ('loans',         'Loan payments',      '#5F6B5B', 'landmark',        'bill',     true, 230),
    ('bills',         'Other bills',        '#7D6E8E', 'receipt',         'bill',     true, 240),
    ('income',        'Income',             '#3F6B4E', 'banknote',        'income',   true, 300),
    ('tax_refund',    'Tax refunds',        '#4E8A62', 'badge-percent',   'income',   true, 310),
    ('transfer',      'Transfers',          '#9C9184', 'arrow-left-right','transfer', true, 400);

-- The CHECK constraints from 00002 named the fixed list; foreign keys take
-- their place. Deleting a category cascades to its budget (the app merges
-- overrides and rules into another category first, and the RESTRICT below
-- catches a delete that skipped that step).
ALTER TABLE topper.budgets
    DROP CONSTRAINT budgets_category_check,
    ADD CONSTRAINT budgets_category_fkey FOREIGN KEY (category)
        REFERENCES topper.categories(id) ON DELETE CASCADE;
ALTER TABLE topper.category_overrides
    DROP CONSTRAINT category_overrides_category_check,
    ADD CONSTRAINT category_overrides_category_fkey FOREIGN KEY (category)
        REFERENCES topper.categories(id) ON DELETE RESTRICT;
ALTER TABLE topper.merchant_rules
    DROP CONSTRAINT merchant_rules_category_check,
    ADD CONSTRAINT merchant_rules_category_fkey FOREIGN KEY (category)
        REFERENCES topper.categories(id) ON DELETE RESTRICT;

-- A budget only makes sense for money out.
-- +goose StatementBegin
CREATE FUNCTION topper.budgets_check_kind() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
    SELECT kind INTO k FROM topper.categories WHERE id = NEW.category;
    IF k IS NULL OR k NOT IN ('spending', 'bill') THEN
        RAISE EXCEPTION 'budgets: category % is not a spending or bill category', NEW.category
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER budgets_check_kind BEFORE INSERT OR UPDATE ON topper.budgets
    FOR EACH ROW EXECUTE FUNCTION topper.budgets_check_kind();

-- is_category now asks the table. It is STABLE, so it can no longer sit in
-- a CHECK constraint; nothing does after this migration.
DROP FUNCTION topper.is_category(TEXT);
-- +goose StatementBegin
CREATE FUNCTION topper.is_category(c TEXT) RETURNS BOOLEAN
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT EXISTS (SELECT 1 FROM topper.categories WHERE id = c)
$$;
-- +goose StatementEnd

-- plaid_category gains the new built-in categories. Medical and personal
-- care leave Health & fitness; fees, taxes and insurance land in Other
-- bills; donations and gifts, childcare and pets, education and tax
-- refunds each get their own. It is replaced in place so the views that
-- call it keep working.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION topper.plaid_category(pfc_primary TEXT, pfc_detailed TEXT) RETURNS TEXT
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
        WHEN pfc_detailed LIKE 'GENERAL_MERCHANDISE_PET_SUPPLIES%' THEN 'kids_pets'
        WHEN pfc_detailed LIKE 'GENERAL_MERCHANDISE_GIFTS_AND_NOVELTIES%' THEN 'gifts'
        WHEN pfc_primary IN ('GENERAL_MERCHANDISE', 'HOME_IMPROVEMENT') THEN 'shopping'
        WHEN pfc_primary = 'TRANSPORTATION' THEN 'transport'
        WHEN pfc_primary = 'TRAVEL' THEN 'travel'
        WHEN pfc_primary = 'MEDICAL' THEN 'medical'
        WHEN pfc_primary = 'PERSONAL_CARE' THEN 'personal_care'
        WHEN pfc_detailed LIKE 'GENERAL_SERVICES_EDUCATION%' THEN 'education'
        WHEN pfc_detailed LIKE 'GENERAL_SERVICES_CHILDCARE%' THEN 'kids_pets'
        WHEN pfc_detailed LIKE 'GENERAL_SERVICES_INSURANCE%' THEN 'bills'
        WHEN pfc_detailed LIKE 'GENERAL_SERVICES_AUTOMOTIVE%' THEN 'transport'
        WHEN pfc_detailed LIKE 'GOVERNMENT_AND_NON_PROFIT_DONATIONS%' THEN 'gifts'
        WHEN pfc_primary IN ('GOVERNMENT_AND_NON_PROFIT', 'BANK_FEES') THEN 'bills'
        WHEN pfc_detailed LIKE 'INCOME_TAX_REFUND%' THEN 'tax_refund'
        WHEN pfc_primary = 'INCOME' THEN 'income'
        WHEN pfc_primary IN ('TRANSFER_IN', 'TRANSFER_OUT', 'LOAN_DISBURSEMENTS') THEN 'transfer'
        ELSE 'other'
    END
$$;
-- +goose StatementEnd

-- Recurring payments the user adds by hand: a name, an amount, how often
-- and when next. merchant_key, when set, lets the app match the entry to
-- the transactions that pay it. amount is a magnitude; direction says
-- which way the money goes.
CREATE TABLE topper.recurring_entries (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    amount            NUMERIC NOT NULL,
    direction         TEXT NOT NULL DEFAULT 'outflow',
    frequency         TEXT NOT NULL,
    next_date         DATE NOT NULL,
    category          TEXT NOT NULL REFERENCES topper.categories(id) ON DELETE RESTRICT,
    account_id        TEXT,
    merchant_key      TEXT,
    iso_currency_code TEXT,
    notes             TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT recurring_entries_id_check CHECK (id <> ''),
    CONSTRAINT recurring_entries_name_check CHECK (btrim(name) <> ''),
    CONSTRAINT recurring_entries_amount_check CHECK (amount >= 0),
    CONSTRAINT recurring_entries_direction_check CHECK (direction IN ('inflow', 'outflow')),
    CONSTRAINT recurring_entries_frequency_check
        CHECK (frequency IN ('WEEKLY', 'BIWEEKLY', 'SEMI_MONTHLY', 'MONTHLY', 'ANNUALLY'))
);
CREATE TRIGGER recurring_entries_set_updated_at BEFORE UPDATE ON topper.recurring_entries
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

-- Streams the user said are not recurring, keyed by the stream id the app
-- shows: Plaid's stream_id, or "detected:<account>:<merchant>:<dir>" for
-- one found in the transaction history.
CREATE TABLE topper.recurring_hidden (
    stream_id  TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT recurring_hidden_id_check CHECK (stream_id <> '')
);
CREATE TRIGGER recurring_hidden_set_updated_at BEFORE UPDATE ON topper.recurring_hidden
    FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at();

-- +goose Down

DROP TABLE topper.recurring_hidden;
DROP TABLE topper.recurring_entries;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION topper.plaid_category(pfc_primary TEXT, pfc_detailed TEXT) RETURNS TEXT
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

DROP FUNCTION topper.is_category(TEXT);
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

DROP TRIGGER budgets_check_kind ON topper.budgets;
DROP FUNCTION topper.budgets_check_kind();

-- Rows that used a category the old list does not know are moved to Other
-- so the CHECK constraints can come back.
UPDATE topper.merchant_rules SET category = 'other' WHERE NOT topper.is_category(category);
UPDATE topper.category_overrides SET category = 'other' WHERE NOT topper.is_category(category);
DELETE FROM topper.budgets WHERE NOT topper.is_category(category);

ALTER TABLE topper.merchant_rules
    DROP CONSTRAINT merchant_rules_category_fkey,
    ADD CONSTRAINT merchant_rules_category_check CHECK (topper.is_category(category));
ALTER TABLE topper.category_overrides
    DROP CONSTRAINT category_overrides_category_fkey,
    ADD CONSTRAINT category_overrides_category_check CHECK (topper.is_category(category));
ALTER TABLE topper.budgets
    DROP CONSTRAINT budgets_category_fkey,
    ADD CONSTRAINT budgets_category_check CHECK (topper.is_category(category) AND category NOT IN ('income', 'transfer'));

DROP TABLE topper.categories;
