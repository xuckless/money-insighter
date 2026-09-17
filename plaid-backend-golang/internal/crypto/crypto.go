// Package crypto implements envelope encryption of Plaid access tokens with
// AES-256-GCM under a versioned keyring.
//
// A [Keyring] holds one or more 32-byte key encryption keys (KEKs) indexed by
// a positive version number. [Keyring.Encrypt] always uses the active version
// and returns that version alongside the blob so it can be stored next to the
// ciphertext; [Keyring.Decrypt] uses whichever version the blob was written
// with. Rotation is therefore: add the new key as the active version, keep the
// old key in the ring until every stored blob has been re-encrypted, then drop
// it.
//
// # Blob format
//
//	nonce (NonceSize bytes) || ciphertext (len(plaintext) bytes) || GCM tag (TagSize bytes)
//
// The nonce is drawn fresh from crypto/rand on every call. The additional
// authenticated data (AAD) given to Encrypt is not stored in the blob; the
// caller must supply the same bytes to Decrypt. plaidsync passes the Plaid
// item_id, so a blob copied onto another item's row fails to decrypt.
//
// # Secrets in errors
//
// Key material enters and leaves this package only wrapped in [secret.Bytes]
// and [secret.Token]. No error produced here carries key bytes, plaintext, or
// the base64 input given to [ParseKey]; errors mention at most key versions
// and byte lengths.
package crypto

import "errors"

const (
	// NonceSize is the length in bytes of the GCM nonce that prefixes every
	// blob.
	NonceSize = 12

	// KeySize is the required length in bytes of every key encryption key
	// (AES-256).
	KeySize = 32

	// TagSize is the length in bytes of the GCM authentication tag that ends
	// every blob. A blob is never shorter than NonceSize+TagSize.
	TagSize = 16
)

var (
	// ErrUnknownKeyVersion is returned by [Keyring.Decrypt] when the keyring
	// holds no key for the requested version.
	ErrUnknownKeyVersion = errors.New("crypto: unknown key version")

	// ErrCiphertextTooShort is returned by [Keyring.Decrypt] when the blob is
	// shorter than NonceSize+TagSize and so cannot even hold a nonce and a
	// tag.
	ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")

	// ErrDecrypt is returned by [Keyring.Decrypt] when GCM authentication
	// fails: the blob was tampered with, truncated past the minimum length,
	// encrypted under a different key, or the AAD does not match. It wraps
	// nothing about the plaintext or the key.
	ErrDecrypt = errors.New("crypto: decryption failed")

	// ErrInvalidKeySize is returned by [ParseKey] and [NewKeyring] when a key
	// is not exactly KeySize bytes long.
	ErrInvalidKeySize = errors.New("crypto: key must be exactly 32 bytes")

	// ErrInvalidKeyEncoding is returned by [ParseKey] when the input is not
	// valid standard (padded or unpadded) base64. The error never carries the
	// input.
	ErrInvalidKeyEncoding = errors.New("crypto: key is not valid base64")

	// ErrInvalidKeyVersion is returned by [NewKeyring] when the active version
	// or any key's version is zero. Version zero is reserved so that an unset
	// key_version column is never mistaken for a real key.
	ErrInvalidKeyVersion = errors.New("crypto: key version must be greater than zero")

	// ErrActiveKeyMissing is returned by [NewKeyring] when the keys map has no
	// entry for the active version.
	ErrActiveKeyMissing = errors.New("crypto: active key version not present in keys")

	// ErrEmptyPlaintext is returned by [Keyring.Encrypt] when given a zero
	// [secret.Token]. An empty access token is never valid, so encrypting one
	// would only hide an upstream bug behind a plausible-looking blob.
	ErrEmptyPlaintext = errors.New("crypto: refusing to encrypt an empty token")
)
