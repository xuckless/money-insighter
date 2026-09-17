package crypto

import (
	"encoding/base64"
	"fmt"

	"plaidsync/internal/secret"
)

// ParseKey decodes a base64-encoded key encryption key, as supplied through
// the PLAIDSYNC_KEK environment variable, into a [secret.Bytes].
//
// Standard base64 is accepted with padding ([base64.StdEncoding]) or without
// ([base64.RawStdEncoding]); the decoders themselves ignore embedded newlines.
// The decoded key must be exactly KeySize bytes. Failures return
// [ErrInvalidKeyEncoding] or [ErrInvalidKeySize]; neither error text ever
// includes the input.
func ParseKey(b64 string) (secret.Bytes, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			// Deliberately not wrapped: the decoder's error is dropped so the
			// message is fixed and cannot depend on the input.
			return secret.Bytes{}, ErrInvalidKeyEncoding
		}
	}
	defer clear(raw)
	if len(raw) != KeySize {
		return secret.Bytes{}, fmt.Errorf("%w: decoded %d bytes", ErrInvalidKeySize, len(raw))
	}
	return secret.NewBytes(raw), nil
}
