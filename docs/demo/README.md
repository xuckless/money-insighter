# The demo dataset

Every screenshot in the documentation shows a fictional person in Toronto
with four accounts and a year of banking. No real account data is used, and
none of these files came near a Production Plaid key.

This page says exactly which parts are real, because a project that makes
claims about where your data goes should not be vague about its own.

## What is real

The last 30 days of the demo go through Plaid Sandbox properly. The capture
script posts the dataset in `custom-user.json` to Plaid as a **custom test
user**, then the app links it the same way it links a real bank: a real
`/sandbox/public_token/create`, a real token exchange, a real
`/transactions/sync`, and Plaid's own enrichment naming the merchants and
assigning every `personal_finance_category`.

That last part matters. The app's categories come from Plaid's categories,
so the merchant descriptions were chosen by asking Plaid what it made of
each one. `merchants.json` records the answer for all 46 — the merchant name
Plaid returns, the category it assigns, and the app category that produces.
Nothing in the demo is categorised by hand.

## What is generated

**Transactions older than 30 days.** Plaid's Sandbox serves only the last 30
days of a custom user, whatever `days_requested` asks for — measured, not
documented: a transaction posted 30 days ago arrives, one posted 31 days ago
does not. A year of screenshots therefore cannot come from Plaid alone. The
earlier months are in `backfill.json` and are inserted straight into the
`transactions` table in the shape plaidsync writes, each row carrying the
`personal_finance_category` Plaid gave that merchant in the window above.

**Daily balance history.** plaidsync records one balance snapshot per account
per day, starting the day the account is linked, and backfills nothing — so a
freshly captured demo has exactly one day of history and every balance chart
would be a single point. The capture script reconstructs the earlier days by
winding today's balance back through the posted ledger. For these accounts
that is exact arithmetic, not an estimate; only the elapsed time is
simulated.

**Budgets.** `budgets.json` is what the demo person has set. A real user sets
these on the Spending page.

## What is never photographed

The capture script reads the whole rendered page before every screenshot and
refuses to take it if the real Plaid client ID, the real secret, or the
scratch data directory is still visible. On top of that it replaces, in the
DOM, just before the shutter:

| Thing | Shown instead |
|---|---|
| Plaid client ID | `65f2a1c0d4e8b7390a1f6c22` |
| Plaid secret | never revealed; the field shows its saved state |
| The encryption key | base64 that decodes to `sample-demo-key-not-real-do-not-use-ever` |
| The data directory | `/home/you/.config/money-insighter` |
| Log lines naming either | the same substitutions, applied to the log text |

The run happens in a throwaway data directory, which is deleted afterwards.

## Regenerating

```sh
node docs/demo/generate.mjs        # rewrites custom-user.json and backfill.json

cd desktop
npm run build && npm run build:go  # capture drives the built app
set -a && . ../plaid-backend-golang/.env && set +a   # Sandbox keys
npm run capture:docs
```

`generate.mjs` is deterministic apart from today's date: the dataset is
positioned relative to the day it runs, so re-running on the same day changes
nothing and re-running later slides the whole year forward. It refuses to
write a dataset that would exceed Plaid's limits (roughly 250 transactions
and 55 kB per custom user, at most 10 accounts).

The capture writes `docs/assets/shots/*.png`, `docs/assets/data/charts.json`,
and the inlined copy of that JSON in `docs/index.html`. All of it changes on
every run — dates move and PNGs re-compress — so re-run it deliberately, not
out of habit.

`DOCS_SCALE=2` captures at twice the pixel density if the display is large
enough to hold the window; otherwise it falls back to 1x and says so.
`DOCS_PASSWORD_STORE=kwallet6` for a KDE session.

## Files

| File | What it is |
|---|---|
| `generate.mjs` | builds the dataset; the only file to edit by hand |
| `merchants.json` | the 46 merchants and Plaid's verdict on each |
| `custom-user.json` | generated — the last 30 days, sent to Plaid |
| `backfill.json` | generated — the earlier months, inserted locally |
| `budgets.json` | the budgets the demo person has set |
