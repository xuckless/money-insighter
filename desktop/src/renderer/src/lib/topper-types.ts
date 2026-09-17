// Row shapes of the topper's views. numeric columns arrive as strings with
// their scale intact; dates as YYYY-MM-DD; timestamps as RFC 3339 UTC.

export interface AccountRow {
  account_id: string;
  item_id: string;
  name: string;
  official_name: string | null;
  mask: string | null;
  type: string;
  subtype: string | null;
  current_balance: string | null;
  available_balance: string | null;
  credit_limit: string | null;
  iso_currency_code: string | null;
  unofficial_currency_code: string | null;
  balance_last_updated_at: string | null;
  first_seen_at: string;
  last_seen_at: string;
  missing_since: string | null;
  updated_at: string;
  institution_id: string | null;
  institution_name: string | null;
  item_status: string;
}

export interface TransactionRow {
  transaction_id: string;
  account_id: string;
  item_id: string;
  amount: string;
  iso_currency_code: string | null;
  unofficial_currency_code: string | null;
  date: string;
  authorized_date: string | null;
  name: string;
  merchant_name: string | null;
  pending: boolean;
  pfc_primary: string | null;
  pfc_detailed: string | null;
  payment_channel: string | null;
}

// Columns requested from transactions/live; `raw` is deliberately left out.
export const TRANSACTION_COLUMNS: (keyof TransactionRow)[] = [
  "transaction_id",
  "account_id",
  "item_id",
  "amount",
  "iso_currency_code",
  "unofficial_currency_code",
  "date",
  "authorized_date",
  "name",
  "merchant_name",
  "pending",
  "pfc_primary",
  "pfc_detailed",
  "payment_channel",
];

export interface SyncStatusRow {
  item_id: string;
  institution_id: string | null;
  institution_name: string | null;
  status: string;
  last_successful_sync_at: string | null;
  last_error_code: string | null;
  last_error_type: string | null;
  last_error_message: string | null;
  last_error_at: string | null;
  consent_expires_at: string | null;
  created_at: string;
  updated_at: string;
  account_count: number;
  last_run_id: number | null;
  last_run_trigger: string | null;
  last_run_started_at: string | null;
  last_run_finished_at: string | null;
  last_run_outcome: string | null;
  last_run_error_code: string | null;
  last_run_error_type: string | null;
  last_run_error_message: string | null;
  last_run_added: number | null;
  last_run_modified: number | null;
  last_run_removed: number | null;
  last_run_request_id: string | null;
  latest_job_id: string | null;
  latest_job_kind: string | null;
  latest_job_state: string | null;
  latest_job_created_at: string | null;
  latest_job_started_at: string | null;
  latest_job_finished_at: string | null;
  latest_job_error_code: string | null;
}
