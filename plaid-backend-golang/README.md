# plaidsync

`plaidsync` is the data layer of a personal-finance tool for the Canadian
market. It owns the Plaid relationship for one deployment and writes
normalised accounts and transactions into Postgres. Everything else
(categorisation, recurring-charge detection, any UI) is a separate consumer
that reads that database and never talks to Plaid.

Target institutions are CIBC and American Express Canada, on the Plaid Trial
plan (free production access, capped at 10 Items; `/item/remove` does not
free a slot). Plaid in Canada is read-only, so the service only ever reads
data.

Deployment model: one Postgres database per client, single-tenant. The
binary is stateless; all state lives in Postgres or in environment-supplied
secrets.

Not in scope, by design: transaction categorisation, recurring-charge
detection, any frontend beyond what is needed to test Link, money movement,
and multi-tenant routing (the schema and store API keep a tenant dimension
addable without a rewrite, but do not implement one).

## Status

The build order from the spec, and where it stands:

| Step | Scope | Status |
|---|---|---|
| 1 | Config, migrations, store layer, crypto package, with tests | done |
| 2 | Plaid wrapper interface + fixture-driven fake | pending |
| 3 | Link token creation and public token exchange | pending |
| 4 | Sync engine (locking, pagination, transactional commit, upserts) | pending |
| 5 | Webhook receiver with signature verification | pending |
| 6 | Scheduler and debouncing | pending |
| 7 | Observability: structured logs, item health endpoint, graceful shutdown | pending |

What the binary does today: loads and validates its configuration, builds
the encryption keyring, opens the database, runs migrations, and serves
`GET /healthz` and `GET /readyz`. The authenticated `/v1` HTTP API (Link
tokens, items, sync triggers, jobs, the webhook receiver) does not exist yet;
it lands in steps 3 to 6. Nothing calls Plaid yet. The Plaid, Link and sync
variables are already read and validated so that a configuration written now
keeps working as the later steps arrive.

## Architecture

```
cmd/plaidsync/          entrypoint: config, logger, keyring, store, HTTP server, signals
internal/api/           HTTP handlers; today only /healthz and /readyz (auth middleware and /v1 come later)
internal/config/        environment loading and validation; Config.LogValue for a secret-free startup log
internal/crypto/        AES-256-GCM envelope encryption of access tokens under a versioned keyring
internal/secret/        Token and Bytes: values that render as [REDACTED] under fmt, JSON and slog
internal/money/         exact decimal Amount (never float64); maps to NUMERIC (unconstrained; each value keeps its own scale)
internal/civil/         calendar Date with no time zone; maps to DATE
internal/store/         the only package that talks to Postgres: pool, migrations runner, every query
migrations/             goose SQL migrations, embedded into the binary
internal/plaid/         (step 2) thin wrapper over plaid-go behind an interface, plus a fake
internal/sync/          (step 4) the sync engine
```

Dependencies beyond the standard library: `github.com/jackc/pgx/v5` and
`github.com/pressly/goose/v3`. Tests use the standard `testing` package only.

Inside `internal/store`:

- `store.go`: `Open`, `Close`, `Ping`, transaction helpers, `ErrNotFound`,
  `ErrItemLocked`, `ErrInvalidDatabaseURL`.
- `migrate.go`: `Migrate` (goose `Up`, idempotent, serialised by a session
  advisory lock) and `MigrationVersion`.
- `types.go`: every row type and enum (`Item`, `Credential`, `Account`,
  `Transaction`, `Job`, `SyncRun`, `SyncBatch`, `ApplyResult`, `ItemTx`).
- `items.go`: `plaid_items` reads and writes (`UpsertItem`, `GetItem`,
  `ListItems`, `GetCredential`, `UpdateCredential`, `SetItemStatus`,
  `MarkItemRemoved`) plus the read side of the child tables
  (`ListAccounts`, `GetTransaction`, `ListTransactions`).
- `jobs.go`, `runs.go`: `sync_jobs` and `sync_runs`.
- `accounts.go`, `transactions.go`: the account and transaction halves of
  `ApplySyncBatch` (upserts, missing-account flagging, soft deletes, pending
  links); write-only, always inside the item transaction.
- `lock.go` and `sync_batch.go`: `WithItemLock` and `ItemTx.ApplySyncBatch`,
  the transactional core the sync engine will be built on.
- `rowscan.go`: row collection into the typed structs, list limit/offset
  validation and Postgres error classification.
- `uuid.go`: job id generation (v4 from `crypto/rand`) and validation.

## Configuration

