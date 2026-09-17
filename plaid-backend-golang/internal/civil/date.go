// Package civil provides Date, a calendar date with no time zone, for Plaid's
// date and authorized_date fields (YYYY-MM-DD) and the DATE columns that
// store them. Keeping dates out of time.Time is deliberate: a date coerced
// into an instant acquires a zone, and a zone shift moves a transaction
// across a day boundary.
package civil

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidDate is wrapped by every parse failure (ParseDate, UnmarshalJSON,
// UnmarshalText, Scan) so callers can branch on it with errors.Is.
var ErrInvalidDate = errors.New("civil: invalid date")

// errInputPreview bounds how much of an offending input is quoted in errors.
const errInputPreview = 40

// Date is a calendar date. The zero value (0000-00-00) is not a valid date;
// use IsZero to detect it and *Date for optional / nullable dates.
//
// Dates are comparable with ==.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate parses a strict YYYY-MM-DD string: exactly ten ASCII characters,
// four-digit year >= 0001, zero-padded month and day, and a day that exists
// in that month (2023-02-29 and 2024-02-30 are rejected). Anything else,
// including surrounding whitespace, a time component, or a different
// separator, is rejected with an error wrapping ErrInvalidDate.
func ParseDate(s string) (Date, error) {
	d, ok := parse(s)
	if !ok {
		return Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, preview(s))
	}
	return d, nil
}

// MustParseDate is ParseDate that panics on error. It is for tests and
// constants.
func MustParseDate(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// DateOf returns the calendar date of t in t's own location. Callers choose
// the location; DateOf(t.UTC()) and DateOf(t.In(loc)) can differ by a day.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// String formats the date as YYYY-MM-DD. It never fails; the zero value
// formats as "0000-00-00".
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// IsZero reports whether d is the zero value.
func (d Date) IsZero() bool { return d == Date{} }

// Before reports whether d is earlier than o.
func (d Date) Before(o Date) bool {
	if d.Year != o.Year {
		return d.Year < o.Year
	}
	if d.Month != o.Month {
		return d.Month < o.Month
	}
	return d.Day < o.Day
}

// After reports whether d is later than o.
func (d Date) After(o Date) bool { return o.Before(d) }

// Equal reports whether d and o are the same date. It is the same as d == o.
func (d Date) Equal(o Date) bool { return d == o }

// valid reports whether d names a real calendar date.
func (d Date) valid() bool {
	return d.Year >= 1 && d.Year <= 9999 &&
		d.Month >= time.January && d.Month <= time.December &&
		d.Day >= 1 && d.Day <= daysIn(d.Year, d.Month)
}

// daysIn returns the number of days in the given month of the given year
// (proleptic Gregorian calendar, which is what Postgres DATE uses).
func daysIn(year int, month time.Month) int {
	switch month {
	case time.February:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case time.April, time.June, time.September, time.November:
		return 30
	default:
		return 31
	}
}

// parse implements the strict grammar; ok is false on any deviation.
func parse(s string) (Date, bool) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return Date{}, false
	}
	y, ok1 := digits(s[0:4])
	m, ok2 := digits(s[5:7])
	dd, ok3 := digits(s[8:10])
	if !ok1 || !ok2 || !ok3 {
		return Date{}, false
	}
	d := Date{Year: y, Month: time.Month(m), Day: dd}
	if !d.valid() {
		return Date{}, false
	}
	return d, true
}

// digits converts a run of ASCII digits to an int.
func digits(s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// preview truncates s for inclusion in an error message.
func preview(s string) string {
	if len(s) > errInputPreview {
		return s[:errInputPreview] + "..."
	}
	return s
}
