package civil

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalText implements encoding.TextMarshaler, producing YYYY-MM-DD. It
// returns an error for the zero value or any other date that does not exist
// on the calendar, so a placeholder like "0000-00-00" can never be emitted as
// if it were real; optional dates should be *Date.
func (d Date) MarshalText() ([]byte, error) {
	if !d.valid() {
		return nil, fmt.Errorf("civil: cannot marshal invalid date %s", d)
	}
	return []byte(d.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler with the same strict
// grammar as ParseDate.
func (d *Date) UnmarshalText(b []byte) error {
	v, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// MarshalJSON encodes the date as the JSON string "YYYY-MM-DD". The zero
// value encodes as null (there is no date to report); any other invalid date
// is an error.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	text, err := d.MarshalText()
	if err != nil {
		return nil, err
	}
	return []byte(`"` + string(text) + `"`), nil
}

// UnmarshalJSON accepts a JSON string in YYYY-MM-DD form. A JSON null is a
// no-op, following the encoding/json convention; anything else is an error.
func (d *Date) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("civil: unmarshal JSON: %w", err)
	}
	return d.UnmarshalText([]byte(s))
}
