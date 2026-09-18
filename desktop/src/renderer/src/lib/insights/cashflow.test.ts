import { describe, expect, it } from "vitest";

import type { StreamRow } from "@/lib/topper-types";

import { everydaySpending, inAndOut, lowPoint, pastBalances, projectBalance, safeToSpend, streamFlows } from "./cashflow";

const base: Omit<StreamRow, "stream_id" | "direction" | "category" | "frequency" | "predicted_next_date" | "last_amount"> = {
  source: "plaid",
  item_id: "i",
  account_id: "chq",
  description: "X",
  merchant_name: null,
  merchant_key: "x",
  pfc_primary: null,
  pfc_detailed: null,
  first_date: "2026-01-01",
  last_date: "2026-09-01",
  average_amount: null,
  iso_currency_code: "CAD",
  unofficial_currency_code: null,
  is_active: true,
  status: "MATURE",
  transaction_count: 3,
  account_name: "Chequing",
  account_mask: "2291",
  account_type: "depository",
  account_subtype: "checking",
  institution_name: "CIBC",
  updated_at: "2026-09-17T00:00:00Z",
};

const streams: StreamRow[] = [
  { ...base, stream_id: "pay", direction: "inflow", category: "income", frequency: "BIWEEKLY", predicted_next_date: "2026-09-25", last_amount: "-3050" },
  { ...base, stream_id: "amex", direction: "outflow", category: "transfer", frequency: "MONTHLY", predicted_next_date: "2026-09-24", last_amount: "642.19", pfc_detailed: "LOAN_PAYMENTS_CREDIT_CARD_PAYMENT" },
  { ...base, stream_id: "rent", direction: "outflow", category: "housing", frequency: "MONTHLY", predicted_next_date: "2026-10-01", last_amount: "1450" },
  { ...base, stream_id: "card", account_id: "amex", direction: "outflow", category: "subscriptions", frequency: "MONTHLY", predicted_next_date: "2026-09-19", last_amount: "12.99" },
];

describe("cashflow", () => {
  const cash = new Set(["chq"]);

  it("expands only the cash accounts' streams, signed from the holder's side", () => {
    const flows = streamFlows(streams, cash, "2026-09-18", "2026-10-01");
    expect(flows.map((f) => [f.day, f.amount, f.kind])).toEqual([
      ["2026-09-24", -642.19, "payment"],
      ["2026-09-25", 3050, "pay"],
      ["2026-10-01", -1450, "payment"],
    ]);
  });

  it("works balances backwards through posted transactions", () => {
    const txns = [
      { date: "2026-09-17", amount: "100", pending: false, category: "dining", merchant_key: "a" },
      { date: "2026-09-16", amount: "-50", pending: false, category: "income", merchant_key: "b" },
      { date: "2026-09-17", amount: "999", pending: true, category: "dining", merchant_key: "c" },
    ];
    expect(pastBalances(1000, txns, "2026-09-15", "2026-09-17")).toEqual([
      { day: "2026-09-15", balance: 1050 },
      { day: "2026-09-16", balance: 1100 },
      { day: "2026-09-17", balance: 1000 },
    ]);
  });

  it("leaves stream merchants out of the everyday rate", () => {
    const txns = [
      { date: "2026-09-01", amount: "20", pending: false, category: "dining", merchant_key: "cafe" },
      { date: "2026-09-02", amount: "1450", pending: false, category: "housing", merchant_key: "rent" },
      { date: "2026-09-02", amount: "500", pending: false, category: "transfer", merchant_key: "tfsa" },
    ];
    const r = everydaySpending(txns, "2026-09-01", "2026-09-02", new Set(["rent"]));
    expect(r.mean).toBe(10);
  });

  it("projects, finds the low point and what is safe to spend", () => {
    const flows = streamFlows(streams, cash, "2026-09-18", "2026-10-17");
    const points = projectBalance(3412.58, flows, { mean: 0, sd: 0 }, "2026-09-17", 30);
    expect(points[7].balance).toBeCloseTo(3412.58 - 642.19);
    expect(lowPoint(points)?.day).toBe("2026-09-24");

    const s = safeToSpend(3412.58, flows, "2026-09-17", 500);
    expect(s.payday?.day).toBe("2026-09-25");
    expect(s.until).toBe("2026-09-24");
    expect(s.outflows.map((f) => f.stream.stream_id)).toEqual(["amex"]);
    expect(s.safe).toBeCloseTo(2270.39);
    expect(s.days).toBe(8);
  });

  it("totals income against spending per month", () => {
    const monthly = new Map([["2026-08-01", new Map([["income", -6100], ["dining", 400], ["housing", 1450], ["transfer", 500]])]]);
    expect(inAndOut(monthly, ["2026-08-01", "2026-09-01"])).toEqual([
      { month: "2026-08-01", income: 6100, spending: 1850 },
      { month: "2026-09-01", income: 0, spending: 0 },
    ]);
  });
});
