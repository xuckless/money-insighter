import { addMonths, type ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import { DETECT_COLUMNS, DETECT_MONTHS, detectStreams } from "@/lib/insights/detect";
import { entryStream, priceChangeCandidate, type PriceChange } from "@/lib/insights/recurring";
import { topper } from "@/lib/topper";
import type { CategoryRow, EntryFrequency, StreamRow } from "@/lib/topper-types";

import { registerCategories, type Category } from "@shared/categories";

// Loaders shared by several screens. Each returns plain data; screens call
// them inside useLoad.

// Categories.

function fromRow(r: CategoryRow): Category {
  return { id: r.id, label: r.label, color: r.color, icon: r.icon, kind: r.kind, builtin: r.builtin, sortOrder: r.sort_order };
}

// loadCategories reads the table and registers it for every lookup.
export async function loadCategories(): Promise<Category[]> {
  const rows = await topper.categories();
  const list = rows.map(fromRow);
  registerCategories(list);
  return list;
}

export async function saveCategory(c: Category): Promise<void> {
  await topper.upsert("categories", {
    id: c.id,
    label: c.label.trim(),
    color: c.color,
    icon: c.icon,
    kind: c.kind,
    builtin: c.builtin,
    sort_order: c.sortOrder,
  });
}

// deleteCategory moves everything that points at `id` (overrides, merchant
// rules, hand-added recurring entries) to `into`, then deletes the row; its
// budget goes with it.
export async function deleteCategory(id: string, into: string): Promise<void> {
  if (id === into) throw new Error("choose a different category to merge into");
  const filter = { category: `eq.${id}` };
  const [overrides, rules, entries] = await Promise.all([
    topper.categoryOverrides({ filters: filter }),
    topper.merchantRules({ filters: filter }),
    topper.recurringEntries({ filters: filter }),
  ]);
  if (overrides.length) await topper.upsert("category_overrides", overrides.map((o) => ({ transaction_id: o.transaction_id, category: into })));
  if (rules.length) await topper.upsert("merchant_rules", rules.map((r) => ({ merchant_key: r.merchant_key, category: into })));
  if (entries.length) {
    await topper.upsert(
      "recurring_entries",
      entries.map((e) => ({
        id: e.id,
        name: e.name,
        amount: e.amount,
        direction: e.direction,
        frequency: e.frequency,
        next_date: e.next_date,
        category: into,
        account_id: e.account_id,
        merchant_key: e.merchant_key,
        iso_currency_code: e.iso_currency_code,
        notes: e.notes,
      })),
    );
  }
  await topper.remove("categories", { id: `eq.${id}` });
}

// Budgets.

export async function loadBudgets(): Promise<Map<string, number>> {
  const rows = await topper.budgets();
  return new Map(rows.map((r) => [r.category, num(r.monthly_amount)]));
}

export async function saveBudgets(budgets: Map<string, number>): Promise<void> {
  const keep = [...budgets].filter(([, v]) => v > 0);
  const drop = [...budgets].filter(([, v]) => v <= 0).map(([c]) => c);
  if (keep.length) await topper.upsert("budgets", keep.map(([category, v]) => ({ category, monthly_amount: v.toFixed(2) })));
  if (drop.length) await topper.remove("budgets", { category: `in.(${drop.join(",")})` });
}

// Preferences with their defaults. The value column is JSON.
export const preferenceDefaults = {
  cushion: 500,
} as const;

export type PreferenceKey = keyof typeof preferenceDefaults;

export async function loadPreferences(): Promise<{ [K in PreferenceKey]: (typeof preferenceDefaults)[K] | number }> {
  const rows = await topper.preferences();
  const out: Record<string, unknown> = { ...preferenceDefaults };
  for (const r of rows) if (r.key in preferenceDefaults) out[r.key] = r.value;
  return out as { [K in PreferenceKey]: number };
}

export async function savePreference(key: PreferenceKey, value: unknown): Promise<void> {
  await topper.upsert("preferences", { key, value });
}

export function dateRange(from: ISODate, to: ISODate): string[] {
  return [`gte.${from}`, `lte.${to}`];
}

// Recurring.

export interface Streams {
  // Every active stream the screens should show, all sources merged.
  active: StreamRow[];
  // Streams the user marked as not recurring, in case they want them back.
  hidden: StreamRow[];
}

// loadStreams merges the three sources into one list: Plaid's streams
// when the add-on is on, streams detected from the transaction history,
// and entries the user added. A detected stream that Plaid already
// reports, or that a hand-added entry matches by merchant and account, is
// dropped in favour of the other.
export async function loadStreams(today: ISODate, plaidOn: boolean): Promise<Streams> {
  const [plaid, entries, hidden, txns] = await Promise.all([
    plaidOn ? topper.streams({ filters: { is_active: "eq.true" } }) : Promise.resolve([] as StreamRow[]),
    topper.recurringEntries(),
    topper.recurringHidden(),
    topper.categorizedAll({
      filters: { date: [`gte.${addMonths(today, -DETECT_MONTHS)}`], pending: "eq.false" },
      select: DETECT_COLUMNS,
    }),
  ]);
  const key = (s: Pick<StreamRow, "account_id" | "merchant_key" | "direction">) => `${s.account_id}|${s.merchant_key}|${s.direction}`;
  const manual = entries.map((e) => entryStream(e, today));
  const covered = new Set([...plaid.map(key), ...manual.filter((m) => m.merchant_key).map(key)]);
  const detected = detectStreams(txns, today).filter((d) => !covered.has(key(d)));
  const manualKeys = new Set(manual.filter((m) => m.merchant_key).map(key));
  const all = [...plaid.filter((p) => !manualKeys.has(key(p))), ...detected, ...manual];
  const hiddenIds = new Set(hidden.map((h) => h.stream_id));
  return { active: all.filter((s) => !hiddenIds.has(s.stream_id)), hidden: all.filter((s) => hiddenIds.has(s.stream_id)) };
}

export async function loadActiveStreams(today: ISODate, plaidOn: boolean): Promise<StreamRow[]> {
  return (await loadStreams(today, plaidOn)).active;
}

export interface RecurringEntryInput {
  id?: string;
  name: string;
  amount: number;
  direction: "inflow" | "outflow";
  frequency: EntryFrequency;
  next_date: ISODate;
  category: string;
  account_id: string | null;
  merchant_key: string | null;
  notes: string | null;
}

export async function saveRecurringEntry(e: RecurringEntryInput): Promise<void> {
  await topper.upsert("recurring_entries", {
    id: e.id ?? crypto.randomUUID(),
    name: e.name.trim(),
    amount: Math.abs(e.amount).toFixed(2),
    direction: e.direction,
    frequency: e.frequency,
    next_date: e.next_date,
    category: e.category,
    account_id: e.account_id,
    merchant_key: e.merchant_key,
    notes: e.notes,
  });
}

export async function deleteRecurringEntry(id: string): Promise<void> {
  await topper.remove("recurring_entries", { id: `eq.${id}` });
}

export async function hideStream(streamId: string): Promise<void> {
  await topper.upsert("recurring_hidden", { stream_id: streamId });
}

export async function unhideStream(streamId: string): Promise<void> {
  await topper.remove("recurring_hidden", { stream_id: `eq.${streamId}` });
}

// resolvePriceChanges confirms a price change for the subscriptions whose
// last charge differs from their average, by reading the stream merchant's
// two most recent distinct charges on the same account. The average alone
// would blur the old price with the new one.
export async function resolvePriceChanges(streams: StreamRow[]): Promise<Map<string, PriceChange>> {
  const out = new Map<string, PriceChange>();
  await Promise.all(
    streams.filter(priceChangeCandidate).map(async (s) => {
      const page = await topper.categorized({
        filters: { merchant_key: `eq.${s.merchant_key}`, account_id: `eq.${s.account_id}`, pending: "eq.false" },
        select: ["amount", "date"],
        orderBy: "date",
        order: "desc",
        limit: 6,
      });
      const amounts = page.data.map((t) => Math.abs(num(t.amount)));
      const to = amounts[0];
      const from = amounts.find((a) => Math.abs(a - to) >= 0.005);
      if (to !== undefined && from !== undefined && Math.abs(to - from) >= 0.5) out.set(s.stream_id, { from, to });
    }),
  );
  return out;
}
