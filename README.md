# money-insighter

A personal-finance desktop app for the Canadian market, built on Plaid.
You install it, paste your Plaid keys, connect your banks, and every
account and transaction is synced into a Postgres database that lives on
your machine. Nothing runs anywhere else.

The app is an Electron shell around two Go services and an embedded
Postgres:

- **`plaid-backend-golang/`** (`plaidsync`) owns the Plaid relationship:
  Hosted Link sessions, token exchange, the sync engine and a job queue. It
  writes normalised accounts and transactions into Postgres and is the only
  thing that ever talks to Plaid.
- **`postgres-topper/`** (`topper`) is the REST access layer over that
  database: read-only access to plaidsync's tables and views, read-write
  consumer tables in schema `topper`.
- **`desktop/`** is the app: it starts Postgres, plaidsync and the topper on
  loopback ports, keeps the secrets in the OS keychain, and renders the UI
  (React). The UI talks to the two services through the main process; the
  renderer never holds a token.

The services never call each other. The database is the integration point.
Each Go directory is its own module with its own README, `.env.example`,
Makefile and tests; `go.work` at the root lets editors see both.

## Layout

```
desktop/               Electron app: main process (supervisor, settings, IPC), preload, renderer
plaid-backend-golang/  plaidsync
postgres-topper/       topper
go.work                both Go modules
```

## How it connects to Plaid

- **Keys.** The first-run screen asks for the Plaid environment (Sandbox or
  Production), the client ID and the matching secret, from
  dashboard.plaid.com under Developers → Keys. Everything else (the API
  tokens between the app and its services, the key that encrypts bank
  access tokens, the database password) is generated on the spot and
  stored with Electron's `safeStorage`, which uses the OS keyring
  (libsecret or KWallet on Linux, DPAPI on Windows, Keychain on macOS).
- **Linking.** Connecting a bank uses Plaid **Hosted Link**: the app opens
  Plaid's own page in your browser and polls until you are done. Because
  Link never runs inside the app, no redirect URI, allowed origin or
  webhook URL has to be registered in the dashboard, and OAuth institutions
  (CIBC among them) work in Production, where Plaid requires HTTPS redirect
  URIs that a desktop app cannot serve.
- **Syncing.** There are no webhooks (a laptop has no public URL). plaidsync
  syncs every connection on a schedule (1 hour by default, adjustable in
  Settings) and whenever you press Sync now.
- **Back up the encryption key.** After the first start the app shows the
  key that encrypts bank access tokens and asks you to save it. The key is
  stored in the keyring; if that is ever lost, the key is the only way to
  read the connections you already made. Without it you reconnect every
  bank.

Plaid's Trial plan allows ten Production items; Sandbox items never count.
`Remove` on a connection calls `/item/remove`, which does not free a
Trial slot.

## Running from source

Prerequisites: Go 1.26, Node 22, and for the Go test suites Docker with
Compose (the development Postgres on `127.0.0.1:5433`).

```sh
cd desktop
npm install
npm run dev          # cross-compiles plaidsync and topper for this machine, then starts Electron
```

Data lives in Electron's user-data directory (`~/.config/money-insighter`
on Linux, `%APPDATA%\money-insighter` on Windows): `config.json`,
`secrets.bin`, `postgres/` and `logs/`. Settings → Open folder shows it.

Checks:

```sh
cd plaid-backend-golang && make check && make test-db && make test-sandbox   # the last needs Sandbox keys in .env
cd postgres-topper     && make check && make test-db
cd desktop             && npm run typecheck && npm run lint
```

## Building installers

```sh
cd desktop
npm run dist:linux   # AppImage and .deb in desktop/dist/
npm run dist:win     # NSIS installer; cross-builds on Linux (Wine is not needed)
npm run dist         # both
```

Each installer bundles the Go binaries for its platform
(`scripts/build-go.mjs`) and the Postgres binaries for its platform (the
`@embedded-postgres/<platform>` npm package; `scripts/fetch-postgres.mjs`
pulls one npm would skip on this host). Builds are unsigned: Windows
SmartScreen and macOS Gatekeeper will warn until a signing certificate is
configured in `electron-builder.yml`.

## Development notes

- The Go services are unchanged by the desktop app: they still read their
  configuration from the environment, which the app's supervisor builds
  from `config.json` and the decrypted secrets. Run them by hand with
  `make run` in each directory when working on them alone.
- plaidsync's `PLAIDSYNC_REDIRECT_URI` and `PLAIDSYNC_WEBHOOK_URL` remain
  for a deployment that runs Plaid Link itself or has a public URL; the app
  sets neither.
- The renderer reaches the services only through `window.api` (see
  `desktop/src/shared/api.ts`); the main process validates every request
  and holds the tokens.
