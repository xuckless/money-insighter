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

Every step of the build order is done:

| Step | Scope | Status |
|---|---|---|
| 1 | Config, migrations, store layer, crypto package, with tests | done |
| 2 | Plaid wrapper interface + fixture-driven fake | done |
| 3 | Link token creation and public token exchange | done |
| 4 | Sync engine (locking, pagination, transactional commit, upserts) | done |
| 5 | Webhook receiver with signature verification | done |
| 6 | Scheduler and debouncing | done |
| 7 | Observability: structured logs, item health, graceful shutdown | done |

The binary loads and validates its configuration, builds the encryption
keyring, opens the database, runs migrations, and serves the probes, the
bearer-authenticated `/v1` API and the Plaid webhook receiver. A pool of
workers in the same process drains the `sync_jobs` queue, and a scheduler
sweeps every syncable item once per `PLAIDSYNC_SYNC_INTERVAL`. The whole
path has been exercised against Plaid Sandbox (`make test-sandbox`).

What is not built: a Link UI. In Sandbox, `POST /v1/sandbox/items` links
an item without one; in Production the client application hosts Plaid Link
and calls `POST /v1/link/token` and `POST /v1/link/exchange`.

Two things worth knowing about the Sandbox route. It sets
`options.transactions.days_requested` from the configuration, as Link does;
Plaid's own default for that endpoint is 90 days, so without it a Sandbox
item would carry less history than a real one. And Plaid serves a custom
user's transactions only if they posted within the **last 30 days** —
measured, not documented — whatever `days_requested` asks for, which is why
the documentation's demo dataset is built the way `docs/demo/README.md`
describes.

## Architecture

```
cmd/plaidsync/          entrypoint: config, logger, keyring, store, Plaid client, engine, job runner, HTTP server, signals
internal/api/           HTTP handlers: probes, bearer auth, /v1 routes, the webhook receiver and its JWT verifier
internal/config/        environment loading and validation; Config.LogValue for a secret-free startup log
internal/crypto/        AES-256-GCM envelope encryption of access tokens under a versioned keyring
internal/secret/        Token and Bytes: values that render as [REDACTED] under fmt, JSON and slog
internal/money/         exact decimal Amount (never float64); maps to NUMERIC (unconstrained; each value keeps its own scale)
internal/civil/         calendar Date with no time zone; maps to DATE
internal/store/         the only package that talks to Postgres: pool, migrations runner, every query
migrations/             goose SQL migrations, embedded into the binary
internal/plaid/         Client interface over plaid-go; raw-JSON decoders so money never touches float64; error classifier
internal/plaid/plaidtest/ scripted in-memory Client built from embedded Plaid-shaped fixtures, for tests
internal/sync/          the sync engine: one locked pagination, retries, one transactional commit, audit row
internal/jobs/          sync_jobs as a queue: workers (SKIP LOCKED), debounce, coalescing, restart requeue, scheduler
internal/store/storetest/ throwaway migrated databases for tests outside package store
internal/e2e/           the opt-in Sandbox end-to-end test (build tag sandbox)
```

Dependencies beyond the standard library: `github.com/jackc/pgx/v5`,
`github.com/pressly/goose/v3`, `github.com/plaid/plaid-go/v47` and
`github.com/golang-jwt/jwt/v5` (webhook verification). Tests use the
standard `testing` package only.

### How a sync works

`internal/sync.Engine.SyncItem` takes the item's advisory lock
(`store.WithItemLock`, which also holds the row `FOR NO KEY UPDATE`),
decrypts the access token inside the transaction, and calls
`/transactions/sync` with `count=500` and `personal_finance_category_version=v2`
from the saved cursor (empty on the first sync) until `has_more` is false,
accumulating every page in memory. Any error discards the accumulated pages
and, when it is retryable (rate limits, institution or Plaid outages,
`TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION`), restarts from the original
cursor after an exponential backoff with jitter, up to
`PLAIDSYNC_SYNC_MAX_ATTEMPTS`. Once the pagination completes the engine
calls `/accounts/get` for the canonical account list (the accounts in a
sync response are only the ones with transactions in it) and hands
everything to `ItemTx.ApplySyncBatch`, which upserts accounts and rows,
soft-deletes removals, links pending rows to their posted successors, and
saves the new cursor, all in the one transaction, then writes the
`sync_runs` audit row. A first sync that Plaid answers with an empty cursor
(`NOT_READY`) succeeds without writing anything; `SYNC_UPDATES_AVAILABLE`
arrives when the history is ready.

