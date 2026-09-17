package civil

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// Value implements driver.Valuer. It returns the string "YYYY-MM-DD"; pgx
// sends strings in text format, so Postgres parses it as a DATE with no zone
// involved. The zero value (or any invalid date) is an error rather than a
// bogus row: nullable columns should use *Date, where nil becomes NULL.
func (d Date) Value() (driver.Value, error) {
	if !d.valid() {
		return nil, fmt.Errorf("civil: cannot store invalid date %s", d)
	}
	return d.String(), nil
}

// Scan implements sql.Scanner. It accepts:
//   - time.Time: what pgx hands a sql.Scanner for a DATE column. pgx builds
//     that value as midnight UTC on the date (both text and binary result
//     formats), so the calendar date is taken from t.UTC(). A time.Time that
//     was built in another location would be interpreted in UTC too; that is
//     intentional, because a DATE has no zone to honour.
//   - string and []byte: YYYY-MM-DD text. Postgres's "infinity" and
//     "-infinity" dates arrive as strings and are rejected.
//   - nil (SQL NULL): rejected; use *Date for nullable columns.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return errors.New("civil: cannot scan NULL into Date; use *Date for nullable columns")
	case time.Time:
		*d = DateOf(v.UTC())
		return nil
	case string:
		return d.scanString(v)
	case []byte:
		return d.scanString(string(v))
	default:
		return fmt.Errorf("civil: cannot scan %T into Date", src)
	}
}

func (d *Date) scanString(s string) error {
	v, err := ParseDate(s)
	if err != nil {
		return fmt.Errorf("civil: scan: %w", err)
	}
	*d = v
	return nil
}
