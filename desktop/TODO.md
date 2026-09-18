# TODO

Left out of the Almanac redesign (2026-09-17) on purpose. Each item says
what is missing and what already exists to build on.

## Recurring

- **Edit a detected stream** (wrong frequency or amount) without re-adding
  it by hand. Detection (`lib/insights/detect.ts`) and hand-added entries
  landed with the 2026-09-18 overhaul; a detected stream can be hidden or
  turned into a manual entry, not corrected in place.
- **Semi-monthly detection.** Payroll on the 15th and last day reads as
  every two weeks.
- **Card payment amounts from Plaid Liabilities** (statement balance, due
  date) instead of the card-payment stream's last amount.

## Balances and accounts

- **Net worth history before the app started recording.** Snapshots begin
  the first day this version syncs. Bank accounts could be worked back
  through transactions (Cash flow already does this for 60 days);
  investment accounts cannot, their value moves without transactions.
- **Investment holdings and performance** (Plaid Investments). Only
  balances are shown.
- **Multi-currency.** Accounts in currencies other than the primary one are
  listed but left out of totals and charts; there is no conversion.

## Categories and budgets

- **Category groups** (a Bills group holding Internet, Insurance…). The
  list is flat; `kind` is the only grouping.
- **Reorder categories** by hand; `sort_order` exists but has no UI.
- **Split a transaction** across categories.
- **Rollover budgets** and budgets that differ by month.

## Look and feel

- **Dark mode** and a Light / Dark / System switch. The palette is only
  defined for light in `globals.css`.
- **Merchant logos** on rows (Plaid's `logo_url` is already in the
  categorized view) in place of the category glyph.
- **Narrow windows.** The layout is tuned from about 1280 px wide; below
  1024 px (the window minimum) is untested.
- **Chart accessibility.** Charts carry an `aria-label` summary; there is no
  keyboard focus on points or a table alternative.

## Notifications

- Price changes, a low projected balance, a category running ahead of
  pace and a connection needing sign-in are only shown when the app is
  open. OS notifications while it runs in the background are not built.
