package api_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"postgres-topper/internal/api"
	"postgres-topper/internal/auth"
	"postgres-topper/internal/cache"
	"postgres-topper/internal/catalog"
	"postgres-topper/internal/config"
	"postgres-topper/internal/testdb"
	"postgres-topper/internal/views"
)

const (
	readTok  = "read-token-0123456789abcdef0123456789abcdef"
	writeTok = "write-token-0123456789abcdef0123456789abcde"
)

type env struct {
	t   *testing.T
	db  *testdb.DB
	srv *httptest.Server
	log *bytes.Buffer
}

// newEnv builds a seeded database with the consumer tables, loads the
// catalog and starts the full handler chain on an httptest server. pool,
// when non-nil, replaces the admin pool for catalog loading and serving.
func newEnv(t *testing.T, cacheTTL time.Duration, pool *pgxpool.Pool) *env {
	t.Helper()
	db := testdb.New(t)
	db.CreateConsumerTables(t)
	db.SeedPlaid(t)
	if pool == nil {
		pool = db.Pool
	}
	cat, err := catalog.Load(testdb.Ctx(t), pool, catalog.Spec{
		ReadTables:  catalog.PlaidTables,
		WriteTables: []string{"tx_notes", "events"},
		Views:       views.All,
		Denied:      catalog.PlaidDenylist,
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	cfg := &config.Config{
		APIKeys: []config.APIKey{
			{Name: "reader", Scope: config.ScopeRead, Hash: sha256.Sum256([]byte(readTok))},
			{Name: "writer", Scope: config.ScopeReadWrite, Hash: sha256.Sum256([]byte(writeTok))},
		},
		CacheTTL:     cacheTTL,
		QueryTimeout: 10 * time.Second,
		MaxBodyBytes: 4096,
		CORSOrigins:  []string{"https://app.example.com"},
	}
	logBuf := &bytes.Buffer{}
	s := api.New(api.Deps{
		Config:  cfg,
		Pool:    pool,
		Catalog: cat,
		Keyring: auth.NewKeyring(cfg.APIKeys),
		Cache:   cache.New(cacheTTL, 100),
		Logger:  slog.New(slog.NewJSONHandler(logBuf, nil)),
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, db: db, srv: srv, log: logBuf}
}

type resp struct {
	code   int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, r.body)
	}
	return m
}

func (r resp) errMsg(t *testing.T) string {
	t.Helper()
	m := r.json(t)
	s, _ := m["error"].(string)
	return s
}

func (r resp) data(t *testing.T) []map[string]any {
	t.Helper()
	m := r.json(t)
	raw, ok := m["data"].([]any)
	if !ok {
		t.Fatalf("no data array in %s", r.body)
	}
	out := make([]map[string]any, len(raw))
	for i, v := range raw {
		out[i] = v.(map[string]any)
	}
	return out
}

func (e *env) do(method, path, token string, body string, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{code: res.StatusCode, header: res.Header, body: b}
}

func (e *env) get(path string) resp { return e.do("GET", path, readTok, "") }

func (e *env) expect(r resp, code int, contains string) {
	e.t.Helper()
	if r.code != code {
		e.t.Errorf("status = %d, want %d; body: %s", r.code, code, r.body)
	}
	// Compare against the body with JSON quote escapes undone so an
	// expectation can be written the way the message reads.
	if contains != "" && !strings.Contains(strings.ReplaceAll(string(r.body), `\"`, `"`), contains) {
		e.t.Errorf("body lacks %q: %s", contains, r.body)
	}
	if ct := r.header.Get("Content-Type"); r.code != 304 && r.code != 204 && !strings.HasPrefix(ct, "application/json") {
		e.t.Errorf("Content-Type = %q for %d", ct, r.code)
	}
}

func ids(rows []map[string]any, key string) string {
	var out []string
	for _, r := range rows {
		out = append(out, fmt.Sprint(r[key]))
	}
	return strings.Join(out, ",")
}

func TestProbesAndAuth(t *testing.T) {
	e := newEnv(t, 0, nil)
	e.expect(e.do("GET", "/healthz", "", ""), 200, `"ok"`)
	e.expect(e.do("GET", "/readyz", "", ""), 200, `"ok"`)
	e.expect(e.do("GET", "/nope", "", ""), 404, "not found")
	e.expect(e.do("GET", "/v1/", "", ""), 401, "missing bearer token")
	e.expect(e.do("GET", "/v1/transactions", "bad", ""), 401, "invalid token")
	e.expect(e.do("POST", "/v1/tx_notes", readTok, "{}"), 403, "scope read does not allow POST")
	e.expect(e.do("DELETE", "/v1/tx_notes?note=x", readTok, ""), 403, "scope read does not allow DELETE")
	r := e.do("PUT", "/v1/tx_notes", writeTok, "{}")
	e.expect(r, 405, "method not allowed")
	if r.header.Get("Allow") != "GET, HEAD, POST, DELETE" {
		t.Errorf("Allow = %q", r.header.Get("Allow"))
	}
	e.expect(e.get("/v1/nope"), 404, `unknown table "nope"`)
	e.expect(e.get("/v1/views/nope"), 404, `unknown view "nope"`)
	e.expect(e.get("/v1/views"), 404, `unknown table "views"`)
	e.expect(e.get("/v1/a/b"), 404, "not found")
	if r := e.get("/v1/"); r.header.Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id")
	}
	if strings.Contains(e.log.String(), readTok) || strings.Contains(e.log.String(), writeTok) {
		t.Error("access log contains a token")
	}
	if !strings.Contains(e.log.String(), `"caller":"reader"`) {
		t.Errorf("access log lacks the caller name:\n%s", e.log.String())
	}
}

