// Package storetest gives tests outside package store a freshly migrated
// throwaway database. It mirrors the helper package store uses for its
// own tests: PLAIDSYNC_TEST_DATABASE_URL names an administrative
// connection whose user can CREATE DATABASE; each call creates
// plaidsync_test_<random>, opens a Store on it, runs the migrations and
// drops the database when the test ends. Tests skip when the variable is
// unset, so `go test ./...` passes without Postgres.
package storetest

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

	"plaidsync/internal/store"
)

// EnvVar names the administrative database URL.
const EnvVar = "PLAIDSYNC_TEST_DATABASE_URL"

// Timeout bounds every database call made by the helpers.
const Timeout = 30 * time.Second

// Ctx returns a context that is canceled when the test ends and that times
// out well before `go test` would.
func Ctx(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	t.Cleanup(cancel)
	return ctx
}

// New gives the test its own freshly migrated database, or skips the test
// when PLAIDSYNC_TEST_DATABASE_URL is unset.
func New(t testing.TB) *store.Store {
	t.Helper()
	adminURL := os.Getenv(EnvVar)
	if adminURL == "" {
		t.Skipf("%s not set", EnvVar)
	}
	dbName := "plaidsync_test_" + randomHex(t, 8)
	testURL := withDatabase(t, adminURL, dbName)

	adminExec(t, adminURL, `CREATE DATABASE `+quoteIdent(dbName))
	t.Cleanup(func() {
		adminExec(t, adminURL, `DROP DATABASE `+quoteIdent(dbName)+` WITH (FORCE)`)
	})

	ctx := Ctx(t)
	s, err := store.Open(ctx, testURL)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return s
}

func adminExec(t testing.TB, adminURL, sql string) {
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

func withDatabase(t testing.TB, adminURL, dbName string) string {
	t.Helper()
	if !strings.HasPrefix(adminURL, "postgres://") && !strings.HasPrefix(adminURL, "postgresql://") {
		t.Fatalf("%s must be a postgres:// URL", EnvVar)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + dbName
	return u.String()
}

func randomHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b)
}

func quoteIdent(s string) string {
	return fmt.Sprintf(`"%s"`, strings.ReplaceAll(s, `"`, `""`))
}
