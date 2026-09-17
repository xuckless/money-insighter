package money

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCanonicalises(t *testing.T) {
	tests := []struct {
		in, want string
		scale    int
	}{
		{"0", "0", 0},
		{"-0", "0", 0},
		{"0.0", "0", 0},
		{"-0.000", "0", 0},
		{"00", "0", 0},
		{"0e5", "0", 0},
		{"0E-5", "0", 0},
		{"1", "1", 0},
		{"-1", "-1", 0},
		{"007", "7", 0},
		{"-007", "-7", 0},
		{"1.50", "1.5", 1},
		{"1.500", "1.5", 1},
		{"12.34", "12.34", 2},
		{"-12.34", "-12.34", 2},
		{"0.10", "0.1", 1},
		{"0.01", "0.01", 2},
		{"-0.01", "-0.01", 2},
		{"000.010", "0.01", 2},
		{"1500", "1500", 0},
		{"1500.00", "1500", 0},
		{"9999999999.99", "9999999999.99", 2},
		{"1e2", "100", 0},
		{"1E2", "100", 0},
		{"1e+2", "100", 0},
		{"1.5E-1", "0.15", 2},
		{"15e-1", "1.5", 1},
		{"1234e-2", "12.34", 2},   // pgtype.Numeric's historical text form
		{"-1234e-2", "-12.34", 2}, //
		{"100e-2", "1", 0},
		{"1e-3", "0.001", 3},
		{"1.0e0", "1", 0},
		{"12.5e1", "125", 0},
		{"0.5e1", "5", 0},
		{"1e0000000000000000000002", "100", 0},
		{"12345678901234567890.12345678901234567890", "12345678901234567890.1234567890123456789", 19},
		{"0.1000000000000000055511151231257827", "0.1000000000000000055511151231257827", 34},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			a, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tc.in, err)
			}
			if got := a.String(); got != tc.want {
				t.Errorf("Parse(%q).String() = %q, want %q", tc.in, got, tc.want)
			}
			if got := a.Scale(); got != tc.scale {
				t.Errorf("Parse(%q).Scale() = %d, want %d", tc.in, got, tc.scale)
			}
			// Canonical form must be a fixed point of Parse.
			again, err := Parse(a.String())
			if err != nil || again != a {
				t.Errorf("Parse(%q) is not a fixed point: %v, %v", a.String(), again, err)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	tests := []string{
		"",
		" ",
		" 1",
		"1 ",
		"+1",
		"-",
		".",
		".5",
		"5.",
		"-.5",
		"1.",
		"1..2",
		"1.2.3",
		"--1",
		"1-",
		"1_000",
		"1,000",
		"abc",
		"12abc",
		"NaN",
		"nan",
		"Inf",
		"Infinity",
		"-Infinity",
		"+Infinity",
		"e5",
		"1e",
		"1e+",
		"1e-",
		"1ee2",
		"1e2.5",
		"1e2e3",
		"1e1001",
		"1e-1001",
		"1e99999999999999999999",
		"0x10",
		"١٢",    // Arabic-Indic digits
		"１２",    // full-width digits
		"1\x00", // embedded NUL
		"1\n",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			a, err := Parse(in)
			if err == nil {
				t.Fatalf("Parse(%q) = %q, want error", in, a)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Parse(%q) error %v does not wrap ErrInvalid", in, err)
			}
			if !a.IsZero() {
				t.Errorf("Parse(%q) returned non-zero amount %q with error", in, a)
			}
		})
	}
}

func TestParseErrorTruncatesInput(t *testing.T) {
	in := strings.Repeat("x", 1000)
	_, err := Parse(in)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 120 {
		t.Errorf("error message too long (%d bytes): %q", len(err.Error()), err.Error())
	}
}

func TestMustParse(t *testing.T) {
	if got := MustParse("1.50").String(); got != "1.5" {
		t.Errorf("MustParse(\"1.50\") = %q, want \"1.5\"", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustParse(\"abc\") did not panic")
		}
	}()
	MustParse("abc")
}

func TestZeroValue(t *testing.T) {
	var a Amount
	if got := a.String(); got != "0" {
		t.Errorf("zero value String() = %q, want \"0\"", got)
	}
	if !a.IsZero() {
		t.Error("zero value IsZero() = false")
	}
	if a.IsNegative() {
		t.Error("zero value IsNegative() = true")
	}
	if got := a.Scale(); got != 0 {
		t.Errorf("zero value Scale() = %d, want 0", got)
	}
	if !a.Neg().IsZero() {
		t.Error("zero value Neg() is not zero")
	}
	if a != Zero() {
		t.Error("zero value != Zero()")
	}
	if a != MustParse("0") {
		t.Error("zero value != MustParse(\"0\"): zero must have a single representation")
	}
	if a != MustParse("-0.00") {
		t.Error("zero value != MustParse(\"-0.00\")")
	}
	if !a.Equal(Zero()) || a.Cmp(Zero()) != 0 {
		t.Error("zero value is not Equal to / Cmp-equal to Zero()")
	}
}

func TestIsNegativeAndNeg(t *testing.T) {
	tests := []struct {
		in       string
		negative bool
		neg      string
	}{
		{"0", false, "0"},
		{"-0", false, "0"},
		{"1", false, "-1"},
		{"-1", true, "1"},
		{"0.01", false, "-0.01"},
		{"-0.01", true, "0.01"},
		{"1500", false, "-1500"},
	}
	for _, tc := range tests {
		a := MustParse(tc.in)
		if got := a.IsNegative(); got != tc.negative {
			t.Errorf("%q.IsNegative() = %v, want %v", tc.in, got, tc.negative)
		}
		if got := a.Neg().String(); got != tc.neg {
			t.Errorf("%q.Neg() = %q, want %q", tc.in, got, tc.neg)
		}
		if got := a.Neg().Neg(); got != a {
			t.Errorf("%q.Neg().Neg() = %q, want %q", tc.in, got, a)
		}
	}
}

func TestIsZero(t *testing.T) {
	for _, in := range []string{"0", "-0", "0.0", "0e9", "000"} {
		if !MustParse(in).IsZero() {
			t.Errorf("%q.IsZero() = false", in)
		}
	}
	for _, in := range []string{"1", "-1", "0.1", "-0.001", "1e-100"} {
		if MustParse(in).IsZero() {
			t.Errorf("%q.IsZero() = true", in)
		}
	}
}
