import type { AccountRow, SyncStatusRow, TransactionRow } from "@/lib/topper-types";

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

function toSearch(q: Query): string {
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

export const topper = {
  accounts: (q: Query = {}) => get<AccountRow>("/v1/views/accounts", q),
  transactions: (q: Query = {}) => get<TransactionRow>("/v1/views/transactions/live", q),
  syncStatus: (q: Query = {}) => get<SyncStatusRow>("/v1/views/sync/status", q),
};

// listSafe strips characters the topper's in.() and or=() lists cannot
// carry, so user input cannot change the shape of a filter.
export function listSafe(value: string): string {
  return value.replace(/[(),]/g, " ").trim();
}
