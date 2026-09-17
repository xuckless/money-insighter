package money

import (
	"math/big"
	"strconv"
	"strings"
)

// Cmp compares a and b numerically and returns -1, 0, or +1. The comparison
// is exact: both amounts are scaled to a common number of fractional digits
// and compared as big integers.
func (a Amount) Cmp(b Amount) int {
	scale := max(a.Scale(), b.Scale())
	return a.scaled(scale).Cmp(b.scaled(scale))
}

// Equal reports whether a and b are numerically equal. Because Amount is
// canonical this is the same as a == b.
func (a Amount) Equal(b Amount) bool { return a == b }

// scaled returns a × 10^scale as an integer. scale must be >= a.Scale().
func (a Amount) scaled(scale int) *big.Int {
	s := a.String()
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	digits := sign + intPart + frac + strings.Repeat("0", scale-len(frac))
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		// Unreachable: s is only ever set by canonicalize.
		panic("money: corrupt Amount " + strconv.Quote(a.s))
	}
	return n
}
