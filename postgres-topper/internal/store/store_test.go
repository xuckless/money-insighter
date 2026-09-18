package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"postgres-topper/internal/store"
	"postgres-topper/internal/testdb"
)

func TestOpenRejectsBadURL(t *testing.T) {
	_, err := store.Open(context.Background(), "postgres://[::1")
	if !errors.Is(err, store.ErrInvalidDatabaseURL) {
		t.Fatalf("err = %v, want ErrInvalidDatabaseURL", err)
	}
}

func TestMigrateUsesTopperVersionTable(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)

	v, err := db.Store.MigrationVersion(ctx)
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if v != 3 {
		t.Errorf("version = %d, want 3", v)
	}

	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM topper.goose_db_version WHERE version_id = 1`).Scan(&count); err != nil {
		t.Fatalf("topper.goose_db_version: %v", err)
	}
	if count != 1 {
		t.Errorf("topper.goose_db_version rows for version 1 = %d", count)
	}

	// plaidsync's own version table must not have seen the topper migration.
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.goose_db_version WHERE version_id = 1`).Scan(&count); err != nil {
		t.Fatalf("public.goose_db_version: %v", err)
	}
	if count != 1 {
		t.Errorf("public.goose_db_version rows for version 1 = %d, want exactly plaidsync's", count)
	}

	var fn string
	if err := db.Pool.QueryRow(ctx, `SELECT to_regprocedure('topper.set_updated_at()')::text`).Scan(&fn); err != nil {
		t.Fatalf("set_updated_at: %v", err)
	}

	// Idempotent.
	if err := db.Store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestRequireTopperSchema(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)
	if err := db.Store.RequireTopperSchema(ctx); err != nil {
		t.Fatalf("RequireTopperSchema: %v", err)
	}
	db.Exec(t, `DROP SCHEMA topper CASCADE`)
	if err := db.Store.RequireTopperSchema(ctx); !errors.Is(err, store.ErrNoTopperSchema) {
		t.Fatalf("err = %v, want ErrNoTopperSchema", err)
	}
}

func TestWaitForPlaidSchema(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)
	if err := db.Store.WaitForPlaidSchema(ctx, 0); err != nil {
		t.Fatalf("with plaid tables present: %v", err)
	}

	db.Exec(t, `DROP TABLE public.plaid_recurring_streams CASCADE`)
	start := time.Now()
	err := db.Store.WaitForPlaidSchema(ctx, 1500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not present after") {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 1400*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("waited %v, want about 1.5s", elapsed)
	}
}

func TestClassify(t *testing.T) {
	pg := func(code string) error { return &pgconn.PgError{Code: code} }
	cases := []struct {
		err  error
		want store.Kind
	}{
		{nil, store.KindOther},
		{errors.New("x"), store.KindOther},
		{context.Canceled, store.KindCanceled},
		{context.DeadlineExceeded, store.KindTimeout},
		{pg("23505"), store.KindUnique},
		{pg("23503"), store.KindForeignKey},
		{pg("23502"), store.KindNotNull},
		{pg("23514"), store.KindCheck},
		{pg("23P01"), store.KindIntegrity},
		{pg("22P02"), store.KindData},
		{pg("22003"), store.KindData},
		{pg("42883"), store.KindUndefinedOp},
		{pg("42804"), store.KindUndefinedOp},
		{pg("42501"), store.KindPrivilege},
		{pg("57014"), store.KindTimeout},
		{pg("42P01"), store.KindOther},
	}
	for _, tc := range cases {
		if got, _ := store.Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
