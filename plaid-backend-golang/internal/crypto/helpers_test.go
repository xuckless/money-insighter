package crypto

import (
	"testing"

	"plaidsync/internal/secret"
)

// rawTestKey returns a deterministic KeySize-byte key derived from fill, so
// tests can rebuild the same key to check the wire format by hand.
func rawTestKey(fill byte) []byte {
	b := make([]byte, KeySize)
	for i := range b {
		b[i] = fill + byte(i)*7
	}
	return b
}

// testKey wraps rawTestKey(fill) in a secret.Bytes.
func testKey(fill byte) secret.Bytes {
	return secret.NewBytes(rawTestKey(fill))
}

// keyFill maps a version to the fill byte used for its test key, so tests
// that need the raw key for a version can recompute it.
func keyFill(version uint32) byte {
	return byte(version) * 31
}

// newTestKeyring builds a keyring holding one distinct key per version, with
// the given active version, and fails the test on error.
func newTestKeyring(t *testing.T, active uint32, versions ...uint32) *Keyring {
	t.Helper()
	keys := make(map[uint32]secret.Bytes, len(versions))
	for _, v := range versions {
		keys[v] = testKey(keyFill(v))
	}
	kr, err := NewKeyring(active, keys)
	if err != nil {
		t.Fatalf("NewKeyring(%d, versions %v): %v", active, versions, err)
	}
	return kr
}
