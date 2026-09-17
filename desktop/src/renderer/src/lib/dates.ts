// Calendar-date helpers. Dates are ISO YYYY-MM-DD strings, as the topper
// sends them, and all arithmetic happens at UTC midnight so a daylight
// saving change can never move a date by a day.

export type ISODate = string;

const DAY = 86_400_000;

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const MONTHS_LONG = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];
const WEEKDAYS_LONG = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function toDate(d: ISODate): Date {
  return new Date(`${d}T00:00:00Z`);
}

function toISO(t: Date): ISODate {
  return t.toISOString().slice(0, 10);
}

// todayISO is the local calendar date: the day the user is living in,
// which is what "today" means on every screen.
export function todayISO(now = new Date()): ISODate {
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

export function addDays(d: ISODate, n: number): ISODate {
  return toISO(new Date(toDate(d).getTime() + n * DAY));
}

// diffDays is b − a in whole days.
export function diffDays(a: ISODate, b: ISODate): number {
  return Math.round((toDate(b).getTime() - toDate(a).getTime()) / DAY);
}

export function monthStart(d: ISODate): ISODate {
  return `${d.slice(0, 7)}-01`;
}

export function daysInMonth(d: ISODate): number {
  const t = toDate(d);
  return new Date(Date.UTC(t.getUTCFullYear(), t.getUTCMonth() + 1, 0)).getUTCDate();
}

export function monthEnd(d: ISODate): ISODate {
  return `${d.slice(0, 7)}-${String(daysInMonth(d)).padStart(2, "0")}`;
}

// addMonths moves by whole months, clamping the day to the target month's
// length (Jan 31 + 1 month is Feb 28 or 29).
export function addMonths(d: ISODate, n: number): ISODate {
  const t = toDate(d);
  const first = new Date(Date.UTC(t.getUTCFullYear(), t.getUTCMonth() + n, 1));
  const last = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0)).getUTCDate();
  first.setUTCDate(Math.min(t.getUTCDate(), last));
  return toISO(first);
}

export function dayOfMonth(d: ISODate): number {
  return Number(d.slice(8, 10));
}

// weekdayMon0 is the day of the week with Monday as 0, for calendar grids
// that start on Monday.
export function weekdayMon0(d: ISODate): number {
  return (toDate(d).getUTCDay() + 6) % 7;
}

export function quarterStart(d: ISODate): ISODate {
  const m = Number(d.slice(5, 7));
  const qm = Math.floor((m - 1) / 3) * 3 + 1;
  return `${d.slice(0, 4)}-${String(qm).padStart(2, "0")}-01`;
}

export function yearStart(d: ISODate): ISODate {
  return `${d.slice(0, 4)}-01-01`;
}

export function minDate(a: ISODate, b: ISODate): ISODate {
  return a < b ? a : b;
}

export function maxDate(a: ISODate, b: ISODate): ISODate {
  return a > b ? a : b;
}

// "Sep 17"
export function fmtDay(d: ISODate): string {
  const t = toDate(d);
  return `${MONTHS[t.getUTCMonth()]} ${t.getUTCDate()}`;
}

// "Sep"
export function fmtMonthShort(d: ISODate): string {
  return MONTHS[toDate(d).getUTCMonth()];
}

// "September"
export function fmtMonth(d: ISODate): string {
  return MONTHS_LONG[toDate(d).getUTCMonth()];
}

// "September 2026"
export function fmtMonthYear(d: ISODate): string {
  return `${fmtMonth(d)} ${d.slice(0, 4)}`;
}

// "Thursday"
export function fmtWeekday(d: ISODate): string {
  return WEEKDAYS_LONG[toDate(d).getUTCDay()];
}

// "Thursday, September 17"
export function fmtLongDay(d: ISODate): string {
  return `${fmtWeekday(d)}, ${fmtMonth(d)} ${dayOfMonth(d)}`;
}

// fmtRelativeDay is "Today", "Yesterday", "Tomorrow", a weekday within the
// coming week ("Saturday"), or "Sep 15".
export function fmtRelativeDay(d: ISODate, today: ISODate): string {
  const n = diffDays(today, d);
  if (n === 0) return "Today";
  if (n === -1) return "Yesterday";
  if (n === 1) return "Tomorrow";
  if (n > 1 && n < 7) return fmtWeekday(d);
  return fmtDay(d);
}

// eachDay lists every date from a to b inclusive.
export function eachDay(a: ISODate, b: ISODate): ISODate[] {
  const out: ISODate[] = [];
  for (let d = a; d <= b; d = addDays(d, 1)) out.push(d);
  return out;
}
