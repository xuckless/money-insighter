package secret

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

// ErrNotJSONString is returned by [Token.UnmarshalJSON] when the input is not
// a JSON string. The error never carries the offending input.
var ErrNotJSONString = errors.New("secret: expected a JSON string")

// Token holds a sensitive string such as a Plaid access token, public token,
// API secret, or database URL.
//
// A Token renders as [Redacted] under every fmt verb (%v, %+v, %#v, %s, %q,
// %x, %d, ...), when marshalled to JSON or text, and when logged through
// log/slog. [Token.Expose] is the only way to read the value. The zero value
// is an empty token on which every method is safe to call.
//
// Internally the value is stored XOR-masked with a process-random pad so that
// reflective formatting of a struct holding a Token in an unexported field
// shows only meaningless bytes. This is not a security boundary against
// memory inspection; see the package documentation.
//
// Token contains a slice and is therefore not comparable with ==; use
// [Token.Equal], which compares in constant time.
type Token struct {
	masked []byte
}

// NewToken wraps s. An empty string yields the zero Token.
func NewToken(s string) Token {
	return Token{masked: maskString(s)}
}

// Expose returns the plaintext value. It is the only accessor for the value;
// call it as late as possible and do not store the result.
func (t Token) Expose() string {
	return unmaskString(t.masked)
}

// IsZero reports whether t is the zero value or wraps the empty string.
func (t Token) IsZero() bool {
	return len(t.masked) == 0
}

// String implements [fmt.Stringer] and returns [Redacted].
func (t Token) String() string {
	return Redacted
}

// GoString implements [fmt.GoStringer] and returns [Redacted].
func (t Token) GoString() string {
	return Redacted
}

// Format implements [fmt.Formatter] and writes [Redacted] for every verb and
// flag combination, so %v, %+v, %#v, %s, %q, %x, %d and any other verb all
// produce the same placeholder.
func (t Token) Format(f fmt.State, _ rune) {
	io.WriteString(f, Redacted)
}

// MarshalJSON implements [json.Marshaler] and returns the JSON string
// "[REDACTED]".
func (t Token) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// MarshalText implements [encoding.TextMarshaler] and returns [Redacted].
func (t Token) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}

// UnmarshalJSON implements [json.Unmarshaler]. It accepts a JSON string and
// replaces t with its value, so request bodies can be decoded straight into
// Token fields. A JSON null leaves t unchanged, following the encoding/json
// convention. Any other input returns [ErrNotJSONString]; the error never
// includes the input.
func (t *Token) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return ErrNotJSONString
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// The decoder's message can quote input bytes, so it is dropped.
		return ErrNotJSONString
	}
	*t = NewToken(s)
	return nil
}

// UnmarshalText implements [encoding.TextUnmarshaler] and replaces t with the
// given text. Empty text yields the zero Token.
func (t *Token) UnmarshalText(b []byte) error {
	*t = NewToken(string(b))
	return nil
}

// LogValue implements [slog.LogValuer] and returns [Redacted] as a string
// value, so slog handlers never see the plaintext.
func (t Token) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

// Equal reports whether t and o wrap the same value, comparing in constant
// time with crypto/subtle. Two zero Tokens are equal.
func (t Token) Equal(o Token) bool {
	// Both sides are masked with the same pad from offset zero, so the masked
	// forms are equal exactly when the plaintexts are, and no plaintext needs
	// to be materialised for the comparison.
	return subtle.ConstantTimeCompare(t.masked, o.masked) == 1
}
