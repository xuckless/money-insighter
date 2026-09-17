// Package testdb gives database-backed tests a throwaway database that
// looks exactly like one plaidsync left behind, with the topper schema and
// migrations applied on top. It is imported only from _test files.
//
// Tests read TOPPER_TEST_DATABASE_URL and skip when it is unset. The user in
// that URL needs CREATEDB (the plaidsync Compose user is a superuser). Each
// call to [New] creates topper_test_<random> and drops it when the test
// ends, so tests are isolated and can run in parallel.
//
// plaidsync's migrations are applied from its source tree:
// TOPPER_TEST_PLAIDSYNC_MIGRATIONS names the directory, defaulting to
// ../plaid-backend-golang/migrations relative to this module's root.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"postgres-topper/internal/store"
)

const (
	// EnvURL names the admin connection used to create throwaway databases.
	EnvURL = "TOPPER_TEST_DATABASE_URL"
	// EnvPlaidsyncMigrations overrides where plaidsync's migrations are read
	// from.
	EnvPlaidsyncMigrations = "TOPPER_TEST_PLAIDSYNC_MIGRATIONS"

	defaultPlaidsyncMigrations = "../plaid-backend-golang/migrations"

	// Timeout bounds every database call made by the helpers in this
	// package.
	Timeout = 30 * time.Second
)

// DB is one test's private database.
type DB struct {
	// Name is the database name.
	Name string
	// URL connects to the database as the admin user.
	URL string
	// Pool is an open pool on URL.
	Pool *pgxpool.Pool
	// Store is a store.Store on the same database, already migrated.
	Store *store.Store
}

// New creates, migrates and returns a fresh database, or skips the test
// when TOPPER_TEST_DATABASE_URL is unset.
func New(t *testing.T) *DB {
	t.Helper()
	adminURL := os.Getenv(EnvURL)
	if adminURL == "" {
		t.Skipf("%s not set", EnvURL)
	}

	name := "topper_test_" + randomHex(t, 8)
	testURL := withDatabase(t, adminURL, name)

	adminExec(t, adminURL, `CREATE DATABASE `+QuoteIdent(name))
	t.Cleanup(func() {
		adminExec(t, adminURL, `DROP DATABASE `+QuoteIdent(name)+` WITH (FORCE)`)
	})

	ctx := Ctx(t)
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)

	applyPlaidsyncMigrations(t, pool)
	if _, err := pool.Exec(ctx, `CREATE SCHEMA topper`); err != nil {
		t.Fatalf("create topper schema: %v", err)
	}

	st, err := store.Open(ctx, testURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate topper schema: %v", err)
	}
	return &DB{Name: name, URL: testURL, Pool: pool, Store: st}
}

// Ctx returns a context that is canceled when the test ends and that times
// out well before `go test` would.
func Ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	t.Cleanup(cancel)
	return ctx
}

