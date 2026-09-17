package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestMigrateIdempotent runs Migrate on an already-migrated database (the
// helper ran it once) and expects no error and the same version.
func TestMigrateIdempotent(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	for i := 0; i < 2; i++ {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("Migrate run %d: %v", i+2, err)
		}
	}
	v, err := s.MigrationVersion(ctx)
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if v != 1 {
		t.Fatalf("MigrationVersion = %d, want 1", v)
	}
}

// TestMigrationVersionBeforeMigrate proves the version is 0 on a database
// that has no migrations applied, without needing Migrate first.
func TestMigrationVersionBeforeMigrate(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	// Roll the schema back with raw DDL and drop goose's table so this is a
	// genuinely fresh database as far as goose is concerned.
	for _, stmt := range []string{
		`DROP TABLE sync_runs, sync_jobs, transactions, plaid_accounts, plaid_items`,
		`DROP FUNCTION plaidsync_set_updated_at()`,
		`DROP TABLE goose_db_version`,
	} {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	v, err := s.MigrationVersion(ctx)
	if err != nil {
		t.Fatalf("MigrationVersion on empty database: %v", err)
	}
	if v != 0 {
		t.Fatalf("MigrationVersion on empty database = %d, want 0", v)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate after reset: %v", err)
	}
	if v, err = s.MigrationVersion(ctx); err != nil || v != 1 {
		t.Fatalf("MigrationVersion after re-migrate = %d, %v; want 1, nil", v, err)
	}
}

// TestMigrateDownUp exercises the Down section of every migration through
// goose itself: rolling all the way back must leave nothing behind, and
// migrating up again must succeed from that state.
func TestMigrateDownUp(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	p, db, err := s.gooseProvider()
	if err != nil {
		t.Fatalf("gooseProvider: %v", err)
	}
	defer closeQuietly(db)

	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0): %v", err)
	}
	var leftover int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'goose_db_version'`).Scan(&leftover); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if leftover != 0 {
		t.Errorf("%d tables left after DownTo(0), want 0", leftover)
	}
	var functions int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_proc WHERE proname = 'plaidsync_set_updated_at'`).Scan(&functions); err != nil {
		t.Fatalf("count functions: %v", err)
	}
	if functions != 0 {
		t.Errorf("plaidsync_set_updated_at still exists after DownTo(0)")
	}
	if v, err := s.MigrationVersion(ctx); err != nil || v != 0 {
		t.Fatalf("MigrationVersion after DownTo(0) = %d, %v; want 0, nil", v, err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate after DownTo(0): %v", err)
	}
	if v, err := s.MigrationVersion(ctx); err != nil || v != 1 {
		t.Fatalf("MigrationVersion after re-migrate = %d, %v; want 1, nil", v, err)
	}
}

// TestMigrateCreatesTables checks that every table the store depends on
// exists, with the updated_at trigger function alongside.
func TestMigrateCreatesTables(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	for _, table := range []string{"plaid_items", "plaid_accounts", "transactions", "sync_jobs", "sync_runs"} {
		var exists bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("query information_schema for %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after Migrate", table)
		}
	}

	var triggers int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.triggers WHERE trigger_name LIKE '%_set_updated_at'`).Scan(&triggers); err != nil {
		t.Fatalf("count triggers: %v", err)
	}
	if triggers != 3 {
		t.Errorf("found %d *_set_updated_at triggers, want 3", triggers)
	}
}

// TestUpdatedAtTrigger proves the plpgsql function survived goose's
// statement splitting and actually fires.
func TestUpdatedAtTrigger(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_trigger")

	var before, after string
	if err := s.pool.QueryRow(ctx, `SELECT updated_at::text FROM plaid_items WHERE item_id = $1`, "item_trigger").Scan(&before); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	// Move updated_at into the past so a trigger-driven now() is visibly later.
	if _, err := s.pool.Exec(ctx, `UPDATE plaid_items SET updated_at = now() - interval '1 day' WHERE item_id = $1`, "item_trigger"); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE plaid_items SET institution_name = 'Renamed' WHERE item_id = $1`, "item_trigger"); err != nil {
		t.Fatalf("update institution_name: %v", err)
	}
	var stale bool
	if err := s.pool.QueryRow(ctx, `SELECT updated_at < now() - interval '1 hour' FROM plaid_items WHERE item_id = $1`, "item_trigger").Scan(&stale); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if stale {
		t.Errorf("updated_at was not refreshed by the trigger (was %s)", before)
	}
	if err := s.pool.QueryRow(ctx, `SELECT updated_at::text FROM plaid_items WHERE item_id = $1`, "item_trigger").Scan(&after); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	if after == before {
		t.Errorf("updated_at unchanged across an UPDATE: %s", after)
	}
}

