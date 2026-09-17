import { sumDecimals } from "@/lib/format";
import type { AccountRow } from "@/lib/topper-types";

// Plaid reports credit and loan balances as the amount owed (positive).
export const LIABILITY_TYPES: ReadonlySet<string> = new Set(["credit", "loan"]);

export interface CurrencyTotals {
  currency: string;
  assets: string;
  liabilities: string;
  net: string;
  accounts: number;
}

export function totalsByCurrency(accounts: AccountRow[]): CurrencyTotals[] {
  const groups = new Map<string, { assets: string[]; liabilities: string[]; n: number }>();
  for (const a of accounts) {
    const currency = a.iso_currency_code ?? a.unofficial_currency_code ?? "CAD";
    const g = groups.get(currency) ?? { assets: [], liabilities: [], n: 0 };
    g.n++;
    if (a.current_balance !== null) {
      (LIABILITY_TYPES.has(a.type) ? g.liabilities : g.assets).push(a.current_balance);
    }
    groups.set(currency, g);
  }
  return [...groups.entries()]
    .map(([currency, g]) => {
      const assets = sumDecimals(g.assets);
      const liabilities = sumDecimals(g.liabilities);
      const net = sumDecimals([assets, liabilities.startsWith("-") ? liabilities.slice(1) : `-${liabilities}`]);
      return { currency, assets, liabilities, net, accounts: g.n };
    })
    .sort((a, b) => b.accounts - a.accounts);
}
