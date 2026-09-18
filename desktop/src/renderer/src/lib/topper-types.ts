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

export interface SyncStatusRow extends SyncStatusRecurring {
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

export interface SyncStatusRecurring {
  recurring_checked_at: string | null;
  recurring_refreshed_at: string | null;
  recurring_error_code: string | null;
  recurring_error_message: string | null;
}

// transactions/categorized: the live ledger with its effective category.
export interface CategorizedRow {
  transaction_id: string;
  account_id: string;
  item_id: string;
  amount: string;
  iso_currency_code: string | null;
  unofficial_currency_code: string | null;
  date: string;
  authorized_date: string | null;
  datetime: string | null;
  name: string;
  merchant_name: string | null;
  merchant_entity_id: string | null;
  merchant_key: string;
  pending: boolean;
  pfc_primary: string | null;
  pfc_detailed: string | null;
  pfc_confidence: string | null;
  payment_channel: string | null;
  logo_url: string | null;
  category: string;
  category_source: "override" | "rule" | "plaid";
  plaid_category: string;
  needs_category: boolean;
  account_name: string;
  account_mask: string | null;
  account_type: string;
  account_subtype: string | null;
  institution_name: string | null;
}

export interface CategoryDayRow {
  day: string;
  category: string;
  iso_currency_code: string | null;
  amount: string;
  transactions: number;
}

export interface CategoryMonthRow {
  month: string;
  category: string;
  iso_currency_code: string | null;
  amount: string;
  transactions: number;
}

export interface MerchantMonthRow {
  month: string;
  category: string;
  merchant_key: string;
  iso_currency_code: string | null;
  merchant: string;
  amount: string;
  transactions: number;
}

export interface BalanceDayRow {
  day: string;
  account_id: string;
  item_id: string;
  account_name: string;
  account_mask: string | null;
  account_type: string;
  account_subtype: string | null;
  institution_name: string | null;
  current_balance: string | null;
  available_balance: string | null;
  credit_limit: string | null;
  iso_currency_code: string | null;
  unofficial_currency_code: string | null;
  missing_since: string | null;
}

export type StreamFrequency = "WEEKLY" | "BIWEEKLY" | "SEMI_MONTHLY" | "MONTHLY" | "ANNUALLY" | "UNKNOWN";

// Where a recurring stream came from: Plaid's add-on, the app's own
// detection over the transaction history, or the user's hand.
export type StreamSource = "plaid" | "detected" | "manual";

// recurring/streams: Plaid's live recurring streams. Amounts keep Plaid's
// sign: outflows positive, inflows negative. Detected and manual streams
// are built in the same shape (lib/queries loadStreams) so every screen
// reads one list.
export interface StreamRow {
  stream_id: string;
  source: StreamSource;
  item_id: string;
  account_id: string;
  direction: "inflow" | "outflow";
  description: string;
  merchant_name: string | null;
  merchant_key: string;
  pfc_primary: string | null;
  pfc_detailed: string | null;
  category: string;
  frequency: StreamFrequency;
  first_date: string;
  last_date: string;
  predicted_next_date: string | null;
  average_amount: string | null;
  last_amount: string | null;
  iso_currency_code: string | null;
  unofficial_currency_code: string | null;
  is_active: boolean;
  status: "MATURE" | "EARLY_DETECTION" | "TOMBSTONED" | "UNKNOWN";
  transaction_count: number;
  account_name: string;
  account_mask: string | null;
  account_type: string;
  account_subtype: string | null;
  institution_name: string | null;
  updated_at: string;
}

// topper.categories: the category list, built-in rows and the user's own.
export interface CategoryRow {
  id: string;
  label: string;
  color: string;
  icon: string;
  kind: "spending" | "bill" | "income" | "transfer";
  builtin: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export type EntryFrequency = Exclude<StreamFrequency, "UNKNOWN">;

// recurring/entries: recurring payments the user added by hand, with the
// account each is paid from when one was chosen.
export interface RecurringEntryRow {
  id: string;
  name: string;
  amount: string;
  direction: "inflow" | "outflow";
  frequency: EntryFrequency;
  next_date: string;
  category: string;
  category_kind: "spending" | "bill" | "income" | "transfer";
  account_id: string | null;
  merchant_key: string | null;
  notes: string | null;
  iso_currency_code: string | null;
  unofficial_currency_code: string | null;
  item_id: string | null;
  account_name: string | null;
  account_mask: string | null;
  account_type: string | null;
  account_subtype: string | null;
  institution_name: string | null;
  created_at: string;
  updated_at: string;
}

export interface RecurringHiddenRow {
  stream_id: string;
  created_at: string;
  updated_at: string;
}

export interface CategoryOverrideRow {
  transaction_id: string;
  category: string;
  created_at: string;
  updated_at: string;
}

export interface MerchantRuleRow {
  merchant_key: string;
  category: string;
  created_at: string;
  updated_at: string;
}

export interface BudgetRow {
  category: string;
  monthly_amount: string;
  created_at: string;
  updated_at: string;
}

export interface PreferenceRow {
  key: string;
  value: unknown;
  created_at: string;
  updated_at: string;
}
