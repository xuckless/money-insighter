import { describe, expect, it } from "vitest";

import { fmt, fmtAxis, fmtSigned, niceCeil, num } from "./money";

describe("money", () => {
  it("formats with a true minus sign", () => {
    expect(fmt(1968)).toBe("$1,968");
    expect(fmt(-1208.44, 2)).toBe("−$1,208.44");
    expect(fmt(-0.001, 2)).toBe("$0.00");
    expect(fmtSigned(3050)).toBe("+$3,050.00");
    expect(fmtSigned(-12.99)).toBe("−$12.99");
  });

  it("labels axes compactly and picks tidy maxima", () => {
    expect(fmtAxis(4000)).toBe("$4k");
    expect(fmtAxis(2500)).toBe("$2.5k");
    expect(fmtAxis(0)).toBe("$0");
    expect(niceCeil(3164)).toBe(5000);
    expect(niceCeil(2100)).toBe(2500);
  });

  it("parses decimal strings leniently", () => {
    expect(num("12.34")).toBe(12.34);
    expect(num(null)).toBe(0);
    expect(num("x")).toBe(0);
  });
});
