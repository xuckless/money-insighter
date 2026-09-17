package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"plaidsync/internal/secret"
)

func TestNewKeyring_Valid(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 3, 7, 1, 3)
	if got := kr.ActiveVersion(); got != 3 {
		t.Fatalf("ActiveVersion = %d, want 3", got)
	}
	if got := kr.Versions(); !slices.Equal(got, []uint32{1, 3, 7}) {
		t.Fatalf("Versions = %v, want [1 3 7]", got)
	}
}

func TestNewKeyring_VersionsReturnsCopy(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1, 2)
	v := kr.Versions()
	v[0] = 99
	if got := kr.Versions(); !slices.Equal(got, []uint32{1, 2}) {
		t.Fatalf("Versions after mutating a previous result = %v, want [1 2]", got)
	}
}

func TestNewKeyring_Rejects(t *testing.T) {
	t.Parallel()
	good := testKey(1)
	tests := []struct {
		name   string
		active uint32
		keys   map[uint32]secret.Bytes
		want   error
	}{
		{"active zero", 0, map[uint32]secret.Bytes{1: good}, ErrInvalidKeyVersion},
		{"active zero and present", 0, map[uint32]secret.Bytes{0: good}, ErrInvalidKeyVersion},
		{"active missing", 2, map[uint32]secret.Bytes{1: good}, ErrActiveKeyMissing},
		{"nil map", 1, nil, ErrActiveKeyMissing},
		{"empty map", 1, map[uint32]secret.Bytes{}, ErrActiveKeyMissing},
		{"version zero present", 1, map[uint32]secret.Bytes{0: good, 1: good}, ErrInvalidKeyVersion},
		{"key 31 bytes", 1, map[uint32]secret.Bytes{1: secret.NewBytes(make([]byte, 31))}, ErrInvalidKeySize},
		{"key 33 bytes", 1, map[uint32]secret.Bytes{1: secret.NewBytes(make([]byte, 33))}, ErrInvalidKeySize},
		{"zero key", 1, map[uint32]secret.Bytes{1: {}}, ErrInvalidKeySize},
		{"bad non-active key", 2, map[uint32]secret.Bytes{1: secret.NewBytes(make([]byte, 16)), 2: good}, ErrInvalidKeySize},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kr, err := NewKeyring(tc.active, tc.keys)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewKeyring error = %v, want %v", err, tc.want)
			}
			if kr != nil {
				t.Fatal("NewKeyring returned a keyring alongside an error")
			}
		})
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	plaintexts := []string{
		"a",
		"access-sandbox-0123abcd-4567-89ef-0123-456789abcdef",
		"unicode: héllo wörld ☃ 日本語",
		strings.Repeat("x", 4096),
		"\x00\x01\x02\xff binary-ish\n\t",
	}
	aads := [][]byte{nil, {}, []byte("item-abc123")}
	for _, pt := range plaintexts {
		for _, aad := range aads {
			blob, version, err := kr.Encrypt(secret.NewToken(pt), aad)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if version != 1 {
				t.Fatalf("Encrypt version = %d, want 1", version)
			}
			if want := NonceSize + len(pt) + TagSize; len(blob) != want {
				t.Fatalf("len(blob) = %d, want %d", len(blob), want)
			}
			got, err := kr.Decrypt(blob, version, aad)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if got.Expose() != pt {
				t.Fatalf("round trip mismatch for plaintext of length %d", len(pt))
			}
		}
	}
}

