package civil

import (
	"encoding"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var (
	_ encoding.TextMarshaler   = Date{}
	_ encoding.TextUnmarshaler = (*Date)(nil)
	_ json.Marshaler           = Date{}
	_ json.Unmarshaler         = (*Date)(nil)
)

func TestJSONRoundTrip(t *testing.T) {
	for _, in := range []string{"2024-01-02", "2024-03-31", "2024-02-29", "0001-01-01", "9999-12-31"} {
		d := MustParseDate(in)
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", in, err)
		}
		if want := `"` + in + `"`; string(b) != want {
			t.Errorf("Marshal(%s) = %s, want %s", in, b, want)
		}
		var back Date
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", b, err)
		}
		if back != d {
			t.Errorf("round trip of %s gave %v", in, back)
		}
	}
}

func TestJSONStructFields(t *testing.T) {
	type txn struct {
		Date           Date  `json:"date"`
		AuthorizedDate *Date `json:"authorized_date"`
	}

	var got txn
	if err := json.Unmarshal([]byte(`{"date":"2024-03-31","authorized_date":null}`), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Date != MustParseDate("2024-03-31") || got.AuthorizedDate != nil {
		t.Errorf("Unmarshal = %+v", got)
	}

	if err := json.Unmarshal([]byte(`{"date":"2024-03-31","authorized_date":"2024-03-29"}`), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.AuthorizedDate == nil || *got.AuthorizedDate != MustParseDate("2024-03-29") {
		t.Errorf("authorized_date = %v, want 2024-03-29", got.AuthorizedDate)
	}

	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"date":"2024-03-31","authorized_date":"2024-03-29"}`; string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}

	// A nil pointer marshals as null without calling MarshalJSON.
	out, err = json.Marshal(txn{Date: MustParseDate("2024-03-31")})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"date":"2024-03-31","authorized_date":null}`; string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
}

func TestUnmarshalJSONRejects(t *testing.T) {
	tests := []string{
		`"2024-02-30"`,
		`"2024-1-2"`,
		`""`,
		`"2024-01-02T00:00:00Z"`,
		`20240102`,
		`2024`,
		`true`,
		`{}`,
		`[]`,
		`["2024-01-02"]`,
		`{"date":"2024-01-02"}`,
		`"null"`,
		`abc`,
		``,
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			d := MustParseDate("2020-06-15")
			if err := json.Unmarshal([]byte(in), &d); err == nil {
				t.Fatalf("Unmarshal(%s) = %v, want error", in, d)
			}
			if d != MustParseDate("2020-06-15") {
				t.Errorf("Unmarshal(%s) modified the target to %v on error", in, d)
			}
		})
	}

	var d Date
	if err := json.Unmarshal([]byte(`"2024-02-30"`), &d); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("Unmarshal(\"2024-02-30\") error %v does not wrap ErrInvalidDate", err)
	}
}

func TestUnmarshalJSONNullIsNoOp(t *testing.T) {
	d := MustParseDate("2020-06-15")
	if err := d.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("UnmarshalJSON(null): %v", err)
	}
	if d != MustParseDate("2020-06-15") {
		t.Errorf("UnmarshalJSON(null) changed the value to %v", d)
	}
}

func TestMarshalZeroAndInvalid(t *testing.T) {
	// The zero value has no date to report: JSON null.
	b, err := json.Marshal(Date{})
	if err != nil {
		t.Fatalf("Marshal(zero): %v", err)
	}
	if string(b) != "null" {
		t.Errorf("Marshal(zero) = %s, want null", b)
	}
	if _, err := (Date{}).MarshalText(); err == nil {
		t.Error("MarshalText(zero) succeeded, want error")
	}

	// A hand-built impossible date must never be emitted as if it were real.
	bad := Date{2024, time.February, 30}
	if b, err := json.Marshal(bad); err == nil {
		t.Errorf("Marshal(2024-02-30) = %s, want error", b)
	}
	if b, err := bad.MarshalText(); err == nil {
		t.Errorf("MarshalText(2024-02-30) = %s, want error", b)
	}
	if b, err := (Date{2024, 13, 1}).MarshalText(); err == nil {
		t.Errorf("MarshalText(month 13) = %s, want error", b)
	}
}

func TestTextRoundTrip(t *testing.T) {
	d := MustParseDate("2024-03-31")
	b, err := d.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(b) != "2024-03-31" {
		t.Errorf("MarshalText = %q", b)
	}
	var back Date
	if err := back.UnmarshalText(b); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if back != d {
		t.Errorf("text round trip gave %v", back)
	}
	if err := back.UnmarshalText([]byte("2024-02-30")); err == nil {
		t.Error("UnmarshalText(2024-02-30) succeeded")
	}

	// TextMarshaler makes Date usable as a JSON map key.
	m := map[Date]int{d: 1}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal(map): %v", err)
	}
	if string(out) != `{"2024-03-31":1}` {
		t.Errorf("Marshal(map) = %s", out)
	}
	var backMap map[Date]int
	if err := json.Unmarshal(out, &backMap); err != nil {
		t.Fatalf("Unmarshal(map): %v", err)
	}
	if backMap[d] != 1 {
		t.Errorf("Unmarshal(map) = %v", backMap)
	}
}