func TestListing(t *testing.T) {
	e := newEnv(t, 0, nil)
	r := e.get("/v1/")
	e.expect(r, 200, "")
	m := r.json(t)
	tables := m["tables"].([]any)
	if len(tables) != 7 {
		t.Errorf("tables = %d", len(tables))
	}
	for _, tv := range tables {
		tb := tv.(map[string]any)
		cols := tb["columns"].([]any)
		for _, cv := range cols {
			if name := cv.(map[string]any)["name"]; name == "encrypted_access_token" || name == "key_version" {
				t.Errorf("listing exposes %v", name)
			}
		}
		if tb["name"] == "tx_notes" && tb["mode"] != "readwrite" {
			t.Errorf("tx_notes mode = %v", tb["mode"])
		}
		if tb["name"] == "transactions" && tb["mode"] != "read" {
			t.Errorf("transactions mode = %v", tb["mode"])
		}
	}
	vs := m["views"].([]any)
	if len(vs) != 10 || vs[0].(map[string]any)["path"] != "/v1/views/accounts" {
		t.Errorf("views = %v", vs)
	}
}

func TestReadExactTypes(t *testing.T) {
	e := newEnv(t, 0, nil)
	r := e.get("/v1/transactions?transaction_id=txn_1")
	e.expect(r, 200, "")
	body := string(r.body)
	for _, want := range []string{
		`"amount":"12.34"`,
		`"date":"2026-09-01"`,
		`"authorized_date":"2026-08-31"`,
		`"datetime":"2026-09-01T12:00:00Z"`,
		`"pending":false`,
		`"big": 12345678901234567890`, // jsonb::text keeps the number exact (Postgres normalises spacing)
		`"authorized_datetime":null`,
		`"table":"transactions","count":1,"limit":100,"offset":0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s:\n%s", want, body)
		}
	}
	// Keys are in column order, not sorted.
	if strings.Index(body, `"transaction_id"`) > strings.Index(body, `"amount"`) {
		t.Errorf("keys are not in column order:\n%s", body)
	}

	r = e.get("/v1/sync_jobs")
	e.expect(r, 200, `"job_id":"11111111-1111-4111-8111-111111111111"`)
	r = e.get("/v1/sync_runs?order_by=run_id")
	e.expect(r, 200, `"run_id":1`)
	r = e.get("/v1/plaid_accounts?account_id=acc_b1")
	e.expect(r, 200, `"current_balance":"-250.50"`)
	e.expect(r, 200, `"available_balance":null`)
}

func TestDenylist(t *testing.T) {
	e := newEnv(t, 0, nil)
	r := e.get("/v1/plaid_items")
	e.expect(r, 200, `"institution_name":"Bank A"`)
	if strings.Contains(string(r.body), "encrypted_access_token") || strings.Contains(string(r.body), "key_version") {
		t.Errorf("credential columns in body:\n%s", r.body)
	}
	e.expect(e.get("/v1/plaid_items?select=encrypted_access_token"), 400, "not accessible")
	e.expect(e.get("/v1/plaid_items?select=item_id,key_version"), 400, "not accessible")
	e.expect(e.get("/v1/plaid_items?encrypted_access_token=is.notnull"), 400, "not accessible")
	e.expect(e.get("/v1/plaid_items?order_by=key_version"), 400, "not accessible")
	e.expect(e.get("/v1/plaid_items?or=(key_version.gt.0)"), 400, "not accessible")
}

func TestFiltersAndPaging(t *testing.T) {
	e := newEnv(t, 0, nil)
	r := e.get("/v1/transactions?account_id=acc_a1&amount=gte.10&order_by=amount&order=desc&count=exact&limit=2")
	e.expect(r, 200, `"total":3`) // txn_1 12.34, txn_2 and txn_3 50.00; txn_5 is 9.99
	if got := ids(r.data(t), "transaction_id"); got != "txn_2,txn_3" {
		t.Errorf("rows = %s", got)
	}
	r = e.get("/v1/transactions?account_id=acc_a1&amount=gte.10&order_by=amount&order=desc&count=exact&limit=2&offset=2")
	if got := ids(r.data(t), "transaction_id"); got != "txn_1" {
		t.Errorf("page 2 = %s", got)
	}
	r = e.get("/v1/transactions?transaction_id=in.(txn_7,txn_4)&select=transaction_id,name")
	if got := ids(r.data(t), "name"); got != "Payroll,Dinner" {
		t.Errorf("in = %s", got)
	}
	r = e.get("/v1/transactions?merchant_name=is.null&select=transaction_id")
	if got := ids(r.data(t), "transaction_id"); got != "txn_4,txn_6" {
		t.Errorf("is.null = %s", got)
	}
	r = e.get("/v1/transactions?or=(amount.lt.0,pending.eq.true)&select=transaction_id")
	if got := ids(r.data(t), "transaction_id"); got != "txn_2,txn_4" {
		t.Errorf("or = %s", got)
	}
	r = e.get("/v1/transactions?name=ilike.%25gro%25&select=transaction_id")
	if got := ids(r.data(t), "transaction_id"); got != "txn_2,txn_3" {
		t.Errorf("ilike = %s", got)
	}
	r = e.get("/v1/transactions?date=gte.2026-09-02&date=lte.2026-09-03&select=transaction_id")
	if got := ids(r.data(t), "transaction_id"); got != "txn_2,txn_3,txn_4" {
		t.Errorf("date range = %s", got)
	}
	e.expect(e.get("/v1/transactions?amount=gte.abc"), 400, "invalid value")
	e.expect(e.get("/v1/transactions?date=eq.notadate"), 400, "invalid value")
	// Postgres accepts relative date literals; that is its input function's
	// call, not the topper's.
	e.expect(e.get("/v1/transactions?date=eq.yesterday"), 200, `"count":0`)
	e.expect(e.get("/v1/transactions?amount=like.1"), 400, "requires a text column")
	e.expect(e.get("/v1/transactions?bogus=1"), 400, `unknown query parameter or column "bogus"`)
	e.expect(e.get("/v1/transactions?limit=5000"), 400, "between 1 and 1000")
}

func TestViews(t *testing.T) {
	e := newEnv(t, 0, nil)
	r := e.get("/v1/views/transactions/live?select=transaction_id")
	e.expect(r, 200, `"view":"transactions/live"`)
	if got := ids(r.data(t), "transaction_id"); got != "txn_6,txn_4,txn_3,txn_1,txn_7" {
		t.Errorf("live = %s", got)
	}
	r = e.get("/v1/views/transactions/live?account_id=acc_a1&order_by=amount&select=transaction_id")
	if got := ids(r.data(t), "transaction_id"); got != "txn_4,txn_6,txn_1,txn_3" {
		t.Errorf("live filtered = %s", got)
	}

	r = e.get("/v1/views/accounts")
	e.expect(r, 200, `"item_status":"login_required"`)
	if got := ids(r.data(t), "account_id"); got != "acc_a1,acc_b1" {
		t.Errorf("accounts = %s", got)
	}
	r = e.get("/v1/views/accounts?include_missing=1")
	if got := ids(r.data(t), "account_id"); got != "acc_a1,acc_a2,acc_b1" {
		t.Errorf("accounts incl. missing = %s", got)
	}
	e.expect(e.get("/v1/views/accounts?include_missing=maybe"), 400, "must be 1 or 0")
	e.expect(e.get("/v1/transactions?include_missing=1"), 400, "unknown query parameter")

	r = e.get("/v1/views/sync/status")
	e.expect(r, 200, "")
	rows := r.data(t)
	if len(rows) != 2 {
		t.Fatalf("sync/status rows = %d", len(rows))
	}
	a, b := rows[0], rows[1]
	if a["item_id"] != "item_a" || a["last_run_outcome"] != "success" || a["last_run_request_id"] != "req_2" || a["latest_job_state"] != "succeeded" || fmt.Sprint(a["account_count"]) != "1" {
		t.Errorf("item_a status = %v", a)
	}
	if b["item_id"] != "item_b" || b["last_run_outcome"] != nil || b["latest_job_id"] != nil || fmt.Sprint(b["account_count"]) != "1" || b["status"] != "login_required" {
		t.Errorf("item_b status = %v", b)
	}
	e.expect(e.do("POST", "/v1/views/accounts", writeTok, "{}"), 405, "method not allowed")
}

func TestUpsertAndDelete(t *testing.T) {
	e := newEnv(t, 0, nil)
	post := func(path, body string) resp {
		return e.do("POST", path, writeTok, body, "Content-Type", "application/json")
	}

	r := post("/v1/tx_notes", `{"transaction_id":"txn_1","note":"coffee","tags":["a",{"k":1}],"amount_override":12.5,"due":"2026-10-01","labels":["x","y z"]}`)
	e.expect(r, 200, `"table":"tx_notes","count":1`)
	row := r.data(t)[0]
	if row["amount_override"] != "12.50" || row["due"] != "2026-10-01" || row["note"] != "coffee" {
		t.Errorf("row = %v", row)
	}
	if fmt.Sprint(row["tags"]) != `[a map[k:1]]` || fmt.Sprint(row["labels"]) != `[x y z]` {
		t.Errorf("json/array columns = %v %v", row["tags"], row["labels"])
	}
	first := row["updated_at"].(string)

	time.Sleep(10 * time.Millisecond)
	r = post("/v1/tx_notes", `{"transaction_id":"txn_1","note":"latte"}`)
	e.expect(r, 200, `"note":"latte"`)
	row = r.data(t)[0]
	if row["amount_override"] != "12.50" {
		t.Errorf("upsert clobbered an unsent column: %v", row)
	}
	if row["updated_at"].(string) <= first {
		t.Errorf("updated_at not bumped: %s -> %s", first, row["updated_at"])
	}

	r = post("/v1/tx_notes", `[{"transaction_id":"txn_3","note":"a"},{"transaction_id":"txn_4","note":"b","labels":null}]`)
	e.expect(r, 200, `"count":2`)

	r = post("/v1/tx_notes", `{"transaction_id":"txn_1"}`)
	e.expect(r, 200, `"count":0`) // DO NOTHING on conflict returns no row

	e.expect(post("/v1/tx_notes", `{"transaction_id":"txn_1","nope":1}`), 400, `row 0: unknown column "nope"`)
	e.expect(post("/v1/tx_notes", `{"transaction_id":"txn_1","amount_override":"abc"}`), 400, "row 0: invalid value")
	e.expect(post("/v1/tx_notes", `{"transaction_id":"does_not_exist","note":"x"}`), 422, "row 0: foreign key violation")
	e.expect(post("/v1/tx_notes", `[{"transaction_id":"txn_6"},{"transaction_id":"txn_1","tags":"not json"}]`), 400, "row 1: invalid value")
	e.expect(post("/v1/tx_notes", `not json`), 400, "not valid JSON")
	e.expect(post("/v1/tx_notes", `{"transaction_id":"txn_1"} trailing`), 400, "trailing data")
	e.expect(post("/v1/tx_notes", `[]`), 400, "empty array")
	e.expect(post("/v1/tx_notes", `[1]`), 400, "row 0: not a JSON object")
	e.expect(post("/v1/tx_notes", `"str"`), 400, "must be a JSON object")
	e.expect(post("/v1/tx_notes", `{"transaction_id":"txn_1","note":"`+strings.Repeat("x", 5000)+`"}`), 413, "too large")
	e.expect(post("/v1/transactions", `{"transaction_id":"x"}`), 405, `table "transactions" is read-only`)
	e.expect(e.do("DELETE", "/v1/transactions?transaction_id=x", writeTok, ""), 405, "read-only")

	// The failed batch above must not have inserted txn_6.
	r = e.get("/v1/tx_notes?transaction_id=txn_6")
	e.expect(r, 200, `"count":0`)

	// Identity ALWAYS key: insert only, supplied key is dropped.
	r = post("/v1/events", `{"event_id":999,"kind":"test","payload":{"n":1}}`)
	e.expect(r, 200, `"kind":"test"`)
	if fmt.Sprint(r.data(t)[0]["event_id"]) == "999" {
		t.Error("identity ALWAYS column accepted a caller value")
	}
	r = post("/v1/events", `{"kind":"again"}`)
	e.expect(r, 200, `"count":1`)
	r = e.get("/v1/events?count=exact")
	e.expect(r, 200, `"total":2`)

	// Delete.
	e.expect(e.do("DELETE", "/v1/tx_notes", writeTok, ""), 400, "without a filter")
	e.expect(e.do("DELETE", "/v1/tx_notes?limit=1", writeTok, ""), 400, "without a filter")
	r = e.do("DELETE", "/v1/tx_notes?transaction_id=in.(txn_3,txn_4)", writeTok, "")
	e.expect(r, 200, `"deleted":2`)
	r = e.do("DELETE", "/v1/tx_notes?note=is.null", writeTok, "")
	e.expect(r, 200, `"deleted":0`)
	r = e.get("/v1/tx_notes?select=transaction_id")
	if got := ids(r.data(t), "transaction_id"); got != "txn_1" {
		t.Errorf("after delete = %s", got)
	}
	// FK violation on delete is a 409: events has no dependents, so exercise
	// it through a direct constraint instead.
	e.db.Exec(t, `CREATE TABLE topper.dep (id INT PRIMARY KEY, note_id TEXT REFERENCES topper.tx_notes(transaction_id))`)
	e.db.Exec(t, `INSERT INTO topper.dep VALUES (1, 'txn_1')`)
	e.expect(e.do("DELETE", "/v1/tx_notes?transaction_id=txn_1", writeTok, ""), 409, "foreign key violation")
}

func TestCacheAndConditional(t *testing.T) {
	e := newEnv(t, time.Minute, nil)
	r1 := e.get("/v1/transactions?limit=1")
	e.expect(r1, 200, "")
	etag := r1.header.Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) || !strings.HasPrefix(r1.header.Get("Cache-Control"), "private, max-age=") {
		t.Errorf("headers = %v", r1.header)
	}
	r2 := e.do("GET", "/v1/transactions?limit=1", readTok, "", "If-None-Match", etag)
	if r2.code != 304 || len(r2.body) != 0 {
		t.Errorf("conditional = %d %q", r2.code, r2.body)
	}
	// A different caller shares the entry.
	r3 := e.do("GET", "/v1/transactions?limit=1", writeTok, "", "If-None-Match", etag)
	if r3.code != 304 {
		t.Errorf("shared cache = %d", r3.code)
	}
	// Writable tables are never cached.
	r4 := e.get("/v1/tx_notes")
	if r4.header.Get("Cache-Control") != "no-store" {
		t.Errorf("tx_notes Cache-Control = %q", r4.header.Get("Cache-Control"))
	}
	e.do("POST", "/v1/tx_notes", writeTok, `{"transaction_id":"txn_1","note":"n"}`)
	e.expect(e.get("/v1/tx_notes"), 200, `"note":"n"`)
	// Views are cached too.
	r5 := e.get("/v1/views/accounts")
	if !strings.HasPrefix(r5.header.Get("Cache-Control"), "private, max-age=") {
		t.Errorf("view Cache-Control = %q", r5.header.Get("Cache-Control"))
	}
	// Errors are never cached.
	r6 := e.get("/v1/transactions?bogus=1")
	if r6.header.Get("Cache-Control") != "no-store" {
		t.Errorf("error Cache-Control = %q", r6.header.Get("Cache-Control"))
	}
}

func TestGzipHeadCORS(t *testing.T) {
	e := newEnv(t, 0, nil)
	r := e.do("GET", "/v1/transactions?limit=2", readTok, "", "Accept-Encoding", "gzip")
	if r.header.Get("Content-Encoding") != "gzip" || !strings.Contains(r.header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("gzip headers = %v", r.header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(r.body))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !strings.Contains(string(plain), `"count":2`) {
		t.Errorf("decoded = %s", plain)
	}
	r = e.do("GET", "/v1/transactions?bogus=1", readTok, "", "Accept-Encoding", "gzip")
	if r.header.Get("Content-Encoding") != "" || !strings.Contains(string(r.body), "bogus") {
		t.Errorf("error was compressed: %v %s", r.header, r.body)
	}
	r = e.do("GET", "/v1/transactions?limit=1", readTok, "", "Accept-Encoding", "gzip", "If-None-Match", "*")
	if r.code != 304 || r.header.Get("Content-Encoding") != "" {
		t.Errorf("304 = %d %v", r.code, r.header)
	}

	r = e.do("HEAD", "/v1/transactions?limit=1", readTok, "")
	if r.code != 200 || len(r.body) != 0 || r.header.Get("ETag") == "" {
		t.Errorf("HEAD = %d %q %v", r.code, r.body, r.header)
	}

	r = e.do("OPTIONS", "/v1/transactions", "", "", "Origin", "https://app.example.com", "Access-Control-Request-Method", "GET")
	if r.code != 204 || r.header.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("preflight = %d %v", r.code, r.header)
	}
	r = e.do("GET", "/v1/transactions?limit=1", readTok, "", "Origin", "https://evil.example.com")
	if r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unlisted origin allowed: %v", r.header)
	}
	r = e.do("OPTIONS", "/v1/transactions", "", "", "Origin", "https://evil.example.com", "Access-Control-Request-Method", "GET")
	if r.code != 401 {
		t.Errorf("unlisted preflight = %d", r.code)
	}
}

// TestRestrictedRole models the production grants: a role that owns the
// topper schema and can only SELECT from public. The catalog must load
// under it (information_schema visibility) and writes to public must be
// refused by Postgres regardless of the allowlist.
func TestRestrictedRole(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)
	role := "topper_test_role_" + db.Name[len("topper_test_"):]
	pass := "pw-" + role
	q := testdb.QuoteIdent
	db.Exec(t, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, q(role), pass))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testdb.Timeout)
		defer cancel()
		_, _ = db.Pool.Exec(ctx, `DROP OWNED BY `+q(role))
		_, _ = db.Pool.Exec(ctx, `DROP ROLE `+q(role))
	})
	// The grants from deploy/postgres-init/topper.sql, minus the role
	// creation.
	db.Exec(t, `GRANT USAGE ON SCHEMA public TO `+q(role))
	db.Exec(t, `GRANT SELECT ON ALL TABLES IN SCHEMA public TO `+q(role))
	db.Exec(t, `ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA public GRANT SELECT ON TABLES TO `+q(role))
	db.Exec(t, `ALTER SCHEMA topper OWNER TO `+q(role))
	db.Exec(t, `GRANT ALL ON ALL TABLES IN SCHEMA topper TO `+q(role))
	db.Exec(t, `GRANT ALL ON ALL SEQUENCES IN SCHEMA topper TO `+q(role))
	db.SeedPlaid(t)
	db.CreateConsumerTables(t)
	db.Exec(t, `GRANT ALL ON ALL TABLES IN SCHEMA topper TO `+q(role))
	db.Exec(t, `GRANT ALL ON ALL SEQUENCES IN SCHEMA topper TO `+q(role))

	url := strings.Replace(db.URL, "://", "://"+role+":"+pass+"@", 1)
	url = url[:strings.Index(url, "@")+1] + db.URL[strings.LastIndex(db.URL, "@")+1:]
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	var who string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&who); err != nil || who != role {
		t.Fatalf("connected as %q (%v), want %q", who, err, role)
	}
	_, err = pool.Exec(ctx, `UPDATE public.plaid_items SET status = 'active'`)
	if err == nil || !strings.Contains(err.Error(), "42501") && !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("write to public as restricted role: %v", err)
	}
	var tok []byte
	if err := pool.QueryRow(ctx, `SELECT encrypted_access_token FROM public.plaid_items LIMIT 1`).Scan(&tok); err != nil {
		t.Fatalf("SELECT on public as restricted role: %v", err)
	}

	cat, err := catalog.Load(ctx, pool, catalog.Spec{
		ReadTables:  catalog.PlaidTables,
		WriteTables: []string{"tx_notes", "events"},
		Views:       views.All,
		Denied:      catalog.PlaidDenylist,
	})
	if err != nil {
		t.Fatalf("catalog under restricted role: %v", err)
	}
	tx, _ := cat.Table("transactions")
	if strings.Join(tx.PK, ",") != "transaction_id" {
		t.Errorf("PK under restricted role = %v", tx.PK)
	}

	cfg := &config.Config{
		APIKeys:      []config.APIKey{{Name: "w", Scope: config.ScopeReadWrite, Hash: sha256.Sum256([]byte(writeTok))}},
		QueryTimeout: 10 * time.Second, MaxBodyBytes: 4096,
	}
	s := api.New(api.Deps{Config: cfg, Pool: pool, Catalog: cat, Keyring: auth.NewKeyring(cfg.APIKeys), Cache: cache.New(0, 1), Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	e := &env{t: t, db: db, srv: srv, log: &bytes.Buffer{}}
	e.expect(e.do("GET", "/v1/views/transactions/live?limit=1", writeTok, ""), 200, `"view":"transactions/live","count":1`)
	e.expect(e.do("POST", "/v1/tx_notes", writeTok, `{"transaction_id":"txn_1","note":"ok"}`), 200, `"note":"ok"`)
	e.expect(e.do("POST", "/v1/events", writeTok, `{"kind":"k"}`), 200, `"kind":"k"`)
}

func TestInsightViews(t *testing.T) {
	e := newEnv(t, 0, nil)
	e.db.Exec(t, `INSERT INTO topper.merchant_rules (merchant_key, category) VALUES ('bistro', 'entertainment')`)
	e.db.Exec(t, `INSERT INTO topper.category_overrides (transaction_id, category) VALUES ('txn_3', 'groceries')`)
	e.db.Exec(t, `UPDATE public.transactions SET pfc_confidence = 'VERY_HIGH' WHERE pfc_primary IS NOT NULL`)

	r := e.get("/v1/views/transactions/categorized?select=transaction_id,category,category_source,needs_category,account_name,merchant_key")
	e.expect(r, 200, `"view":"transactions/categorized"`)
	byID := map[string]map[string]any{}
	for _, row := range r.data(t) {
		byID[row["transaction_id"].(string)] = row
	}
	if len(byID) != 5 {
		t.Fatalf("categorized rows = %v", byID)
	}
	for id, want := range map[string]string{
		"txn_1": "dining/plaid/false", "txn_3": "groceries/override/false", "txn_4": "income/plaid/false",
		"txn_6": "other/plaid/true", "txn_7": "entertainment/rule/false",
	} {
		row := byID[id]
		if got := fmt.Sprintf("%v/%v/%v", row["category"], row["category_source"], row["needs_category"]); got != want {
			t.Errorf("%s = %s, want %s", id, got, want)
		}
	}
	if byID["txn_7"]["merchant_key"] != "bistro" || byID["txn_1"]["account_name"] != "Chequing" {
		t.Errorf("txn_7 key %v, txn_1 account %v", byID["txn_7"]["merchant_key"], byID["txn_1"]["account_name"])
	}

	r = e.get("/v1/views/categories/daily?day=eq.2026-09-01")
	e.expect(r, 200, "")
	rows := r.data(t)
	if len(rows) != 2 || rows[0]["category"] != "dining" || rows[0]["amount"] != "12.34" ||
		rows[1]["category"] != "entertainment" || rows[1]["amount"] != "100.00" {
		t.Errorf("daily 2026-09-01 = %v", rows)
	}

	r = e.get("/v1/views/categories/monthly?category=eq.dining")
	if rows = r.data(t); len(rows) != 1 || rows[0]["month"] != "2026-09-01" || rows[0]["amount"] != "12.34" || fmt.Sprint(rows[0]["transactions"]) != "1" {
		t.Errorf("monthly dining = %v", rows)
	}

	r = e.get("/v1/views/merchants/monthly?category=eq.groceries")
	if rows = r.data(t); len(rows) != 1 || rows[0]["merchant"] != "Grocer" || rows[0]["merchant_key"] != "grocer" {
		t.Errorf("merchants groceries = %v", rows)
	}

	// Seeding plaid_accounts fired plaidsync's snapshot trigger.
	r = e.get("/v1/views/balances/daily?select=account_id,current_balance,institution_name")
	if got := ids(r.data(t), "account_id"); got != "acc_a1,acc_a2,acc_b1" {
		t.Errorf("balances/daily accounts = %s", got)
	}

	e.db.Exec(t, `
		INSERT INTO public.plaid_recurring_streams (stream_id, item_id, account_id, direction, description, merchant_name,
			pfc_primary, pfc_detailed, frequency, first_date, last_date, predicted_next_date, average_amount, last_amount,
			iso_currency_code, is_active, status, transaction_ids, raw, removed_at) VALUES
		('s_1', 'item_a', 'acc_a1', 'outflow', 'SPOTIFY', 'Spotify', 'ENTERTAINMENT', 'ENTERTAINMENT_MUSIC_AND_AUDIO',
			'MONTHLY', '2026-01-19', '2026-08-19', '2026-09-19', 11.99, 12.99, NULL, true, 'MATURE', '{t1,t2}', '{}', NULL),
		('s_2', 'item_a', 'acc_a1', 'outflow', 'GONE', NULL, NULL, NULL,
			'MONTHLY', '2026-01-01', '2026-02-01', NULL, 5, 5, 'CAD', false, 'TOMBSTONED', '{}', '{}', now())`)
	r = e.get("/v1/views/recurring/streams")
	if rows = r.data(t); len(rows) != 1 || rows[0]["stream_id"] != "s_1" || rows[0]["category"] != "subscriptions" ||
		rows[0]["last_amount"] != "12.99" || fmt.Sprint(rows[0]["transaction_count"]) != "2" || rows[0]["iso_currency_code"] != "CAD" ||
		rows[0]["source"] != "plaid" {
		t.Errorf("recurring/streams = %v", rows)
	}

	e.db.Exec(t, `INSERT INTO topper.recurring_entries (id, name, amount, frequency, next_date, category, account_id)
		VALUES ('m_1', 'Gym', 45, 'MONTHLY', '2026-10-01', 'health', 'acc_a1'), ('m_2', 'Rent', 1500, 'MONTHLY', '2026-10-01', 'housing', NULL)`)
	r = e.get("/v1/views/recurring/entries?select=id,category_kind,iso_currency_code,account_name,institution_name")
	if rows = r.data(t); len(rows) != 2 || rows[0]["id"] != "m_1" || rows[0]["category_kind"] != "spending" ||
		rows[0]["iso_currency_code"] != "CAD" || rows[0]["account_name"] == nil || rows[1]["account_name"] != nil {
		t.Errorf("recurring/entries = %v", rows)
	}
}

func TestCategoriesTable(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM topper.categories WHERE builtin`).Scan(&n); err != nil || n != 21 {
		t.Fatalf("builtin categories = %d, %v", n, err)
	}
	for _, c := range []struct{ primary, detailed, want string }{
		{"MEDICAL", "MEDICAL_DENTAL_CARE", "medical"},
		{"PERSONAL_CARE", "PERSONAL_CARE_HAIR_AND_BEAUTY", "personal_care"},
		{"GENERAL_SERVICES", "GENERAL_SERVICES_EDUCATION", "education"},
		{"GENERAL_SERVICES", "GENERAL_SERVICES_INSURANCE", "bills"},
		{"BANK_FEES", "BANK_FEES_OVERDRAFT_FEES", "bills"},
		{"GOVERNMENT_AND_NON_PROFIT", "GOVERNMENT_AND_NON_PROFIT_DONATIONS", "gifts"},
		{"GOVERNMENT_AND_NON_PROFIT", "GOVERNMENT_AND_NON_PROFIT_TAX_PAYMENT", "bills"},
		{"INCOME", "INCOME_TAX_REFUND", "tax_refund"},
		{"INCOME", "INCOME_WAGES", "income"},
		{"GENERAL_MERCHANDISE", "GENERAL_MERCHANDISE_PET_SUPPLIES", "kids_pets"},
		{"FOOD_AND_DRINK", "FOOD_AND_DRINK_GROCERIES", "groceries"},
	} {
		var got string
		if err := db.Pool.QueryRow(ctx, `SELECT topper.plaid_category($1, $2)`, c.primary, c.detailed).Scan(&got); err != nil || got != c.want {
			t.Errorf("plaid_category(%s, %s) = %q, %v; want %q", c.primary, c.detailed, got, err, c.want)
		}
	}

	// A custom category can be used everywhere a built-in one can, and a
	// delete cascades to its budget but is refused while rules point at it.
	for _, stmt := range []string{
		`INSERT INTO topper.categories (id, label, color, icon, kind) VALUES ('c_hobby', 'Hobby', '#123456', 'tag', 'spending')`,
		`INSERT INTO topper.budgets (category, monthly_amount) VALUES ('c_hobby', 50)`,
		`INSERT INTO topper.merchant_rules (merchant_key, category) VALUES ('lego', 'c_hobby')`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM topper.categories WHERE id = 'c_hobby'`); err == nil {
		t.Errorf("deleting a category with rules: accepted")
	}
	db.Exec(t, `UPDATE topper.merchant_rules SET category = 'other' WHERE category = 'c_hobby'`)
	db.Exec(t, `DELETE FROM topper.categories WHERE id = 'c_hobby'`)
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM topper.budgets`).Scan(&n); err != nil || n != 0 {
		t.Errorf("budget after category delete = %d, %v", n, err)
	}
}

