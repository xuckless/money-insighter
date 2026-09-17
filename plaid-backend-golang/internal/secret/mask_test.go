package secret

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// These white-box tests pin the storage invariant the package relies on: the
// in-memory representation never holds the plaintext, and differs from it at
// every byte position.

func TestPadHasNoZeroBytes(t *testing.T) {
	if len(pad) != padLen {
		t.Fatalf("pad length %d, want %d", len(pad), padLen)
	}
	for i, b := range pad {
		if b == 0 {
			t.Errorf("pad[%d] is zero; a masked byte there would equal its plaintext", i)
		}
	}
	// A pad of all-identical bytes would indicate a broken random source.
	if bytes.Count(pad[:], pad[:1]) == padLen {
		t.Error("pad is a single repeated byte")
	}
}

func TestMaskIsAnInvolution(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		{0},
		[]byte("a"),
		[]byte("some plaintext"),
		bytes.Repeat([]byte("0123456789"), 100), // longer than the pad: exercises cycling
	}
	for _, in := range inputs {
		m := mask(in)
		if len(in) == 0 {
			if m != nil {
				t.Errorf("mask(%v) = %v, want nil", in, m)
			}
			continue
		}
		if len(m) != len(in) {
			t.Fatalf("mask changed the length: %d -> %d", len(in), len(m))
		}
		if &m[0] == &in[0] {
			t.Error("mask returned the input slice instead of a copy")
		}
		for i := range in {
			if m[i] == in[i] {
				t.Errorf("mask left byte %d unchanged", i)
			}
			if want := in[i] ^ pad[i%padLen]; m[i] != want {
				t.Errorf("mask byte %d = %#x, want %#x (pad must cycle)", i, m[i], want)
			}
		}
		if back := mask(m); !bytes.Equal(back, in) {
			t.Errorf("mask(mask(x)) = %v, want %v", back, in)
		}
		if s := string(in); !bytes.Equal(maskString(s), m) {
			t.Error("maskString disagrees with mask")
		}
		if got := unmaskString(m); got != string(in) {
			t.Errorf("unmaskString = %q, want %q", got, in)
		}
	}
	if maskString("") != nil {
		t.Error(`maskString("") should be nil`)
	}
	if unmaskString(nil) != "" {
		t.Error("unmaskString(nil) should be empty")
	}
}

func TestTokenStorageNeverHoldsPlaintext(t *testing.T) {
	const plain = "access-sandbox-STORAGE-must-not-leak-1c4f"
	tok := NewToken(plain)
	stored := tok.masked

	if len(stored) != len(plain) {
		t.Fatalf("stored length %d, want %d", len(stored), len(plain))
	}
	if bytes.Contains(stored, []byte(plain)) {
		t.Fatal("stored bytes contain the plaintext")
	}
	for i := range stored {
		if stored[i] == plain[i] {
			t.Errorf("stored byte %d equals the plaintext byte", i)
		}
	}
	if strings.Contains(hex.EncodeToString(stored), hex.EncodeToString([]byte(plain))) {
		t.Error("stored bytes contain the plaintext in hex")
	}
	if NewToken("").masked != nil {
		t.Error(`NewToken("") should store nil`)
	}
}

func TestBytesStorageNeverHoldsPlaintext(t *testing.T) {
	plain := []byte("KEK-STORAGE-must-not-leak-2d5e-32bytes!!")
	b := NewBytes(plain)
	stored := b.masked

	if len(stored) != len(plain) {
		t.Fatalf("stored length %d, want %d", len(stored), len(plain))
	}
	if bytes.Contains(stored, plain) {
		t.Fatal("stored bytes contain the plaintext")
	}
	for i := range stored {
		if stored[i] == plain[i] {
			t.Errorf("stored byte %d equals the plaintext byte", i)
		}
	}
	if &stored[0] == &plain[0] {
		t.Error("NewBytes stored the caller's slice")
	}
	if NewBytes(nil).masked != nil || NewBytes([]byte{}).masked != nil {
		t.Error("empty input should store nil")
	}
}