// TestCredentialCheckConstraint proves the database, not just the Go code,
// enforces "removed if and only if the credential is NULL" and "credential
// and key_version are NULL together".
func TestCredentialCheckConstraint(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)
	seedItem(t, s, "item_check")

	cases := []struct {
		name       string
		sql        string
		constraint string
	}{
		{
			name:       "active item with NULL credential",
			sql:        `UPDATE plaid_items SET encrypted_access_token = NULL, key_version = NULL WHERE item_id = $1`,
			constraint: "plaid_items_credential_check",
		},
		{
			name:       "removed item keeping its credential",
			sql:        `UPDATE plaid_items SET status = 'removed' WHERE item_id = $1`,
			constraint: "plaid_items_credential_check",
		},
		{
			name:       "credential without key_version",
			sql:        `UPDATE plaid_items SET key_version = NULL WHERE item_id = $1`,
			constraint: "plaid_items_key_version_check",
		},
		{
			name:       "unknown status",
			sql:        `UPDATE plaid_items SET status = 'bogus' WHERE item_id = $1`,
			constraint: "plaid_items_status_check",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.pool.Exec(ctx, tc.sql, "item_check")
			if err == nil {
				t.Fatalf("violating UPDATE succeeded, want %s to reject it", tc.constraint)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("error is %T (%v), want *pgconn.PgError", err, err)
			}
			if pgErr.Code != "23514" { // check_violation
				t.Errorf("SQLSTATE = %s, want 23514 (check_violation): %v", pgErr.Code, err)
			}
			if pgErr.ConstraintName != tc.constraint {
				t.Errorf("constraint = %q, want %q", pgErr.ConstraintName, tc.constraint)
			}
		})
	}

	// The legal transition still works: removed together with NULLs.
	if _, err := s.pool.Exec(ctx,
		`UPDATE plaid_items SET status = 'removed', encrypted_access_token = NULL, key_version = NULL WHERE item_id = $1`,
		"item_check"); err != nil {
		t.Fatalf("legal removal rejected: %v", err)
	}
}

// TestOpenRejectsBadURL checks that Open fails cleanly, and without echoing
// the credential, when the URL cannot be parsed or the server is not there.
// Parse failures must not echo any part of the URL at all: pgx's own parse
// error prints the connection string (password masked, everything else in
// the clear) and quotes the fragment it choked on.
func TestOpenRejectsBadURL(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	const password = "s3cr3t-password-value"
	// marker is planted in every non-password position a parse error could
	// echo: user, host, database and the malformed query parameter.
	const marker = "zq9marker"
	cases := []struct {
		url   string
		parse bool // a parse failure, so ErrInvalidDatabaseURL and no URL fragment
	}{
		{"postgres://plaidsync:" + password + "@127.0.0.1:1/plaidsync?sslmode=disable&connect_timeout=1", false},
		{"host=127.0.0.1 port=1 user=plaidsync password=" + password + " dbname=plaidsync sslmode=disable connect_timeout=1", false},
		{"postgres://" + marker + ":" + password + "@" + marker + ".invalid:1/" + marker + "?pool_max_conns=" + marker, true},
		{"postgres://" + marker + ":" + password + "@" + marker + ".invalid:1/" + marker + "?sslmode=" + marker, true},
		{"postgres://" + marker + ":" + password + "@" + marker + ".invalid:notaport/" + marker, true},
		{"user=" + marker + " password=" + password + " host=" + marker + ".invalid " + marker, true},
		{marker + password, true},
	}
	for _, tc := range cases {
		s, err := Open(ctx, tc.url)
		if err == nil {
			s.Close()
			t.Errorf("Open(%q) succeeded, want error", tc.url)
			continue
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, strings.ToLower(password)) {
			t.Errorf("Open error leaks the password: %s", msg)
		}
		if !tc.parse {
			continue
		}
		if !errors.Is(err, ErrInvalidDatabaseURL) {
			t.Errorf("Open(%q) = %v, want ErrInvalidDatabaseURL", tc.url, err)
		}
		if strings.Contains(msg, marker) {
			t.Errorf("Open parse error echoes part of the URL: %s", msg)
		}
	}
}

// TestPingAndClose exercises Ping on a live store and checks Close is safe
// to call twice.
func TestPingAndClose(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	s.Close()
	s.Close()
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping after Close succeeded, want error")
	}
}

// TestWithTx checks the helper commits on nil, rolls back on error, and
// still rolls back when the caller's context is already dead.
func TestWithTx(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := testCtx(t)

	countItems := func() int {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM plaid_items`).Scan(&n); err != nil {
			t.Fatalf("count items: %v", err)
		}
		return n
	}
	insert := func(ctx context.Context, tx querier, id string) error {
		_, err := tx.Exec(ctx, `INSERT INTO plaid_items (item_id, encrypted_access_token, key_version) VALUES ($1, '\x00'::bytea, 1)`, id)
		return err
	}

	if err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error { return insert(ctx, tx, "committed") }); err != nil {
		t.Fatalf("withTx commit path: %v", err)
	}
	if n := countItems(); n != 1 {
		t.Fatalf("after commit: %d items, want 1", n)
	}

	boom := errors.New("boom")
	err := s.withTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := insert(ctx, tx, "rolled_back"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("withTx returned %v, want %v", err, boom)
	}
	if n := countItems(); n != 1 {
		t.Fatalf("after rollback: %d items, want 1", n)
	}

	canceled, cancel := context.WithCancel(ctx)
	err = s.withTx(canceled, func(ctx context.Context, tx pgx.Tx) error {
		if err := insert(ctx, tx, "canceled"); err != nil {
			return err
		}
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("withTx with canceled context returned %v, want context.Canceled", err)
	}
	if n := countItems(); n != 1 {
		t.Fatalf("after canceled tx: %d items, want 1", n)
	}
	// The pool must still be healthy afterwards.
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping after canceled tx: %v", err)
	}
}
