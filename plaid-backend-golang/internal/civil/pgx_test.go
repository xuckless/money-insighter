package civil

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

// TestPgxDateRoundTrip proves the driver contract end to end: a Date passed
// as a query argument lands in a DATE column unchanged and scans back as the
// same Date in both the binary (pgx default) and text result formats, with
// the session time zone deliberately set to a negative-offset zone so a zone
// bug would show up as an off-by-one-day.
func TestPgxDateRoundTrip(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	if _, err := conn.Exec(ctx, `SET TIME ZONE 'America/Vancouver'`); err != nil {
		t.Fatalf("set time zone: %v", err)
	}
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE civil_test (id int PRIMARY KEY, d date NOT NULL, nullable date)`); err != nil {
		t.Fatalf("create temp table: %v", err)
	}

	values := []string{"2024-01-01", "2024-01-02", "2024-03-31", "2024-02-29", "2023-12-31", "0001-01-01", "9999-12-31"}
	for i, v := range values {
		if _, err := conn.Exec(ctx, `INSERT INTO civil_test (id, d, nullable) VALUES ($1, $2, NULL)`, i, MustParseDate(v)); err != nil {
			t.Fatalf("insert %s: %v", v, err)
		}
	}

	formats := map[string]pgx.QueryResultFormats{
		"binary": {pgx.BinaryFormatCode},
		"text":   {pgx.TextFormatCode},
	}
	for name, format := range formats {
		t.Run(name, func(t *testing.T) {
			for i, v := range values {
				var got Date
				var nullable *Date
				var asText string
				var asTime time.Time
				err := conn.QueryRow(ctx, `SELECT d, nullable, d::text, d FROM civil_test WHERE id = $1`, format, i).Scan(&got, &nullable, &asText, &asTime)
				if err != nil {
					t.Fatalf("select %s: %v", v, err)
				}
				if got != MustParseDate(v) {
					t.Errorf("round trip of %s via DATE gave %v (postgres text %q)", v, got, asText)
				}
				if nullable != nil {
					t.Errorf("NULL scanned into *Date gave %v, want nil", *nullable)
				}
				// The assumption Scan is built on: pgx materialises a DATE as
				// midnight UTC, whatever the session time zone says.
				if asTime.Location() != time.UTC || asTime.Hour() != 0 || asTime.Minute() != 0 {
					t.Errorf("pgx decoded DATE %s as %v, expected midnight UTC", v, asTime)
				}
				if DateOf(asTime) != MustParseDate(v) {
					t.Errorf("pgx decoded DATE %s as %v, whose UTC date is %v", v, asTime, DateOf(asTime))
				}
			}
		})
	}
}

// TestPgxDateScanForms covers what else Postgres can hand back for a DATE.
func TestPgxDateScanForms(t *testing.T) {
	conn := testConn(t)
	ctx := context.Background()

	var d Date
	if err := conn.QueryRow(ctx, `SELECT DATE '2024-03-31'`).Scan(&d); err != nil {
		t.Fatalf("scan literal: %v", err)
	}
	if d != MustParseDate("2024-03-31") {
		t.Errorf("DATE '2024-03-31' scanned as %v", d)
	}

	if err := conn.QueryRow(ctx, `SELECT NULL::date`).Scan(&d); err == nil {
		t.Error("scanning NULL into a non-pointer Date succeeded, want error")
	}

	for _, special := range []string{"infinity", "-infinity"} {
		if err := conn.QueryRow(ctx, `SELECT $1::date`, special).Scan(&d); err == nil {
			t.Errorf("scanning %s succeeded as %v, want error", special, d)
		}
	}

	// pgx hands a sql.Scanner a time.Time for timestamptz as well, so a Date
	// can technically be scanned from one; it then gets the UTC calendar date
	// of the instant, which is 2024-01-02 here even though the local wall
	// clock said 2024-01-01. That is exactly the day-shift the spec warns
	// about, and why dates must live in DATE columns, never TIMESTAMPTZ.
	if err := conn.QueryRow(ctx, `SELECT '2024-01-01 23:00:00-08'::timestamptz`).Scan(&d); err != nil {
		t.Fatalf("scan timestamptz: %v", err)
	}
	if d != MustParseDate("2024-01-02") {
		t.Errorf("timestamptz 2024-01-01 23:00-08 scanned as %v, want its UTC date 2024-01-02", d)
	}
}
