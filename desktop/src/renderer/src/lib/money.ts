// Number formatting for the insight screens. Stored amounts stay decimal
// strings (see format.ts); aggregates, averages and projections are
// estimates and are computed as numbers, then rounded for display. Negative
// figures use a true minus sign (U+2212), as the design does.

const MINUS = "−";

const formatters = new Map<string, Intl.NumberFormat>();

function formatter(currency: string, digits: number): Intl.NumberFormat {
  const key = `${currency}:${digits}`;
  let f = formatters.get(key);
  if (!f) {
    try {
      f = new Intl.NumberFormat("en-CA", {
        style: "currency",
        currency,
        currencyDisplay: "narrowSymbol",
        minimumFractionDigits: digits,
        maximumFractionDigits: digits,
      });
    } catch {
      f = new Intl.NumberFormat("en-CA", { minimumFractionDigits: digits, maximumFractionDigits: digits });
    }
    formatters.set(key, f);
  }
  return f;
}

// num parses a decimal string; null, empty and garbage are 0.
export function num(s: string | null | undefined): number {
  if (s === null || s === undefined || s === "") return 0;
  const n = Number(s);
  return Number.isFinite(n) ? n : 0;
}

// fmt formats an amount: "$1,968", "−$1,208.44" with digits 2.
export function fmt(n: number, digits = 0, currency = "CAD"): string {
  const r = Math.abs(n) < 0.5 * 10 ** -digits ? 0 : n;
  const out = formatter(currency, digits).format(Math.abs(r));
  return r < 0 ? MINUS + out : out;
}

// fmtSigned always shows the sign: "+$3,050.00", "−$12.99".
export function fmtSigned(n: number, digits = 2, currency = "CAD"): string {
  const s = fmt(n, digits, currency);
  return n > 0 && s !== fmt(0, digits, currency) ? `+${s}` : s;
}

// fmtAxis is a compact axis label: "$4k", "$0", "$850", "−$2k".
export function fmtAxis(n: number): string {
  const sign = n < 0 ? MINUS : "";
  const a = Math.abs(n);
  if (a >= 1000) {
    const k = a / 1000;
    return `${sign}$${Number.isInteger(k) ? k : k.toFixed(1)}k`;
  }
  return `${sign}$${Math.round(a)}`;
}

// fmtPct is a whole percentage: "17%".
export function fmtPct(ratio: number): string {
  return `${Math.round(ratio * 100)}%`;
}

// niceCeil rounds up to a tidy axis maximum: 1, 2, 2.5, 5 or 10 times a
// power of ten.
export function niceCeil(n: number): number {
  if (n <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(n));
  for (const m of [1, 2, 2.5, 5, 10]) {
    if (m * p >= n) return m * p;
  }
  return 10 * p;
}

// niceStep picks a tick step giving about `count` ticks up to max.
export function niceStep(max: number, count = 4): number {
  return niceCeil(max / count);
}
