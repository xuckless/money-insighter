// Package views declares the typed read views served under /v1/views/.
// Each is a SELECT written here in Go and wrapped as a subquery by the
// query layer; none is a database view, so plaidsync can change its tables
// without a dependent object in the way. Column names and types are read
// from Postgres at startup by preparing each SELECT, so the list below is
// the only place a view's shape is written down.
package views

import "postgres-topper/internal/catalog"

// TransactionsLive is the canonical ledger: every transaction that has
// neither been removed by Plaid nor superseded by its posted successor.
// Consumers that filter on removed_at and superseded_by themselves get the
// same rows; this view exists so they never get the predicate wrong.
var TransactionsLive = catalog.ViewSpec{
	Name:      "transactions/live",
	FromTable: "transactions",
	Forced: []string{
		`"removed_at" IS NULL`,
		`"superseded_by" IS NULL`,
	},
	DefaultOrder: []catalog.Order{{Col: "date", Desc: true}, {Col: "transaction_id"}},
	Tiebreak:     []string{"transaction_id"},
}

// Accounts joins each account to its item's institution and status. By
// default accounts plaidsync has flagged as missing (no longer listed by
// the institution) are excluded; include_missing=1 keeps them.
var Accounts = catalog.ViewSpec{
	Name: "accounts",
	SQL: `SELECT a.account_id, a.item_id, a.name, a.official_name, a.mask, a.type, a.subtype,
       a.current_balance, a.available_balance, a.credit_limit,
       a.iso_currency_code, a.unofficial_currency_code, a.balance_last_updated_at,
       a.first_seen_at, a.last_seen_at, a.missing_since, a.updated_at,
       i.institution_id, i.institution_name, i.status AS item_status
FROM public.plaid_accounts a
JOIN public.plaid_items i ON i.item_id = a.item_id`,
	DefaultOrder: []catalog.Order{{Col: "item_id"}, {Col: "account_id"}},
	Tiebreak:     []string{"account_id"},
	Toggles:      []catalog.Toggle{{Key: "include_missing", WhenOff: `"missing_since" IS NULL`}},
}

// SyncStatus gives one row per item with what a consumer needs to judge
// freshness: the item's status and last successful sync, its most recent
// sync run and most recent job, and how many live accounts it has.
var SyncStatus = catalog.ViewSpec{
	Name: "sync/status",
	SQL: `SELECT i.item_id, i.institution_id, i.institution_name, i.status,
       i.last_successful_sync_at,
       i.last_error_code, i.last_error_type, i.last_error_message, i.last_error_at,
       i.consent_expires_at, i.created_at, i.updated_at,
       (SELECT count(*) FROM public.plaid_accounts pa
         WHERE pa.item_id = i.item_id AND pa.missing_since IS NULL) AS account_count,
       r.run_id       AS last_run_id,
       r.trigger      AS last_run_trigger,
       r.started_at   AS last_run_started_at,
       r.finished_at  AS last_run_finished_at,
       r.outcome      AS last_run_outcome,
       r.error_code   AS last_run_error_code,
       r.error_type   AS last_run_error_type,
       r.error_message AS last_run_error_message,
       r.added        AS last_run_added,
       r.modified     AS last_run_modified,
       r.removed      AS last_run_removed,
       r.request_id   AS last_run_request_id,
       j.job_id       AS latest_job_id,
       j.kind         AS latest_job_kind,
       j.state        AS latest_job_state,
       j.created_at   AS latest_job_created_at,
       j.started_at   AS latest_job_started_at,
       j.finished_at  AS latest_job_finished_at,
       j.error_code   AS latest_job_error_code,
       i.recurring_checked_at, i.recurring_refreshed_at,
       i.recurring_error_code, i.recurring_error_message
FROM public.plaid_items i
LEFT JOIN LATERAL (
    SELECT * FROM public.sync_runs sr WHERE sr.item_id = i.item_id
    ORDER BY sr.started_at DESC, sr.run_id DESC LIMIT 1
) r ON true
LEFT JOIN LATERAL (
    SELECT * FROM public.sync_jobs sj WHERE sj.item_id = i.item_id
    ORDER BY sj.created_at DESC, sj.job_id DESC LIMIT 1
) j ON true`,
	DefaultOrder: []catalog.Order{{Col: "item_id"}},
	Tiebreak:     []string{"item_id"},
}

