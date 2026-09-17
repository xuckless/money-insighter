package civil

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

var (
	_ driver.Valuer = Date{}
	_ sql.Scanner   = (*Date)(nil)
)

func TestValue(t *testing.T) {
	v, err := MustParseDate("2024-03-31").Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if s, ok := v.(string); !ok || s != "2024-03-31" {
		t.Errorf("Value = %#v, want the string \"2024-03-31\"", v)
	}

	// Never write a placeholder date.
	if v, err := (Date{}).Value(); err == nil {
		t.Errorf("Value(zero) = %#v, want error", v)
	}
	if v, err := (Date{2024, time.February, 30}).Value(); err == nil {
		t.Errorf("Value(2024-02-30) = %#v, want error", v)
	}
}

func TestScanTimeUTCMidnight(t *testing.T) {
	// This is exactly what pgx hands a sql.Scanner for a DATE column.
	var d Date
	if err := d.Scan(time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if d != MustParseDate("2024-01-02") {
		t.Errorf("Scan(2024-01-02 00:00 UTC) = %v, want 2024-01-02", d)
	}

	if err := d.Scan(time.Date(2024, time.March, 31, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if d != MustParseDate("2024-03-31") {
		t.Errorf("Scan(2024-03-31 00:00 UTC) = %v, want 2024-03-31", d)
	}
}

// TestScanTimeIsInterpretedInUTC documents the assumption Scan relies on.
//
// pgx decodes a DATE column as time.Date(y, m, d, 0, 0, 0, 0, time.UTC) — see
// pgtype/date.go in pgx v5 — regardless of the connection's or the machine's
// time zone. Scan therefore reads the calendar fields from t.UTC(). The
// instant "2024-01-01 23:00 in a UTC-8 zone" is the same instant as
// "2024-01-02 00:00 UTC", so if pgx ever handed us a DATE in a local zone this
// would misfile the row by a day; that is not what pgx does, and the
// database-backed test in pgx_test.go checks it against a real server.
func TestScanTimeIsInterpretedInUTC(t *testing.T) {
	vancouver := time.FixedZone("PST", -8*3600)
	localEvening := time.Date(2024, time.January, 1, 23, 0, 0, 0, vancouver)
	utcMidnight := time.Date(2024, time.January, 2, 7, 0, 0, 0, time.UTC)
	if !localEvening.Equal(utcMidnight) {
		t.Fatal("test setup: the two times should be the same instant")
	}

	var d Date
	if err := d.Scan(localEvening); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if d != MustParseDate("2024-01-02") {
		t.Errorf("Scan(2024-01-01 23:00 UTC-8) = %v, want 2024-01-02: Scan must use t.UTC(), not the local wall clock", d)
	}
	// DateOf, by contrast, honours the time's own location; callers that
	// hold a local time must choose deliberately.
	if got := DateOf(localEvening); got != MustParseDate("2024-01-01") {
		t.Errorf("DateOf(2024-01-01 23:00 UTC-8) = %v, want 2024-01-01", got)
	}

	// A positive-offset zone the other way round: 2024-01-02 01:00 UTC+2 is
	// 2024-01-01 23:00 UTC.
	if err := d.Scan(time.Date(2024, time.January, 2, 1, 0, 0, 0, time.FixedZone("EET", 2*3600))); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if d != MustParseDate("2024-01-01") {
		t.Errorf("Scan(2024-01-02 01:00 UTC+2) = %v, want 2024-01-01", d)
	}
}

func TestScanTextForms(t *testing.T) {
	var d Date
	if err := d.Scan("2024-03-31"); err != nil {
		t.Fatalf("Scan(string): %v", err)
	}
	if d != MustParseDate("2024-03-31") {
		t.Errorf("Scan(string) = %v", d)
	}
	if err := d.Scan([]byte("2023-02-28")); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if d != MustParseDate("2023-02-28") {
		t.Errorf("Scan([]byte) = %v", d)
	}
}

func TestScanRejects(t *testing.T) {
	tests := []struct {
		name string
		src  any
	}{
		{"nil (SQL NULL)", nil},
		{"infinity", "infinity"},
		{"-infinity", "-infinity"},
		{"garbage", "abc"},
		{"empty", ""},
		{"impossible date", "2024-02-30"},
		{"timestamp text", "2024-01-02 00:00:00"},
		{"int64", int64(20240102)},
		{"float64", float64(20240102)},
		{"bool", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := MustParseDate("2020-06-15")
			if err := d.Scan(tc.src); err == nil {
				t.Fatalf("Scan(%#v) = %v, want error", tc.src, d)
			}
			if d != MustParseDate("2020-06-15") {
				t.Errorf("Scan(%#v) modified the target to %v on error", tc.src, d)
			}
		})
	}

	var d Date
	if err := d.Scan("2024-02-30"); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("Scan(\"2024-02-30\") error %v does not wrap ErrInvalidDate", err)
	}
}

func TestScanValueRoundTrip(t *testing.T) {
	for _, in := range []string{"2024-01-02", "2024-03-31", "2024-02-29", "0001-01-01", "9999-12-31"} {
		d := MustParseDate(in)
		v, err := d.Value()
		if err != nil {
			t.Fatalf("Value(%s): %v", in, err)
		}
		var back Date
		if err := back.Scan(v); err != nil {
			t.Fatalf("Scan(%v): %v", v, err)
		}
		if back != d {
			t.Errorf("Scan(Value(%s)) = %v", in, back)
		}
	}
}
