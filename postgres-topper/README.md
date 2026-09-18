# topper

`topper` is the REST access layer over the plaidsync Postgres. plaidsync owns
the Plaid relationship and writes accounts and transactions into the
database; every other consumer (the desktop UI, categorisation,
recurring-charge detection) reads that data and stores its own results.
The topper is what those consumers talk to, on loopback, with a bearer
token.

It exposes:

- plaidsync's five tables **read-only**: `plaid_items`, `plaid_accounts`,
  `transactions`, `sync_jobs`, `sync_runs`. Read-only is enforced by the
  Postgres role the topper connects as, not only by the topper.
- consumer tables in schema `topper` **read-write**, named in
  `TOPPER_WRITE_TABLES`. The topper owns that schema and runs its own goose
  migrations for it.
- typed views: the live transaction ledger, accounts with their
  institution, per-item sync status, and the desktop app's insight views
  (categorised transactions, daily and monthly category totals, merchant
  totals, daily balances, recurring streams).

Not in scope: talking to Plaid (plaidsync does), business logic of any kind,
multi-tenancy, and any exposure beyond the machine it runs on.

## Architecture

```
cmd/topper/          entrypoint: config, logger, schema wait, migrations, catalog, HTTP server, signals
internal/config/     TOPPER_* loading and validation; Config.LogValue for a secret-free startup log
internal/secret/     Token: renders as [REDACTED] under fmt, JSON and slog (copied from plaidsync)
internal/store/      pgx pool, wait for plaidsync's schema, goose migrations, Postgres error classes
internal/catalog/    the allowlist: tables, views, columns, keys and types, introspected once at boot
internal/query/      PostgREST-style grammar, SQL builders (select, count, upsert, delete), row encoder
internal/views/      the three views, as SQL text in Go
internal/auth/       bearer tokens, scopes, constant-time lookup
internal/cache/      response cache with singleflight and weak ETags
internal/api/        handlers and middleware (recover, access log, CORS, gzip)
internal/testdb/     database test helper: fresh DB with plaidsync's and the topper's migrations
migrations/          goose SQL migrations for schema topper, embedded into the binary
deploy/              Postgres init SQL (the topper role and its grants) for a database with separate roles
```

Dependencies beyond the standard library: `github.com/jackc/pgx/v5`,
`github.com/pressly/goose/v3`, `golang.org/x/sync` (singleflight). Tests use
the standard `testing` package only.

## Security model

- **Database role.** The topper connects as role `topper`, created by
  `deploy/postgres-init/topper.sql`: it owns schema `topper`, has `SELECT`
  on every table in `public` (including tables plaidsync adds later, via
  `ALTER DEFAULT PRIVILEGES`), and nothing else. A bug in the allowlist
  cannot write to plaidsync's tables; Postgres refuses with `42501`.
- **Allowlist.** A table name in a URL must be a key of the catalog. Column
  names in filters, `select`, `order_by` and request bodies must be columns
  of that table as read from `information_schema` at startup. Every
  identifier is double-quoted, every value is a bound parameter. Unknown
  query parameters are a 400, never ignored, so a typo in a DELETE filter
  cannot widen the match.
- **Denylist.** `plaid_items.encrypted_access_token` and
  `plaid_items.key_version` never appear in a response and cannot be
  filtered or ordered by. `SELECT *` is never generated; projections are
  built from the catalog.
- **Tokens.** Static bearer tokens with a `read` or `readwrite` scope. Each
  presented token is hashed and compared in constant time against every
  configured key. Tokens are never logged; the key's name is. A token in
  the query string is not accepted.
- **Exposure.** The desktop app runs the topper on a loopback port chosen at
  launch and is its only caller; nothing listens on any other interface.
  There, the topper connects as the same superuser as plaidsync (one user,
  one machine), so the role split above applies only to a deployment that
  runs `deploy/postgres-init/topper.sql`.

## API

Every `/v1` request carries `Authorization: Bearer <token>`. Every response
is JSON; errors are `{"error":"message"}` with the matching status.
`GET /healthz` (liveness) and `GET /readyz` (database ping) are open.

### Discovery

`GET /v1/` lists every table and view with its mode, primary key, columns
and types:

```json
{"tables":[{"name":"transactions","mode":"read","schema":"public","primary_key":["transaction_id"],"columns":[{"name":"transaction_id","type":"text","nullable":false},...]}],
 "views":[{"name":"transactions/live","path":"/v1/views/transactions/live","toggles":[],"columns":[...]}]}
```

