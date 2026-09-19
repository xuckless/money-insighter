# desktop

The Money Insighter desktop app: an Electron shell that runs an embedded
Postgres, plaidsync and the topper on loopback ports and renders the UI.

## Layout

```
electron.vite.config.ts   three Vite builds: main, preload, renderer
electron-builder.yml      packaging (AppImage/deb, NSIS, dmg); asar is off on purpose
scripts/build-go.mjs      cross-compiles ../plaid-backend-golang and ../postgres-topper into resources/bin/<os>-<arch>/
scripts/fetch-postgres.mjs installs @embedded-postgres/<platform> for a platform npm skipped on this host
scripts/lib/app.mjs       Playwright plumbing shared by smoke.mjs and capture-docs.mjs
scripts/capture-docs.mjs  screenshots for the documentation, from the demo dataset
build/icon.png            app icon
src/shared/api.ts         the renderer <-> main contract (window.api); the only thing both sides import
src/main/
  index.ts                app lifecycle, the window, single-instance lock, ordered shutdown
  supervisor.ts           starts Postgres -> plaidsync -> topper, publishes AppState, holds ports and tokens
  postgres.ts             embedded-postgres wrapper: initdb on first run, creates the database and schema topper
  services.ts             spawns a Go binary with a minimal environment, waits for /readyz, stops it
  settings.ts             config.json (plain) and secrets.bin (safeStorage); validation; secret generation
  ipc.ts                  ipcMain handlers; validates every argument; proxies HTTP to the services
  http.ts                 the proxy: bearer token, JSON in and out, 503 on transport failure
  logs.ts                 per-service ring buffer and logs/<service>.log
  paths.ts                data directory layout; where the Go binaries are in dev and packaged
  ports.ts                free loopback port
src/preload/index.ts      contextBridge exposing window.api
src/shared/categories.ts  the fixed spending categories (ids, labels, colours); mirrors the topper's SQL mapping
src/renderer/src/
  pages/                  Overview, Spending, Cash flow, Recurring, Accounts, Transactions, Profile, Settings
  components/             app shell, panels and page intro, hand-drawn SVG charts, dialogs, shadcn ui/
  lib/insights/           pure calculations behind the screens (pace, projections, safe to spend, notes); unit tested
  lib/topper.ts, lib/plaidsync.ts  the two service clients over window.api
  assets/fonts/           Instrument Sans and Newsreader (OFL), bundled because the CSP only loads local fonts
vitest.config.ts          unit tests for src/renderer/src/lib
TODO.md                   what the redesign left for later
```

## Process model

The main process is the only privileged part. It:

1. reads `config.json` and decrypts `secrets.bin` (Plaid secret, the two
   service tokens, the access-token encryption key, the database password);
2. starts Postgres in `<userData>/postgres` (initdb on the first run),
   creates database `plaidsync` and schema `topper`;
3. spawns plaidsync with its `PLAIDSYNC_*` environment, waits for
   `/readyz` (it migrates the database on start);
