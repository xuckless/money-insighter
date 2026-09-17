import { describe, expect, it } from "vitest";

import type { BalanceDayRow } from "@/lib/topper-types";

import { accountClass, accountLabel, initials, netWorthSeries, primaryCurrency } from "./accounts";

const snap = (day: string, account_id: string, account_type: string, current_balance: string, missing_since: string | null = null): BalanceDayRow => ({
  day,
  account_id,
  item_id: "i",
  account_name: account_id,
  account_mask: null,
  account_type,
  account_subtype: null,
  institution_name: null,
  current_balance,
  available_balance: null,
  credit_limit: null,
  iso_currency_code: "CAD",
  unofficial_currency_code: null,
  missing_since,
});

describe("accounts", () => {
  it("classifies accounts", () => {
    expect(accountClass("depository", "checking")).toBe("chequing");
    expect(accountClass("depository", "savings")).toBe("savings");
    expect(accountClass("credit", "credit card")).toBe("credit");
    expect(accountClass("investment", "tfsa")).toBe("investment");
    expect(accountLabel("depository", "checking", "2291")).toBe("Chequing ••2291");
  });

  it("picks the most common currency", () => {
    const c = (iso: string) => ({ iso_currency_code: iso, unofficial_currency_code: null });
    expect(primaryCurrency([c("USD"), c("CAD"), c("CAD")])).toBe("CAD");
    expect(primaryCurrency([])).toBe("CAD");
  });

  it("carries balances forward and subtracts what is owed", () => {
    const rows = [
      snap("2026-09-15", "chq", "depository", "1000"),
      snap("2026-09-15", "card", "credit", "200"),
      snap("2026-09-17", "chq", "depository", "1500"),
    ];
    expect(netWorthSeries(rows, "CAD", "2026-09-01", "2026-09-17")).toEqual([
      { day: "2026-09-15", value: 800 },
      { day: "2026-09-16", value: 800 },
      { day: "2026-09-17", value: 1300 },
    ]);
  });

  it("stops counting an account once it goes missing", () => {
    const rows = [snap("2026-09-15", "old", "depository", "10", "2026-09-16T00:00:00Z"), snap("2026-09-15", "chq", "depository", "5")];
    expect(netWorthSeries(rows, "CAD", "2026-09-15", "2026-09-16").map((p) => p.value)).toEqual([15, 5]);
  });

  it("makes tile initials", () => {
    expect(initials("CIBC")).toBe("CIBC");
    expect(initials("EQ Bank")).toBe("EQ");
    expect(initials("American Express")).toBe("AE");
    expect(initials("Wealthsimple")).toBe("WE");
  });
});
