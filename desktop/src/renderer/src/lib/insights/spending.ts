import { addDays, addMonths, diffDays, eachDay, monthStart, type ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import type { CategoryDayRow, CategoryMonthRow } from "@/lib/topper-types";

import { category, isSpending } from "@shared/categories";

// Rent, mortgage and loan payments land once a month in large amounts;
// counting them in a running total makes the pace line jump and hides
// everyday spending. The pace chart and budget headline leave them out.
const PACE_EXCLUDED: ReadonlySet<string> = new Set(["housing", "loans"]);

export function isPaceCategory(id: string): boolean {
  return isSpending(id) && !PACE_EXCLUDED.has(id);
}

// Daily is spending per day and category, in one currency. Amounts are net
// (refunds reduce them) with money out positive, as Plaid reports it.
export type Daily = Map<ISODate, Map<string, number>>;

export function indexDaily(rows: CategoryDayRow[], currency: string): Daily {
  const idx: Daily = new Map();
  for (const r of rows) {
    if ((r.iso_currency_code ?? "CAD") !== currency) continue;
    let day = idx.get(r.day);
    if (!day) idx.set(r.day, (day = new Map()));
    day.set(r.category, (day.get(r.category) ?? 0) + num(r.amount));
  }
  return idx;
}

export type CategoryFilter = (id: string) => boolean;

export function dayTotal(idx: Daily, day: ISODate, keep: CategoryFilter): number {
  let t = 0;
  for (const [cat, v] of idx.get(day) ?? []) if (keep(cat)) t += v;
  return t;
}

export function sumRange(idx: Daily, from: ISODate, to: ISODate, keep: CategoryFilter): number {
  let t = 0;
  for (const [day, cats] of idx) {
    if (day < from || day > to) continue;
    for (const [cat, v] of cats) if (keep(cat)) t += v;
  }
  return t;
}

export function byCategory(idx: Daily, from: ISODate, to: ISODate, keep: CategoryFilter = isSpending): Map<string, number> {
  const out = new Map<string, number>();
  for (const [day, cats] of idx) {
    if (day < from || day > to) continue;
    for (const [cat, v] of cats) if (keep(cat)) out.set(cat, (out.get(cat) ?? 0) + v);
  }
  return out;
}

// cumulative is the running total for each day from `from` to `to`.
export function cumulative(idx: Daily, from: ISODate, to: ISODate, keep: CategoryFilter): number[] {
  let run = 0;
  return eachDay(from, to).map((d) => (run += dayTotal(idx, d, keep)));
}

export interface Projection {
  spent: number;
  // Daily rate used for the rest of the period, and its day-to-day spread.
  rate: number;
  sd: number;
  remainingDays: number;
  projected: number;
  // True when the rate came from earlier months rather than this one.
  fromHistory: boolean;
}

// project estimates where spending ends up by periodEnd. The daily rate is
// the mean of the lookback window before the period when there is data
// there, else this period's own mean so far. The likely range around the
// projection widens with the square root of the days left, as the spread
// of a sum of independent days does.
export function project(
  idx: Daily,
  periodStart: ISODate,
  periodEnd: ISODate,
  asOf: ISODate,
  keep: CategoryFilter,
  lookbackDays = 90,
): Projection {
  const spent = sumRange(idx, periodStart, asOf, keep);
  const remainingDays = Math.max(0, diffDays(asOf, periodEnd));
  const histFrom = addDays(periodStart, -lookbackDays);
  const histTo = addDays(periodStart, -1);
  let hasHistory = false;
  for (const day of idx.keys()) {
    if (day >= histFrom && day <= histTo) {
      hasHistory = true;
      break;
    }
  }
  const days = hasHistory ? eachDay(histFrom, histTo) : eachDay(periodStart, asOf);
  const values = days.map((d) => dayTotal(idx, d, keep));
  const mean = values.reduce((a, b) => a + b, 0) / Math.max(1, values.length);
  const variance = values.reduce((a, v) => a + (v - mean) ** 2, 0) / Math.max(1, values.length - 1);
  const rate = Math.max(0, mean);
  return {
    spent,
    rate,
    sd: Math.sqrt(variance),
    remainingDays,
    projected: spent + rate * remainingDays,
    fromHistory: hasHistory,
  };
}

// usualSoFar is the average amount spent over the first `elapsedDays` of
// each of the `periods` periods before periodStart, where `back(start, i)`
// gives the start of the i-th earlier period. Periods with no data at all
// (before the account's history) are skipped; null when none has data.
export function usualSoFar(
  idx: Daily,
  periodStart: ISODate,
  elapsedDays: number,
  periods: number,
  back: (start: ISODate, i: number) => ISODate,
  keep: CategoryFilter,
  hasData: (from: ISODate, to: ISODate) => boolean,
): number | null {
  let total = 0;
  let n = 0;
  for (let i = 1; i <= periods; i++) {
    const start = back(periodStart, i);
    const nextStart = back(periodStart, i - 1);
    const end = addDays(start, elapsedDays - 1) < nextStart ? addDays(start, elapsedDays - 1) : addDays(nextStart, -1);
    if (!hasData(start, addDays(nextStart, -1))) continue;
    total += sumRange(idx, start, end, keep);
    n++;
  }
  return n ? total / n : null;
}

// monthsBack is the `back` function for calendar months.
export const monthsBack = (start: ISODate, i: number) => addMonths(monthStart(start), -i);

// coveredFrom returns a check for usualSoFar: a period counts only when the
// history reaches back to (nearly) its start, so the month an account was
// linked in, with only its last few weeks of data, does not drag the
// "usual" figure down.
export function coveredFrom(idx: Daily, graceDays = 7): (from: ISODate, to: ISODate) => boolean {
  const first = firstDataDay(idx);
  return (from) => first !== null && first <= addDays(from, graceDays);
}

// firstDataDay is the earliest day with any row, or null.
export function firstDataDay(idx: Daily): ISODate | null {
  let first: ISODate | null = null;
  for (const d of idx.keys()) if (first === null || d < first) first = d;
  return first;
}

// monthlyTotals indexes categories/monthly rows: month → category → amount.
export function indexMonthly(rows: CategoryMonthRow[], currency: string): Map<ISODate, Map<string, number>> {
  const out = new Map<ISODate, Map<string, number>>();
  for (const r of rows) {
    if ((r.iso_currency_code ?? "CAD") !== currency) continue;
    let m = out.get(r.month);
    if (!m) out.set(r.month, (m = new Map()));
    m.set(r.category, (m.get(r.category) ?? 0) + num(r.amount));
  }
  return out;
}

// roundBudget rounds a suggested budget up to a tidy figure: tens under
// $200, twenty-fives under $1,000, fifties above.
export function roundBudget(n: number): number {
  if (n <= 0) return 0;
  const step = n < 200 ? 10 : n < 1000 ? 25 : 50;
  return Math.ceil(n / step) * step;
}

// suggestBudgets proposes a monthly budget per spending category: the
// average of the complete months before thisMonth (up to `months`) in which
// the category had spending at all, rounded up.
export function suggestBudgets(
  monthly: Map<ISODate, Map<string, number>>,
  thisMonth: ISODate,
  months = 3,
): Map<string, number> {
  const sums = new Map<string, { total: number; n: number }>();
  for (let i = 1; i <= months; i++) {
    const m = monthly.get(addMonths(thisMonth, -i));
    if (!m) continue;
    for (const [cat, v] of m) {
      if (!isSpending(cat)) continue;
      const s = sums.get(cat) ?? { total: 0, n: 0 };
      s.total += v;
      s.n++;
      sums.set(cat, s);
    }
  }
  const out = new Map<string, number>();
  for (const [cat, { total, n }] of sums) {
    const avg = total / n;
    if (avg > 0) out.set(cat, roundBudget(avg));
  }
  return out;
}

// sharePhrase says a fraction in words for a headline.
export function sharePhrase(ratio: number): string {
  if (ratio >= 0.9) return "Nearly all";
  if (ratio >= 0.6) return "Most";
  if (ratio >= 0.45) return "Almost half";
  if (ratio >= 0.3) return "About a third";
  if (ratio >= 0.22) return "About a quarter";
  return `${Math.round(ratio * 100)}%`;
}

// spendingHeadline is the Spending page's sentence about where the money
// went in the period: food when groceries and dining together dominate,
// otherwise the largest category.
export function spendingHeadline(totals: Map<string, number>, periodName: string, fmtAmount: (n: number) => string): {
  lead: string;
  amount: string;
  tail: string;
} | null {
  const entries = [...totals].filter(([, v]) => v > 0);
  const total = entries.reduce((a, [, v]) => a + v, 0);
  if (total <= 0) return null;
  const food = (totals.get("groceries") ?? 0) + (totals.get("dining") ?? 0);
  if (food / total >= 0.3 && (totals.get("groceries") ?? 0) > 0 && (totals.get("dining") ?? 0) > 0) {
    return {
      lead: `${sharePhrase(food / total)} of ${periodName} has gone to food: `,
      amount: fmtAmount(food),
      tail: " across groceries and dining.",
    };
  }
  entries.sort((a, b) => b[1] - a[1]);
  const [top, v] = entries[0];
  return {
    lead: `${category(top).label} leads ${periodName}: `,
    amount: fmtAmount(v),
    tail: `, ${Math.round((v / total) * 100)}% of everything you spent.`,
  };
}

// heatLevel buckets a day's spending into one of five shades relative to
// the period's busiest day.
export function heatLevel(value: number, max: number): 0 | 1 | 2 | 3 | 4 {
  if (max <= 0 || value <= 0) return 0;
  const r = value / max;
  return r < 0.2 ? 0 : r < 0.4 ? 1 : r < 0.6 ? 2 : r < 0.8 ? 3 : 4;
}