### Reading a table or view

`GET /v1/{table}` and `GET /v1/views/{name}` accept the same query grammar:

| Parameter | Meaning |
|---|---|
| `col=value` | `col = value` |
| `col=op.value` | `op` is `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like`, `ilike` (the last two on text columns only; supply `%` yourself) |
| `col=in.(a,b,c)` | `col IN (a, b, c)`; values may not contain commas or parentheses; at most 200 |
| `col=is.null`, `col=is.notnull` | NULL tests |
| `or=(col.op.value,col.in.(a,b),col.is.null)` | one OR group, ANDed with everything else; repeat `or=` for several groups |
| `select=col1,col2` | project these columns (default: every exposed column) |
| `order_by=col&order=asc\|desc` | one sort column (default: the primary key, or the view's own order); the primary key is always appended as a tiebreak |
| `limit=N`, `offset=N` | 1..1000 (default 100) and >= 0 |
| `count=exact` | add `"total"`, the row count without limit/offset |

Repeating a column parameter ANDs the conditions: `date=gte.2026-01-01&date=lt.2026-02-01`.
Values are parsed by Postgres, so `amount=gte.abc` is a 400 with Postgres's
message, and `date=eq.yesterday` is a valid date.

Response:

```json
{"table":"transactions","count":2,"limit":2,"offset":0,"total":1234,"data":[{...},{...}]}
```

Views say `"view"` instead of `"table"`. Keys inside each row are in column
order.

JSON rendering is exact: `NUMERIC` is a string (`"12.34"`), `DATE` is a
string (`"2026-09-09"`), `TIMESTAMPTZ` is RFC 3339 in UTC, `UUID` is a
string, `JSONB` is embedded verbatim (plaidsync's `raw` columns come back
as the original Plaid objects, big integers intact), arrays are JSON
arrays. Nothing that carries money ever passes through a float.

### Caching

Reads of read-only tables and views are cached in memory for
`TOPPER_CACHE_TTL` (default 30 s, shared by every caller since all callers
see the same data) and carry `ETag` and `Cache-Control: private,
max-age=N`. Send `If-None-Match` to get a `304`. Writable tables are never
cached (`Cache-Control: no-store`). Responses are gzip-encoded when the
client sends `Accept-Encoding: gzip`.

### Writing a consumer table

Only tables listed in `TOPPER_WRITE_TABLES` accept writes, and only with a
`readwrite` token. A write to a plaidsync table is a 405.

`POST /v1/{table}` with one object or an array of objects upserts them in
one transaction and returns the rows as stored:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8080/v1/tx_notes \
  -d '[{"transaction_id":"abc","category":"groceries","tags":["weekly"]}]'
# {"table":"tx_notes","count":1,"data":[{"transaction_id":"abc","category":"groceries",...}]}
```

- Every key must be a column (400 otherwise). Generated and
  identity-`ALWAYS` columns are dropped from the insert.
- When every primary key column is supplied, a conflict updates the other
  supplied columns (`ON CONFLICT ... DO UPDATE`); columns not sent keep
  their value. When only the key is supplied, a conflict does nothing and
  that row is absent from `data`, so `count` can be below the number sent.
  Without the full key it is a plain insert.
- Values are cast by Postgres: a bad numeric is a 400, a foreign key to a
  transaction that does not exist is a 422, a unique violation is a 409.
- Bodies are capped at `TOPPER_MAX_BODY_BYTES` (413 above it).

`DELETE /v1/{table}?filters` deletes the matching rows and answers
`{"table":"tx_notes","deleted":N}`. The same filter grammar applies; a
DELETE without any condition is refused with a 400.

### Views

| Path | Rows | Extra parameters |
|---|---|---|
| `/v1/views/transactions/live` | `transactions` with `removed_at IS NULL AND superseded_by IS NULL`: the ledger without retracted rows and without pending rows that have since posted. Ordered by `date DESC, transaction_id`. | the table grammar |
| `/v1/views/accounts` | `plaid_accounts` joined to `plaid_items` for `institution_id`, `institution_name`, `item_status`. Accounts flagged `missing_since` are excluded. | `include_missing=1` |
| `/v1/views/sync/status` | one row per item: status, `last_successful_sync_at`, last error, consent expiry, `account_count`, the latest `sync_runs` row as `last_run_*`, the latest `sync_jobs` row as `latest_job_*`, and the recurring add-on's last refresh as `recurring_*` | the table grammar |
| `/v1/views/transactions/categorized` | the live ledger with each row's effective `category` (the user's `topper.category_overrides`, else `topper.merchant_rules` by `merchant_key`, else Plaid's category mapped by `topper.plaid_category`), `category_source`, `needs_category`, and the account's name, mask, type and institution. A missing currency falls back to the account's. | the table grammar |
| `/v1/views/categories/daily` | net amount and count per `day`, `category` and currency over the categorised ledger. Filters on `day` are pushed into the aggregate. | the table grammar |
| `/v1/views/categories/monthly` | the same per calendar `month` (the month's first day). | the table grammar |
| `/v1/views/merchants/monthly` | per `month`, `category` and `merchant_key`, with the latest display name as `merchant`. | the table grammar |
| `/v1/views/balances/daily` | plaidsync's `account_balance_snapshots` with each account's type and institution. | the table grammar |
| `/v1/views/recurring/streams` | plaidsync's live recurring streams (not removed, item not removed) with their account and app `category`; `source` is `plaid`. | the table grammar |
| `/v1/views/recurring/entries` | `topper.recurring_entries` (recurring payments the user added by hand) with the account each is paid from and its category's `kind`. | the table grammar |

Migration `00002_insights.sql` adds the category functions
(`topper.plaid_category`, `topper.merchant_key`, `topper.is_category`) and
the desktop app's tables: `budgets`, `category_overrides`, `merchant_rules`
and `preferences`. `00003_categories.sql` turns the category list into a
table, `topper.categories` (`id`, `label`, `color`, `icon`, `kind` of
`spending`, `bill`, `income` or `transfer`, `builtin`), seeded with the
built-in set that `desktop/src/shared/categories.ts` mirrors; budgets,
overrides and rules reference it by foreign key, and a delete cascades to
the budget but is refused while overrides or rules still point at the
category (the app merges them into another category first). It also adds
`recurring_entries` (recurring payments added by hand) and
`recurring_hidden` (streams the user marked as not recurring).

### Status codes

| Status | When |
|---|---|
| 200 | success |
| 304 | `If-None-Match` matched |
| 400 | bad query grammar, unknown column or parameter, denied column, bad JSON, value Postgres cannot parse, unfiltered DELETE |
| 401 | missing or unknown token |
| 403 | `read` token on POST or DELETE |
| 404 | unknown table or view |
| 405 | write to a read-only table, or a method the path does not support (`Allow` header set) |
| 409 | unique violation on POST; foreign key violation on DELETE |
| 413 | body over `TOPPER_MAX_BODY_BYTES` |
| 422 | foreign key, NOT NULL or CHECK violation on POST |
| 500 | anything else; the SQLSTATE is in the log, never in the response |
| 504 | the query exceeded `TOPPER_QUERY_TIMEOUT` |

Every response carries `X-Request-Id`, which is also in the access log line.

## Configuration

Every setting is a `TOPPER_*` environment variable; `.env.example`
documents them and a test fails if it and `internal/config` disagree.
Values are trimmed, an empty value is unset, and every problem is reported
at once.

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `TOPPER_BIND_ADDR` | no | `127.0.0.1:8080` | `host:port` to listen on. Plain HTTP; keep it behind TLS termination. |
| `TOPPER_DATABASE_URL` | yes | | Postgres URL for the `topper` role. |
| `TOPPER_API_KEYS` | yes | | Comma-separated `name:scope:token`; scope `read` or `readwrite`; token >= 32 bytes (`openssl rand -hex 32`). |
| `TOPPER_WRITE_TABLES` | no | none | Tables in schema `topper` exposed read-write. Each must exist at startup. |
| `TOPPER_CACHE_TTL` | no | `30s` | Response cache lifetime for read-only relations; `0` disables. |
| `TOPPER_CORS_ORIGINS` | no | none | Exact origins allowed from browsers. Unset disables CORS headers. |
| `TOPPER_QUERY_TIMEOUT` | no | `15s` | Bound on each database call. |
| `TOPPER_STARTUP_WAIT` | no | `60s` | How long to wait for plaidsync's tables before failing. |
| `TOPPER_MAX_BODY_BYTES` | no | `5242880` | POST body cap. |
| `TOPPER_LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error`. |
| `TOPPER_LOG_FORMAT` | no | `json` | `json` or `text`, to stderr. |
| `TOPPER_SHUTDOWN_TIMEOUT` | no | `30s` | Drain time after SIGINT/SIGTERM. |

Startup order: load config, open the pool, wait for plaidsync's tables
(`TOPPER_STARTUP_WAIT`), require schema `topper`, apply the topper's own
migrations, introspect the catalog, listen. Any failure exits 1 with the
reason on stderr.

## Running locally

Prerequisites: Go 1.26, Docker with Compose, `openssl`, `curl`, and the
plaidsync repo at `../plaid-backend-golang` (its Compose Postgres on
`127.0.0.1:5433` is the development database, and its migrations are what
the topper's tests apply).

```sh
make db-up            # plaidsync's Postgres, if not already running
make db-init-topper   # create role topper (password "topper") and schema topper; idempotent
cp .env.example .env  # then put real tokens in TOPPER_API_KEYS
make run
```

plaidsync must have run its migrations against that database at least once
(`make run` in `../plaid-backend-golang`, or `make test-db` here creates
throwaway databases and does not need it). Then:

```sh
curl -i http://127.0.0.1:8080/healthz
curl -s -H "Authorization: Bearer $TOKEN" 'http://127.0.0.1:8080/v1/'
curl -s -H "Authorization: Bearer $TOKEN" 'http://127.0.0.1:8080/v1/views/transactions/live?limit=5'
```

## In the desktop app

`../desktop` starts an embedded Postgres, then plaidsync, then the topper,
on loopback ports it picks at launch, with a generated readwrite-scope token
in `TOPPER_API_KEYS` and `TOPPER_WRITE_TABLES=budgets,category_overrides,merchant_rules,preferences,categories,recurring_entries,recurring_hidden`, `TOPPER_CACHE_TTL=2s` so a finished sync shows up at
once, and `TOPPER_STARTUP_WAIT=60s` so it can come up while plaidsync is
still migrating. The app creates schema `topper` itself before the topper
starts (there is no separate role; see Security model). Logs are in the
app's data directory under `logs/topper.log`.

## Adding a consumer table

1. Add a goose migration under `migrations/` that creates the table in
   schema `topper` (schema-qualify every name; never rely on `search_path`).
   Give it a real primary key so upserts work, and attach
   `topper.set_updated_at()` if it has an `updated_at` column. Never edit a
   migration that has shipped.
2. Add the table name to `TOPPER_WRITE_TABLES`.
3. Restart the topper. It applies the migration, validates the name against
   `information_schema`, and the table is live under `/v1/{table}`.

Column changes to plaidsync's tables also need a topper restart: the
catalog is read once at startup.

## Tests

```sh
make test      # unit tests; database-backed tests skip
make test-db   # everything, against the development Postgres
go test -race -count=1 ./...   # with TOPPER_TEST_DATABASE_URL set
```

Database-backed tests read `TOPPER_TEST_DATABASE_URL` (the user needs
`CREATEDB`; the Compose user is a superuser). Each test creates its own
`topper_test_<random>` database, applies plaidsync's migrations from
`../plaid-backend-golang/migrations` (override with
`TOPPER_TEST_PLAIDSYNC_MIGRATIONS`), then the topper's, and drops it when
done. The API suite covers the denylist, the grammar end to end, exact
rendering of numeric/date/jsonb/array values, upsert and delete semantics,
the views, scopes, ETags and gzip, and a restricted-role test that models
the production grants and asserts Postgres refuses writes to `public`.

## Operational notes

- Logs are `log/slog`, JSON by default, on stderr. The startup line shows
  the configuration with the database URL redacted and API keys as name
  and scope only. Every request logs method, path, query, status, bytes,
  duration, caller name, request id and remote address; never the token.
- `/readyz` pings the database with a 5 second bound and returns 503 when
  it fails.
- The HTTP server applies a 10 s read-header timeout, 30 s read timeout,
  60 s write timeout and 2 min idle timeout.
- The response cache is per process; the deployment is one container, so
  that is the whole cache.
- goose records the topper's migrations in `topper.goose_db_version`, apart
  from plaidsync's `public.goose_db_version`, and takes a different advisory
  lock, so both services can restart at the same time.
