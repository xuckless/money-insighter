import { describe, expect, it } from "vitest";

import { notes, type NoteInput } from "./notes";

const base: NoteInput = {
  currency: "CAD",
  today: "2026-09-17",
  isCurrentMonth: true,
  spent: new Map(),
  usual: new Map(),
  fixed: (c) => c === "subscriptions",
  budgetTotal: 0,
  projected: 0,
  priceChanges: [],
  needsCategory: 0,
  utilization: null,
  reconnect: [],
};

const text = (runs: ReturnType<typeof notes>[number]["runs"]) =>
  runs.map((r) => (typeof r === "string" ? r : "strong" in r ? r.strong : r.link)).join("");

describe("notes", () => {
  it("says nothing when nothing stands out", () => {
    expect(notes(base)).toEqual([]);
  });

  it("flags a category well above its usual pace, but not fixed costs", () => {
    const n = notes({
      ...base,
      spent: new Map([["dining", 436], ["subscriptions", 300]]),
      usual: new Map([["dining", 316], ["subscriptions", 100]]),
    });
    expect(n).toHaveLength(1);
    expect(text(n[0].runs)).toBe("Dining & takeout is running 38% above your usual pace: $436 so far, against $316 by this point in a typical month.");
  });

  it("describes a price change at the next renewal", () => {
    const n = notes({ ...base, priceChanges: [{ name: "Spotify", from: 11.99, to: 12.99, next: "2026-09-19", yearly: 155.88 }] });
    expect(text(n[0].runs)).toBe("Spotify goes from $11.99 to $12.99 at Saturday’s renewal. That’s $12 more a year.");
  });

  it("puts reconnects first and counts uncategorised transactions", () => {
    const n = notes({ ...base, needsCategory: 5, reconnect: ["Wealthsimple"] });
    expect(n.map((x) => x.id)).toEqual(["reconnect-Wealthsimple", "uncategorized"]);
    expect(text(n[1].runs)).toBe("5 transactions need a category. Sort them now");
  });
});
