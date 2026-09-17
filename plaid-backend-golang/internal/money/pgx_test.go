package money

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// testConn connects to the database named by PLAIDSYNC_TEST_DATABASE_URL, or
// skips the test when it is unset. The tests below only use SELECTs and a
// session-local temporary table, so they cannot interfere with other tests
// sharing the database.
func testConn(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("PLAIDSYNC_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PLAIDSYNC_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// TestPgxNumericRoundTrip proves the driver contract end to end: an Amount
// passed as a query argument lands in NUMERIC(14,2) exactly, and scanning it
// back yields the identical Amount in both the binary (pgx default) and text
// result formats.
func TestPgxNumericRoundTrip(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE money_test (id int PRIMARY KEY, amount numeric(14,2), nullable numeric(14,2))`); err != nil {
		t.Fatalf("create temp table: %v", err)
	}

	values := []string{"0", "12.34", "-0.01", "1500", "9999999999.99", "-999999999999.99", "0.1", "-1"}
	for i, v := range values {
		if _, err := conn.Exec(ctx, `INSERT INTO money_test (id, amount, nullable) VALUES ($1, $2, NULL)`, i, MustParse(v)); err != nil {
			t.Fatalf("insert %q: %v", v, err)
		}
	}

	formats := map[string]pgx.QueryResultFormats{
		"binary": {pgx.BinaryFormatCode},
		"text":   {pgx.TextFormatCode},
	}
	for name, format := range formats {
		t.Run(name, func(t *testing.T) {
			for i, v := range values {
				var got Amount
				var nullable *Amount
				var asText string
				err := conn.QueryRow(ctx, `SELECT amount, nullable, amount::text FROM money_test WHERE id = $1`, format, i).Scan(&got, &nullable, &asText)
				if err != nil {
					t.Fatalf("select %q: %v", v, err)
				}
				if want := MustParse(v); got != want {
					t.Errorf("round trip of %q via NUMERIC gave %q (postgres text %q)", v, got, asText)
				}
				if nullable != nil {
					t.Errorf("NULL scanned into *Amount gave %q, want nil", *nullable)
				}
			}
		})
	}
}

// TestPgxNumericScanForms covers the other things Postgres can hand back for
// a NUMERIC: values from literals, NULL into a non-pointer target, and the
// special values NaN/Infinity, which must be rejected rather than silently
// mapped to something.
func TestPgxNumericScanForms(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	var a Amount
	if err := conn.QueryRow(ctx, `SELECT 12.340::numeric`).Scan(&a); err != nil {
		t.Fatalf("scan literal: %v", err)
	}
	if a.String() != "12.34" {
		t.Errorf("SELECT 12.340::numeric scanned as %q, want 12.34", a)
	}

	if err := conn.QueryRow(ctx, `SELECT 1e10::numeric`).Scan(&a); err != nil {
		t.Fatalf("scan 1e10: %v", err)
	}
	if a.String() != "10000000000" {
		t.Errorf("SELECT 1e10::numeric scanned as %q, want 10000000000", a)
	}

	if err := conn.QueryRow(ctx, `SELECT NULL::numeric`).Scan(&a); err == nil {
		t.Error("scanning NULL into a non-pointer Amount succeeded, want error")
	}

	for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
		if err := conn.QueryRow(ctx, `SELECT $1::numeric`, special).Scan(&a); err == nil {
			t.Errorf("scanning %s succeeded as %q, want error", special, a)
		}
	}

	// A float8 column must not silently flow into an Amount.
	if err := conn.QueryRow(ctx, `SELECT 12.34::float8`).Scan(&a); err == nil {
		t.Errorf("scanning float8 into Amount succeeded as %q, want error", a)
	}

	// Integer columns are fine (pgx hands a sql.Scanner an int64).
	if err := conn.QueryRow(ctx, `SELECT -42::bigint`).Scan(&a); err != nil {
		t.Fatalf("scan bigint: %v", err)
	}
	if a.String() != "-42" {
		t.Errorf("SELECT -42::bigint scanned as %q, want -42", a)
	}
}
