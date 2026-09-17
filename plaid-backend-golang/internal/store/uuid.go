package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// Job ids are v4 UUIDs. Postgres stores them as the native UUID type; this
// package handles them as canonical lowercase text
// (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx) so callers never need pgtype.
// Only these two helpers know the format.

// uuidTextLen is the length of a canonical UUID string.
const uuidTextLen = 36

// newUUID returns a fresh random (version 4, variant 1) UUID in canonical
// lowercase text form, using crypto/rand.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return formatUUID(b), nil
}

// formatUUID renders 16 bytes as canonical lowercase UUID text.
func formatUUID(b [16]byte) string {
	var dst [uuidTextLen]byte
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst[:])
}

// normalizeUUID validates that s is a UUID in canonical text form (hex
// groups of 8-4-4-4-12, either case) and returns it lowercased. ok is false
// for anything else, including the braced, URN and bare-hex forms Postgres
// would accept: the ids this service hands out are always canonical, so a
// different shape is a client mistake, not an alias. Validating here keeps
// a malformed id from reaching the database as a syntax error.
func normalizeUUID(s string) (string, bool) {
	if len(s) != uuidTextLen {
		return "", false
	}
	for i := 0; i < uuidTextLen; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return "", false
			}
		}
	}
	return strings.ToLower(s), true
}
