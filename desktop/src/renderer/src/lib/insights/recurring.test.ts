import { describe, expect, it } from "vitest";

import type { StreamRow } from "@/lib/topper-types";

import { monthlyAmount, occurrences, priceChangeCandidate, streamGroup, streamName, varies } from "./recurring";

const stream = (over: Partial<StreamRow>): StreamRow => ({
  stream_id: "s",
  source: "plaid",
  item_id: "i",
  account_id: "a",
  direction: "outflow",
  description: "SPOTIFY P2C4A1",
  merchant_name: null,
  merchant_key: "spotify",
  pfc_primary: null,
  pfc_detailed: null,
  category: "subscriptions",
  frequency: "MONTHLY",
  first_date: "2026-01-19",
  last_date: "2026-08-19",
  predicted_next_date: "2026-09-19",
  average_amount: "11.99",
  last_amount: "11.99",
  iso_currency_code: "CAD",
  unofficial_currency_code: null,
  is_active: true,
  status: "MATURE",
  transaction_count: 8,
  account_name: "Cobalt",
  account_mask: "1004",
  account_type: "credit",
  account_subtype: "credit card",
  institution_name: "American Express",
  updated_at: "2026-09-17T00:00:00Z",
  ...over,
});

describe("recurring", () => {
  it("groups streams", () => {
    expect(streamGroup(stream({}))).toBe("subscriptions");
    expect(streamGroup(stream({ category: "utilities" }))).toBe("bills");
    expect(streamGroup(stream({ direction: "inflow", category: "income" }))).toBe("income");
    expect(streamGroup(stream({ category: "transfer" }))).toBe("income");
  });

  it("converts to monthly amounts", () => {
    expect(monthlyAmount(stream({ frequency: "BIWEEKLY", last_amount: "-3050" }))).toBeCloseTo((3050 * 26) / 12);
    expect(monthlyAmount(stream({ frequency: "ANNUALLY", last_amount: "99" }))).toBeCloseTo(8.25);
  });

  it("lists occurrences in both directions from the predicted date", () => {
    expect(occurrences(stream({}), "2026-08-01", "2026-10-31")).toEqual(["2026-08-19", "2026-09-19", "2026-10-19"]);
    expect(occurrences(stream({ frequency: "BIWEEKLY", predicted_next_date: "2026-09-25" }), "2026-09-01", "2026-10-15")).toEqual([
      "2026-09-11",
      "2026-09-25",
      "2026-10-09",
    ]);
    expect(occurrences(stream({ frequency: "UNKNOWN" }), "2026-09-01", "2026-12-31")).toEqual(["2026-09-19"]);
    expect(occurrences(stream({}), "2027-01-01", "2026-12-31")).toEqual([]);
  });

  it("names streams and spots changes", () => {
    expect(streamName(stream({}))).toBe("Spotify P2c4a1");
    expect(streamName(stream({ merchant_name: "Spotify" }))).toBe("Spotify");
    expect(priceChangeCandidate(stream({ average_amount: "12.09", last_amount: "12.99" }))).toBe(true);
    expect(priceChangeCandidate(stream({ average_amount: "12.09", last_amount: "12.19" }))).toBe(false);
    expect(priceChangeCandidate(stream({ category: "utilities", average_amount: "80", last_amount: "96" }))).toBe(false);
    expect(varies(stream({ average_amount: "80", last_amount: "96" }))).toBe(true);
  });
});
