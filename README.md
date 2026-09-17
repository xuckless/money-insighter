# money-insighter

A personal-finance stack for the Canadian market, built on Plaid. Two Go
services share one Postgres:

- **`plaid-backend-golang/`** (`plaidsync`) owns the Plaid relationship:
  Link tokens, token exchange, the sync engine, the webhook receiver and a
  job queue. It writes normalised accounts and transactions into Postgres
  and is the only thing that ever talks to Plaid.
- **`postgres-topper/`** (`topper`) is the REST access layer over that
  database for everything else (categorisation, recurring-charge detection,
  the UI): read-only access to plaidsync's tables and views, read-write
  consumer tables in schema `topper`.

They never call each other. The database is the integration point:
plaidsync writes, the topper reads through a role that cannot write to
plaidsync's tables. Each directory is its own Go module with its own
README, `.env.example`, Makefile and tests; `go.work` at the root lets
editors see both.

## Layout

```
docker-compose.yml     the production stack: postgres, plaidsync, topper, tailscale sidecar
deploy/ts-serve.json   what the sidecar publishes (see below)
.env.example           stack secrets: POSTGRES_PASSWORD, TOPPER_DB_PASSWORD, TS_AUTHKEY
plaid-backend-golang/  plaidsync
postgres-topper/       topper
```

## Exposure

One Tailscale node, `money-topper`, publishes everything:

| URL | Service | Reach |
|---|---|---|
| `https://money-topper.<tailnet>.ts.net/` | topper | tailnet only |
| `https://money-topper.<tailnet>.ts.net/plaidsync/` | plaidsync API | tailnet only; serve strips the prefix, so `/plaidsync/v1/items` is plaidsync's `/v1/items` |
| `https://money-topper.<tailnet>.ts.net:8443/v1/webhooks/plaid` | plaidsync webhook receiver | **public** via Funnel; only that path is forwarded, and every delivery must carry a valid Plaid signature |

Postgres has no host port. Funnel must be enabled for the tailnet and the
node's ACL must grant the `funnel` attribute; port 8443 is one of the ports
Funnel allows.

## Running the stack

```sh
cp .env.example .env                                            # fill in
cp plaid-backend-golang/.env.example plaid-backend-golang/.env  # fill in; PLAIDSYNC_WEBHOOK_URL is the :8443 URL above
cp postgres-topper/.env.example postgres-topper/.env            # fill in TOPPER_API_KEYS
docker compose up -d --build
docker compose logs -f plaidsync topper
```

Then, from a device on the tailnet, link a Sandbox item and watch it sync:

```sh
B=https://money-topper.<tailnet>.ts.net/plaidsync
T="Authorization: Bearer $PLAIDSYNC_API_TOKEN"
curl -s -X POST -H "$T" $B/v1/sandbox/items        # 201 with the item and its initial job
curl -s -H "$T" $B/v1/items/<item_id>              # status, recent runs and jobs
curl -s -H "Authorization: Bearer $TOPPER_TOKEN" 'https://money-topper.<tailnet>.ts.net/v1/views/transactions/live?limit=5'
```

In Production the client application hosts Plaid Link, gets a token from
`POST /plaidsync/v1/link/token`, and posts the public token to
`POST /plaidsync/v1/link/exchange`. Sandbox items never count against the
Trial plan's ten Production items; Production ones do, and
`DELETE /v1/items/{id}` does not free a slot.

## Development

Each service runs and tests on its own against the development Postgres in
`plaid-backend-golang/docker-compose.yml` (host port 5433):

```sh
cd plaid-backend-golang && make check && make test-db && make test-sandbox   # the last needs Sandbox keys in .env
cd postgres-topper     && make check && make test-db
```
