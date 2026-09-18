import { addDays, addMonths, type ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import type { RecurringEntryRow, StreamFrequency, StreamRow, StreamSource } from "@/lib/topper-types";

import { isIncome, isTransfer } from "@shared/categories";

import { nextOnOrAfter } from "./detect";

// Recurring streams, whatever their source (Plaid, detected here, or added
// by hand), grouped the way the Recurring screen shows them. Amounts here
// are magnitudes; direction says which way money moves.

export type StreamGroup = "bills" | "subscriptions" | "income";

const SUBSCRIPTION_CATEGORIES: ReadonlySet<string> = new Set(["subscriptions", "entertainment"]);

export function streamGroup(s: Pick<StreamRow, "direction" | "category">): StreamGroup {
  if (s.direction === "inflow" || isTransfer(s.category) || isIncome(s.category)) return "income";
  return SUBSCRIPTION_CATEGORIES.has(s.category) ? "subscriptions" : "bills";
}

export const sourceLabel: Record<StreamSource, string> = {
  plaid: "Plaid",
  detected: "Detected",
  manual: "Added by me",
};

// entryStream shapes a hand-added entry like a stream so the screens read
// one list. Its next date is the entry's, moved forward past today when
// the entry was saved a while ago.
export function entryStream(e: RecurringEntryRow, today: ISODate): StreamRow {
  const sign = e.direction === "outflow" ? 1 : -1;
  const amount = (sign * Math.abs(num(e.amount))).toFixed(2);
  const next = nextOnOrAfter(e.next_date, e.frequency, today);
  return {
    stream_id: `manual:${e.id}`,
    source: "manual",
    item_id: e.item_id ?? "",
    account_id: e.account_id ?? "",
    direction: e.direction,
    description: e.name,
    merchant_name: e.name,
    merchant_key: e.merchant_key ?? "",
    pfc_primary: null,
    pfc_detailed: null,
    category: e.category,
    frequency: e.frequency,
    first_date: e.next_date,
    last_date: next,
    predicted_next_date: next,
    average_amount: amount,
    last_amount: amount,
    iso_currency_code: e.iso_currency_code,
    unofficial_currency_code: e.unofficial_currency_code,
    is_active: true,
    status: "MATURE",
    transaction_count: 0,
    account_name: e.account_name ?? "",
    account_mask: e.account_mask,
    account_type: e.account_type ?? "",
    account_subtype: e.account_subtype,
    institution_name: e.institution_name,
    updated_at: e.updated_at,
  };
}

// streamAmount is the stream's current amount as a positive number: the
// last charge when Plaid has one, else the average.
export function streamAmount(s: Pick<StreamRow, "last_amount" | "average_amount">): number {
  return Math.abs(num(s.last_amount ?? s.average_amount));
}

export const frequencyLabel: Record<StreamFrequency, string> = {
  WEEKLY: "Weekly",
  BIWEEKLY: "Every 2 weeks",
  SEMI_MONTHLY: "Twice a month",
  MONTHLY: "Monthly",
  ANNUALLY: "Yearly",
  UNKNOWN: "Irregular",
};

const perYear: Record<StreamFrequency, number> = {
  WEEKLY: 52,
  BIWEEKLY: 26,
  SEMI_MONTHLY: 24,
  MONTHLY: 12,
  ANNUALLY: 1,
  UNKNOWN: 12,
};

export function yearlyAmount(s: Pick<StreamRow, "frequency" | "last_amount" | "average_amount">): number {
  return streamAmount(s) * perYear[s.frequency];
}

export function monthlyAmount(s: Pick<StreamRow, "frequency" | "last_amount" | "average_amount">): number {
  return yearlyAmount(s) / 12;
}

// varies reports a bill whose amount moves from charge to charge (hydro,
// gas), so the schedule reads "Monthly · varies" rather than a price change.
export function varies(s: Pick<StreamRow, "last_amount" | "average_amount">): boolean {
  const avg = Math.abs(num(s.average_amount));
  const last = Math.abs(num(s.last_amount));
  return avg > 0 && Math.abs(last - avg) / avg > 0.05;
}

// occurrences lists the dates a stream is expected to land on between from
// and to inclusive, stepping out from its predicted next date (or its last
// date when Plaid predicts none) in both directions. Irregular streams only
// land on the predicted date itself.
export function occurrences(
  s: Pick<StreamRow, "frequency" | "predicted_next_date" | "last_date">,
  from: ISODate,
  to: ISODate,
): ISODate[] {
  const anchor = s.predicted_next_date ?? s.last_date;
  const at = (i: number): ISODate[] => {
    switch (s.frequency) {
      case "WEEKLY":
        return [addDays(anchor, 7 * i)];
      case "BIWEEKLY":
        return [addDays(anchor, 14 * i)];
      case "SEMI_MONTHLY": {
        const m = addMonths(anchor, i);
        return [m, addDays(m, 15)];
      }
      case "MONTHLY":
        return [addMonths(anchor, i)];
      case "ANNUALLY":
        return [addMonths(anchor, 12 * i)];
      default:
        return i === 0 ? [anchor] : [];
    }
  };
  const out = new Set<ISODate>();
  for (const dir of [1, -1]) {
    for (let i = dir === 1 ? 0 : -1; Math.abs(i) < 400; i += dir) {
      const dates = at(i);
      if (dates.length === 0) break;
      let inside = false;
      let beyond = false;
      for (const d of dates) {
        if (d >= from && d <= to) {
          out.add(d);
          inside = true;
        }
        if ((dir === 1 && d > to) || (dir === -1 && d < from)) beyond = true;
      }
      if (beyond && !inside) break;
    }
  }
  return [...out].sort();
}

// streamName is the merchant when Plaid resolved one, else the description
// in title case ("PAYROLL DEPOSIT" becomes "Payroll Deposit").
export function streamName(s: Pick<StreamRow, "merchant_name" | "description">): string {
  if (s.merchant_name) return s.merchant_name;
  return s.description.toLowerCase().replace(/\b\w/g, (c) => c.toUpperCase());
}

export interface PriceChange {
  from: number;
  to: number;
}

// priceChangeCandidate reports whether a subscription's last charge differs
// from its average enough to be worth confirming against the charge before
// it: at least 50 cents and 1%.
export function priceChangeCandidate(s: StreamRow): boolean {
  if (s.source === "manual" || streamGroup(s) !== "subscriptions") return false;
  const avg = Math.abs(num(s.average_amount));
  const last = Math.abs(num(s.last_amount));
  const d = Math.abs(last - avg);
  return avg > 0 && d >= 0.5 && d / avg >= 0.01;
}
