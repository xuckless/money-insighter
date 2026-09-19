// Builds the demo dataset the documentation screenshots are taken from.
//
//   node docs/demo/generate.mjs
//
// A fictional person in Toronto with four accounts and a year of banking.
// The year is split in two because Plaid's Sandbox will only serve the last
// 30 days of a custom user, whatever days_requested asks for:
//
//   custom-user.json  the last 30 days, sent to Plaid as the custom user, so
//                     those transactions are linked, exchanged, synced and
//                     categorised by Plaid exactly as a real bank feed is.
//   backfill.json     everything older, inserted straight into the database
//                     by scripts/capture-docs.mjs in the shape plaidsync
//                     writes. Each row carries the personal_finance_category
//                     Plaid itself assigned to that merchant; merchants.json
//                     records those verdicts, and how they were obtained.
//
// Amounts follow Plaid's sign convention: positive is money out.
//
// Output is deterministic: the only input that varies is today's date, so
// re-running on the same day is a no-op and re-running later slides the
// whole year forward.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const merchants = JSON.parse(readFileSync(join(here, "merchants.json"), "utf8"));

// Plaid serves a custom user's transactions only if date_posted is within
// this many days of today. Measured, not documented: day 30 arrives, day 31
// does not.
const PLAID_WINDOW_DAYS = 30;
const MONTHS = 12;

// ---------------------------------------------------------------- utilities

// mulberry32: a small seeded PRNG, so the dataset is the same every run.
function rng(seed) {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = rng(0x4d4f4e45); // "MONE"
const pick = (list) => list[Math.floor(rand() * list.length)];
const between = (lo, hi) => Math.round((lo + rand() * (hi - lo)) * 100) / 100;

const today = new Date();
today.setUTCHours(0, 0, 0, 0);
const iso = (d) => d.toISOString().slice(0, 10);
const addDays = (d, n) => { const c = new Date(d); c.setUTCDate(c.getUTCDate() + n); return c; };
// monthsAgo(0) is the current month; day is clamped to the month's length.
const dayOfMonth = (back, day) => {
  const d = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth() - back, 1));
  const last = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 0)).getUTCDate();
  d.setUTCDate(Math.min(day, last));
  return d;
};
const daysBack = (d) => Math.round((today - d) / 864e5);

function merchant(description) {
  const m = merchants[description];
  if (!m) throw new Error(`merchant not in merchants.json: ${description}`);
  return m;
}
const inCategory = (category) =>
  Object.keys(merchants).filter((k) => merchants[k].category === category);

// ---------------------------------------------------------------- accounts

const accounts = [
  {
    key: "chequing", type: "depository", subtype: "checking", mask: "4417",
    name: "Everyday Chequing", official_name: "Tartan-Dominion Everyday Chequing",
    starting_balance: 4218.44, available_balance: 4018.44,
  },
  {
    key: "savings", type: "depository", subtype: "savings", mask: "8802",
    name: "Rainy Day Savings", official_name: "Tartan-Dominion High Interest Savings",
    starting_balance: 11850.0,
  },
  {
    key: "visa", type: "credit", subtype: "credit card", mask: "1193",
    name: "Aurora Cashback Visa", official_name: "Aurora Cashback Visa Card",
    // Plaid derives a card's available balance as limit - balance in
    // floating point, so a balance that is not exact in binary comes back
    // as 6657.8099999999995 and plaidsync rightly refuses it. Quarters are
    // exact; keep card balances on them.
    starting_balance: 1342.25, limit: 8000,
  },
  {
    key: "mastercard", type: "credit", subtype: "credit card", mask: "6027",
    name: "Summit Travel Mastercard", official_name: "Summit Travel Rewards Mastercard",
    starting_balance: 486.5, limit: 5000,
  },
];

// ------------------------------------------------------------- transactions

const rows = []; // { account, date (Date), amount, description }
const add = (account, date, amount, description) => {
  if (date > today) return; // Plaid drops future dates; so do we
  rows.push({ account, date, amount: Math.round(amount * 100) / 100, description });
};