func TestInsightTablesRejectUnknownCategories(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)
	for _, stmt := range []string{
		`INSERT INTO topper.budgets (category, monthly_amount) VALUES ('income', 10)`,
		`INSERT INTO topper.budgets (category, monthly_amount) VALUES ('dining', -1)`,
		`INSERT INTO topper.budgets (category, monthly_amount) VALUES ('transfer', 10)`,
		`INSERT INTO topper.category_overrides (transaction_id, category) VALUES ('t', 'snacks')`,
		`INSERT INTO topper.merchant_rules (merchant_key, category) VALUES ('', 'dining')`,
		`INSERT INTO topper.merchant_rules (merchant_key, category) VALUES ('x', 'snacks')`,
		`INSERT INTO topper.categories (id, label, color, icon, kind) VALUES ('Bad Id', 'x', '#123456', 'tag', 'spending')`,
		`INSERT INTO topper.categories (id, label, color, icon, kind) VALUES ('c_x', 'x', 'red', 'tag', 'spending')`,
		`INSERT INTO topper.categories (id, label, color, icon, kind) VALUES ('c_y', 'y', '#123456', 'tag', 'fun')`,
		`INSERT INTO topper.recurring_entries (id, name, amount, frequency, next_date, category) VALUES ('r', 'x', 1, 'DAILY', '2026-01-01', 'dining')`,
		`INSERT INTO topper.recurring_entries (id, name, amount, frequency, next_date, category) VALUES ('r', 'x', 1, 'MONTHLY', '2026-01-01', 'nope')`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%s: accepted", stmt)
		}
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO topper.budgets (category, monthly_amount) VALUES ('dining', 600)`); err != nil {
		t.Errorf("valid budget: %v", err)
	}
}
