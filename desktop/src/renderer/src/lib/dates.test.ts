import { describe, expect, it } from "vitest";

import {
  addDays,
  addMonths,
  daysInMonth,
  diffDays,
  eachDay,
  fmtLongDay,
  fmtRelativeDay,
  quarterStart,
  todayISO,
  weekdayMon0,
} from "./dates";

describe("dates", () => {
  it("does calendar arithmetic across month and year ends", () => {
    expect(addDays("2026-09-30", 1)).toBe("2026-10-01");
    expect(addDays("2026-01-01", -1)).toBe("2025-12-31");
    expect(diffDays("2026-09-17", "2026-10-01")).toBe(14);
    expect(daysInMonth("2028-02-10")).toBe(29);
    expect(eachDay("2026-02-27", "2026-03-02")).toEqual(["2026-02-27", "2026-02-28", "2026-03-01", "2026-03-02"]);
  });

  it("clamps addMonths to the target month's length", () => {
    expect(addMonths("2026-01-31", 1)).toBe("2026-02-28");
    expect(addMonths("2026-03-15", -3)).toBe("2025-12-15");
  });

  it("starts weeks on Monday and quarters on the first month", () => {
    expect(weekdayMon0("2026-09-14")).toBe(0); // a Monday
    expect(weekdayMon0("2026-09-20")).toBe(6);
    expect(quarterStart("2026-09-17")).toBe("2026-07-01");
  });

  it("formats days the way the screens read", () => {
    expect(fmtLongDay("2026-09-17")).toBe("Thursday, September 17");
    expect(fmtRelativeDay("2026-09-17", "2026-09-17")).toBe("Today");
    expect(fmtRelativeDay("2026-09-16", "2026-09-17")).toBe("Yesterday");
    expect(fmtRelativeDay("2026-09-19", "2026-09-17")).toBe("Saturday");
    expect(fmtRelativeDay("2026-09-15", "2026-09-17")).toBe("Sep 15");
  });

  it("uses the local date for today", () => {
    expect(todayISO(new Date(2026, 8, 17, 23, 59))).toBe("2026-09-17");
  });
});
