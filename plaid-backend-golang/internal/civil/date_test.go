package civil

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseDateAndString(t *testing.T) {
	tests := []struct {
		in   string
		want Date
	}{
		{"2024-01-02", Date{2024, time.January, 2}},
		{"2024-03-31", Date{2024, time.March, 31}},
		{"2024-02-29", Date{2024, time.February, 29}}, // leap year
		{"2000-02-29", Date{2000, time.February, 29}}, // divisible by 400: leap
		{"2023-12-31", Date{2023, time.December, 31}},
		{"1999-04-30", Date{1999, time.April, 30}},
		{"0001-01-01", Date{1, time.January, 1}},
		{"9999-12-31", Date{9999, time.December, 31}},
		{"0099-06-15", Date{99, time.June, 15}},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseDate(tc.in)
			if err != nil {
				t.Fatalf("ParseDate(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseDate(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			if s := got.String(); s != tc.in {
				t.Errorf("ParseDate(%q).String() = %q", tc.in, s)
			}
		})
	}
}

func TestParseDateRejects(t *testing.T) {
	tests := []string{
		"",
		"2024-02-30", // February never has 30 days
		"2023-02-29", // not a leap year
		"1900-02-29", // divisible by 100 but not 400: not a leap year
		"2100-02-29",
		"2024-04-31",
		"2024-06-31",
		"2024-09-31",
		"2024-11-31",
		"2024-13-01",
		"2024-00-01",
		"2024-01-00",
		"2024-01-32",
		"0000-01-01", // year 0 does not exist
		"2024-1-2",   // not zero-padded
		"2024-01-2",
		"24-01-02",
		"20240102",
		"2024/01/02",
		"2024.01.02",
		"2024-01-02T00:00:00Z", // a time, not a date
		"2024-01-02 00:00:00",
		"2024-01-02 ",
		" 2024-01-02",
		"2024-01-02\n",
		"2024-01-0x",
		"２０２４-01-02", // full-width digits
		"abcd-ef-gh",
		"infinity",
		"-infinity",
		"null",
		"+2024-01-02",
		"-2024-01-02",
		"10000-01-01",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			d, err := ParseDate(in)
			if err == nil {
				t.Fatalf("ParseDate(%q) = %v, want error", in, d)
			}
			if !errors.Is(err, ErrInvalidDate) {
				t.Errorf("ParseDate(%q) error %v does not wrap ErrInvalidDate", in, err)
			}
			if !d.IsZero() {
				t.Errorf("ParseDate(%q) returned %v with an error, want zero", in, d)
			}
		})
	}
}

func TestParseDateErrorTruncatesInput(t *testing.T) {
	_, err := ParseDate(strings.Repeat("x", 1000))
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 120 {
		t.Errorf("error message too long (%d bytes)", len(err.Error()))
	}
}

func TestMustParseDate(t *testing.T) {
	if got := MustParseDate("2024-01-02"); got != (Date{2024, time.January, 2}) {
		t.Errorf("MustParseDate = %v", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustParseDate(\"2024-02-30\") did not panic")
		}
	}()
	MustParseDate("2024-02-30")
}

func TestDateOfUsesTheTimesOwnLocation(t *testing.T) {
	vancouver := time.FixedZone("PST", -8*3600)
	// 2024-01-01 23:00 in Vancouver is 2024-01-02 07:00 UTC.
	local := time.Date(2024, time.January, 1, 23, 0, 0, 0, vancouver)

	if got := DateOf(local); got != MustParseDate("2024-01-01") {
		t.Errorf("DateOf(local) = %v, want 2024-01-01 (the wall-clock date in its own zone)", got)
	}
	if got := DateOf(local.UTC()); got != MustParseDate("2024-01-02") {
		t.Errorf("DateOf(local.UTC()) = %v, want 2024-01-02", got)
	}
	if got := DateOf(time.Date(2024, time.March, 31, 0, 0, 0, 0, time.UTC)); got != MustParseDate("2024-03-31") {
		t.Errorf("DateOf(UTC midnight) = %v, want 2024-03-31", got)
	}
}

func TestZeroValue(t *testing.T) {
	var d Date
	if !d.IsZero() {
		t.Error("zero value IsZero() = false")
	}
	if got := d.String(); got != "0000-00-00" {
		t.Errorf("zero value String() = %q", got)
	}
	if MustParseDate("2024-01-02").IsZero() {
		t.Error("real date IsZero() = true")
	}
	if !d.Equal(Date{}) || d.Before(Date{}) || d.After(Date{}) {
		t.Error("zero value comparison with itself is wrong")
	}
	// The zero value sorts before every real date.
	if !d.Before(MustParseDate("0001-01-01")) {
		t.Error("zero value is not Before 0001-01-01")
	}
}

func TestComparisons(t *testing.T) {
	tests := []struct {
		a, b   string
		before bool
	}{
		{"2024-01-01", "2024-01-02", true},
		{"2024-01-31", "2024-02-01", true},
		{"2023-12-31", "2024-01-01", true},
		{"2024-02-29", "2024-03-01", true},
		{"0001-01-01", "9999-12-31", true},
		{"2024-01-02", "2024-01-02", false},
	}
	for _, tc := range tests {
		a, b := MustParseDate(tc.a), MustParseDate(tc.b)
		equal := tc.a == tc.b
		if got := a.Before(b); got != tc.before {
			t.Errorf("%s.Before(%s) = %v, want %v", tc.a, tc.b, got, tc.before)
		}
		if got := b.After(a); got != tc.before {
			t.Errorf("%s.After(%s) = %v, want %v", tc.b, tc.a, got, tc.before)
		}
		if got := b.Before(a); got {
			t.Errorf("%s.Before(%s) = true", tc.b, tc.a)
		}
		if got := a.After(b); got {
			t.Errorf("%s.After(%s) = true", tc.a, tc.b)
		}
		if got := a.Equal(b); got != equal {
			t.Errorf("%s.Equal(%s) = %v, want %v", tc.a, tc.b, got, equal)
		}
		if got := a == b; got != equal {
			t.Errorf("%s == %s is %v, want %v", tc.a, tc.b, got, equal)
		}
	}
}