4. spawns the topper the same way (`TOPPER_STARTUP_WAIT` lets it wait for
   plaidsync's tables);
5. pushes `AppState` to the renderer on every change and answers IPC.

The renderer is sandboxed (`sandbox: true`, context isolation, no Node)
and can only call what `src/shared/api.ts` declares. Requests to the
services go through `plaidsync:request`, `topper:get`, `topper:post` and
`topper:delete`, which attach the tokens; the renderer never sees a token,
a port or a database URL. The topper token has readwrite scope so the app
can save its own data (categories, budgets, category overrides, merchant
rules, preferences, hand-added recurring entries, hidden streams), and the
IPC layer only lets writes reach those tables (`writeTables` in
`src/shared/api.ts`); an unfiltered delete is refused.
`openExternal` accepts http(s) only. Closing the window quits the app and
stops the services in reverse order; Postgres is shut down cleanly.

Bank linking is Plaid Hosted Link: the renderer asks plaidsync for a
session, the main process opens the URL in the system browser, and the
renderer polls plaidsync's status route until the user is done.

## Development

```sh
npm install
npm run dev              # builds the Go binaries for this host, then electron-vite dev with HMR
npm run typecheck        # main/preload and renderer
npm run lint
npm test                 # vitest: the renderer's pure modules
```

The screens share one flat system: a paper palette in
`src/renderer/src/globals.css` (shadcn's tokens are mapped onto it, light
only for now), Instrument Sans for everything and Newsreader only for large
figures (the `figure` utility), panels with a 4px corner and one padding
(`components/panel.tsx`), and a 12-column grid (`Grid` in
`components/page-header.tsx`) so panels line up. Every page opens with a
compact `PageHeader` and a strip of `Stat` tiles. Categories draw as an icon
in a tinted circle (`components/category-icon.tsx`, a curated lucide set).
Charts are plain SVG in `components/charts/chart.tsx`, stretched to their
box with non-scaling strokes and HTML labels on top.

Categories live in `topper.categories` (seeded with the built-in set that
`src/shared/categories.ts` mirrors) and are loaded once by the shell into
`hooks/use-categories.ts`; the Categories page adds, edits and removes them,
and a removed category is merged into another. Each has a kind: `spending`
is paced day by day and budgeted, `bill` is budgeted but left out of the
daily pace, `income` and `transfer` are neither.

Where the numbers come from:

- Spending, budgets and "vs usual" read the topper's `categories/daily`,
  `categories/monthly` and `merchants/monthly` views, which apply the user's
  category overrides and merchant rules in SQL.
- Net worth history reads `balances/daily`, plaidsync's daily balance
  snapshots. History starts the day the app first synced with this
  version; nothing is backfilled.
- The cash balance line on Cash flow is worked back from today's chequing
  and savings balances through posted transactions (exact for bank
  accounts); the projection applies the recurring streams below.
- Recurring, Coming up, Safe to spend and price-change notes read one
  merged list (`loadStreams` in `lib/queries.ts`) from three sources:
  streams detected in the transaction history by `lib/insights/detect.ts`
  (steady interval, steady amount, recent), entries the user added by hand
  (`recurring/entries`), and Plaid's `recurring/streams` when the Recurring
  add-on is on (Settings → Add-ons, `recurringEnabled` in config.json; off
  by default, in Production Plaid bills for it). A stream the user marks as
  not recurring goes in `recurring_hidden`.

Plaid keys and mode live on the Profile page. Secrets are kept per mode in
`secrets.bin`, so switching between Sandbox and Production asks for a
secret only the first time. Connections belong to the mode they were made
in.

Only one instance runs at a time (the Postgres data directory cannot be
shared). Logs are in `<userData>/logs/` and on the Logs page. To start
over, quit the app and delete the user-data directory, or point
`MONEY_INSIGHTER_DATA_DIR` somewhere else.

`npm run smoke` (after `npm run build`, with `PLAID_CLIENT_ID` and
`PLAID_SECRET` for Sandbox in the environment) drives the built app with
Playwright through first-run setup, the stack starting, the key-backup
prompt, a Sandbox item syncing, every screen, writes to the app's own
tables (and a refused one), turning on the Recurring add-on and waiting
for its streams, the Profile page refusing a mode switch without a secret,
a Hosted Link session and a clean quit, in a scratch data directory.
`SMOKE_EXECUTABLE=dist/linux-unpacked/money-insighter` runs it against the
packaged app; `SMOKE_SCREENSHOTS=<dir>` saves a full-page PNG of each
screen. It shares its Playwright plumbing with the capture script below
(`scripts/lib/app.mjs`), which is where the keyring switch and the startup
waits live.

`npm run capture:docs` takes the screenshots the documentation is built
from: the same machinery, but aimed at producing images rather than
assertions. It links the demo dataset in `docs/demo/` as a Plaid custom
Sandbox user, backfills the months Plaid will not serve, writes
`docs/assets/shots/` and the chart data inlined into `docs/index.html`, and
refuses to photograph a page that still shows a real key, secret or path.
See `docs/demo/README.md`.

On Linux, secrets are encrypted with the session keyring (GNOME Keyring or
KWallet over the Secret Service API). Without one, Electron's plain-text
backend is used and the Settings page says so; the file is then only
obfuscated. `MONEY_INSIGHTER_PASSWORD_STORE` forces a backend for the rare
case where Electron cannot work it out or something else has chosen wrongly
— Playwright starts Electron with `--password-store=basic` from inside the
main process, which no command line flag can outrank, so the documentation
capture sets it. Unset, Electron decides, which is right on every ordinary
desktop. The quit-time warning `done is not a function` printed by
`embedded-postgres`'s exit hook is harmless: the cluster has already been
stopped by the supervisor.

## Packaging

```sh
npm run dist:linux       # AppImage + deb
npm run dist:win         # NSIS installer (cross-built on Linux)
npm run dist:mac         # DMG (macOS only; signs if a certificate is present)
```

The `.deb` target runs electron-builder's bundled `fpm`, whose Ruby needs
`libcrypt.so.1`; on Fedora that is `sudo dnf install libxcrypt-compat`.
The AppImage has no such dependency.

`extraResources` ships `resources/bin/<os>-<arch>/` as `resources/bin/`.
The Postgres binaries come from the `@embedded-postgres/<platform>` package
in `node_modules`; per-target `files` rules keep the other platforms out of
each installer. `embedded-postgres` is pinned to an exact version because
`scripts/fetch-postgres.mjs` must fetch the platform package at the same
version.

### Signing

macOS builds are signed with a Developer ID Application certificate and
notarized by Apple, so they open without a Gatekeeper warning. The Release
workflow does this on a `macos-latest` runner; it needs five repository
secrets:

| Secret | Value |
| --- | --- |
| `CSC_LINK` | base64 of a Developer ID Application `.p12` |
| `CSC_KEY_PASSWORD` | its export password |
| `APPLE_API_KEY_P8` | base64 of an App Store Connect `.p8` key |
| `APPLE_API_KEY_ID` | the key's ID |
| `APPLE_API_ISSUER` | the issuer ID |

The `.p12` must carry the *Developer ID Certification Authority* intermediate
alongside the leaf — `openssl pkcs12 -export -certfile DeveloperIDG2CA.pem`.
electron-builder's bundled trust store has only the WWDR intermediate, so
without it the identity does not resolve and signing fails with `cannot find
valid "Developer ID Application" identity`.

`mac.entitlements` and `mac.entitlementsInherit` both point at
`build/entitlements.mac.plist`. Setting only the first is a silent trap: the
second is what covers the nested binaries, and unset it falls back to
electron-builder's own template.

`npm run dist:mac` works on a Mac with the certificate in the login keychain.
Without one it produces an unsigned `.app` and skips notarization with a
warning, which is enough to check packaging but not to ship.

Windows installers are not signed; SmartScreen warns until the installer
accrues reputation.

`scripts/check-pack.mjs` runs as an `afterPack` hook on macOS and fails the
build if the Postgres binaries for the target arch are missing, their dylib
symlinks were not hydrated, or a Go service lost its executable bit — each
of which yields a bundle that signs and notarizes cleanly and then dies on
first launch.
