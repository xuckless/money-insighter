package money

import (
	"database/sql/driver"
	"errors"
	"testing"
)

var _ driver.Valuer = Amount{}

func TestValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "0"},
		{"12.34", "12.34"},
		{"-0.01", "-0.01"},
		{"1500.00", "1500"},
		{"9999999999.99", "9999999999.99"},
	}
	for _, tc := range tests {
		v, err := MustParse(tc.in).Value()
		if err != nil {
			t.Fatalf("Value(%q): %v", tc.in, err)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("Value(%q) = %T, want string", tc.in, v)
		}
		if s != tc.want {
			t.Errorf("Value(%q) = %q, want %q", tc.in, s, tc.want)
		}
	}

	var zero Amount
	if v, err := zero.Value(); err != nil || v != "0" {
		t.Errorf("zero value Value() = %v, %v; want \"0\"", v, err)
	}
}

func TestScanValueRoundTrip(t *testing.T) {
	for _, in := range []string{"0", "12.34", "-0.01", "1500", "9999999999.99", "-123456789012.12"} {
		a := MustParse(in)
		v, err := a.Value()
		if err != nil {
			t.Fatalf("Value(%q): %v", in, err)
		}
		var back Amount
		if err := back.Scan(v); err != nil {
			t.Fatalf("Scan(%v): %v", v, err)
		}
		if back != a {
			t.Errorf("Scan(Value(%q)) = %q", in, back)
		}
	}
}

func TestScanAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  any
		want string
	}{
		{"string", "12.34", "12.34"},
		{"string negative", "-0.01", "-0.01"},
		{"string postgres numeric text", "1500.00", "1500"},
		{"string exponent (pgtype.Numeric legacy text)", "1234e-2", "12.34"},
		{"bytes", []byte("12.34"), "12.34"},
		{"bytes zero", []byte("0.00"), "0"},
		{"int64", int64(-5), "-5"},
		{"int64 zero", int64(0), "0"},
		{"int64 large", int64(9223372036854775807), "9223372036854775807"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var a Amount
			if err := a.Scan(tc.src); err != nil {
				t.Fatalf("Scan(%#v): %v", tc.src, err)
			}
			if got := a.String(); got != tc.want {
				t.Errorf("Scan(%#v) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestScanRejectsFloat64(t *testing.T) {
	for _, f := range []float64{0, 1.1, 12.34, -0.01, 1e2} {
		a := MustParse("42")
		err := a.Scan(f)
		if err == nil {
			t.Fatalf("Scan(float64 %v) succeeded with %q, want error", f, a)
		}
		if !errors.Is(err, ErrScanFloat64) {
			t.Errorf("Scan(float64 %v) error %v does not wrap ErrScanFloat64", f, err)
		}
		if a.String() != "42" {
			t.Errorf("Scan(float64 %v) modified the target to %q on error", f, a)
		}
	}
}

func TestScanRejectsOther(t *testing.T) {
	tests := []struct {
		name string
		src  any
	}{
		{"nil (SQL NULL)", nil},
		{"bool", true},
		{"float32", float32(1.5)},
		{"int (not int64)", int(5)},
		{"NaN text", "NaN"},
		{"Infinity text", "Infinity"},
		{"-Infinity text", "-Infinity"},
		{"garbage text", "abc"},
		{"empty text", ""},
		{"empty bytes", []byte{}},
		{"nil bytes", []byte(nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := MustParse("42")
			if err := a.Scan(tc.src); err == nil {
				t.Fatalf("Scan(%#v) succeeded with %q, want error", tc.src, a)
			}
			if a.String() != "42" {
				t.Errorf("Scan(%#v) modified the target to %q on error", tc.src, a)
			}
		})
	}

	var a Amount
	if err := a.Scan("abc"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Scan(\"abc\") error %v does not wrap ErrInvalid", err)
	}
}
