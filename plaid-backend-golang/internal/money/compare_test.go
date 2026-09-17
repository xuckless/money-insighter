package money

import "testing"

func TestCmp(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0", "0", 0},
		{"0", "-0", 0},
		{"1", "1.0", 0},
		{"1.10", "1.1", 0},
		{"1e2", "100", 0},
		{"12.34", "12.34", 0},
		{"-1", "1", -1},
		{"1", "-1", 1},
		{"0.001", "0.0001", 1},
		{"0.0001", "0.001", -1},
		{"-0.5", "-0.4", -1},
		{"-0.4", "-0.5", 1},
		{"100", "99.999", 1},
		{"99.999", "100", -1},
		{"-0.0000001", "0", -1},
		{"0", "0.0000001", -1},
		{"9999999999999999999999", "9999999999999999999998", 1},
		{"9999999999999999999998", "9999999999999999999999", -1},
		{"12345678901234567890.12345678901234567891", "12345678901234567890.1234567890123456789", 1},
		{"-9999999999.99", "-9999999999.98", -1},
		{"10", "9", 1},
		{"9", "10", -1},
		{"0.9", "0.10", 1},
		{"-10", "-9", -1},
	}
	for _, tc := range tests {
		a, b := MustParse(tc.a), MustParse(tc.b)
		if got := a.Cmp(b); got != tc.want {
			t.Errorf("%q.Cmp(%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := b.Cmp(a); got != -tc.want {
			t.Errorf("%q.Cmp(%q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
		if got := a.Equal(b); got != (tc.want == 0) {
			t.Errorf("%q.Equal(%q) = %v, want %v", tc.a, tc.b, got, tc.want == 0)
		}
		if got := a == b; got != (tc.want == 0) {
			t.Errorf("%q == %q is %v, want %v (canonical form must make == exact)", tc.a, tc.b, got, tc.want == 0)
		}
	}
}

func TestCmpSelf(t *testing.T) {
	for _, in := range []string{"0", "1", "-1", "0.001", "-123456789.123456789", "1e100"} {
		a := MustParse(in)
		if a.Cmp(a) != 0 || !a.Equal(a) {
			t.Errorf("%q is not equal to itself", in)
		}
	}
}