// Exec runs one statement and fails the test on error.
func (d *DB) Exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := d.Pool.Exec(Ctx(t), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// CreateConsumerTables creates two writable tables in schema topper that
// exercise every projection kind and both primary-key styles:
//
//   - topper.tx_notes: text primary key supplied by the caller, jsonb,
//     numeric, date and a trigger-maintained updated_at.
//   - topper.events: identity-ALWAYS bigint primary key, so rows can be
//     inserted but never upserted through the API.
func (d *DB) CreateConsumerTables(t *testing.T) {
	t.Helper()
	d.Exec(t, `
		CREATE TABLE topper.tx_notes (
			transaction_id  TEXT PRIMARY KEY REFERENCES public.transactions(transaction_id),
			note            TEXT,
			tags            JSONB NOT NULL DEFAULT '[]'::jsonb,
			amount_override NUMERIC(14,2),
			due             DATE,
			labels          TEXT[],
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	d.Exec(t, `
		CREATE TRIGGER tx_notes_set_updated_at BEFORE UPDATE ON topper.tx_notes
		FOR EACH ROW EXECUTE FUNCTION topper.set_updated_at()`)
	d.Exec(t, `
		CREATE TABLE topper.events (
			event_id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			kind       TEXT NOT NULL,
			payload    JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
}

// SeedPlaid inserts a small, deterministic plaidsync data set:
//
//   - items item_a (active, institution "Bank A") and item_b
//     (login_required, institution "Bank B")
//   - accounts acc_a1 and acc_a2 on item_a, acc_b1 on item_b; acc_a2 has
//     missing_since set
//   - transactions txn_1..txn_6 on acc_a1 (txn_5 removed, txn_2 pending and
//     superseded by txn_3), txn_7 on acc_b1
//   - one sync job and two sync runs for item_a, none for item_b
func (d *DB) SeedPlaid(t *testing.T) {
	t.Helper()
	d.Exec(t, `
		INSERT INTO public.plaid_items (item_id, institution_id, institution_name, encrypted_access_token, key_version, status, last_successful_sync_at, raw) VALUES
		('item_a', 'ins_a', 'Bank A', '\x00'::bytea, 1, 'active', '2026-09-01T10:00:00Z', '{"item_id":"item_a"}'),
		('item_b', 'ins_b', 'Bank B', '\x00'::bytea, 1, 'login_required', NULL, '{"item_id":"item_b"}')`)
	d.Exec(t, `
		INSERT INTO public.plaid_accounts (account_id, item_id, name, mask, type, subtype, current_balance, available_balance, credit_limit, iso_currency_code, raw, missing_since) VALUES
		('acc_a1', 'item_a', 'Chequing', '1234', 'depository', 'checking', 1500.25, 1400.00, NULL, 'CAD', '{"account_id":"acc_a1"}', NULL),
		('acc_a2', 'item_a', 'Old Savings', '5678', 'depository', 'savings', 10.00, 10.00, NULL, 'CAD', '{"account_id":"acc_a2"}', '2026-08-01T00:00:00Z'),
		('acc_b1', 'item_b', 'Amex', '9999', 'credit', 'credit card', -250.50, NULL, 5000.00, 'CAD', '{"account_id":"acc_b1"}', NULL)`)
	d.Exec(t, `
		INSERT INTO public.transactions (transaction_id, account_id, item_id, amount, iso_currency_code, date, authorized_date, datetime, name, merchant_name, pending, pending_transaction_id, pfc_primary, payment_channel, raw, superseded_by, superseded_at, removed_at) VALUES
		('txn_1', 'acc_a1', 'item_a',  12.34, 'CAD', '2026-09-01', '2026-08-31', '2026-09-01T12:00:00Z', 'Coffee',   'Cafe',    false, NULL,    'FOOD_AND_DRINK', 'in store', '{"transaction_id":"txn_1","amount":12.34,"big":12345678901234567890}', NULL, NULL, NULL),
		('txn_2', 'acc_a1', 'item_a',  50.00, 'CAD', '2026-09-02', NULL,         NULL,                   'Grocer',   'Grocer',  true,  NULL,    'FOOD_AND_DRINK', 'in store', '{"transaction_id":"txn_2"}', 'txn_3', '2026-09-03T00:00:00Z', NULL),
		('txn_3', 'acc_a1', 'item_a',  50.00, 'CAD', '2026-09-02', '2026-09-02', NULL,                   'Grocer',   'Grocer',  false, 'txn_2', 'FOOD_AND_DRINK', 'in store', '{"transaction_id":"txn_3"}', NULL, NULL, NULL),
		('txn_4', 'acc_a1', 'item_a', -1000.00, 'CAD', '2026-09-03', NULL,       NULL,                   'Payroll',  NULL,      false, NULL,    'INCOME',         'other',    '{"transaction_id":"txn_4"}', NULL, NULL, NULL),
		('txn_5', 'acc_a1', 'item_a',   9.99, 'CAD', '2026-09-04', NULL,         NULL,                   'Refunded', 'Shop',    false, NULL,    'GENERAL_MERCHANDISE', 'online', '{"transaction_id":"txn_5"}', NULL, NULL, '2026-09-05T00:00:00Z'),
		('txn_6', 'acc_a1', 'item_a',   0.01, 'CAD', '2026-09-05', NULL,         NULL,                   'Penny',    NULL,      false, NULL,    NULL,             'other',    '{"transaction_id":"txn_6"}', NULL, NULL, NULL),
		('txn_7', 'acc_b1', 'item_b', 100.00, 'CAD', '2026-09-01', NULL,         NULL,                   'Dinner',   'Bistro',  false, NULL,    'FOOD_AND_DRINK', 'in store', '{"transaction_id":"txn_7"}', NULL, NULL, NULL)`)
	d.Exec(t, `
		INSERT INTO public.sync_jobs (job_id, item_id, kind, state, created_at, started_at, finished_at) VALUES
		('11111111-1111-4111-8111-111111111111', 'item_a', 'manual', 'succeeded', '2026-09-01T09:59:00Z', '2026-09-01T09:59:30Z', '2026-09-01T10:00:00Z')`)
	d.Exec(t, `
		INSERT INTO public.sync_runs (item_id, job_id, trigger, started_at, finished_at, cursor_before, cursor_after, pages, added, modified, removed, inserted, updated, superseded, accounts_seen, accounts_missing, outcome, request_id) VALUES
		('item_a', NULL, 'scheduled', '2026-08-31T10:00:00Z', '2026-08-31T10:00:05Z', NULL, 'c1', 1, 3, 0, 0, 3, 0, 0, 2, 0, 'success', 'req_1'),
		('item_a', '11111111-1111-4111-8111-111111111111', 'manual', '2026-09-01T09:59:30Z', '2026-09-01T10:00:00Z', 'c1', 'c2', 2, 3, 1, 1, 3, 1, 1, 2, 1, 'success', 'req_2')`)
}

// applyPlaidsyncMigrations runs plaidsync's goose migrations from its
// source tree, using goose's default version table (public.goose_db_version)
// so the database is indistinguishable from one plaidsync migrated itself.
func applyPlaidsyncMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	dir := plaidsyncMigrationsDir(t)
	db := stdlib.OpenDBFromPool(pool)
	defer func(db *sql.DB) { _ = db.Close() }(db)
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir))
	if err != nil {
		t.Fatalf("goose provider for %s: %v", dir, err)
	}
	if _, err := p.Up(Ctx(t)); err != nil {
		t.Fatalf("apply plaidsync migrations from %s: %v", dir, err)
	}
}

