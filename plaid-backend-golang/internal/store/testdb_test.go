package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// testDatabaseURLEnv names the admin connection used to create throwaway
// databases. The user in that URL needs CREATEDB (the Compose user is a
// superuser).
const testDatabaseURLEnv = "PLAIDSYNC_TEST_DATABASE_URL"

// testTimeout bounds every database call made by the helpers in this file.
const testTimeout = 30 * time.Second

// testCtx returns a context that is canceled when the test ends and that
// times out well before `go test` would.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// newTestStore gives the test its own freshly migrated database.
//
// It reads PLAIDSYNC_TEST_DATABASE_URL (skipping the test when unset),
// connects to that database as an administrative session, creates
// plaidsync_test_<random hex>, opens a Store on it through Open, runs
// Migrate, and registers cleanup that closes the Store and then drops the
// database WITH (FORCE). Because every call gets its own database, tests
// can run in parallel with each other and with other packages.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	adminURL := os.Getenv(testDatabaseURLEnv)
	if adminURL == "" {
		t.Skipf("%s not set", testDatabaseURLEnv)
	}

	dbName := "plaidsync_test_" + randomHex(t, 8)
	testURL := withDatabase(t, adminURL, dbName)

	adminExec(t, adminURL, `CREATE DATABASE `+quoteIdent(dbName))
	t.Cleanup(func() {
		adminExec(t, adminURL, `DROP DATABASE `+quoteIdent(dbName)+` WITH (FORCE)`)
	})

	ctx := testCtx(t)
	s, err := Open(ctx, testURL)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(s.Close)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return s
}

// seedItem inserts an active item with a dummy credential straight into
// plaid_items with raw SQL, so tests of other tables have an item to hang
// rows off without depending on UpsertItem. Seeding the same id twice
// leaves one active row.
func seedItem(t *testing.T, s *Store, itemID string) {
	t.Helper()
	ctx := testCtx(t)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO plaid_items (item_id, institution_id, institution_name, encrypted_access_token, key_version, raw)
		VALUES ($1, 'ins_test', 'Test Institution', $2, 1, '{"item_id": "seeded"}'::jsonb)
		ON CONFLICT (item_id) DO UPDATE SET
			status = 'active',
			encrypted_access_token = EXCLUDED.encrypted_access_token,
			key_version = EXCLUDED.key_version,
			last_error_code = NULL, last_error_type = NULL, last_error_message = NULL, last_error_at = NULL`,
		itemID, []byte("dummy-ciphertext:"+itemID))
	if err != nil {
		t.Fatalf("seed item %q: %v", itemID, err)
	}
}

// adminExec runs one statement on a fresh administrative connection. It is
// used for CREATE/DROP DATABASE, which cannot run inside a transaction or
// on the database being dropped.
func adminExec(t *testing.T, adminURL, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
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
// database. The test URL is required to be in postgres:// form (that is
// what the contract and Makefile use), so the path is simply replaced.
func withDatabase(t *testing.T, adminURL, dbName string) string {
	t.Helper()
	if !strings.HasPrefix(adminURL, "postgres://") && !strings.HasPrefix(adminURL, "postgresql://") {
		t.Fatalf("%s must be a postgres:// URL", testDatabaseURLEnv)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLEnv, err)
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

// quoteIdent double-quotes an identifier for interpolation into DDL, which
// takes no parameters. The names built here are [a-z0-9_] only, so this is
// belt and braces.
func quoteIdent(s string) string {
	return fmt.Sprintf(`"%s"`, strings.ReplaceAll(s, `"`, `""`))
}
