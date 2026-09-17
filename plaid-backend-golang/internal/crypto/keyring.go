package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"plaidsync/internal/secret"
)

// Keyring holds one or more KeySize-byte key encryption keys by version.
// Encryption always uses the active version; decryption uses whichever
// version the blob was written with.
//
// A Keyring is immutable after construction and safe for concurrent use: the
// AEAD for each key is built once in [NewKeyring], and an AEAD returned by
// [cipher.NewGCM] is safe to share between goroutines.
//
// Raw key bytes are not retained; only the AES key schedules inside the
// AEADs are. Formatting a Keyring with fmt or logging it through slog shows
// the active version and the list of versions, never key material.
type Keyring struct {
	active   uint32
	aeads    map[uint32]cipher.AEAD
	versions []uint32 // ascending
}

// NewKeyring builds a Keyring from keys, using active for encryption.
//
// It returns [ErrInvalidKeyVersion] if active or any key's version is zero,
// [ErrActiveKeyMissing] if keys has no entry for active, and
// [ErrInvalidKeySize] if any key is not exactly KeySize bytes. Validation
// stops at the first problem, checked in ascending version order, so the
// error is deterministic. The keys map is not retained.
func NewKeyring(active uint32, keys map[uint32]secret.Bytes) (*Keyring, error) {
	if active == 0 {
		return nil, fmt.Errorf("%w: active version is 0", ErrInvalidKeyVersion)
	}
	if _, ok := keys[active]; !ok {
		return nil, fmt.Errorf("%w: version %d", ErrActiveKeyMissing, active)
	}
	versions := slices.Sorted(maps.Keys(keys))
	aeads := make(map[uint32]cipher.AEAD, len(versions))
	for _, v := range versions {
		if v == 0 {
			return nil, fmt.Errorf("%w: keys contain version 0", ErrInvalidKeyVersion)
		}
		aead, err := newAEAD(keys[v])
		if err != nil {
			return nil, fmt.Errorf("key version %d: %w", v, err)
		}
		aeads[v] = aead
	}
	return &Keyring{active: active, aeads: aeads, versions: versions}, nil
}

// newAEAD constructs the AES-256-GCM AEAD for one key. The exposed key copy is
// zeroed before returning; aes.NewCipher keeps its own expanded schedule.
func newAEAD(key secret.Bytes) (cipher.AEAD, error) {
	if key.Len() != KeySize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKeySize, key.Len())
	}
	raw := key.Expose()
	defer clear(raw)
	block, err := aes.NewCipher(raw)
	if err != nil {
		// aes.KeySizeError reports only the length, never the bytes.
		return nil, fmt.Errorf("crypto: new AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new GCM: %w", err)
	}
	// The blob format below hard-codes these sizes; guard against a future
	// change in the standard library rather than silently producing blobs
	// that Decrypt cannot parse.
	if aead.NonceSize() != NonceSize || aead.Overhead() != TagSize {
		return nil, errors.New("crypto: unexpected GCM nonce size or overhead")
	}
	return aead, nil
}

// ActiveVersion returns the key version used by [Keyring.Encrypt].
func (k *Keyring) ActiveVersion() uint32 {
	return k.active
}

// Versions returns every key version in the ring in ascending order. The
// returned slice is a copy.
func (k *Keyring) Versions() []uint32 {
	return slices.Clone(k.versions)
}

// Encrypt seals plaintext under the active key and returns
// nonce || ciphertext || tag together with the key version used. The nonce
// is drawn fresh from crypto/rand on every call, so encrypting the same
// plaintext twice yields different blobs.
//
// aad binds the blob to its context and must be presented again to
// [Keyring.Decrypt]; plaidsync passes the Plaid item_id so a ciphertext copied
// to another row will not decrypt. A nil and an empty aad are equivalent.
//
// A zero plaintext is rejected with [ErrEmptyPlaintext].
func (k *Keyring) Encrypt(plaintext secret.Token, aad []byte) (blob []byte, version uint32, err error) {
	if plaintext.IsZero() {
		return nil, 0, ErrEmptyPlaintext
	}
	aead := k.aeads[k.active]

	pt := []byte(plaintext.Expose())
	defer clear(pt)

	blob = make([]byte, NonceSize, NonceSize+len(pt)+TagSize)
	if _, err := rand.Read(blob[:NonceSize]); err != nil {
		return nil, 0, fmt.Errorf("crypto: nonce: %w", err)
	}
	// Seal appends after the nonce already in blob; the nonce argument aliases
	// the prefix that Seal never writes to.
	blob = aead.Seal(blob, blob[:NonceSize], pt, aad)
	return blob, k.active, nil
}

// Decrypt opens a blob produced by [Keyring.Encrypt] under the key with the
// given version and the same aad.
//
// It returns [ErrUnknownKeyVersion] if the ring has no key for version,
// [ErrCiphertextTooShort] if blob is shorter than NonceSize+TagSize, and
// [ErrDecrypt] if authentication fails for any reason (tampering, a different
// key, or mismatched aad). On error the returned Token is the zero value, and
// the error never carries plaintext or key material.
func (k *Keyring) Decrypt(blob []byte, version uint32, aad []byte) (secret.Token, error) {
	aead, ok := k.aeads[version]
	if !ok {
		return secret.Token{}, fmt.Errorf("%w: %d", ErrUnknownKeyVersion, version)
	}
	if len(blob) < NonceSize+TagSize {
		return secret.Token{}, fmt.Errorf("%w: %d bytes, need at least %d",
			ErrCiphertextTooShort, len(blob), NonceSize+TagSize)
	}
	pt, err := aead.Open(nil, blob[:NonceSize], blob[NonceSize:], aad)
	if err != nil {
		// The GCM error is intentionally not wrapped; ErrDecrypt is the whole
		// story and its text is fixed.
		return secret.Token{}, fmt.Errorf("%w: key version %d", ErrDecrypt, version)
	}
	defer clear(pt)
	return secret.NewToken(string(pt)), nil
}

// String implements [fmt.Stringer]. It reports the active version and the
// versions present, never key material.
func (k *Keyring) String() string {
	if k == nil {
		return "crypto.Keyring(nil)"
	}
	return fmt.Sprintf("crypto.Keyring{active:%d versions:%v}", k.active, k.versions)
}

// GoString implements [fmt.GoStringer] and returns the same summary as
// [Keyring.String], so %#v also shows no key material.
func (k *Keyring) GoString() string {
	return k.String()
}

// LogValue implements [slog.LogValuer] and returns a group with the active
// version and the sorted list of versions.
func (k *Keyring) LogValue() slog.Value {
	if k == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.Uint64("active_version", uint64(k.active)),
		slog.Any("versions", k.versions),
	)
}
