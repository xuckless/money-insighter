package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"postgres-topper/internal/secret"
)

// reader pulls typed values out of a [LookupFunc] and accumulates a
// [VarError] for every variable that is missing or malformed instead of
// stopping at the first one. Each getter returns the default (or zero) value
// when it records an error, so [Load] can keep building the Config and
// report everything at once.
//
// The parse errors from strconv, time, net/url and encoding/base64 quote
// the input they rejected, so they are never wrapped; every reason string
// here is fixed text about the constraint.
type reader struct {
	lookup LookupFunc
	errs   []error
}

// fail records a problem with key.
func (r *reader) fail(key, reason string) {
	r.errs = append(r.errs, &VarError{Var: key, Reason: reason})
}

// failf records a problem with key using a format string. Arguments must not
// include the variable's value.
func (r *reader) failf(key, format string, args ...any) {
	r.fail(key, fmt.Sprintf(format, args...))
}

// err returns every recorded problem joined into one error, or nil.
func (r *reader) err() error {
	return errors.Join(r.errs...)
}

// value returns the whitespace-trimmed value of key. A variable that is
// unset, or set to whitespace only, reports ok == false and is treated as
// absent everywhere.
func (r *reader) value(key string) (v string, ok bool) {
	v, ok = r.lookup(key)
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	return v, true
}

// optional returns the value of key, or def when it is absent.
func (r *reader) optional(key, def string) string {
	if v, ok := r.value(key); ok {
		return v
	}
	return def
}

// required returns the value of key and records an error when it is absent
// or still holds an unedited placeholder from .env.example.
func (r *reader) required(key string) (v string, ok bool) {
	v, ok = r.value(key)
	if !ok {
		r.fail(key, "required but not set")
		return "", false
	}
	if isPlaceholder(v) {
		r.fail(key, "still holds the <placeholder> from .env.example")
		return "", false
	}
	return v, true
}

// isPlaceholder reports whether v is an <angle-bracket placeholder> of the
// kind .env.example uses for values the operator must fill in. Every
// required variable is either a credential or a URL, none of which can
// legitimately start with '<' and end with '>', so an unedited copy of the
// example fails at startup instead of at the first request.
func isPlaceholder(v string) bool {
	return len(v) >= 2 && v[0] == '<' && v[len(v)-1] == '>'
}

// requiredToken reads a required secret straight into a [secret.Token] so
// the caller never holds it as a plain string.
func (r *reader) requiredToken(key string) secret.Token {
	v, ok := r.required(key)
	if !ok {
		return secret.Token{}
	}
	return secret.NewToken(v)
}

// choice returns the value of key lowercased when it is one of choices,
// which must be lowercase. Absent yields def; anything else records an
// error listing the accepted values and yields def.
func (r *reader) choice(key, def string, choices ...string) string {
	v, ok := r.value(key)
	if !ok {
		return def
	}
	v = strings.ToLower(v)
	if slices.Contains(choices, v) {
		return v
	}
	quoted := make([]string, len(choices))
	for i, c := range choices {
		quoted[i] = strconv.Quote(c)
	}
	r.failf(key, "must be one of %s", strings.Join(quoted, ", "))
	return def
}

// boolean parses key with the strconv.ParseBool vocabulary (true/false,
// t/f, 1/0, case-insensitive). Absent yields def.
func (r *reader) boolean(key string, def bool) bool {
	v, ok := r.value(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(strings.ToLower(v))
	if err != nil {
		r.fail(key, "must be true or false")
		return def
	}
	return b
}

// integer parses key as a decimal integer. ok reports whether the caller
// holds a usable value (the default or a parsed one); it is false only when
// an error was recorded, so callers do not pile range errors on top of a
// syntax error.
func (r *reader) integer(key string, def int) (n int, ok bool) {
	v, present := r.value(key)
	if !present {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.fail(key, "must be an integer")
		return def, false
	}
	return n, true
}

// intAtLeast parses key and requires the value to be >= minimum.
func (r *reader) intAtLeast(key string, def, minimum int) int {
	n, ok := r.integer(key, def)
	if ok && n < minimum {
		r.failf(key, "must be at least %d", minimum)
		return def
	}
	return n
}

// intInRange parses key and requires lo <= value <= hi.
func (r *reader) intInRange(key string, def, lo, hi int) int {
	n, ok := r.integer(key, def)
	if ok && (n < lo || n > hi) {
		r.failf(key, "must be between %d and %d", lo, hi)
		return def
	}
	return n
}

// duration parses key with time.ParseDuration ("30s", "15m", "6h"). ok is
// false only when an error was recorded.
func (r *reader) duration(key string, def time.Duration) (d time.Duration, ok bool) {
	v, present := r.value(key)
	if !present {
		return def, true
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail(key, `must be a duration such as "30s", "15m" or "6h"`)
		return def, false
	}
	return d, true
}

// positiveDuration parses key and requires a value greater than zero.
func (r *reader) positiveDuration(key string, def time.Duration) time.Duration {
	d, ok := r.duration(key, def)
	if ok && d <= 0 {
		r.fail(key, "must be greater than 0")
		return def
	}
	return d
}

// nonNegativeDuration parses key and allows zero.
func (r *reader) nonNegativeDuration(key string, def time.Duration) time.Duration {
	d, ok := r.duration(key, def)
	if ok && d < 0 {
		r.fail(key, "must not be negative")
		return def
	}
	return d
}

// list splits a comma-separated value into trimmed, non-empty items. Absent
// yields a copy of def; an empty def yields an empty, non-nil slice so
// callers can rely on len() and range without nil checks.
func (r *reader) list(key string, def []string) []string {
	v, ok := r.value(key)
	if !ok {
		out := make([]string, 0, len(def))
		return append(out, def...)
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
