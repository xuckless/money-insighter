package money

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
)

// ErrScanFloat64 is returned by Scan when the driver supplies a float64. A
// float has already lost precision, so accepting it would make silent
// corruption possible; the fix is to read the column as NUMERIC (or text),
// not to convert.
var ErrScanFloat64 = errors.New("money: refusing to scan float64 into Amount")

// Value implements driver.Valuer. It returns the canonical string; pgx sends
// strings in text format, so the value lands in a NUMERIC column exactly.
func (a Amount) Value() (driver.Value, error) {
	return a.String(), nil
}

// Scan implements sql.Scanner. It accepts the forms a driver may hand over:
//   - string and []byte: the decimal text of a NUMERIC column (pgx passes
//     NUMERIC to a sql.Scanner as a string in both text and binary result
//     formats; "NaN" and "Infinity" are rejected).
//   - int64: an integer column.
//   - float64: always rejected with ErrScanFloat64.
//   - nil (SQL NULL): rejected; use *Amount for nullable columns so NULL
//     becomes a nil pointer.
func (a *Amount) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return errors.New("money: cannot scan NULL into Amount; use *Amount for nullable columns")
	case string:
		return a.scanString(v)
	case []byte:
		return a.scanString(string(v))
	case int64:
		*a = MustParse(strconv.FormatInt(v, 10))
		return nil
	case float64:
		return ErrScanFloat64
	default:
		return fmt.Errorf("money: cannot scan %T into Amount", src)
	}
}

func (a *Amount) scanString(s string) error {
	v, err := Parse(s)
	if err != nil {
		return fmt.Errorf("money: scan: %w", err)
	}
	*a = v
	return nil
}
