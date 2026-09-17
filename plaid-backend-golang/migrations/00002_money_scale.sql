-- +goose Up

-- Plaid reports some balances and amounts with more than two fractional
-- digits (investment and crypto accounts in particular; Sandbox's "Plaid
-- IRA" has a balance of 23631.9805). The store refuses to round, so the
-- columns must hold what Plaid sends. Unconstrained NUMERIC keeps every
-- value at exactly the scale it was written with ("12.34" stays "12.34",
-- "23631.9805" stays "23631.9805"), which is what money.Amount produces and
-- what the topper renders; a fixed NUMERIC(p,6) would instead pad every
-- amount to six decimals. Existing values are unchanged.

ALTER TABLE plaid_accounts
    ALTER COLUMN current_balance   TYPE NUMERIC,
    ALTER COLUMN available_balance TYPE NUMERIC,
    ALTER COLUMN credit_limit      TYPE NUMERIC;

ALTER TABLE transactions
    ALTER COLUMN amount TYPE NUMERIC;

-- +goose Down

-- Narrowing rounds any value with more than two fractional digits, which
-- is why Up exists; only run this on a database that has none.
ALTER TABLE transactions
    ALTER COLUMN amount TYPE NUMERIC(14,2);

ALTER TABLE plaid_accounts
    ALTER COLUMN current_balance   TYPE NUMERIC(14,2),
    ALTER COLUMN available_balance TYPE NUMERIC(14,2),
    ALTER COLUMN credit_limit      TYPE NUMERIC(14,2);