Every setting comes from the environment. `internal/config` trims values,
treats an empty value as unset, matches enumerated values
case-insensitively, and reports every problem at once as one joined error
(so one failed start lists everything to fix). Error messages name the
variable, never its value.

`.env.example` documents the same variables with comments and placeholders;
`internal/config` has a test that fails if the two ever disagree.

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `PLAIDSYNC_BIND_ADDR` | no | `127.0.0.1:8080` | `host:port` the HTTP server listens on; numeric port 1..65535. The service speaks plain HTTP, so keep it on loopback unless a TLS-terminating proxy sits in front. A non-loopback value is accepted with a startup warning. |
| `PLAIDSYNC_DATABASE_URL` | yes | | Postgres connection string (pgx URL or `key=value` DSN). The pool is sized to at least 20 connections because sync transactions stay open for a whole pagination. |
| `PLAIDSYNC_API_TOKEN` | yes | | Static bearer token every `/v1/*` request must present (from step 3). At least 32 bytes. `openssl rand -hex 32` |
| `PLAIDSYNC_KEK_VERSION` | no | `1` | Version number of `PLAIDSYNC_KEK`; new access tokens are encrypted under it. Positive integer. |
| `PLAIDSYNC_KEK` | yes | | Key encryption key for `PLAIDSYNC_KEK_VERSION`: base64 (padded or not) of exactly 32 bytes. `openssl rand -base64 32` |
| `PLAIDSYNC_KEK_PREVIOUS` | no | none | Older keys still needed to decrypt rows written before a rotation, as comma-separated `version:base64` pairs. Versions must be positive, distinct, and differ from `PLAIDSYNC_KEK_VERSION`; each key is 32 bytes. |
| `PLAID_CLIENT_ID` | yes | | Plaid client ID. |
| `PLAID_SECRET` | yes | | Plaid secret for the environment in `PLAID_ENV`. |
| `PLAID_ENV` | no | `sandbox` | `sandbox` or `production`. |
| `PLAIDSYNC_REDIRECT_URI` | no | none | OAuth redirect URI registered in the Plaid dashboard (CIBC needs the OAuth flow). Absolute `https` URL; `http` is accepted only for `localhost` / loopback hosts. |
| `PLAIDSYNC_WEBHOOK_URL` | no | none | Public URL Plaid delivers webhooks to. Absolute `http(s)` URL; `https` is required when `PLAID_ENV=production`. |
| `PLAIDSYNC_COUNTRY_CODES` | no | `CA` | Comma-separated ISO 3166-1 alpha-2 codes for Link, stored uppercase, no repeats. `CA` is required for Canadian institutions. |
| `PLAIDSYNC_PRODUCTS` | no | `transactions` | Comma-separated Plaid products Link must obtain consent for, stored lowercase, no repeats. |
| `PLAIDSYNC_REQUIRED_IF_SUPPORTED_PRODUCTS` | no | none | Comma-separated products used only when the institution supports them (for example `liabilities`). |
| `PLAIDSYNC_OPTIONAL_PRODUCTS` | no | none | Comma-separated products the user may decline during Link. |
| `PLAIDSYNC_TRANSACTIONS_DAYS_REQUESTED` | no | `730` | Days of history requested at Link time, 1..730. |
| `PLAIDSYNC_LINK_CLIENT_NAME` | no | `plaidsync` | Application name shown inside Link. |
| `PLAIDSYNC_LINK_LANGUAGE` | no | `en` | Two-letter language code for the Link UI, stored lowercase. |
| `PLAIDSYNC_SYNC_MIN_INTERVAL` | no | `15m` | Debounce for manually triggered syncs: a trigger arriving sooner than this after a successful sync is answered from the database without calling Plaid. Go duration, not negative; `0` disables the debounce. |
| `PLAIDSYNC_SYNC_INTERVAL` | no | `6h` | How often the in-process scheduler syncs every active item. Positive Go duration. |
| `PLAIDSYNC_SCHEDULER_ENABLED` | no | `true` | Whether the in-process scheduler runs. `true`/`false` (also `1`/`0`, `t`/`f`). |
| `PLAIDSYNC_SYNC_MAX_ATTEMPTS` | no | `5` | Attempts per run before a retryable Plaid error is given up on until the next scheduled run. At least 1. |
| `PLAIDSYNC_SYNC_RETRY_BASE` | no | `2s` | Initial backoff between retry attempts (exponential with jitter). Positive Go duration. |
| `PLAIDSYNC_SYNC_RETRY_MAX` | no | `2m` | Upper bound on the backoff. Positive and at least `PLAIDSYNC_SYNC_RETRY_BASE`. |
| `PLAIDSYNC_SYNC_CONCURRENCY` | no | `2` | How many items the scheduler syncs in parallel. At least 1. |
| `PLAIDSYNC_LOG_LEVEL` | no | `info` | Minimum log level: `debug`, `info`, `warn` or `error`. |
| `PLAIDSYNC_LOG_FORMAT` | no | `json` | Log encoding: `json` or `text`. Logs go to stderr. |
| `PLAIDSYNC_SHUTDOWN_TIMEOUT` | no | `30s` | How long graceful shutdown waits for in-flight requests after SIGINT or SIGTERM. Positive Go duration. |