// The views below serve the desktop app's insights. They share one idea
// of a transaction's category: the user's override for that transaction,
// else the user's rule for its merchant, else Plaid's category mapped by
// topper.plaid_category. Amounts keep Plaid's sign (positive is money out),
// so a category's net amount is its spending with refunds taken off.

// categorizedSQL is the live ledger with the effective category, the
// merchant key rules match on, and the account each row belongs to. A
// transaction without a currency takes its account's, as Plaid's Sandbox
// sends some that way.
const categorizedSQL = `SELECT t.transaction_id, t.account_id, t.item_id, t.amount,
       COALESCE(t.iso_currency_code, a.iso_currency_code) AS iso_currency_code,
       COALESCE(t.unofficial_currency_code, a.unofficial_currency_code) AS unofficial_currency_code,
       t.date, t.authorized_date, t.datetime,
       t.name, t.merchant_name, t.merchant_entity_id,
       topper.merchant_key(t.merchant_name, t.name) AS merchant_key,
       t.pending, t.pfc_primary, t.pfc_detailed, t.pfc_confidence, t.payment_channel,
       t.raw->>'logo_url' AS logo_url,
       COALESCE(o.category, r.category, topper.plaid_category(t.pfc_primary, t.pfc_detailed)) AS category,
       CASE WHEN o.category IS NOT NULL THEN 'override'
            WHEN r.category IS NOT NULL THEN 'rule'
            ELSE 'plaid' END AS category_source,
       topper.plaid_category(t.pfc_primary, t.pfc_detailed) AS plaid_category,
       (o.category IS NULL AND r.category IS NULL AND t.amount > 0
        AND topper.plaid_category(t.pfc_primary, t.pfc_detailed) NOT IN ('transfer', 'income')
        AND (t.pfc_primary IS NULL OR t.pfc_confidence = 'UNKNOWN'
             OR (t.pfc_confidence = 'LOW' AND (t.merchant_name IS NULL
                 OR topper.plaid_category(t.pfc_primary, t.pfc_detailed) = 'other')))) AS needs_category,
       a.name AS account_name, a.mask AS account_mask, a.type AS account_type, a.subtype AS account_subtype,
       i.institution_name
FROM public.transactions t
JOIN public.plaid_accounts a ON a.account_id = t.account_id
JOIN public.plaid_items i ON i.item_id = t.item_id
LEFT JOIN topper.category_overrides o ON o.transaction_id = t.transaction_id
LEFT JOIN topper.merchant_rules r ON r.merchant_key = topper.merchant_key(t.merchant_name, t.name)
WHERE t.removed_at IS NULL AND t.superseded_by IS NULL`

// TransactionsCategorized is the live ledger with each row's effective
// category and account. needs_category marks money out the user has not
// categorised and Plaid could not place: no category at all, UNKNOWN
// confidence, or LOW confidence without a resolved merchant or mapping
// only to 'other'. Plaid marks many correct categories LOW (a ride share
// with a known merchant), so LOW alone is not enough.
var TransactionsCategorized = catalog.ViewSpec{
	Name:         "transactions/categorized",
	SQL:          categorizedSQL,
	DefaultOrder: []catalog.Order{{Col: "date", Desc: true}, {Col: "transaction_id"}},
	Tiebreak:     []string{"transaction_id"},
}

// CategoriesDaily totals the live ledger per day, category and currency.
// Filters on day are pushed into the aggregate by Postgres, so a date range
// reads only that range.
var CategoriesDaily = catalog.ViewSpec{
	Name: "categories/daily",
	SQL: `SELECT c.date AS day, c.category, c.iso_currency_code,
       sum(c.amount) AS amount, count(*) AS transactions
FROM (` + categorizedSQL + `) c
GROUP BY c.date, c.category, c.iso_currency_code`,
	DefaultOrder: []catalog.Order{{Col: "day"}, {Col: "category"}},
	Tiebreak:     []string{"day", "category", "iso_currency_code"},
}

