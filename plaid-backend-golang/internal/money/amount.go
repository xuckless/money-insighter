// Package money provides Amount, an exact decimal number used for every
// monetary value that crosses this service: Plaid JSON in, Postgres NUMERIC
// out, and JSON responses. It never goes through float64.
//
// An Amount is a canonical decimal string. Two amounts with the same numeric
// value always have the same representation, so Amounts are comparable with ==
// (Equal is the same thing spelled out). The zero value is 0 and is safe to
// use; nullable database columns are modelled as *Amount, where nil is NULL.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid is wrapped by every parse failure (Parse, UnmarshalJSON, Scan)
// so callers can distinguish "bad input" from other errors with errors.Is.
var ErrInvalid = errors.New("money: invalid amount")

// maxExponent bounds the exponent accepted in scientific notation. Every
// mantissa*10^exp is a finite decimal, but an unbounded exponent would let a
// four-byte literal such as "1e999999999" allocate a gigabyte-long string.
// No real amount comes anywhere near 10^1000.
const maxExponent = 1000

// errInputPreview is how many bytes of an offending input are quoted in an
// error message; enough to recognise it, not enough to bloat logs.
const errInputPreview = 40

// Amount is an exact decimal number in canonical string form, e.g. "-12.34",
// "0", "1500". Canonical means: optional leading '-', no leading zeros in the
// integer part (except a lone "0"), no trailing zeros in the fraction, no
// trailing '.', and negative zero normalises to "0".
//
// The zero value of Amount is 0. Amounts may be compared with ==.
type Amount struct {
	// s holds the canonical string, or "" for zero. Storing zero as "" makes
	// the zero value of Amount and Parse("0") identical, so == is exact.
	s string
}

// Zero returns the amount 0.
func Zero() Amount { return Amount{} }

// Parse parses a decimal string into an Amount.
//
// It accepts the plain form -?\d+(\.\d+)? and JSON-style exponent forms
// (1e2, 1.5E-1, 12e+3), which are all exactly representable. Leading and
// trailing zeros are allowed on input and removed in the result. Anything
// else — empty input, whitespace, a leading '+', a bare '.', NaN, Inf,
// non-ASCII digits, exponents beyond ±1000 — is rejected with an error that
// wraps ErrInvalid.
func Parse(s string) (Amount, error) {
	c, ok := canonicalize(s)
	if !ok {
		return Amount{}, fmt.Errorf("%w: %q", ErrInvalid, preview(s))
	}
	return Amount{s: c}, nil
}

// MustParse is Parse that panics on error. It is for tests and constants.
func MustParse(s string) Amount {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}

// String returns the canonical decimal form; "0" for the zero value.
func (a Amount) String() string {
	if a.s == "" {
		return "0"
	}
	return a.s
}

// IsZero reports whether the amount is exactly 0.
func (a Amount) IsZero() bool { return a.s == "" }

// IsNegative reports whether the amount is strictly less than 0.
func (a Amount) IsNegative() bool { return a.s != "" && a.s[0] == '-' }

// Neg returns the amount with its sign flipped. Neg of 0 is 0.
func (a Amount) Neg() Amount {
	switch {
	case a.s == "":
		return Amount{}
	case a.s[0] == '-':
		return Amount{s: a.s[1:]}
	default:
		return Amount{s: "-" + a.s}
	}
}

// Scale returns the number of fractional digits in canonical form:
// "12.5" -> 1, "1500" -> 0, "0.001" -> 3.
func (a Amount) Scale() int {
	i := strings.IndexByte(a.s, '.')
	if i < 0 {
		return 0
	}
	return len(a.s) - i - 1
}

// canonicalize converts s to canonical form ("" for zero). ok is false when s
// is not a valid decimal literal. This is the only place the grammar lives.
func canonicalize(s string) (canon string, ok bool) {
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}

	// Split off an optional exponent: mantissa[eE][+-]digits.
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant = s[:i]
		e, err := strconv.ParseInt(s[i+1:], 10, 32)
		if err != nil || e > maxExponent || e < -maxExponent {
			return "", false
		}
		exp = int(e)
	}

	// Split the mantissa into integer and fraction; both must be non-empty
	// ASCII digit runs when present ("5." and ".5" are rejected).
	intPart, fracPart := mant, ""
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		intPart, fracPart = mant[:i], mant[i+1:]
		if fracPart == "" {
			return "", false
		}
	}
	if intPart == "" || !isDigits(intPart) || !isDigits(fracPart) {
		return "", false
	}

	// Represent the value as digits × 10^-scale, then normalise.
	digits := intPart + fracPart
	scale := len(fracPart) - exp
	if scale < 0 {
		digits += strings.Repeat("0", -scale)
		scale = 0
	}
	digits = strings.TrimLeft(digits, "0")
	for scale > 0 && len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
		scale--
	}
	if digits == "" {
		return "", true // zero, including "-0", "0.000", "0e5"
	}

	var b strings.Builder
	b.Grow(len(digits) + scale + 3)
	if neg {
		b.WriteByte('-')
	}
	switch {
	case scale == 0:
		b.WriteString(digits)
	case len(digits) <= scale:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", scale-len(digits)))
		b.WriteString(digits)
	default:
		b.WriteString(digits[:len(digits)-scale])
		b.WriteByte('.')
		b.WriteString(digits[len(digits)-scale:])
	}
	return b.String(), true
}

// isDigits reports whether s consists only of ASCII digits (an empty string
// qualifies; callers check emptiness where it matters).
func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// preview truncates s for inclusion in an error message.
func preview(s string) string {
	if len(s) > errInputPreview {
		return s[:errInputPreview] + "..."
	}
	return s
}
