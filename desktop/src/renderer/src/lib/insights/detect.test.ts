import { describe, expect, it } from "vitest";

import { addDays, addMonths } from "@/lib/dates";

import { detectStreams, nextOnOrAfter, type DetectRow } from "./detect";

const today = "2026-09-17";

let n = 0;
const row = (date: string, amount: number, over: Partial<DetectRow> = {}): DetectRow => ({
  transaction_id: `t${++n}`,
  account_id: "acc",
  item_id: "item",
  amount: amount.toFixed(2),
  iso_currency_code: "CAD",
  unofficial_currency_code: null,
  date,
  name: "SPOTIFY",
  merchant_name: "Spotify",
  merchant_key: "spotify",
  pending: false,
  pfc_primary: "ENTERTAINMENT",
  pfc_detailed: "ENTERTAINMENT_MUSIC_AND_AUDIO",
  category: "subscriptions",
  account_name: "Chequing",
  account_mask: "1234",
  account_type: "depository",
  account_subtype: "checking",
  institution_name: "Bank",
  ...over,
});

const monthly = (start: string, months: number, amount: number, over: Partial<DetectRow> = {}) =>
  Array.from({ length: months }, (_, i) => row(addMonths(start, i), amount, over));

describe("detectStreams", () => {
  it("finds a monthly subscription and predicts the next charge", () => {
    const streams = detectStreams(monthly("2026-03-19", 6, 11.99), today);
    expect(streams).toHaveLength(1);
    const s = streams[0];
    expect(s.source).toBe("detected");
    expect(s.frequency).toBe("MONTHLY");
    expect(s.status).toBe("MATURE");
    expect(s.transaction_count).toBe(6);
    expect(s.last_date).toBe("2026-08-19");
    expect(s.predicted_next_date).toBe("2026-09-19");
    expect(s.last_amount).toBe("11.99");
    expect(s.direction).toBe("outflow");
    expect(s.stream_id).toBe("detected:acc:spotify:outflow");
  });

  it("needs three monthly charges and marks a young stream as early", () => {
    expect(detectStreams(monthly("2026-07-01", 2, 20), today)).toHaveLength(0);
    const s = detectStreams(monthly("2026-06-01", 3, 20), today);
    expect(s).toHaveLength(1);
    expect(s[0].status).toBe("EARLY_DETECTION");
  });

  it("skips merchants whose amounts wander", () => {
    const rows = [row("2026-06-05", 84.1), row("2026-07-05", 31.2), row("2026-08-05", 120), row("2026-09-05", 62)].map((r) => ({
      ...r,
      merchant_key: "loblaws",
      category: "groceries",
    }));
    expect(detectStreams(rows, today)).toHaveLength(0);
  });

  it("lets a bill vary more than everyday spending", () => {
    const bill = { merchant_key: "hydro", category: "utilities" as const };
    const rows = [row("2026-06-05", 70, bill), row("2026-07-05", 95, bill), row("2026-08-05", 110, bill), row("2026-09-05", 72, bill)];
    expect(detectStreams(rows, today)).toHaveLength(1);
    const shop = rows.map((r) => ({ ...r, category: "shopping" }));
    expect(detectStreams(shop, today)).toHaveLength(0);
  });

  it("detects weekly and biweekly deposits as inflows", () => {
    const pay = { merchant_key: "acme payroll", category: "income", name: "ACME PAYROLL", merchant_name: null };
    const biweekly = Array.from({ length: 6 }, (_, i) => row(addDays("2026-06-26", 14 * i), -2100, pay));
    const s = detectStreams(biweekly, today);
    expect(s).toHaveLength(1);
    expect(s[0].frequency).toBe("BIWEEKLY");
    expect(s[0].direction).toBe("inflow");
    expect(s[0].average_amount).toBe("-2100.00");
    expect(s[0].predicted_next_date).toBe("2026-09-18");

    const weekly = Array.from({ length: 8 }, (_, i) => row(addDays("2026-07-24", 7 * i), -500, pay));
    expect(detectStreams(weekly, today)[0].frequency).toBe("WEEKLY");
  });

  it("drops a stream whose last charge is long gone", () => {
    expect(detectStreams(monthly("2025-09-01", 6, 10), today)).toHaveLength(0);
  });

  it("tolerates a late charge and ignores pending rows", () => {
    const rows = [...monthly("2026-04-03", 4, 15), row("2026-08-09", 15), row("2026-09-04", 15), row("2026-09-16", 15, { pending: true })];
    const s = detectStreams(rows, today);
    expect(s).toHaveLength(1);
    expect(s[0].transaction_count).toBe(6);
  });

  it("finds a yearly charge from two years", () => {
    const s = detectStreams([row("2025-09-01", 99, { merchant_key: "domain" }), row("2026-09-01", 99, { merchant_key: "domain" })], today);
    expect(s).toHaveLength(1);
    expect(s[0].frequency).toBe("ANNUALLY");
    expect(s[0].predicted_next_date).toBe("2027-09-01");
  });

  it("keeps a merchant's inflows and outflows apart and one charge per day", () => {
    const rows = [...monthly("2026-05-10", 5, 30), row("2026-07-10", 30), row("2026-07-11", -30)];
    const s = detectStreams(rows, today);
    expect(s).toHaveLength(1);
    expect(s[0].direction).toBe("outflow");
  });
});

describe("nextOnOrAfter", () => {
  it("steps forward until today", () => {
    expect(nextOnOrAfter("2026-06-15", "MONTHLY", today)).toBe("2026-10-15");
    expect(nextOnOrAfter("2026-09-17", "WEEKLY", today)).toBe("2026-09-17");
  });
});
