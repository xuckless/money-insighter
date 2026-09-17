package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"plaidsync/internal/secret"
)

// distinctivePlaintext is chosen so that any echo of it, or of its hex, base64,
// or Go-quoted forms, in an error string is unmistakable.
const distinctivePlaintext = "PLAINTEXT-SENTINEL-9f2c1a7e-DO-NOT-LEAK"

// forms returns the representations of s that a careless error message might
// embed: raw, hex, std base64, raw base64, %q, and Go byte-slice syntax.
func forms(s string) []string {
	b := []byte(s)
	return []string{
		s,
		hex.EncodeToString(b),
		base64.StdEncoding.EncodeToString(b),
		base64.RawStdEncoding.EncodeToString(b),
		fmt.Sprintf("%q", s),
		fmt.Sprintf("%v", b),
		fmt.Sprintf("%x", b),
		"SENTINEL",
	}
}

func assertNoForms(t *testing.T, what, haystack string, secretValue string) {
	t.Helper()
	for _, f := range forms(secretValue) {
		if f != "" && strings.Contains(haystack, f) {
			t.Fatalf("%s contains a form of the secret: %q", what, haystack)
		}
	}
}

// TestDecryptErrors_NeverContainPlaintext exercises every failing Decrypt
// path with a distinctive plaintext and greps the error text for it.
func TestDecryptErrors_NeverContainPlaintext(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t, 1, 1)
	aad := []byte("item-leak")
	blob, v, err := kr.Encrypt(secret.NewToken(distinctivePlaintext), aad)
	if err != nil {
		t.Fatal(err)
	}
	tampered := slices.Clone(blob)
	tampered[NonceSize+3] ^= 0x80

	cases := []struct {
		name string
		blob []byte
		ver  uint32
		aad  []byte
		want error
	}{
		{"unknown version", blob, 9, aad, ErrUnknownKeyVersion},
		{"tampered", tampered, v, aad, ErrDecrypt},
		{"wrong aad", blob, v, []byte("item-other"), ErrDecrypt},
		{"truncated", blob[:5], v, aad, ErrCiphertextTooShort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok, err := kr.Decrypt(tc.blob, tc.ver, tc.aad)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if !tok.IsZero() {
				t.Fatal("non-zero token alongside error")
			}
			assertNoForms(t, "error", err.Error(), distinctivePlaintext)
			// Wrapping the error the way callers will must not change that.
			wrapped := fmt.Errorf("sync item %s: %w", "item-leak", err)
			assertNoForms(t, "wrapped error", wrapped.Error(), distinctivePlaintext)
		})
	}
}

// TestErrors_NeverContainKeyMaterial checks that constructor and decrypt
// errors never embed the key bytes in any common encoding.
func TestErrors_NeverContainKeyMaterial(t *testing.T) {
	t.Parallel()
	raw := rawTestKey(0x5a)
	key := secret.NewBytes(raw)

	// A bad ring built from a wrong-length key derived from the good one.
	short := secret.NewBytes(raw[:31])
	_, err := NewKeyring(1, map[uint32]secret.Bytes{1: short})
	if err == nil {
		t.Fatal("expected error for 31-byte key")
	}
	assertNoForms(t, "NewKeyring error", err.Error(), string(raw[:31]))

	// A failing decrypt on a good ring.
	kr, err := NewKeyring(1, map[uint32]secret.Bytes{1: key})
	if err != nil {
		t.Fatal(err)
	}
	blob, v, err := kr.Encrypt(secret.NewToken("tok"), nil)
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-1] ^= 1
	_, err = kr.Decrypt(blob, v, nil)
	if err == nil {
		t.Fatal("expected decrypt failure")
	}
	assertNoForms(t, "Decrypt error", err.Error(), string(raw))
}

// TestKeyring_FormattingHidesKeys prints the keyring every way a log line or
// panic might and checks that no encoding of the key appears, while the
// version summary does.
func TestKeyring_FormattingHidesKeys(t *testing.T) {
	t.Parallel()
	raw := rawTestKey(0x77)
	kr, err := NewKeyring(2, map[uint32]secret.Bytes{1: testKey(0x11), 2: secret.NewBytes(raw)})
	if err != nil {
		t.Fatal(err)
	}
	// Every verb, on the pointer and on the dereferenced value, plus a struct
	// holding the keyring in an unexported field (reflective path).
	holder := struct{ kr *Keyring }{kr}
	outputs := map[string]string{
		"%v":         fmt.Sprintf("%v", kr),
		"%+v":        fmt.Sprintf("%+v", kr),
		"%#v":        fmt.Sprintf("%#v", kr),
		"%s":         fmt.Sprintf("%s", kr),
		"%q":         fmt.Sprintf("%q", kr),
		"%x":         fmt.Sprintf("%x", kr),
		"%d":         fmt.Sprintf("%d", kr),
		"%v deref":   fmt.Sprintf("%v", *kr),
		"%+v deref":  fmt.Sprintf("%+v", *kr),
		"%#v deref":  fmt.Sprintf("%#v", *kr),
		"holder %v":  fmt.Sprintf("%v", holder),
		"holder %+v": fmt.Sprintf("%+v", holder),
		"error":      errors.New(fmt.Sprintf("keyring %v", kr)).Error(),
	}
	for name, out := range outputs {
		assertNoForms(t, name, out, string(raw))
	}
	for _, want := range []string{"active:2", "versions:[1 2]"} {
		if !strings.Contains(outputs["%v"], want) {
			t.Fatalf("%%v output %q should contain %q", outputs["%v"], want)
		}
	}

	// slog: text and JSON handlers, direct and nested.
	for _, mk := range []func(*bytes.Buffer) slog.Handler{
		func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var buf bytes.Buffer
		logger := slog.New(mk(&buf))
		logger.Info("keyring", slog.Any("keyring", kr), slog.Group("cfg", slog.Any("ring", kr)))
		out := buf.String()
		assertNoForms(t, "slog", out, string(raw))
		if !strings.Contains(out, "active_version") || !strings.Contains(out, "2") {
			t.Fatalf("slog output %q should carry the active version", out)
		}
	}

	// A nil keyring formats without panicking.
	var nilRing *Keyring
	if got := fmt.Sprint(nilRing); got != "crypto.Keyring(nil)" {
		t.Fatalf("nil keyring formats as %q", got)
	}
	if got := nilRing.LogValue(); got.Kind() != slog.KindAny || got.Any() != nil {
		t.Fatalf("nil keyring LogValue = %v, want empty value", got)
	}
}