for (let back = MONTHS - 1; back >= 0; back--) {
  // --- income and fixed costs, all on the chequing account
  // Chosen so the demo person saves a few hundred a month: the balance
  // history is wound backwards from today's balances, so income below
  // outgoings would draw a net worth that falls all year.
  add("chequing", dayOfMonth(back, 15), -4300.0, "PAYROLL DEPOSIT NORTHWIND STUDIO");
  add("chequing", dayOfMonth(back, 1), 1950.0, "RENT PAYMENT 88 HARBORD ST");
  add("chequing", dayOfMonth(back, 8), between(74, 112), "TORONTO HYDRO ELECTRIC");
  add("chequing", dayOfMonth(back, 8), 72.6, "ROGERS WIRELESS");
  add("chequing", dayOfMonth(back, 12), 89.95, "BELL CANADA INTERNET");
  add("chequing", dayOfMonth(back, 20), 312.4, "STUDENT LOAN PAYMENT NSLSC");
  add("chequing", dayOfMonth(back, 16), 400.0, "TRANSFER TO SAVINGS");
  add("savings", dayOfMonth(back, 16), -400.0, "TRANSFER TO SAVINGS");
  if (back % 3 === 1) add("chequing", dayOfMonth(back, 5), 168.0, "INTACT INSURANCE PREMIUM");
  // One tax refund, in the spring.
  if (dayOfMonth(back, 1).getUTCMonth() === 4) {
    add("chequing", dayOfMonth(back, 22), -1284.55, "CANADA REVENUE AGENCY TAX REFUND");
  }

  // --- subscriptions on the Visa. Netflix steps up three months ago, which
  // is what gives the Recurring page a price change to point at.
  add("visa", dayOfMonth(back, 22), back <= 2 ? 18.99 : 16.49, "NETFLIX.COM");
  add("visa", dayOfMonth(back, 4), 11.99, "SPOTIFY P1A2B3C4");
  add("visa", dayOfMonth(back, 27), 9.99, "DISNEY PLUS SUBSCRIPTION");
  add("visa", dayOfMonth(back, 3), 54.29, "GOODLIFE FITNESS CLUB");

  // --- everyday spending on the Visa
  for (let w = 0; w < 4; w++) {
    add("visa", dayOfMonth(back, 2 + w * 7), between(58, 148), pick(inCategory("groceries")));
  }
  for (let i = 0; i < 7; i++) {
    add("visa", dayOfMonth(back, 2 + Math.floor(rand() * 26)), between(7, 46), pick(inCategory("dining")));
  }
  for (let i = 0; i < 4; i++) {
    add("visa", dayOfMonth(back, 2 + Math.floor(rand() * 26)), between(12, 88), pick(inCategory("transport")));
  }
  for (let i = 0; i < 3; i++) {
    add("visa", dayOfMonth(back, 2 + Math.floor(rand() * 26)), between(18, 180), pick(inCategory("shopping")));
  }
  add("visa", dayOfMonth(back, 9), between(14, 62), pick(inCategory("personal_care")));
  if (rand() < 0.6) add("visa", dayOfMonth(back, 18), between(16, 95), pick(inCategory("medical")));
  if (rand() < 0.5) add("visa", dayOfMonth(back, 24), between(22, 74), pick(inCategory("kids_pets")));

  // --- the Mastercard is the discretionary card
  if (rand() < 0.7) add("mastercard", dayOfMonth(back, 11), between(24, 96), pick(inCategory("entertainment")));
  if (back === 2) {
    // A summer trip, so Spending has a visible spike and Travel is not empty.
    add("mastercard", dayOfMonth(back, 6), 862.4, "AIR CANADA 0142668");
    add("mastercard", dayOfMonth(back, 7), 1124.0, "FAIRMONT ROYAL YORK");
    add("mastercard", dayOfMonth(back, 9), 214.75, "EXPEDIA.CA BOOKING");
  }
  if (back === 9) add("mastercard", dayOfMonth(back, 14), 449.0, "PORTER AIRLINES");
  if (back === 7) add("visa", dayOfMonth(back, 6), 120.0, "UNICEF CANADA DONATION");
  if (back === 8) add("visa", dayOfMonth(back, 13), 89.99, "UDEMY ONLINE COURSE");

  // A few charges Plaid cannot place, so the Spending page's "Sort the
  // unknowns" panel has something real to sort. Only in the current month,
  // which is inside Plaid's window, so they are categorised by Plaid (as
  // OTHER) rather than by the backfill.
  if (back === 0) {
    add("visa", dayOfMonth(back, 6), 38.5, "SQ *THE CORNER SHOP");
    add("visa", dayOfMonth(back, 12), 64.0, "POS PURCHASE 88213 TOR");
    add("chequing", dayOfMonth(back, 14), 55.0, "E-TRANSFER SENT 4471");
  }

  // --- card payments, from the chequing account
  add("chequing", dayOfMonth(back, 25), between(820, 1460), "CREDIT CARD PAYMENT VISA");
  add("visa", dayOfMonth(back, 25), -between(820, 1460), "CREDIT CARD PAYMENT VISA");
}

