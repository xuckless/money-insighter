package secret_test

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// plaintext is a distinctive secret used throughout the tests so that an
// accidental appearance in any output is unambiguous.
const plaintext = "access-sandbox-9f3c1e7b-PLAINTEXT-must-not-leak-4d2a"

// leakedForms returns the names of every rendering of plain that appears in
// out: the raw plaintext, its hex (both cases), its base64, and the decimal
// byte-list rendering fmt uses for []byte. An empty result means no leak.
func leakedForms(out, plain string) []string {
	raw := []byte(plain)
	forms := []struct{ name, text string }{
		{"plaintext", plain},
		{"hex", hex.EncodeToString(raw)},
		{"HEX", strings.ToUpper(hex.EncodeToString(raw))},
		{"base64", base64.StdEncoding.EncodeToString(raw)},
		{"byte list", fmt.Sprint(raw)},
	}
	var found []string
	for _, f := range forms {
		if strings.Contains(out, f.text) {
			found = append(found, f.name)
		}
	}
	return found
}

// assertNoLeak fails the test if any rendering of plain appears in out.
func assertNoLeak(t *testing.T, label, out, plain string) {
	t.Helper()
	if plain == "" {
		t.Fatalf("%s: assertNoLeak needs a non-empty plaintext", label)
	}
	if found := leakedForms(out, plain); len(found) > 0 {
		t.Errorf("%s: output leaks the secret as %v:\n%s", label, found, out)
	}
}

// assertRedacted fails the test unless out contains the redaction placeholder
// and no rendering of plain.
func assertRedacted(t *testing.T, label, out, plain string) {
	t.Helper()
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("%s: expected output to contain [REDACTED], got:\n%s", label, out)
	}
	assertNoLeak(t, label, out, plain)
}

// TestLeakDetectorControl is a positive control: the detector must recognise
// every rendering it claims to check, otherwise the other tests prove
// nothing.
func TestLeakDetectorControl(t *testing.T) {
	raw := []byte(plaintext)
	cases := map[string]string{
		"plaintext": "prefix " + plaintext + " suffix",
		"hex":       hex.EncodeToString(raw),
		"HEX":       strings.ToUpper(hex.EncodeToString(raw)),
		"base64":    base64.StdEncoding.EncodeToString(raw),
		"byte list": fmt.Sprintf("{%v}", raw),
	}
	for want, out := range cases {
		found := leakedForms(out, plaintext)
		ok := false
		for _, f := range found {
			if f == want {
				ok = true
			}
		}
		if !ok {
			t.Errorf("detector missed the %s rendering (found %v)", want, found)
		}
	}
	if found := leakedForms("[REDACTED] nothing to see", plaintext); len(found) != 0 {
		t.Errorf("detector reported a false positive: %v", found)
	}
}
