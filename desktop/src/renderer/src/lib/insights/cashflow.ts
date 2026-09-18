import { addDays, diffDays, eachDay, type ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import type { StreamRow } from "@/lib/topper-types";

import { isIncome, isSpending, isTransfer } from "@shared/categories";

import { occurrences, streamAmount, streamName } from "./recurring";

// Cash here means the balance of chequing and savings accounts. Credit card
// spending reaches cash only through the card payment, which Plaid reports
// as its own recurring stream on the chequing account.

export type FlowKind = "pay" | "payment" | "move" | "bill";

export interface Flow {
  day: ISODate;
  // Signed from the account holder's side: money in is positive.
  amount: number;
  name: string;
  kind: FlowKind;
  stream: StreamRow;
}

function flowKind(s: StreamRow): FlowKind {
  if (s.direction === "inflow") return isIncome(s.category) ? "pay" : "move";
  const detailed = s.pfc_detailed ?? "";
  if (detailed.startsWith("TRANSFER_OUT")) return "move";
  if (detailed.startsWith("LOAN_PAYMENTS") || s.category === "housing" || s.category === "loans" || isTransfer(s.category)) return "payment";
  return "bill";
}

// streamFlows expands the active streams of the given accounts into dated
// flows between from and to, in date order.
export function streamFlows(streams: StreamRow[], accountIds: ReadonlySet<string>, from: ISODate, to: ISODate): Flow[] {
  const out: Flow[] = [];
  for (const s of streams) {
    if (!s.is_active || !accountIds.has(s.account_id)) continue;
    const amount = s.direction === "inflow" ? streamAmount(s) : -streamAmount(s);
    if (amount === 0) continue;
    for (const day of occurrences(s, from, to)) {
      out.push({ day, amount, name: streamName(s), kind: flowKind(s), stream: s });
    }
  }
  return out.sort((a, b) => (a.day < b.day ? -1 : a.day > b.day ? 1 : a.amount - b.amount));
}

export interface CashTxn {
  date: ISODate;
  amount: string; // Plaid sign: positive is money out
  pending: boolean;
  category: string;
  merchant_key: string;
}

export interface BalancePoint {
  day: ISODate;
  balance: number;
}

// pastBalances walks cash balances backwards from today's balance through
// posted transactions: the balance at the end of the day before d is the
// balance at the end of d plus what left on d. Pending rows are not in the
// current balance yet, so they are skipped.
export function pastBalances(current: number, txns: CashTxn[], from: ISODate, today: ISODate): BalancePoint[] {
  const byDay = new Map<ISODate, number>();
  for (const t of txns) {
    if (t.pending || t.date < from || t.date > today) continue;
    byDay.set(t.date, (byDay.get(t.date) ?? 0) + num(t.amount));
  }
  const out: BalancePoint[] = [];
  let bal = current;
  for (let d = today; d >= from; d = addDays(d, -1)) {
    out.push({ day: d, balance: bal });
    bal += byDay.get(d) ?? 0;
  }
  return out.reverse();
}

export interface DailyRate {
  mean: number;
  sd: number;
}

// everydaySpending is the daily rate at which cash leaves through spending
// that no recurring stream covers: card-free debit purchases, cash
// withdrawals, e-transfers to people. Transactions from a merchant that
// has a stream on the same accounts are left out so they are not counted
// twice in a projection.
export function everydaySpending(txns: CashTxn[], from: ISODate, to: ISODate, streamKeys: ReadonlySet<string>): DailyRate {
  const byDay = new Map<ISODate, number>();
  for (const t of txns) {
    if (t.pending || t.date < from || t.date > to) continue;
    if (!isSpending(t.category) || streamKeys.has(t.merchant_key)) continue;
    byDay.set(t.date, (byDay.get(t.date) ?? 0) + num(t.amount));
  }
  const values = eachDay(from, to).map((d) => byDay.get(d) ?? 0);
  const mean = values.reduce((a, b) => a + b, 0) / Math.max(1, values.length);
  const variance = values.reduce((a, v) => a + (v - mean) ** 2, 0) / Math.max(1, values.length - 1);
  return { mean: Math.max(0, mean), sd: Math.sqrt(variance) };
}

export interface ProjectedPoint extends BalancePoint {
  low: number;
  high: number;
}

// projectBalance starts from today's balance and applies each day's flows
// and the everyday spending rate for `horizon` days. The likely range grows
// with the square root of the days ahead.
export function projectBalance(current: number, flows: Flow[], rate: DailyRate, today: ISODate, horizon: number): ProjectedPoint[] {
  const byDay = new Map<ISODate, number>();
  for (const f of flows) byDay.set(f.day, (byDay.get(f.day) ?? 0) + f.amount);
  const out: ProjectedPoint[] = [{ day: today, balance: current, low: current, high: current }];
  let bal = current;
  for (let k = 1; k <= horizon; k++) {
    const day = addDays(today, k);
    bal += (byDay.get(day) ?? 0) - rate.mean;
    const spread = rate.sd * Math.sqrt(k);
    out.push({ day, balance: bal, low: bal - spread, high: bal + spread });
  }
  return out;
}

export function lowPoint(points: ProjectedPoint[]): ProjectedPoint | null {
  let low: ProjectedPoint | null = null;
  for (const p of points.slice(1)) if (!low || p.balance < low.balance) low = p;
  return low;
}

export interface SafeToSpend {
  balance: number;
  payday: Flow | null;
  // Last day the money has to last through.
  until: ISODate;
  outflows: Flow[];
  cushion: number;
  safe: number;
  days: number;
  perDay: number;
}

// safeToSpend is what the chequing balance can spare before the next
// paycheque: the balance, less every outflow due on those accounts before
// then, less the cushion the user keeps. Without a predicted paycheque it
// looks two weeks ahead.
export function safeToSpend(balance: number, flows: Flow[], today: ISODate, cushion: number): SafeToSpend {
  const payday = flows.find((f) => f.kind === "pay" && f.day > today && diffDays(today, f.day) <= 45) ?? null;
  const until = payday ? addDays(payday.day, -1) : addDays(today, 14);
  const outflows = flows.filter((f) => f.amount < 0 && f.day > today && f.day <= until);
  const due = outflows.reduce((a, f) => a - f.amount, 0);
  const safe = balance - due - cushion;
  const days = Math.max(1, diffDays(today, until) + 1);
  return { balance, payday, until, outflows, cushion, safe, days, perDay: Math.max(0, safe) / days };
}

export interface MonthInOut {
  month: ISODate;
  income: number;
  spending: number;
}

// inAndOut is take-home pay (the income category) against everything
// spent (every spending category, rent included) per month.
export function inAndOut(monthly: Map<ISODate, Map<string, number>>, months: ISODate[]): MonthInOut[] {
  return months.map((month) => {
    const m = monthly.get(month) ?? new Map<string, number>();
    let income = 0;
    let spending = 0;
    for (const [cat, v] of m) {
      if (isIncome(cat)) income -= v;
      else if (isSpending(cat)) spending += v;
    }
    return { month, income, spending };
  });
}
