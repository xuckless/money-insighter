import type { ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import { priceChangeCandidate, type PriceChange } from "@/lib/insights/recurring";
import { topper } from "@/lib/topper";
import type { StreamRow } from "@/lib/topper-types";

// Loaders shared by several screens. Each returns plain data; screens call
// them inside useLoad.

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

export async function loadActiveStreams(): Promise<StreamRow[]> {
  return topper.streams({ filters: { is_active: "eq.true" } });
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
