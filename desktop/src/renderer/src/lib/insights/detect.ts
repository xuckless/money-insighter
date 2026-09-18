import { addDays, addMonths, diffDays, type ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import type { CategorizedRow, StreamFrequency, StreamRow } from "@/lib/topper-types";

import { isBill } from "@shared/categories";

// Local recurring detection: the same job Plaid's Recurring Transactions
// add-on does, done here over the transaction history so the Recurring
// screen, Coming up and Cash flow work without the add-on. Charges from one
// merchant on one account, in one direction, are a stream when they land at
// a steady interval for a steady amount and the last one is recent.

export type DetectRow = Pick<
  CategorizedRow,
  | "transaction_id"
  | "account_id"
  | "item_id"
  | "amount"
  | "iso_currency_code"
  | "unofficial_currency_code"
  | "date"
  | "name"
  | "merchant_name"
  | "merchant_key"
  | "pending"
  | "pfc_primary"
  | "pfc_detailed"
  | "category"
  | "account_name"
  | "account_mask"
  | "account_type"
  | "account_subtype"
  | "institution_name"
>;

// DETECT_COLUMNS is what a screen selects from transactions/categorized to
// feed detectStreams.
export const DETECT_COLUMNS: (keyof DetectRow)[] = [
  "transaction_id",
  "account_id",
  "item_id",
  "amount",
  "iso_currency_code",
  "unofficial_currency_code",
  "date",
  "name",
  "merchant_name",
  "merchant_key",
  "pending",
  "pfc_primary",
  "pfc_detailed",
  "category",
  "account_name",
  "account_mask",
  "account_type",
  "account_subtype",
  "institution_name",
];

// How far back the history is read. Annual charges need two.
export const DETECT_MONTHS = 25;

interface Rule {
  frequency: Exclude<StreamFrequency, "UNKNOWN" | "SEMI_MONTHLY">;
  // Median gap in days that means this frequency.
  min: number;
  max: number;
  // How far one gap may stray from the median.
  tolerance: number;
  // Charges needed.
  count: number;
  // Nominal period, for the active check.
  period: number;
}

const RULES: Rule[] = [
  { frequency: "WEEKLY", min: 6, max: 8, tolerance: 2, count: 4, period: 7 },
  { frequency: "BIWEEKLY", min: 12.5, max: 16, tolerance: 3, count: 3, period: 14 },
  { frequency: "MONTHLY", min: 26, max: 35, tolerance: 6, count: 3, period: 30.5 },
  { frequency: "ANNUALLY", min: 340, max: 390, tolerance: 25, count: 2, period: 365 },
];

function median(values: number[]): number {
  const s = [...values].sort((a, b) => a - b);
  const mid = Math.floor(s.length / 2);
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

function pickRule(medianGap: number): Rule | null {
  return RULES.find((r) => medianGap >= r.min && medianGap <= r.max) ?? null;
}

// advance steps a date by one period of the frequency.
export function advance(d: ISODate, frequency: StreamFrequency): ISODate {
  switch (frequency) {
    case "WEEKLY":
      return addDays(d, 7);
    case "BIWEEKLY":
      return addDays(d, 14);
    case "SEMI_MONTHLY":
      return addDays(d, 15);
    case "MONTHLY":
      return addMonths(d, 1);
    case "ANNUALLY":
      return addMonths(d, 12);
    default:
      return d;
  }
}

// nextOnOrAfter advances from `from` until the date is on or after `today`.
export function nextOnOrAfter(from: ISODate, frequency: StreamFrequency, today: ISODate): ISODate {
  let next = from;
  for (let i = 0; next < today && i < 200; i++) next = advance(next, frequency);
  return next;
}

export function detectedStreamId(accountId: string, merchantKey: string, direction: "inflow" | "outflow"): string {
  return `detected:${accountId}:${merchantKey}:${direction}`;
}

// detectStreams groups posted transactions by account, merchant and
// direction and keeps the groups that recur. Amounts must stay within 20%
// of the group's median (35% for bills, since hydro and phone bills move),
// gaps must sit within the frequency's tolerance of their median with at
// most one outlier per five charges, and the last charge must be recent
// enough that the next one is still expected.
export function detectStreams(rows: DetectRow[], today: ISODate): StreamRow[] {
  const groups = new Map<string, DetectRow[]>();
  for (const r of rows) {
    if (r.pending || !r.merchant_key) continue;
    const amount = num(r.amount);
    if (Math.abs(amount) < 0.5) continue;
    const dir = amount > 0 ? "outflow" : "inflow";
    const key = `${r.account_id}|${r.merchant_key}|${dir}`;
    let g = groups.get(key);
    if (!g) groups.set(key, (g = []));
    g.push(r);
  }

  const out: StreamRow[] = [];
  for (const g of groups.values()) {
    g.sort((a, b) => (a.date < b.date ? -1 : a.date > b.date ? 1 : 0));
    // One charge per day: a refund and its charge, or a split payment, must
    // not read as a weekly stream.
    const byDay: DetectRow[] = [];
    for (const r of g) if (byDay.length === 0 || byDay[byDay.length - 1].date !== r.date) byDay.push(r);
    if (byDay.length < 2) continue;

    const gaps = byDay.slice(1).map((r, i) => diffDays(byDay[i].date, r.date));
    const rule = pickRule(median(gaps));
    if (!rule || byDay.length < rule.count) continue;
    const mg = median(gaps);
    const outliers = gaps.filter((x) => Math.abs(x - mg) > rule.tolerance).length;
    if (outliers > Math.floor(byDay.length / 5)) continue;

    const amounts = byDay.map((r) => Math.abs(num(r.amount)));
    const ma = median(amounts);
    const slack = (isBill(byDay[byDay.length - 1].category) ? 0.35 : 0.2) * ma;
    if (amounts.some((a) => Math.abs(a - ma) > Math.max(slack, 1))) continue;

    const last = byDay[byDay.length - 1];
    if (diffDays(last.date, today) > rule.period * 1.5 + rule.tolerance) continue;

    const direction = num(last.amount) > 0 ? "outflow" : "inflow";
    const sign = direction === "outflow" ? 1 : -1;
    const mean = amounts.reduce((a, b) => a + b, 0) / amounts.length;
    out.push({
      stream_id: detectedStreamId(last.account_id, last.merchant_key, direction),
      source: "detected",
      item_id: last.item_id,
      account_id: last.account_id,
      direction,
      description: last.name,
      merchant_name: last.merchant_name,
      merchant_key: last.merchant_key,
      pfc_primary: last.pfc_primary,
      pfc_detailed: last.pfc_detailed,
      category: last.category,
      frequency: rule.frequency,
      first_date: byDay[0].date,
      last_date: last.date,
      predicted_next_date: nextOnOrAfter(advance(last.date, rule.frequency), rule.frequency, today),
      average_amount: (sign * mean).toFixed(2),
      last_amount: (sign * Math.abs(num(last.amount))).toFixed(2),
      iso_currency_code: last.iso_currency_code,
      unofficial_currency_code: last.unofficial_currency_code,
      is_active: true,
      status: byDay.length >= 4 ? "MATURE" : "EARLY_DETECTION",
      transaction_count: byDay.length,
      account_name: last.account_name,
      account_mask: last.account_mask,
      account_type: last.account_type,
      account_subtype: last.account_subtype,
      institution_name: last.institution_name,
      updated_at: `${today}T00:00:00Z`,
    });
  }
  return out.sort((a, b) => (a.predicted_next_date! < b.predicted_next_date! ? -1 : a.predicted_next_date! > b.predicted_next_date! ? 1 : a.stream_id.localeCompare(b.stream_id)));
}
