import { describe, expect, it } from "vitest";

import { addDays, eachDay } from "@/lib/dates";
import type { CategoryDayRow } from "@/lib/topper-types";

import {
  byCategory,
  cumulative,
  indexDaily,
  isPaceCategory,
  monthsBack,
  project,
  roundBudget,
  spendingHeadline,
  suggestBudgets,
  usualSoFar,
} from "./spending";

const row = (day: string, category: string, amount: string, currency = "CAD"): CategoryDayRow => ({
  day,
  category,
  amount,
  iso_currency_code: currency,
  transactions: 1,
});

describe("spending", () => {
  it("indexes one currency and nets refunds", () => {
    const idx = indexDaily(
      [row("2026-09-01", "dining", "20"), row("2026-09-01", "dining", "-5"), row("2026-09-01", "dining", "99", "USD")],
      "CAD",
    );
    expect(byCategory(idx, "2026-09-01", "2026-09-30").get("dining")).toBe(15);
  });

  it("keeps rent, loans, income and transfers out of the pace", () => {
    expect(isPaceCategory("groceries")).toBe(true);
    expect(isPaceCategory("housing")).toBe(false);
    expect(isPaceCategory("loans")).toBe(false);
    expect(isPaceCategory("income")).toBe(false);
    expect(isPaceCategory("transfer")).toBe(false);
  });

  it("builds a running total", () => {
    const idx = indexDaily([row("2026-09-01", "dining", "10"), row("2026-09-03", "groceries", "5")], "CAD");
    expect(cumulative(idx, "2026-09-01", "2026-09-04", isPaceCategory)).toEqual([10, 10, 15, 15]);
  });

  it("projects from earlier months' daily rate", () => {
    // $10 a day for the 90 days before September, $20 a day so far.
    const rows = eachDay(addDays("2026-09-01", -90), "2026-08-31").map((d) => row(d, "groceries", "10"));
    rows.push(...eachDay("2026-09-01", "2026-09-10").map((d) => row(d, "groceries", "20")));
    const p = project(indexDaily(rows, "CAD"), "2026-09-01", "2026-09-30", "2026-09-10", isPaceCategory);
    expect(p.fromHistory).toBe(true);
    expect(p.spent).toBe(200);
    expect(p.remainingDays).toBe(20);
    expect(p.projected).toBeCloseTo(400);
    expect(p.sd).toBeCloseTo(0);
  });

  it("falls back to this month's rate without history", () => {
    const rows = eachDay("2026-09-01", "2026-09-10").map((d) => row(d, "dining", "30"));
    const p = project(indexDaily(rows, "CAD"), "2026-09-01", "2026-09-30", "2026-09-10", isPaceCategory);
    expect(p.fromHistory).toBe(false);
    expect(p.projected).toBeCloseTo(900);
  });

  it("averages the same stretch of earlier months, skipping months with no data", () => {
    const rows = [
      row("2026-08-02", "dining", "100"),
      row("2026-08-20", "dining", "999"), // after day 17: not counted
      row("2026-07-05", "dining", "50"),
    ];
    const idx = indexDaily(rows, "CAD");
    const has = (from: string, to: string) => [...idx.keys()].some((d) => d >= from && d <= to);
    const usual = usualSoFar(idx, "2026-09-01", 17, 3, monthsBack, (c) => c === "dining", has);
    expect(usual).toBe(75); // (100 + 50) / 2; June had nothing
  });

  it("suggests budgets from complete months, rounded up", () => {
    const monthly = new Map([
      ["2026-08-01", new Map([["dining", 430], ["income", -6000]])],
      ["2026-07-01", new Map([["dining", 510]])],
      ["2026-09-01", new Map([["dining", 5000]])], // the current month is ignored
    ]);
    const s = suggestBudgets(monthly, "2026-09-01");
    expect(s.get("dining")).toBe(475);
    expect(s.has("income")).toBe(false);
    expect(roundBudget(123)).toBe(130);
    expect(roundBudget(1210)).toBe(1250);
  });

  it("writes the headline about food when it dominates", () => {
    const h = spendingHeadline(new Map([["groceries", 512], ["dining", 436], ["shopping", 1020]]), "September", (n) => `$${n}`);
    expect(h).toEqual({ lead: "Almost half of September has gone to food: ", amount: "$948", tail: " across groceries and dining." });
    const t = spendingHeadline(new Map([["shopping", 800], ["dining", 200]]), "September", (n) => `$${n}`);
    expect(t?.lead).toBe("Shopping leads September: ");
  });
});

describe("coveredFrom", () => {
  it("counts a period only when history reaches its start", async () => {
    const { coveredFrom } = await import("./spending");
    const idx = indexDaily([row("2026-06-20", "dining", "5"), row("2026-08-03", "dining", "5")], "CAD");
    const covered = coveredFrom(idx);
    expect(covered("2026-06-01", "2026-06-30")).toBe(false);
    expect(covered("2026-06-15", "2026-06-30")).toBe(true);
    expect(covered("2026-07-01", "2026-07-31")).toBe(true);
  });
});