// CategoriesMonthly totals the live ledger per calendar month (month is
// the first day of the month), category and currency.
var CategoriesMonthly = catalog.ViewSpec{
	Name: "categories/monthly",
	SQL: `SELECT date_trunc('month', c.date)::date AS month, c.category, c.iso_currency_code,
       sum(c.amount) AS amount, count(*) AS transactions
FROM (` + categorizedSQL + `) c
GROUP BY 1, c.category, c.iso_currency_code`,
	DefaultOrder: []catalog.Order{{Col: "month"}, {Col: "category"}},
	Tiebreak:     []string{"month", "category", "iso_currency_code"},
}

// MerchantsMonthly totals the live ledger per month, category and merchant.
// merchant is the most recent display name seen for the merchant key.
var MerchantsMonthly = catalog.ViewSpec{
	Name: "merchants/monthly",
	SQL: `SELECT date_trunc('month', c.date)::date AS month, c.category, c.merchant_key, c.iso_currency_code,
       (array_agg(COALESCE(c.merchant_name, c.name) ORDER BY c.date DESC))[1] AS merchant,
       sum(c.amount) AS amount, count(*) AS transactions
FROM (` + categorizedSQL + `) c
GROUP BY 1, c.category, c.merchant_key, c.iso_currency_code`,
	DefaultOrder: []catalog.Order{{Col: "month"}, {Col: "category"}, {Col: "merchant_key"}},
	Tiebreak:     []string{"month", "category", "merchant_key", "iso_currency_code"},
}

// BalancesDaily is plaidsync's daily balance snapshots joined to each
// account's type and institution, for net worth and cash history.
var BalancesDaily = catalog.ViewSpec{
	Name: "balances/daily",
	SQL: `SELECT s.day, s.account_id, a.item_id, a.name AS account_name, a.mask AS account_mask,
       a.type AS account_type, a.subtype AS account_subtype, i.institution_name,
       s.current_balance, s.available_balance, s.credit_limit,
       s.iso_currency_code, s.unofficial_currency_code, a.missing_since
FROM public.account_balance_snapshots s
JOIN public.plaid_accounts a ON a.account_id = s.account_id
JOIN public.plaid_items i ON i.item_id = a.item_id`,
	DefaultOrder: []catalog.Order{{Col: "day"}, {Col: "account_id"}},
	Tiebreak:     []string{"day", "account_id"},
}

// RecurringStreams is the live recurring streams of items that are not
// removed, with their account and the app category of the stream. A stream
// without a currency takes its account's.
var RecurringStreams = catalog.ViewSpec{
	Name: "recurring/streams",
	SQL: `SELECT s.stream_id, s.item_id, s.account_id, s.direction, s.description, s.merchant_name,
       topper.merchant_key(s.merchant_name, s.description) AS merchant_key,
       s.pfc_primary, s.pfc_detailed,
       COALESCE(r.category, topper.plaid_category(s.pfc_primary, s.pfc_detailed)) AS category,
       s.frequency, s.first_date, s.last_date, s.predicted_next_date,
       s.average_amount, s.last_amount,
       COALESCE(s.iso_currency_code, a.iso_currency_code) AS iso_currency_code,
       COALESCE(s.unofficial_currency_code, a.unofficial_currency_code) AS unofficial_currency_code,
       s.is_active, s.status, cardinality(s.transaction_ids) AS transaction_count,
       a.name AS account_name, a.mask AS account_mask, a.type AS account_type, a.subtype AS account_subtype,
       i.institution_name, s.updated_at
FROM public.plaid_recurring_streams s
JOIN public.plaid_accounts a ON a.account_id = s.account_id
JOIN public.plaid_items i ON i.item_id = s.item_id
LEFT JOIN topper.merchant_rules r ON r.merchant_key = topper.merchant_key(s.merchant_name, s.description)
WHERE s.removed_at IS NULL AND i.status <> 'removed'`,
	DefaultOrder: []catalog.Order{{Col: "predicted_next_date"}, {Col: "stream_id"}},
	Tiebreak:     []string{"stream_id"},
}

// All lists every view, in the order GET /v1/ reports them.
var All = []catalog.ViewSpec{
	TransactionsLive, Accounts, SyncStatus,
	TransactionsCategorized, CategoriesDaily, CategoriesMonthly, MerchantsMonthly,
	BalancesDaily, RecurringStreams,
}
