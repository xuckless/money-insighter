# TODO

Left out of the Almanac redesign (2026-09-17) on purpose. Each item says
what is missing and what already exists to build on.

## Recurring

- **Local recurring detection** for users without Plaid's Recurring
  Transactions add-on. Group `transactions/categorized` by `merchant_key`,
  amount band and interval; write streams into a topper table with the same
  shape as `recurring/streams` so the screens can read either source.
- **Add recurring by hand** (the design's "Add recurring" button is not
  shown), and **edit or ignore a detected stream** (wrong frequency, a
  subscription that was cancelled). Needs a `topper.stream_overrides`
  table applied in the `recurring/streams` view.
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

- **Custom categories**: add, rename, recolour, merge. The fixed set lives
  in `src/shared/categories.ts` and in the topper's `topper.is_category`
  and `topper.plaid_category`; both would move to a table.
- **Split a transaction** across categories.
- **Rollover budgets** and budgets that differ by month.

## Look and feel

- **Dark mode** and a Light / Dark / System switch. The palette is only
  defined for light in `globals.css`.
- **Narrow windows.** The layout is tuned from about 1280 px wide; below
  1024 px (the window minimum) is untested.
- **Chart accessibility.** Charts carry an `aria-label` summary; there is no
  keyboard focus on points or a table alternative.

## Notifications

- Price changes, a low projected balance, a category running ahead of
  pace and a connection needing sign-in are only shown when the app is
  open. OS notifications while it runs in the background are not built.