Failures are classified by `internal/plaid.Classify` into the `sync_runs`
outcome and the item's status: `needs_reauth` (`ITEM_LOGIN_REQUIRED`,
`PENDING_DISCONNECT`, `USER_PERMISSION_REVOKED`, `ADDITIONAL_CONSENT_REQUIRED`
and the other Link-time codes) moves the item to the matching re-auth
status and the engine stops calling Plaid for it until Link update mode
re-activates it; `fatal` (bad keys, invalid or removed access token,
`TRIAL_CONNECTION_LIMIT`, unsupported products) and unclassified errors set
status `error`; an exhausted `retryable_error` leaves the status alone and
records the error on the item. Every failure is a run row with Plaid's
`error_code`, `error_type` and `request_id`.

### Jobs and the scheduler

`sync_jobs` is the work queue. `POST /v1/items/{id}/sync`, the webhook
receiver, the scheduler and the exchange endpoint insert a queued row;
`PLAIDSYNC_SYNC_CONCURRENCY` workers claim rows one at a time with
`FOR UPDATE SKIP LOCKED` and run the engine. A second trigger for an item
that already has a queued job returns that job instead of a duplicate. A
manual job arriving within `PLAIDSYNC_SYNC_MIN_INTERVAL` of the item's last
successful sync finishes as `skipped` with `error_code=debounced` without
calling Plaid; webhook, scheduled and initial jobs are never debounced. Two
jobs for one item that do collide are serialised by the item lock: the
second finishes `skipped`/`locked`. Jobs still `running` when the process
starts were interrupted by the previous process and are put back in the
queue. The scheduler ticks every `PLAIDSYNC_SYNC_INTERVAL` and queues a
`scheduled` job for every item in status `active` or `error`; it does not
run at start, so a restart never causes a burst of Plaid calls.

