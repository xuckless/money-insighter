// Formatting helpers. Amounts stay decimal strings end to end.

const moneyFormatters = new Map<string, Intl.NumberFormat>();

function moneyFormatter(currency: string): Intl.NumberFormat {
  let f = moneyFormatters.get(currency);
  if (!f) {
    try {
      f = new Intl.NumberFormat("en-CA", { style: "currency", currency });
    } catch {
      // Unofficial currencies (crypto etc.) are not ISO 4217 codes.
      f = new Intl.NumberFormat("en-CA", { minimumFractionDigits: 2, maximumFractionDigits: 8 });
    }
    moneyFormatters.set(currency, f);
  }
  return f;
}

// formatMoney formats a numeric string. Intl.NumberFormat accepts decimal
// strings directly (no float round-trip), so the stored scale is respected.
export function formatMoney(
  amount: string | null | undefined,
  currency: string | null | undefined,
): string {
  if (amount === null || amount === undefined || amount === "") return "—";
  const code = currency ?? "CAD";
  const f = moneyFormatter(code);
  const out = f.format(amount as unknown as number);
  return f.resolvedOptions().style === "currency" ? out : `${out} ${code}`;
}

// negate flips the sign of a decimal string without converting to a float.
// Plaid reports outflows as positive; the UI shows them as negative.
export function negate(amount: string): string {
  const s = amount.trim();
  if (s.startsWith("-")) return s.slice(1);
  if (/^0*(\.0*)?$/.test(s.replace("+", ""))) return s;
  return "-" + s.replace(/^\+/, "");
}

export function isNegative(amount: string): boolean {
  return amount.trim().startsWith("-") && !/^-0*(\.0*)?$/.test(amount.trim());
}

// sumDecimals adds decimal strings exactly using scaled BigInts.
export function sumDecimals(values: (string | null)[]): string {
  let scale = 0;
  const present = values.filter((v): v is string => v !== null && v !== "");
  for (const v of present) {
    const frac = v.split(".")[1];
    if (frac) scale = Math.max(scale, frac.length);
  }
  let total = BigInt(0);
  for (const v of present) {
    const neg = v.startsWith("-");
    const [int, frac = ""] = v.replace(/^[-+]/, "").split(".");
    const n = BigInt((int || "0") + frac.padEnd(scale, "0"));
    total += neg ? -n : n;
  }
  const neg = total < BigInt(0);
  const digits = (neg ? -total : total).toString().padStart(scale + 1, "0");
  const out = scale
    ? `${digits.slice(0, -scale)}.${digits.slice(-scale)}`
    : digits;
  return neg ? `-${out}` : out;
}

const dateFmt = new Intl.DateTimeFormat("en-CA", { dateStyle: "medium", timeZone: "UTC" });
const dateTimeFmt = new Intl.DateTimeFormat("en-CA", {
  dateStyle: "medium",
  timeStyle: "short",
});

// formatDate formats a YYYY-MM-DD calendar date (no timezone shift).
export function formatDate(date: string | null | undefined): string {
  if (!date) return "—";
  return dateFmt.format(new Date(`${date}T00:00:00Z`));
}

export function formatDateTime(ts: string | null | undefined): string {
  if (!ts) return "—";
  return dateTimeFmt.format(new Date(ts));
}

export function formatRelative(ts: string | null | undefined, now = Date.now()): string {
  if (!ts) return "never";
  const secs = Math.round((new Date(ts).getTime() - now) / 1000);
  const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  const abs = Math.abs(secs);
  if (abs < 60) return rtf.format(secs, "second");
  if (abs < 3600) return rtf.format(Math.round(secs / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(secs / 3600), "hour");
  return rtf.format(Math.round(secs / 86400), "day");
}

export function humanize(s: string | null | undefined): string {
  if (!s) return "—";
  return s.replace(/_/g, " ").toLowerCase().replace(/^\w/, (c) => c.toUpperCase());
}
