import { addDays, type ISODate } from "@/lib/dates";
import { num } from "@/lib/money";
import type { AccountRow, BalanceDayRow } from "@/lib/topper-types";

// How the screens group accounts. Plaid's type says asset or liability;
// the subtype separates everyday cash from savings.
export type AccountClass = "chequing" | "savings" | "investment" | "credit" | "loan" | "other";

const SAVINGS_SUBTYPES = new Set(["savings", "cd", "money market", "hsa", "tfsa", "rrsp"]);

export function accountClass(type: string, subtype: string | null): AccountClass {
  switch (type) {
    case "depository":
      return subtype && SAVINGS_SUBTYPES.has(subtype) ? "savings" : "chequing";
    case "credit":
      return "credit";
    case "loan":
      return "loan";
    case "investment":
    case "brokerage":
      return "investment";
    default:
      return "other";
  }
}

export function isLiability(cls: AccountClass): boolean {
  return cls === "credit" || cls === "loan";
}

export function isCash(cls: AccountClass): boolean {
  return cls === "chequing" || cls === "savings";
}

type Currencied = { iso_currency_code: string | null; unofficial_currency_code: string | null };

export function currencyOf(a: Currencied): string {
  return a.iso_currency_code ?? a.unofficial_currency_code ?? "CAD";
}

// primaryCurrency is the currency most accounts are in. Charts and totals
// use it; accounts in other currencies are listed but not added in, since
// adding amounts in two currencies would be meaningless.
export function primaryCurrency(accounts: Currencied[]): string {
  const counts = new Map<string, number>();
  for (const a of accounts) counts.set(currencyOf(a), (counts.get(currencyOf(a)) ?? 0) + 1);
  let best = "CAD";
  let n = 0;
  for (const [c, k] of counts) {
    if (k > n) [best, n] = [c, k];
  }
  return best;
}

// signedBalance is the account's contribution to net worth: Plaid reports
// what is owed on a card or loan as a positive balance.
export function signedBalance(type: string, subtype: string | null, balance: string | null): number {
  const v = num(balance);
  return isLiability(accountClass(type, subtype)) ? -v : v;
}

export interface Summary {
  net: number;
  assets: number;
  liabilities: number; // positive: the amount owed
  byClass: Record<AccountClass, number>; // liabilities positive too
  accounts: number;
}

export function summarize(accounts: AccountRow[]): Summary {
  const byClass: Record<AccountClass, number> = { chequing: 0, savings: 0, investment: 0, credit: 0, loan: 0, other: 0 };
  let assets = 0;
  let liabilities = 0;
  for (const a of accounts) {
    const cls = accountClass(a.type, a.subtype);
    const v = num(a.current_balance);
    byClass[cls] += v;
    if (isLiability(cls)) liabilities += v;
    else assets += v;
  }
  return { net: assets - liabilities, assets, liabilities, byClass, accounts: accounts.length };
}

export interface Utilization {
  owed: number;
  limit: number;
  // owed / limit over the cards that report a limit; null when none does.
  ratio: number | null;
}

export function cardUtilization(accounts: AccountRow[]): Utilization {
  let owed = 0;
  let limitOwed = 0;
  let limit = 0;
  for (const a of accounts) {
    if (accountClass(a.type, a.subtype) !== "credit") continue;
    const v = num(a.current_balance);
    owed += v;
    if (a.credit_limit !== null && num(a.credit_limit) > 0) {
      limit += num(a.credit_limit);
      limitOwed += v;
    }
  }
  return { owed, limit, ratio: limit > 0 ? limitOwed / limit : null };
}

export interface SeriesPoint {
  day: ISODate;
  value: number;
}

// balanceSeries turns daily snapshots into one value per day from the first
// snapshot on or after `from` (or the last one before it) through `to`.
// A day without a snapshot for an account carries that account's last
// known balance forward; an account stops counting from the day it went
// missing. `pick` chooses which accounts count and how.
export function balanceSeries(
  rows: BalanceDayRow[],
  from: ISODate,
  to: ISODate,
  pick: (row: BalanceDayRow) => number | null,
): SeriesPoint[] {
  const sorted = [...rows].sort((a, b) => (a.day < b.day ? -1 : a.day > b.day ? 1 : 0));
  if (sorted.length === 0) return [];
  const current = new Map<string, { value: number; missing: string | null }>();
  const out: SeriesPoint[] = [];
  let i = 0;
  const start = sorted[0].day > from ? sorted[0].day : from;
  for (let day = sorted[0].day; day <= to; day = addDays(day, 1)) {
    for (; i < sorted.length && sorted[i].day === day; i++) {
      const v = pick(sorted[i]);
      if (v !== null) current.set(sorted[i].account_id, { value: v, missing: sorted[i].missing_since });
    }
    if (day < start) continue;
    let total = 0;
    for (const { value, missing } of current.values()) {
      if (missing && missing.slice(0, 10) <= day) continue;
      total += value;
    }
    out.push({ day, value: total });
  }
  return out;
}

export function netWorthSeries(rows: BalanceDayRow[], currency: string, from: ISODate, to: ISODate): SeriesPoint[] {
  return balanceSeries(rows, from, to, (r) =>
    currencyOf(r) === currency ? signedBalance(r.account_type, r.account_subtype, r.current_balance) : null,
  );
}

// initials is the short mark shown in an account's tile: "CIBC", "EQ",
// "AE" for American Express.
export function initials(name: string | null | undefined): string {
  if (!name) return "?";
  const words = name.replace(/[^A-Za-z0-9 ]/g, " ").split(/\s+/).filter(Boolean);
  const first = words[0] ?? "";
  // An acronym leads the name ("CIBC", "EQ Bank", "TD Canada Trust").
  if (first.length >= 2 && first.length <= 4 && first === first.toUpperCase()) return first;
  if (words.length === 1) return first.slice(0, 2).toUpperCase();
  return words
    .slice(0, 2)
    .map((w) => w[0])
    .join("")
    .toUpperCase();
}

// accountLabel is "Chequing ••2291" style: the subtype (or type) and mask.
export function accountLabel(type: string, subtype: string | null, mask: string | null): string {
  const cls = accountClass(type, subtype);
  const base =
    cls === "chequing"
      ? "Chequing"
      : cls === "savings"
        ? "Savings"
        : cls === "credit"
          ? "Credit"
          : cls === "loan"
            ? "Loan"
            : cls === "investment"
              ? "Investment"
              : "Account";
  return mask ? `${base} ••${mask}` : base;
}

// shortAccount is "Amex ••1004": the institution or account name and mask.
export function shortAccount(name: string, institution: string | null, mask: string | null): string {
  const who = institution ? institution.replace(/^American Express$/, "Amex") : name;
  return mask ? `${who} ••${mask}` : who;
}