One product may appear in only one of the three product lists. The three
product lists, the Link settings and the sync settings are consumed by
steps 3, 4 and 6; today they are validated and logged, nothing more.

Secrets (`PLAIDSYNC_DATABASE_URL`, `PLAIDSYNC_API_TOKEN`, `PLAIDSYNC_KEK`,
`PLAIDSYNC_KEK_PREVIOUS`, `PLAID_SECRET`) are held as `secret.Token` /
`secret.Bytes`, which render as `[REDACTED]` under every `fmt` verb, in JSON
and in `slog`. The startup log prints the configuration through
`Config.LogValue`, which lists key versions but never key material.

## Running locally against Sandbox

Prerequisites: Go 1.26, Docker with Compose, `openssl`, `curl`.

1. Start Postgres. `docker-compose.yml` runs `postgres:16-alpine` on
   `127.0.0.1:5433` (not 5432, so it does not collide with a local server)
   with user, password and database all `plaidsync`, and waits for it to be
   healthy:

   ```sh
   make db-up
   ```

2. Create `.env` from the example and fill in the placeholders:

   ```sh
   cp .env.example .env
   openssl rand -hex 32     # -> PLAIDSYNC_API_TOKEN
   openssl rand -base64 32  # -> PLAIDSYNC_KEK
   ```

   Set `PLAID_CLIENT_ID` and `PLAID_SECRET` to the Sandbox keys from
   dashboard.plaid.com (Team Settings, Keys) and leave `PLAID_ENV=sandbox`.
   `PLAIDSYNC_DATABASE_URL` in the example already points at the Compose
   database. `.env` is git-ignored; never commit it. The loader rejects any
   required variable still set to its `<placeholder>`, so an unedited copy
   or a single forgotten line fails at startup, not at the first Plaid call.

3. Build and run. `make run` sources `.env` and starts `bin/plaidsync`,
   which applies any pending migrations before it listens:

   ```sh
   make run
   ```

4. Check the probes:

   ```sh
   curl -i http://127.0.0.1:8080/healthz   # 200 {"status":"ok"}
   curl -i http://127.0.0.1:8080/readyz    # 200 {"status":"ok"}, or 503 {"status":"unavailable"} when Postgres is unreachable
   ```

   Anything other than `GET`/`HEAD` on those paths is a 405; every other
   path is a 404 until the `/v1` routes land.

5. Stop it with Ctrl-C (SIGINT) or `kill <pid>` (SIGTERM). The server stops
   accepting, drains in-flight requests for up to
   `PLAIDSYNC_SHUTDOWN_TIMEOUT`, closes the pool, and exits 0. A second
   signal while draining terminates the process immediately.

The service exits 1 with a message on stderr if configuration is invalid
(every problem listed), the keyring cannot be built, Postgres is unreachable,
a migration fails, or the bind address cannot be opened.

Sandbox tips for the later steps: `/sandbox/item/reset_login` forces
`ITEM_LOGIN_REQUIRED` to exercise the update-mode path, and
`/sandbox/item/fire_webhook` exercises the webhook handler. Do not spend a
real Item slot until the flow works end to end in Sandbox.

## Tests

```sh
make test      # unit tests; database-backed tests skip
make test-db   # starts the Compose database if needed, then runs everything
go test -race -count=1 ./...   # what CI should run, with PLAIDSYNC_TEST_DATABASE_URL set
```

Database-backed tests (all of `internal/store`, plus the pgx round-trip
tests in `internal/money` and `internal/civil`) read
`PLAIDSYNC_TEST_DATABASE_URL` and call `t.Skip` when it is unset.
`make test-db` sets it to the Compose URL,
`postgres://plaidsync:plaidsync@127.0.0.1:5433/plaidsync?sslmode=disable`.
The user in that URL needs `CREATEDB`: every store test creates its own
`plaidsync_test_<random>` database, migrates it, and drops it when done, so
tests are isolated and can run in parallel.

