package money

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMarshalJSONIsString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", `"0"`},
		{"12.34", `"12.34"`},
		{"-0.01", `"-0.01"`},
		{"1500.00", `"1500"`},
	}
	for _, tc := range tests {
		b, err := json.Marshal(MustParse(tc.in))
		if err != nil {
			t.Fatalf("Marshal(%q): %v", tc.in, err)
		}
		if string(b) != tc.want {
			t.Errorf("Marshal(%q) = %s, want %s", tc.in, b, tc.want)
		}
	}

	var zero Amount
	b, err := json.Marshal(zero)
	if err != nil || string(b) != `"0"` {
		t.Errorf("Marshal(zero value) = %s, %v; want \"0\"", b, err)
	}
}

func TestUnmarshalJSONNumberIsExact(t *testing.T) {
	// Each of these would be mangled by a float64 round trip.
	tests := []struct{ in, want string }{
		{`12.34`, "12.34"},
		{`-0.01`, "-0.01"},
		{`0.1`, "0.1"},
		{`0.1000000000000000055511151231257827`, "0.1000000000000000055511151231257827"},
		{`12345678901234567890.12345678901234567890`, "12345678901234567890.1234567890123456789"},
		{`9007199254740993`, "9007199254740993"}, // 2^53 + 1, not representable as float64
		{`1e2`, "100"},
		{`1.5E-1`, "0.15"},
		{`1E+2`, "100"},
		{`-0`, "0"},
		{`-0.0`, "0"},
		{`0`, "0"},
		{`1500`, "1500"},
		{`1500.00`, "1500"},
	}
	for _, tc := range tests {
		var a Amount
		if err := json.Unmarshal([]byte(tc.in), &a); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if got := a.String(); got != tc.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnmarshalJSONString(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"12.34"`, "12.34"},
		{`"-0.01"`, "-0.01"},
		{`"007.50"`, "7.5"},
		{`"1e2"`, "100"},
		{`"0"`, "0"},
		{`"-0"`, "0"},
	}
	for _, tc := range tests {
		var a Amount
		if err := json.Unmarshal([]byte(tc.in), &a); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if got := a.String(); got != tc.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnmarshalJSONRejects(t *testing.T) {
	tests := []string{
		`""`,
		`" 1"`,
		`"abc"`,
		`"NaN"`,
		`"Infinity"`,
		`true`,
		`false`,
		`{}`,
		`[]`,
		`[1]`,
		`{"amount":1}`,
		`"1.2.3"`,
		`1e1001`,
		`abc`,
		`01`, // invalid JSON number
		`.5`,
		`+1`,
		``,
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			a := MustParse("42")
			err := json.Unmarshal([]byte(in), &a)
			if err == nil {
				// json.Unmarshal itself rejects syntactically invalid input before
				// reaching us; that is fine as long as nothing succeeds.
				t.Fatalf("Unmarshal(%s) = %q, want error", in, a)
			}
			if a.String() != "42" {
				t.Errorf("Unmarshal(%s) modified the target to %q on error", in, a)
			}
		})
	}

	// ErrInvalid surfaces for well-formed JSON that is not a valid amount.
	var a Amount
	if err := json.Unmarshal([]byte(`"abc"`), &a); !errors.Is(err, ErrInvalid) {
		t.Errorf("Unmarshal(\"abc\") error %v does not wrap ErrInvalid", err)
	}
}

func TestUnmarshalJSONNullIsNoOp(t *testing.T) {
	a := MustParse("42")
	if err := a.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("UnmarshalJSON(null): %v", err)
	}
	if a.String() != "42" {
		t.Errorf("UnmarshalJSON(null) changed the value to %q", a)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	for _, in := range []string{"0", "1", "-1", "12.34", "-0.01", "1500", "9999999999.99", "0.000001", "123456789012345678901234567890"} {
		a := MustParse(in)
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("Marshal(%q): %v", in, err)
		}
		var back Amount
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if back != a {
			t.Errorf("round trip of %q gave %q via %s", in, back, b)
		}
	}
}

func TestJSONStructFields(t *testing.T) {
	type row struct {
		Amount  Amount  `json:"amount"`
		Balance *Amount `json:"balance"`
		Limit   *Amount `json:"limit"`
	}

	// Plaid-style body: numbers, with a null for a nullable field.
	var r row
	in := `{"amount": 12.34, "balance": -1500.5, "limit": null}`
	if err := json.Unmarshal([]byte(in), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if r.Amount.String() != "12.34" {
		t.Errorf("amount = %q, want 12.34", r.Amount)
	}
	if r.Balance == nil || r.Balance.String() != "-1500.5" {
		t.Errorf("balance = %v, want -1500.5", r.Balance)
	}
	if r.Limit != nil {
		t.Errorf("limit = %v, want nil for JSON null", r.Limit)
	}

	// Absent field leaves the pointer nil and the value zero.
	var r2 row
	if err := json.Unmarshal([]byte(`{}`), &r2); err != nil {
		t.Fatalf("Unmarshal({}): %v", err)
	}
	if !r2.Amount.IsZero() || r2.Balance != nil || r2.Limit != nil {
		t.Errorf("Unmarshal({}) = %+v, want all zero/nil", r2)
	}

	// Our output: strings, null for nil pointers, no bare numbers anywhere.
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"amount":"12.34","balance":"-1500.5","limit":null}`
	if string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
	if strings.Contains(string(out), `:1`) || strings.Contains(string(out), `:-`) {
		t.Errorf("Marshal output contains a bare JSON number: %s", out)
	}
}

func TestUnmarshalJSONNeverUsesFloat(t *testing.T) {
	// A decoder configured with UseNumber and one without must agree, because
	// UnmarshalJSON receives the raw literal either way. This is the
	// "json.Unmarshal of 12.34 gives exactly 12.34" guarantee.
	const in = `{"amount": 0.30000000000000004, "other": 0.1}`
	type doc struct {
		Amount Amount  `json:"amount"`
		Other  float64 `json:"other"`
	}
	var plain, withNumber doc
	if err := json.Unmarshal([]byte(in), &plain); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(in))
	dec.UseNumber()
	if err := dec.Decode(&withNumber); err != nil {
		t.Fatal(err)
	}
	if plain.Amount != withNumber.Amount || plain.Amount.String() != "0.30000000000000004" {
		t.Errorf("amount decoded as %q / %q, want 0.30000000000000004", plain.Amount, withNumber.Amount)
	}
}