rows.sort((a, b) => a.date - b.date || a.account.localeCompare(b.account));

// --------------------------------------------------- split at Plaid's window

const cutoff = addDays(today, -PLAID_WINDOW_DAYS);
const recent = rows.filter((r) => r.date >= cutoff);
const older = rows.filter((r) => r.date < cutoff);

// Plaid caps a custom user at roughly 250 transactions and 55 kB.
if (recent.length > 240) throw new Error(`${recent.length} transactions in the Plaid window; the cap is ~250`);

const customUser = {
  seed: "money-insighter-demo",
  override_accounts: accounts.map((a) => ({
    type: a.type,
    subtype: a.subtype,
    starting_balance: a.starting_balance,
    ...(a.available_balance === undefined ? {} : { available_balance: a.available_balance }),
    currency: "CAD",
    meta: {
      name: a.name,
      official_name: a.official_name,
      number: a.mask,
      ...(a.limit === undefined ? {} : { limit: a.limit }),
    },
    transactions: recent
      .filter((r) => r.account === a.key)
      .map((r) => ({
        // date_transacted is the day it happened, date_posted the day it
        // cleared; the app shows the posted date.
        date_transacted: iso(addDays(r.date, -1)),
        date_posted: iso(r.date),
        amount: r.amount,
        description: r.description,
        currency: "CAD",
      })),
  })),
};

const encoded = JSON.stringify(customUser);
if (encoded.length > 55000) throw new Error(`custom user is ${encoded.length} bytes; the cap is ~55 kB`);

const backfill = {
  generated_on: iso(today),
  note:
    "Inserted directly into the database by scripts/capture-docs.mjs. Plaid's " +
    "Sandbox serves only the last 30 days of a custom user, so the earlier " +
    "months of the demo are written in the shape plaidsync writes, carrying " +
    "the personal_finance_category Plaid itself assigned to each merchant " +
    "(see merchants.json).",
  accounts: accounts.map(({ key, mask, name }) => ({ key, mask, name })),
  transactions: older.map((r, i) => {
    const m = merchant(r.description);
    return {
      id: `demo-${String(i).padStart(4, "0")}`,
      account: r.account,
      date: iso(r.date),
      authorized_date: iso(addDays(r.date, -1)),
      amount: r.amount,
      name: m.name,
      merchant_name: m.merchant_name,
      pfc_primary: m.pfc_primary,
      pfc_detailed: m.pfc_detailed,
      category: m.category,
    };
  }),
};

writeFileSync(join(here, "custom-user.json"), JSON.stringify(customUser, null, 2) + "\n");
writeFileSync(join(here, "backfill.json"), JSON.stringify(backfill, null, 2) + "\n");

const months = new Set(rows.map((r) => iso(r.date).slice(0, 7)));
console.log(`generated ${rows.length} transactions across ${months.size} months`);
console.log(`  custom-user.json  ${recent.length} in Plaid's ${PLAID_WINDOW_DAYS}-day window (${(encoded.length / 1024).toFixed(1)} kB)`);
console.log(`  backfill.json     ${older.length} older, inserted locally`);
console.log(`  oldest ${iso(rows[0].date)}  newest ${iso(rows[rows.length - 1].date)}`);
