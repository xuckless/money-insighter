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
       j.error_code   AS latest_job_error_code
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

// All lists every view, in the order GET /v1/ reports them.
var All = []catalog.ViewSpec{TransactionsLive, Accounts, SyncStatus}