// plaidsyncMigrationsDir resolves the plaidsync migrations directory: the
// override variable if set, else ../plaid-backend-golang/migrations relative
// to this module's root (found by walking up from the working directory to
// the directory holding go.mod).
func plaidsyncMigrationsDir(t *testing.T) string {
	t.Helper()
	if v := os.Getenv(EnvPlaidsyncMigrations); v != "" {
		return v
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			p := filepath.Join(dir, defaultPlaidsyncMigrations)
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("plaidsync migrations not found at %s; set %s", p, EnvPlaidsyncMigrations)
			}
			return p
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("go.mod not found above %s", wd)
		}
	}
}

// adminExec runs one statement on a fresh administrative connection. It is
// used for CREATE/DROP DATABASE, which cannot run inside a transaction or
// on the database being dropped.
func adminExec(t *testing.T, adminURL, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to admin database: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// withDatabase returns adminURL pointing at dbName instead of its own
// database.
func withDatabase(t *testing.T, adminURL, dbName string) string {
	t.Helper()
	if !strings.HasPrefix(adminURL, "postgres://") && !strings.HasPrefix(adminURL, "postgresql://") {
		t.Fatalf("%s must be a postgres:// URL", EnvURL)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvURL, err)
	}
	u.Path = "/" + dbName
	return u.String()
}

// randomHex returns n random bytes as lowercase hex.
func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b)
}

// QuoteIdent double-quotes an identifier for interpolation into DDL.
func QuoteIdent(s string) string {
	return fmt.Sprintf(`"%s"`, strings.ReplaceAll(s, `"`, `""`))
}