func TestEncryptDecrypt_NilAndEmptyAADAreEquivalent(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	blob, v, err := kr.Encrypt(secret.NewToken("tok"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kr.Decrypt(blob, v, []byte{}); err != nil {
		t.Fatalf("Decrypt with empty aad after nil aad: %v", err)
	}
}

func TestEncrypt_RejectsEmptyToken(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	for _, tok := range []secret.Token{{}, secret.NewToken("")} {
		blob, v, err := kr.Encrypt(tok, []byte("item"))
		if !errors.Is(err, ErrEmptyPlaintext) {
			t.Fatalf("Encrypt(empty) error = %v, want %v", err, ErrEmptyPlaintext)
		}
		if blob != nil || v != 0 {
			t.Fatalf("Encrypt(empty) returned blob %v version %d alongside an error", blob, v)
		}
	}
}

// TestEncrypt_BlobLayout pins the wire format the store documents:
// nonce(12) || ciphertext || tag(16), AES-256-GCM, AAD not stored. It opens a
// blob with a hand-built GCM and decrypts a hand-built blob with the keyring.
func TestEncrypt_BlobLayout(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	raw := rawTestKey(keyFill(1))
	block, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	const pt = "layout-check-plaintext"
	aad := []byte("item-layout")

	// Keyring -> hand-built GCM.
	blob, _, err := kr.Encrypt(secret.NewToken(pt), aad)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := gcm.Open(nil, blob[:NonceSize], blob[NonceSize:], aad)
	if err != nil {
		t.Fatalf("hand-built GCM could not open keyring blob: %v", err)
	}
	if string(opened) != pt {
		t.Fatalf("hand-built GCM opened %q, want %q", opened, pt)
	}

	// Hand-built GCM -> keyring.
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	manual := gcm.Seal(slices.Clone(nonce), nonce, []byte(pt), aad)
	if !bytes.Equal(manual[:NonceSize], nonce) {
		t.Fatal("test bug: nonce prefix missing from manual blob")
	}
	got, err := kr.Decrypt(manual, 1, aad)
	if err != nil {
		t.Fatalf("keyring could not decrypt hand-built blob: %v", err)
	}
	if got.Expose() != pt {
		t.Fatalf("keyring decrypted %q, want %q", got.Expose(), pt)
	}
}

func TestEncrypt_NonceFreshness(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	tok := secret.NewToken("same plaintext every time")
	aad := []byte("same aad")
	const n = 200
	nonces := make(map[string]struct{}, n)
	blobs := make(map[string]struct{}, n)
	for range n {
		blob, _, err := kr.Encrypt(tok, aad)
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(blob[:NonceSize])
		if _, dup := nonces[nonce]; dup {
			t.Fatal("nonce reused across encryptions")
		}
		nonces[nonce] = struct{}{}
		if _, dup := blobs[string(blob)]; dup {
			t.Fatal("identical blob produced twice")
		}
		blobs[string(blob)] = struct{}{}
	}
}

func TestDecrypt_UnknownVersion(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	blob, _, err := kr.Encrypt(secret.NewToken("tok"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint32{0, 2, 1 << 31} {
		got, err := kr.Decrypt(blob, v, nil)
		if !errors.Is(err, ErrUnknownKeyVersion) {
			t.Fatalf("Decrypt version %d error = %v, want %v", v, err, ErrUnknownKeyVersion)
		}
		if !got.IsZero() {
			t.Fatal("Decrypt returned a non-zero token alongside an error")
		}
		if !strings.Contains(err.Error(), fmt.Sprint(v)) {
			t.Fatalf("error %q should name the version %d", err, v)
		}
	}
}

// TestDecrypt_TamperedAnywhere flips one bit at every byte offset of the blob
// (nonce, ciphertext, and tag) and expects ErrDecrypt each time.
func TestDecrypt_TamperedAnywhere(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	aad := []byte("item-tamper")
	blob, v, err := kr.Encrypt(secret.NewToken("tamper-me-please-1234567890"), aad)
	if err != nil {
		t.Fatal(err)
	}
	for i := range blob {
		region := "ciphertext"
		switch {
		case i < NonceSize:
			region = "nonce"
		case i >= len(blob)-TagSize:
			region = "tag"
		}
		bad := slices.Clone(blob)
		bad[i] ^= 0x01
		got, err := kr.Decrypt(bad, v, aad)
		if !errors.Is(err, ErrDecrypt) {
			t.Fatalf("byte %d (%s) flipped: error = %v, want %v", i, region, err, ErrDecrypt)
		}
		if !got.IsZero() {
			t.Fatalf("byte %d (%s) flipped: got non-zero token alongside error", i, region)
		}
	}
	// Sanity: the untouched blob still opens.
	if _, err := kr.Decrypt(blob, v, aad); err != nil {
		t.Fatalf("untouched blob failed: %v", err)
	}
}

func TestDecrypt_WrongAAD(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	blob, v, err := kr.Encrypt(secret.NewToken("tok"), []byte("item-A"))
	if err != nil {
		t.Fatal(err)
	}
	for name, aad := range map[string][]byte{
		"other item":  []byte("item-B"),
		"nil":         nil,
		"empty":       {},
		"prefix":      []byte("item-"),
		"extra byte":  []byte("item-A\x00"),
		"case change": []byte("item-a"),
	} {
		got, err := kr.Decrypt(blob, v, aad)
		if !errors.Is(err, ErrDecrypt) {
			t.Fatalf("aad %s: error = %v, want %v", name, err, ErrDecrypt)
		}
		if !got.IsZero() {
			t.Fatalf("aad %s: non-zero token alongside error", name)
		}
	}
}

func TestDecrypt_Truncated(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	aad := []byte("item-trunc")
	blob, v, err := kr.Encrypt(secret.NewToken("truncate-me-0123456789"), aad)
	if err != nil {
		t.Fatal(err)
	}
	const minLen = NonceSize + TagSize
	// Shorter than nonce+tag: structurally invalid.
	for n := 0; n < minLen; n++ {
		got, err := kr.Decrypt(blob[:n], v, aad)
		if !errors.Is(err, ErrCiphertextTooShort) {
			t.Fatalf("len %d: error = %v, want %v", n, err, ErrCiphertextTooShort)
		}
		if !got.IsZero() {
			t.Fatalf("len %d: non-zero token alongside error", n)
		}
	}
	if _, err := kr.Decrypt(nil, v, aad); !errors.Is(err, ErrCiphertextTooShort) {
		t.Fatalf("nil blob: error = %v, want %v", err, ErrCiphertextTooShort)
	}
	// Long enough to parse but missing ciphertext bytes: authentication fails.
	for n := minLen; n < len(blob); n++ {
		if _, err := kr.Decrypt(blob[:n], v, aad); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("len %d: error = %v, want %v", n, err, ErrDecrypt)
		}
	}
	// Extra trailing byte is also an authentication failure, not a panic.
	if _, err := kr.Decrypt(append(slices.Clone(blob), 0), v, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("extended blob: error = %v, want %v", err, ErrDecrypt)
	}
}

func TestDecrypt_WrongKeySameVersion(t *testing.T) {
	t.Parallel()
	a, err := NewKeyring(1, map[uint32]secret.Bytes{1: testKey(0x10)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeyring(1, map[uint32]secret.Bytes{1: testKey(0x20)})
	if err != nil {
		t.Fatal(err)
	}
	blob, v, err := a.Encrypt(secret.NewToken("tok"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Decrypt(blob, v, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt under a different key: error = %v, want %v", err, ErrDecrypt)
	}
}

// TestRotation walks the documented rotation procedure: blobs written under v1
// remain readable after v2 becomes active, new blobs are written under v2, and
// once v1 is dropped its blobs are rejected with ErrUnknownKeyVersion.
func TestRotation(t *testing.T) {
	t.Parallel()
	aad := []byte("item-rotate")
	tok := secret.NewToken("access-token-rotation")

	v1 := newTestKeyring(t, 1, 1)
	oldBlob, oldVer, err := v1.Encrypt(tok, aad)
	if err != nil {
		t.Fatal(err)
	}
	if oldVer != 1 {
		t.Fatalf("version under v1 ring = %d, want 1", oldVer)
	}

	// Rotate: add v2 as active, keep v1 for reads.
	v2 := newTestKeyring(t, 2, 1, 2)
	if got := v2.ActiveVersion(); got != 2 {
		t.Fatalf("ActiveVersion = %d, want 2", got)
	}
	got, err := v2.Decrypt(oldBlob, oldVer, aad)
	if err != nil {
		t.Fatalf("v2 ring could not decrypt v1 blob: %v", err)
	}
	if !got.Equal(tok) {
		t.Fatal("v1 blob decrypted to the wrong value under the v2 ring")
	}

	newBlob, newVer, err := v2.Encrypt(tok, aad)
	if err != nil {
		t.Fatal(err)
	}
	if newVer != 2 {
		t.Fatalf("version under v2 ring = %d, want 2", newVer)
	}
	// A v2 blob is not readable as v1 and vice versa.
	if _, err := v2.Decrypt(newBlob, 1, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("v2 blob decrypted as v1: error = %v, want %v", err, ErrDecrypt)
	}
	if _, err := v2.Decrypt(oldBlob, 2, aad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("v1 blob decrypted as v2: error = %v, want %v", err, ErrDecrypt)
	}
	// The old ring cannot read v2 blobs.
	if _, err := v1.Decrypt(newBlob, newVer, aad); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("v1 ring decrypting v2 blob: error = %v, want %v", err, ErrUnknownKeyVersion)
	}

	// Retire v1 after re-encryption: v1 blobs are now unknown, v2 blobs fine.
	v2only := newTestKeyring(t, 2, 2)
	if _, err := v2only.Decrypt(oldBlob, oldVer, aad); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("retired ring decrypting v1 blob: error = %v, want %v", err, ErrUnknownKeyVersion)
	}
	if got, err := v2only.Decrypt(newBlob, newVer, aad); err != nil || !got.Equal(tok) {
		t.Fatalf("retired ring decrypting v2 blob: token equal=%v err=%v", got.Equal(tok), err)
	}
}

func TestKeyring_ConcurrentUse(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 2, 1, 2)
	const goroutines, iterations = 16, 200
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			aad := fmt.Appendf(nil, "item-%d", g)
			for i := range iterations {
				pt := fmt.Sprintf("g%d-i%d-token", g, i)
				blob, v, err := kr.Encrypt(secret.NewToken(pt), aad)
				if err != nil {
					errs <- err
					return
				}
				got, err := kr.Decrypt(blob, v, aad)
				if err != nil {
					errs <- err
					return
				}
				if got.Expose() != pt {
					errs <- fmt.Errorf("goroutine %d iteration %d: round trip mismatch", g, i)
					return
				}
				_ = kr.Versions()
				_ = kr.ActiveVersion()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
