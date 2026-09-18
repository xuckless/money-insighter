import type {
  AccountRow,
  BalanceDayRow,
  BudgetRow,
  CategorizedRow,
  CategoryDayRow,
  CategoryMonthRow,
  CategoryOverrideRow,
  CategoryRow,
  MerchantMonthRow,
  MerchantRuleRow,
  PreferenceRow,
  RecurringEntryRow,
  RecurringHiddenRow,
  StreamRow,
  SyncStatusRow,
  TransactionRow,
} from "@/lib/topper-types";

import type { WriteTable } from "@shared/api";

export class TopperError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "TopperError";
  }
}

export interface Page<T> {
  data: T[];
  count: number;
  limit: number;
  offset: number;
  total?: number;
}

type Scalar = string | number | boolean;

// Query mirrors the topper's grammar. Each filter entry is a column and one
// or more "op.value" strings (a bare value means eq); repeated conditions on a
// column are ANDed. `or` groups are written as the topper expects, without
// the surrounding "or=(...)".
export interface Query {
  filters?: Record<string, string | string[]>;
  or?: string[];
  select?: string[];
  orderBy?: string;
  order?: "asc" | "desc";
  limit?: number;
  offset?: number;
  count?: boolean;
  toggles?: Record<string, Scalar>;
}

export function toSearch(q: Query): string {
  const p = new URLSearchParams();
  for (const [col, v] of Object.entries(q.filters ?? {})) {
    for (const cond of Array.isArray(v) ? v : [v]) p.append(col, cond);
  }
  for (const group of q.or ?? []) p.append("or", `(${group})`);
  for (const [k, v] of Object.entries(q.toggles ?? {})) p.set(k, String(v));
  if (q.select?.length) p.set("select", q.select.join(","));
  if (q.orderBy) {
    p.set("order_by", q.orderBy);
    if (q.order) p.set("order", q.order);
  }
  if (q.limit !== undefined) p.set("limit", String(q.limit));
  if (q.offset) p.set("offset", String(q.offset));
  if (q.count) p.set("count", "exact");
  return p.toString();
}

async function get<T>(path: string, q: Query = {}): Promise<Page<T>> {
  const { status, body } = await window.api.topper.get(path, toSearch(q));
  if (status < 200 || status >= 300) {
    const e = body as { error?: string } | undefined;
    throw new TopperError(status, e?.error ?? `HTTP ${status}`);
  }
  return body as Page<T>;
}

// getAll follows offset paging until a page comes back short, for the
// aggregate views whose row count is bounded by the date range asked for.
async function getAll<T>(path: string, q: Query = {}): Promise<T[]> {
  const limit = 1000;
  const out: T[] = [];
  for (let offset = 0; ; offset += limit) {
    const page = await get<T>(path, { ...q, limit, offset });
    out.push(...page.data);
    if (page.data.length < limit) return out;
  }
}

function check(status: number, body: unknown) {
  if (status < 200 || status >= 300) {
    const e = body as { error?: string } | undefined;
    throw new TopperError(status, e?.error ?? `HTTP ${status}`);
  }
}

export const topper = {
  accounts: (q: Query = {}) => get<AccountRow>("/v1/views/accounts", q),
  transactions: (q: Query = {}) => get<TransactionRow>("/v1/views/transactions/live", q),
  syncStatus: (q: Query = {}) => get<SyncStatusRow>("/v1/views/sync/status", q),
  categorized: (q: Query = {}) => get<CategorizedRow>("/v1/views/transactions/categorized", q),
  categorizedAll: (q: Query = {}) => getAll<CategorizedRow>("/v1/views/transactions/categorized", q),
  categoriesDaily: (q: Query = {}) => getAll<CategoryDayRow>("/v1/views/categories/daily", q),
  categoriesMonthly: (q: Query = {}) => getAll<CategoryMonthRow>("/v1/views/categories/monthly", q),
  merchantsMonthly: (q: Query = {}) => getAll<MerchantMonthRow>("/v1/views/merchants/monthly", q),
  balancesDaily: (q: Query = {}) => getAll<BalanceDayRow>("/v1/views/balances/daily", q),
  streams: (q: Query = {}) => getAll<StreamRow>("/v1/views/recurring/streams", q),
  recurringEntries: (q: Query = {}) => getAll<RecurringEntryRow>("/v1/views/recurring/entries", q),
  recurringHidden: () => getAll<RecurringHiddenRow>("/v1/recurring_hidden"),
  categories: () => getAll<CategoryRow>("/v1/categories"),
  categoryOverrides: (q: Query = {}) => getAll<CategoryOverrideRow>("/v1/category_overrides", q),
  merchantRules: (q: Query = {}) => getAll<MerchantRuleRow>("/v1/merchant_rules", q),
  budgets: () => getAll<BudgetRow>("/v1/budgets"),
  preferences: () => getAll<PreferenceRow>("/v1/preferences"),

  // upsert writes rows into one of the app's own tables.
  upsert: async (table: WriteTable, rows: object | object[]) => {
    const { status, body } = await window.api.topper.post(table, rows);
    check(status, body);
  },
  // remove deletes the rows of one of the app's own tables matching filters.
  remove: async (table: WriteTable, filters: Record<string, string>) => {
    const search = toSearch({ filters });
    if (!search) throw new Error("remove needs a filter");
    const { status, body } = await window.api.topper.delete(table, search);
    check(status, body);
  },
};

// listSafe strips characters the topper's in.() and or=() lists cannot
// carry, so user input cannot change the shape of a filter.
export function listSafe(value: string): string {
  return value.replace(/[(),]/g, " ").trim();
}