The store tests cover the cases the spec calls out explicitly: a failure
injected between writing rows and saving the cursor leaves nothing behind;
concurrent syncs on one item, where the second gets `ErrItemLocked` at once;
`modified` arriving for a never-seen transaction; `removed` arriving twice;
the pending-to-posted transition in both arrival orders; accounts vanishing
and reappearing; and exact round-tripping of amounts and dates.

## Schema

`migrations/00001_init.sql` creates five tables. The migration files are
embedded into the binary and applied by goose at startup; never edit one
that has shipped, add a new one.

| Table | Holds |
|---|---|
| `plaid_items` | One row per Plaid Item: institution, the encrypted access token and its key version, the `/transactions/sync` cursor, status, last error, last successful sync, consent expiry, and the last `/item/get` payload as `raw JSONB`. |
| `plaid_accounts` | One row per account, keyed by Plaid `account_id`: name, type/subtype, balances as unconstrained `NUMERIC`, both currency codes, `raw JSONB`, `first_seen_at`, `last_seen_at`, `missing_since`. |
| `transactions` | One row per Plaid transaction: amount as unconstrained `NUMERIC` in Plaid's sign convention (positive is money out), both currency codes, `date`/`authorized_date` as `DATE`, nullable `datetime`/`authorized_datetime`, merchant and personal-finance-category fields, `pending`, `pending_transaction_id`, `superseded_by`/`superseded_at`, `removed_at`, `raw JSONB`. |
| `sync_jobs` | The unit the API hands back as a 202 and that clients poll: kind, state, timestamps, error. |
| `sync_runs` | Audit row per sync attempt: trigger, timestamps, cursors before/after, counts as Plaid reported them and as actually written, outcome, error code/type/message, Plaid `request_id`. |

`updated_at` on `plaid_items`, `plaid_accounts` and `transactions` is
maintained by a trigger.

Invariants the schema and the store enforce:

- **Cursor and rows are atomic.** `ItemTx.ApplySyncBatch` writes accounts,
  transactions, soft deletes, pending links and the new cursor in one
  transaction, the same transaction that holds the item's advisory lock.
  There is no window in which rows are committed without their cursor (a
  harmless refetch) or the cursor without its rows (a batch lost silently).
  A test injects a failure between the last row write and the cursor update
  and proves nothing persisted.
- **One sync per item at a time.** `WithItemLock` takes
  `pg_try_advisory_xact_lock(hashtext('plaidsync:item'), hashtext(item_id))`
  and returns `ErrItemLocked` immediately when it is held; it never blocks.
  The lock covers the whole pagination, including the calls to Plaid. The
  item row itself is locked `FOR NO KEY UPDATE`, which blocks concurrent
  updates of the row but not the foreign-key checks of other sessions, so
  recording a `locked` sync run or creating a job for a syncing item does
  not wait for the sync to finish.
- **Soft delete only.** `removed` from Plaid sets `removed_at`; rows are never
  deleted. A second removal keeps the original timestamp, an unknown id is
  ignored, and a later re-delivery of a removed row does not clear
  `removed_at` (Plaid does not resurrect transactions).
- **Pending rows are superseded, not deleted.** When a pending transaction
  posts, Plaid issues a new `transaction_id` and sets `pending_transaction_id`
  on the posted row. The store sets `superseded_by`/`superseded_at` on the
  pending row, in either arrival order, so consumers filtering on
  `removed_at IS NULL AND superseded_by IS NULL` never double-count.
- **Replays are no-ops.** Upserts compare `raw`; an identical row is left
  untouched (`first_seen_at` and `updated_at` unchanged) and counted as
  unchanged.
- **Accounts that vanish are flagged, not dropped.** When a batch lists at
  least one account, every other account of the item gets `missing_since`
  (kept from the first time it went missing) and is reported back so the
  engine can log it on every run. It is cleared when the account reappears.
  An empty account list marks nothing missing.
- **Full payloads are kept.** `raw JSONB` on items, accounts and transactions
  holds the complete Plaid object so fields discovered later can be
  backfilled from the database instead of refetched.
- **Money is exact and dates are dates.** Amounts travel as
  `money.Amount` (a canonical decimal string; JSON numbers are decoded
  through `json.Number`, never `float64`) into unconstrained `NUMERIC`, which
  keeps each value at exactly the scale it was written with, so the column
  always agrees with `raw` and `12.34` reads back as `12.34`, not
  `12.340000`. (The columns started as `NUMERIC(14,2)`; migration `00002`
  widened them after Sandbox returned an investment balance of
  `23631.9805`.) The store still rejects more than six fractional digits as
  a sanity bound.
  `date` and `authorized_date` travel as `civil.Date` into `DATE`, so no
  time-zone conversion can shift a transaction across a day boundary.
