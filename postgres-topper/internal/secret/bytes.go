package secret

import (
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
)

// Bytes holds a sensitive byte slice such as a key encryption key.
//
// Like [Token], a Bytes renders as [Redacted] under every fmt verb, when
// marshalled to JSON or text, and when logged through log/slog; [Bytes.Expose]
// is the only accessor. The value is stored XOR-masked with a process-random
// pad so reflective formatting shows only meaningless bytes; this is not a
// security boundary against memory inspection (see the package
// documentation). The zero value is an empty Bytes on which every method is
// safe to call.
//
// Bytes contains a slice and is therefore not comparable with ==; use
// [Bytes.Equal], which compares in constant time.
type Bytes struct {
	masked []byte
}

// NewBytes wraps a copy of b; later changes to b do not affect the result.
// A nil or empty slice yields the zero Bytes.
func NewBytes(b []byte) Bytes {
	return Bytes{masked: mask(b)}
}

// Expose returns a fresh copy of the plaintext bytes; the caller owns the
// copy and may zero it after use. It returns nil for the zero Bytes. Call it
// as late as possible and do not retain the result.
func (b Bytes) Expose() []byte {
	return mask(b.masked)
}

// Len returns the length of the wrapped value in bytes.
func (b Bytes) Len() int {
	return len(b.masked)
}

// IsZero reports whether b is the zero value or wraps an empty slice.
func (b Bytes) IsZero() bool {
	return len(b.masked) == 0
}

// String implements [fmt.Stringer] and returns [Redacted].
func (b Bytes) String() string {
	return Redacted
}

// GoString implements [fmt.GoStringer] and returns [Redacted].
func (b Bytes) GoString() string {
	return Redacted
}

// Format implements [fmt.Formatter] and writes [Redacted] for every verb and
// flag combination.
func (b Bytes) Format(f fmt.State, _ rune) {
	io.WriteString(f, Redacted)
}

// MarshalJSON implements [json.Marshaler] and returns the JSON string
// "[REDACTED]".
func (b Bytes) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// MarshalText implements [encoding.TextMarshaler] and returns [Redacted].
func (b Bytes) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}

// LogValue implements [slog.LogValuer] and returns [Redacted] as a string
// value, so slog handlers never see the plaintext.
func (b Bytes) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

// Equal reports whether b and o wrap the same bytes, comparing in constant
// time with crypto/subtle. Two zero Bytes are equal.
func (b Bytes) Equal(o Bytes) bool {
	return subtle.ConstantTimeCompare(b.masked, o.masked) == 1
}
