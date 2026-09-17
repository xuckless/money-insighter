package money

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalJSON encodes the amount as a JSON string ("12.34"), never a JSON
// number, so no consumer can accidentally decode it into a float.
func (a Amount) MarshalJSON() ([]byte, error) {
	// The canonical form only contains '-', '.', and ASCII digits, so quoting
	// needs no escaping.
	return []byte(`"` + a.String() + `"`), nil
}

// UnmarshalJSON accepts either a JSON number (as Plaid sends amounts) or a
// JSON string. Numbers are read through json.Number so the literal's digits
// are preserved exactly; float64 is never involved. A JSON null is a no-op,
// following the encoding/json convention.
func (a *Amount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}

	var lit string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &lit); err != nil {
			return fmt.Errorf("money: unmarshal JSON string: %w", err)
		}
	} else {
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return fmt.Errorf("money: unmarshal JSON number: %w", err)
		}
		lit = string(n)
	}

	v, err := Parse(lit)
	if err != nil {
		return err
	}
	*a = v
	return nil
}