- **Timestamps reflect the write, not the transaction start.** The columns
  a sync stamps (`last_successful_sync_at`, `first_seen_at`, `last_seen_at`,
  `missing_since`, `removed_at`, `superseded_at`) use `clock_timestamp()`.
  Postgres' `now()` is the transaction start, which for a sync is before
  the pagination began. The trigger-maintained `updated_at` still uses
  `now()`.
- **Re-linking keeps what it is not told.** `UpsertItem` on an existing item
  replaces the credential and consent expiry, resets the status to active
  and clears the last error, but leaves `institution_id`,
  `institution_name` and `raw` alone when the caller passes nil for them,
  and never touches the cursor.
- **Credentials are ciphertext only.** `encrypted_access_token` holds
  `nonce(12) || ciphertext || GCM tag` produced under `key_version`, with the
  `item_id` as additional authenticated data so a blob copied to another row
  does not decrypt. A removed item has a NULL credential and NULL
  `key_version`; two `CHECK` constraints tie `status = 'removed'` to that.
  `store.Item` never carries the credential; only `GetCredential` /
  `ItemTx.Credential` return the `Credential` struct, and only
  `internal/crypto` can open it.
- **Ids are explicit.** Every store method takes the ids it acts on as
  arguments, and all SQL lives in `internal/store`, so adding a `tenant_id`
  later is mechanical.

## Key rotation

Access tokens are encrypted with AES-256-GCM under a key encryption key
(KEK). The keyring can hold several KEKs by version: encryption always uses
`PLAIDSYNC_KEK` at `PLAIDSYNC_KEK_VERSION`, decryption uses whichever version
the row was written with. Rotating therefore never requires downtime or a
window in which anything is unreadable.

1. Generate the new key: `openssl rand -base64 32`.
2. Move the current key into `PLAIDSYNC_KEK_PREVIOUS` under its current
   version, and put the new key in `PLAIDSYNC_KEK` with
   `PLAIDSYNC_KEK_VERSION` incremented. For example, going from version 1 to
   version 2:

   ```
   PLAIDSYNC_KEK_VERSION=2
   PLAIDSYNC_KEK=<new base64 key>
   PLAIDSYNC_KEK_PREVIOUS=1:<old base64 key>
   ```

3. Restart the service. The startup log shows `kek_active_version` and
   `kek_versions`; existing rows still decrypt under version 1 and every new
   or re-linked Item is written under version 2.
4. Re-encrypt the existing rows under the new version. The store exposes
   `GetCredential` and `UpdateCredential` for this and the keyring exposes
   `Decrypt`/`Encrypt` (with the `item_id` as AAD); a `rewrap` command that
   walks every item is not part of step 1 and will be added with the
   operational tooling in a later step. Until it exists, keep the old key in
   `PLAIDSYNC_KEK_PREVIOUS`. Check what is still on the old version with:

   ```sql
   SELECT key_version, count(*) FROM plaid_items
   WHERE encrypted_access_token IS NOT NULL GROUP BY 1;
   ```

5. When no row remains under the old version, remove it from
   `PLAIDSYNC_KEK_PREVIOUS` and restart. A row that still needs a version
   the keyring no longer has fails to decrypt with `ErrUnknownKeyVersion`;
   putting the key back fixes it.

Losing every copy of a KEK makes the rows written under it permanently
unreadable; the only recovery is re-linking those Items through Link. Store
the keys with the same care as the database itself.

## Operational notes

- Logs are structured (`log/slog`) on stderr, JSON by default. Secrets cannot
  appear in them: the secret types redact themselves and no code path formats
  an access token, the database URL or a key.
- The startup log includes the full non-secret configuration (`config`
  group), the keyring versions, the migration version and the listening
  address.
- `/readyz` pings the database with a 5 second bound and returns 503 with a
  fixed body when it fails; the reason is logged at WARN, not returned.
- The HTTP server applies a 10 second read-header timeout, 30 second read
  (whole request, body included) and write timeouts, and a 2 minute idle
  timeout, so a slow client cannot hold a connection or a handler goroutine
  open indefinitely; there is no TLS. Bind to loopback and put a
  TLS-terminating proxy in front if it must be reachable from elsewhere.
