package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestParseKey_AcceptsStdAndRawEncodings(t *testing.T) {
	t.Parallel()
	raw := rawTestKey(0x42)
	for name, enc := range map[string]string{
		"std":              base64.StdEncoding.EncodeToString(raw),
		"raw":              base64.RawStdEncoding.EncodeToString(raw),
		"std with newline": base64.StdEncoding.EncodeToString(raw) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseKey(enc)
			if err != nil {
				t.Fatalf("ParseKey: %v", err)
			}
			if got.Len() != KeySize {
				t.Fatalf("Len = %d, want %d", got.Len(), KeySize)
			}
			if !bytes.Equal(got.Expose(), raw) {
				t.Fatal("decoded key differs from input")
			}
		})
	}
}

func TestParseKey_Rejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{"31 bytes std", base64.StdEncoding.EncodeToString(make([]byte, 31)), ErrInvalidKeySize},
		{"33 bytes std", base64.StdEncoding.EncodeToString(make([]byte, 33)), ErrInvalidKeySize},
		{"31 bytes raw", base64.RawStdEncoding.EncodeToString(make([]byte, 31)), ErrInvalidKeySize},
		{"33 bytes raw", base64.RawStdEncoding.EncodeToString(make([]byte, 33)), ErrInvalidKeySize},
		{"empty", "", ErrInvalidKeySize},
		{"16 bytes", base64.StdEncoding.EncodeToString(make([]byte, 16)), ErrInvalidKeySize},
		{"not base64", "this is not base64!!", ErrInvalidKeyEncoding},
		{"lone padding", "=", ErrInvalidKeyEncoding},
		{"url-safe alphabet", strings.Repeat("-_", 22), ErrInvalidKeyEncoding},
		{"padding in middle", "AAAA=AAA" + strings.Repeat("A", 36), ErrInvalidKeyEncoding},
		{"internal space", "AAAA AAAA" + strings.Repeat("A", 36), ErrInvalidKeyEncoding},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseKey(tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseKey error = %v, want %v", err, tc.want)
			}
			if !got.IsZero() {
				t.Fatal("ParseKey returned a non-zero key alongside an error")
			}
		})
	}
}

func TestParseKey_ErrorNeverContainsInput(t *testing.T) {
	t.Parallel()
	// Each input is distinctive enough that any echo in the error is obvious.
	inputs := []string{
		"SENTINEL-not-base64-7f3a9c!!",
		base64.StdEncoding.EncodeToString([]byte("SENTINEL-thirty-one-bytes-long!")),
		base64.RawStdEncoding.EncodeToString([]byte("SENTINEL-thirty-three-bytes-long!")),
	}
	for _, in := range inputs {
		_, err := ParseKey(in)
		if err == nil {
			t.Fatalf("ParseKey(%q) succeeded, want error", in)
		}
		msg := err.Error()
		if strings.Contains(msg, in) {
			t.Fatalf("error %q echoes the input", msg)
		}
		// Also guard against a partial echo of the distinctive prefix.
		if strings.Contains(msg, "SENTINEL") || strings.Contains(msg, in[:8]) {
			t.Fatalf("error %q contains part of the input", msg)
		}
	}
}
