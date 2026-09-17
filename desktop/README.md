# desktop

The Money Insighter desktop app: an Electron shell that runs an embedded
Postgres, plaidsync and the topper on loopback ports and renders the UI.

## Layout

```
electron.vite.config.ts   three Vite builds: main, preload, renderer
electron-builder.yml      packaging (AppImage/deb, NSIS, dmg); asar is off on purpose
scripts/build-go.mjs      cross-compiles ../plaid-backend-golang and ../postgres-topper into resources/bin/<os>-<arch>/
scripts/fetch-postgres.mjs installs @embedded-postgres/<platform> for a platform npm skipped on this host
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
src/renderer/             React app (Vite): pages, components, the two service clients over window.api
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
services go through `plaidsync:request` and `topper:get`, which attach the
tokens; the renderer never sees a token, a port or a database URL.
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
```

Only one instance runs at a time (the Postgres data directory cannot be
shared). Logs are in `<userData>/logs/` and on the Logs page. To start
over, quit the app and delete the user-data directory, or point
`MONEY_INSIGHTER_DATA_DIR` somewhere else.

`npm run smoke` (after `npm run build`, with `PLAID_CLIENT_ID` and
`PLAID_SECRET` for Sandbox in the environment) drives the built app with
Playwright through first-run setup, the stack starting, the key-backup
prompt, a Sandbox item syncing, every page, a Hosted Link session and a
clean quit, in a scratch data directory. `SMOKE_EXECUTABLE=dist/linux-unpacked/money-insighter`
runs it against the packaged app.

On Linux, secrets are encrypted with the session keyring (GNOME Keyring or
KWallet over the Secret Service API). Without one, Electron's plain-text
backend is used and the Settings page says so; the file is then only
obfuscated. The quit-time warning `done is not a function` printed by
`embedded-postgres`'s exit hook is harmless: the cluster has already been
stopped by the supervisor.

## Packaging

```sh
npm run dist:linux       # AppImage + deb
npm run dist:win         # NSIS installer (cross-built on Linux)
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

Signing is not configured. For macOS, add `mac.identity`, `hardenedRuntime`
and a notarization step in `electron-builder.yml` when a Developer ID
certificate is available; until then Gatekeeper shows an "unidentified
developer" warning.