A sync that Plaid answers with an empty cursor (`not_ready`: the item's
initial pull is still running on Plaid's side) finishes `succeeded` with
nothing written, and the runner queues a follow-up `initial` job after a
growing delay (15 s, 30 s, 1 m, 2 m, then 5 m four times; `NotReadyDelays`
in `internal/jobs`). A deployment with a webhook URL would hear
`SYNC_UPDATES_AVAILABLE` instead; one without, such as the desktop app,
would otherwise show an empty connection until the next scheduler sweep.
The wait is in-process: after a restart the scheduler covers it.

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
| `PLAIDSYNC_RECURRING_ENABLED` | no | `false` | The recurring transactions add-on: refresh each item's streams from `/transactions/recurring/get` after every successful sync, and at start for items not checked within one `PLAIDSYNC_SYNC_INTERVAL`. Production needs Recurring Transactions enabled on the Plaid account; a failure (commonly `PRODUCT_NOT_ENABLED`) is recorded in `plaid_items.recurring_error_*` and never fails the sync or changes the item's status. |
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

## API

Every `/v1` request except the webhook carries
`Authorization: Bearer $PLAIDSYNC_API_TOKEN` (compared in constant time; a
token in the query string is never accepted). Responses are JSON; errors
are `{"error":"message"}` and, when Plaid was the cause,
`{"error":..., "plaid":{"type","code","message","request_id"}}`. Every
response carries `X-Request-Id`, which is also in the access log line.

| Route | What it does |
|---|---|
| `GET /healthz`, `GET /readyz` | liveness; readiness (database ping, 503 when it fails). Open. |
| `POST /v1/link/token` | Link token for a new item with the configured products, country codes, webhook and redirect URI. `{"link_token","expiration","request_id"}`. |
| `POST /v1/link/exchange` `{"public_token"}` | exchanges the token, stores the encrypted credential first, enriches the row from `/item/get`, points the item's webhook at `PLAIDSYNC_WEBHOOK_URL` when it differs, and queues the initial sync. `201 {"item","job"}`. Also how an update-mode session ends: the same item is re-activated. |
| `POST /v1/link/hosted` | Hosted Link session for a new item: Plaid hosts the Link UI, so no redirect URI or Link SDK is needed; the caller opens the URL in any browser. `201 {"link_token","hosted_link_url","expiration","request_id"}`. |
| `POST /v1/link/hosted/status` `{"link_token"}` | where a hosted session stands, for polling every couple of seconds: `{"status":"pending","started"}` until the user finishes; then `{"status":"completed","item","job"}` (the public token is exchanged exactly once, on the poll that first sees it, and later polls answer from memory), `{"status":"exited","exit"?}` when the user left Link (`exit` is Plaid's error when there was one), or `{"status":"expired"}`. `404` for a token this process did not issue (sessions live in memory; after a restart, start over). |
| `POST /v1/items/{id}/link/hosted` `{"account_selection"?,"additional_consented_products"?}` | update-mode Hosted Link session for an existing item; poll it with the status route above. A finish without a public token (Plaid reports some update-mode sessions that way) re-activates the item with its existing credential and queues a sync. |
| `GET /v1/items` | every item, removed ones included. |
| `GET /v1/items/{id}` | the item with its ten most recent jobs and runs, which together are its health. |
| `POST /v1/items/{id}/link/token` `{"account_selection"?,"additional_consented_products"?}` | update-mode Link token for an existing item. |
| `POST /v1/items/{id}/sync` | queues a manual sync; `202 {"job"}` with `Location: /v1/jobs/{id}`. `409` when the item is removed or waiting for update mode. |
| `GET /v1/jobs/{id}` | poll a job: `queued`, `running`, then `succeeded`, `failed` (`error_code` is the run outcome) or `skipped` (`debounced`, `locked`, `needs_reauth`, `item_removed`). |
| `DELETE /v1/items/{id}` | `/item/remove` then purge the credential; rows stay. On the Trial plan this does not free a slot. |
| `POST /v1/sandbox/items` `{"institution_id"?,"products"?,"override_username"?,"user_config"?,"days_requested"?}` | Sandbox only (the route is not mounted otherwise): links an item through `/sandbox/public_token/create` and the exchange path above, without a browser. Defaults to `ins_109508`. `override_username` picks a different Sandbox test user; with `"user_custom"`, `user_config` carries [Plaid's custom user](https://plaid.com/docs/sandbox/user-custom/) — the accounts, balances and transactions the item will return — as an object or as the escaped string Plaid itself takes. `days_requested` overrides the configured history depth. |
| `POST /v1/webhooks/plaid` | Plaid's webhook receiver. No bearer: the `Plaid-Verification` ES256 JWT is checked (key fetched by `kid` from `/webhook_verification_key/get` and cached, expired keys refused, `iat` within five minutes, `request_body_sha256` against the raw body). Verified webhooks are always `200`. |

Webhooks handled: `TRANSACTIONS/SYNC_UPDATES_AVAILABLE` queues a sync;
`ITEM/ERROR` moves the item to the status its error code implies;
`ITEM/PENDING_DISCONNECT` and `PENDING_EXPIRATION` set `pending_expiration`;
`ITEM/USER_PERMISSION_REVOKED` sets `permission_revoked`;
`ITEM/LOGIN_REPAIRED` sets `active` and queues a sync;
`ITEM/NEW_ACCOUNTS_AVAILABLE` is logged (a Canadian non-OAuth item needs
update mode with account selection to add accounts). Everything else,
including the legacy `TRANSACTIONS` codes, is acknowledged and ignored.

Item JSON: `item_id`, `institution_id`, `institution_name`, `status`
(`active`, `login_required`, `pending_expiration`, `permission_revoked`,
`error`, `removed`), `has_cursor`, `last_error` (`code`, `type`, `message`,
`at`, or null), `last_successful_sync_at`, `consent_expires_at`,
`created_at`, `updated_at`. The credential and the raw Plaid object are
never returned; the topper serves `raw` to consumers that need it.

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

4. Check the probes, then link a Sandbox item and watch it sync:

   ```sh
   curl -i http://127.0.0.1:8080/healthz   # 200 {"status":"ok"}
   curl -i http://127.0.0.1:8080/readyz    # 200 {"status":"ok"}, or 503 {"status":"unavailable"} when Postgres is unreachable

   T="Authorization: Bearer $PLAIDSYNC_API_TOKEN"
   curl -s -X POST -H "$T" http://127.0.0.1:8080/v1/sandbox/items          # 201 {"item":{...},"job":{...}}
   curl -s -H "$T" http://127.0.0.1:8080/v1/jobs/<job_id>                   # poll until succeeded
   curl -s -H "$T" http://127.0.0.1:8080/v1/items/<item_id>                 # status, runs, jobs
   curl -s -X POST -H "$T" http://127.0.0.1:8080/v1/items/<item_id>/sync    # 202; a second one within 15m is skipped as debounced
   ```

   Sandbox usually answers the very first sync with `NOT_READY` (the run
   succeeds with nothing written); the scheduler, a webhook or another
   manual sync a few seconds later brings the history in. Without a public
   `PLAIDSYNC_WEBHOOK_URL` there are no webhooks locally, which is fine for
   this.

5. Stop it with Ctrl-C (SIGINT) or `kill <pid>` (SIGTERM). The server stops
   accepting, drains in-flight requests for up to
   `PLAIDSYNC_SHUTDOWN_TIMEOUT`, closes the pool, and exits 0. A second
   signal while draining terminates the process immediately.

The service exits 1 with a message on stderr if configuration is invalid
(every problem listed), the keyring cannot be built, Postgres is unreachable,
a migration fails, or the bind address cannot be opened.

Sandbox tips: `/sandbox/item/reset_login` forces `ITEM_LOGIN_REQUIRED`
(the next sync ends `needs_reauth` and the item waits for update mode), and
`/sandbox/item/fire_webhook` exercises the webhook receiver once
`PLAIDSYNC_WEBHOOK_URL` is public. Both are wrapped by `internal/plaid` and
scripted by the fake. Do not spend a real Item slot until the flow works
end to end in Sandbox.

## Tests

```sh
make test          # unit tests; database-backed tests skip
make test-db       # starts the Compose database if needed, then runs everything
make test-sandbox  # opt-in: the end-to-end test against Plaid Sandbox with the keys in .env
go test -race -count=1 ./...   # what CI should run, with PLAIDSYNC_TEST_DATABASE_URL set
```

Database-backed tests (all of `internal/store`, the engine, the job runner
and the API, plus the pgx round-trip tests in `internal/money` and
`internal/civil`) read
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

The engine, runner and API tests run the real code against the fake Plaid
in `internal/plaid/plaidtest`, scripted from embedded Plaid-shaped JSON
fixtures and decoded by the same functions the real client uses: a
two-page pagination with a pending row that posts, a mutation error that
restarts from the original cursor, retry exhaustion, `ITEM_LOGIN_REQUIRED`,
the not-ready first sync, the item lock, cancellation, an undecryptable
credential, debouncing, coalescing, restart requeue, the scheduler sweep,
every route including auth failures, and signed webhooks (the test signs
with a generated P-256 key registered in the fake). `internal/plaid`'s own
tests run the real client against an `httptest` server and check the exact
request bodies sent to Plaid.

`make test-sandbox` (build tag `sandbox`) links a Sandbox item, waits for
its history, syncs it into a throwaway database, resyncs from the cursor,
forces `ITEM_LOGIN_REQUIRED` and checks the classification, then removes
the item. It needs `PLAID_CLIENT_ID` and `PLAID_SECRET` in `.env` and
refuses to run with `PLAID_ENV=production`.

## Schema

`migrations/00001_init.sql` creates the five core tables; `00003` and
`00004` add balance history and the recurring add-on. The migration files
are embedded into the binary and applied by goose at startup; never edit
one that has shipped, add a new one.

| Table | Holds |
|---|---|
| `plaid_items` | One row per Plaid Item: institution, the encrypted access token and its key version, the `/transactions/sync` cursor, status, last error, last successful sync, consent expiry, and the last `/item/get` payload as `raw JSONB`. |
| `plaid_accounts` | One row per account, keyed by Plaid `account_id`: name, type/subtype, balances as unconstrained `NUMERIC`, both currency codes, `raw JSONB`, `first_seen_at`, `last_seen_at`, `missing_since`. |
| `transactions` | One row per Plaid transaction: amount as unconstrained `NUMERIC` in Plaid's sign convention (positive is money out), both currency codes, `date`/`authorized_date` as `DATE`, nullable `datetime`/`authorized_datetime`, merchant and personal-finance-category fields, `pending`, `pending_transaction_id`, `superseded_by`/`superseded_at`, `removed_at`, `raw JSONB`. |
| `sync_jobs` | The unit the API hands back as a 202 and that clients poll: kind, state, timestamps, error. |
| `sync_runs` | Audit row per sync attempt: trigger, timestamps, cursors before/after, counts as Plaid reported them and as actually written, outcome, error code/type/message, Plaid `request_id`. |
| `account_balance_snapshots` | One row per account per day with its balances after that day's last write, filled by a trigger on `plaid_accounts` so every writer records history. The only record of how balances moved (net worth and cash charts). |
| `plaid_recurring_streams` | Plaid's recurring streams from `/transactions/recurring/get` when `PLAIDSYNC_RECURRING_ENABLED` is on: direction, merchant, category, frequency, first/last/predicted dates, average and last amounts (rounded to cents; Plaid sends them as floats), status, transaction ids, `raw JSONB`. Streams Plaid stops returning get `removed_at`. The outcome of each item's last refresh is on `plaid_items.recurring_*`. |

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
  The desktop app (`../desktop`) runs it on a loopback port and is its
  only client; nothing is reachable from outside the machine.
- Every request is logged with method, path, status, bytes, duration,
  request id and remote address, never the query string or a body. Every
  Plaid call is logged at DEBUG with endpoint, status, duration and Plaid's
  `request_id`, and at WARN when it fails. Sync outcomes are logged at INFO
  (success) or WARN (failure) with the run id and counts.
- Shutdown: SIGINT or SIGTERM stops the listener, drains in-flight requests
  for up to `PLAIDSYNC_SHUTDOWN_TIMEOUT`, and cancels the job runner. A sync
  in flight is canceled (Plaid calls stop, nothing is committed) and its job
  stays `running` in the table; the next start requeues it, exactly as
  after a crash.
- Item health is `GET /v1/items/{id}`: the status, the last error, the
  last successful sync, and the recent jobs and runs. The topper's
  `/v1/views/sync/status` shows the same from the database.
