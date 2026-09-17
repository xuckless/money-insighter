package secret

import "crypto/rand"

// padLen is the length in bytes of the process-wide masking pad. Values longer
// than the pad cycle over it.
const padLen = 256

// pad is the process-random XOR pad shared by every [Token] and [Bytes] in
// this process. It is generated once from crypto/rand at package
// initialisation and never changes afterwards, so a value masked at any point
// can be unmasked at any later point in the same process.
//
// No pad byte is zero, so every masked byte differs from the plaintext byte
// it covers. This keeps reflective formatting from ever showing a plaintext
// byte at its own position.
var pad = newPad()

// newPad draws padLen random bytes from crypto/rand and re-draws any zero
// bytes. It panics if crypto/rand is unavailable: a process that cannot read
// system randomness must not start, and since Go 1.24 crypto/rand.Read does
// not return errors on supported platforms anyway.
func newPad() [padLen]byte {
	var p [padLen]byte
	fill := func(b []byte) {
		if _, err := rand.Read(b); err != nil {
			panic("secret: crypto/rand unavailable: " + err.Error())
		}
	}
	fill(p[:])
	for i := range p {
		for p[i] == 0 {
			fill(p[i : i+1])
		}
	}
	return p
}

// mask XORs src with the pad (cycling over the pad's length) into a new
// slice. Because XOR is an involution the same function unmasks. Empty input
// yields nil so that a masked empty value and the zero value are
// indistinguishable.
func mask(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	out := make([]byte, len(src))
	for i, c := range src {
		out[i] = c ^ pad[i%padLen]
	}
	return out
}

// maskString is mask for a string source. It avoids materialising an
// intermediate plaintext byte slice.
func maskString(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = s[i] ^ pad[i%padLen]
	}
	return out
}

// unmaskString unmasks a masked slice into a fresh string.
func unmaskString(masked []byte) string {
	if len(masked) == 0 {
		return ""
	}
	return string(mask(masked))
}
