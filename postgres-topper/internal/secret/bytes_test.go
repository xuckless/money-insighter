package secret_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"postgres-topper/internal/secret"
)

// Compile-time interface checks.
var (
	_ fmt.Stringer           = secret.Bytes{}
	_ fmt.GoStringer         = secret.Bytes{}
	_ fmt.Formatter          = secret.Bytes{}
	_ json.Marshaler         = secret.Bytes{}
	_ encoding.TextMarshaler = secret.Bytes{}
	_ slog.LogValuer         = secret.Bytes{}
)

// hiddenBytes holds a Bytes in an unexported field so fmt has to walk it
// reflectively.
type hiddenBytes struct {
	b secret.Bytes
}

// exposedBytes holds a Bytes in an exported field.
type exposedBytes struct {
	Key secret.Bytes `json:"key"`
}

// keyMaterial is a distinctive 32-byte value (a KEK-shaped secret) made of
// printable bytes so that every rendering is recognisable.
var keyMaterial = []byte("KEK-PLAINTEXT-must-not-leak-3b9e")

func TestBytesExposeRoundTrip(t *testing.T) {
	long := bytes.Repeat([]byte{0x00, 0x01, 0xfe, 0xff}, 300) // 1200 bytes, longer than the pad
	cases := [][]byte{
		nil,
		{},
		{0},
		{0xff},
		keyMaterial,
		[]byte("with\x00nul\x00bytes"),
		long,
	}
	for _, in := range cases {
		b := secret.NewBytes(in)
		got := b.Expose()
		if !bytes.Equal(got, in) {
			t.Errorf("NewBytes(%v).Expose() = %v", in, got)
		}
		if b.Len() != len(in) {
			t.Errorf("Len() = %d, want %d", b.Len(), len(in))
		}
		if b.IsZero() != (len(in) == 0) {
			t.Errorf("IsZero() = %v for input of length %d", b.IsZero(), len(in))
		}
		// Exposing twice must be stable.
		if !bytes.Equal(b.Expose(), in) {
			t.Error("second Expose() differs")
		}
	}
	if secret.NewBytes(nil).Expose() != nil {
		t.Error("Expose() of the zero Bytes should be nil")
	}
}

func TestBytesCopiesOnTheWayInAndOut(t *testing.T) {
	in := append([]byte(nil), keyMaterial...)
	b := secret.NewBytes(in)

	// Mutating the caller's slice after construction must not affect b.
	for i := range in {
		in[i] = 'X'
	}
	if !bytes.Equal(b.Expose(), keyMaterial) {
		t.Error("NewBytes did not copy its input")
	}

	// Mutating an exposed copy must not affect b or later copies.
	out := b.Expose()
	for i := range out {
		out[i] = 'Y'
	}
	if !bytes.Equal(b.Expose(), keyMaterial) {
		t.Error("Expose did not return an independent copy")
	}
	first, second := b.Expose(), b.Expose()
	if len(first) > 0 && &first[0] == &second[0] {
		t.Error("successive Expose calls share a backing array")
	}
}

func TestBytesNotComparable(t *testing.T) {
	if reflect.TypeOf(secret.Bytes{}).Comparable() {
		t.Error("Bytes must not be comparable with ==")
	}
}

func TestBytesEveryVerbIsRedacted(t *testing.T) {
	b := secret.NewBytes(keyMaterial)
	plain := string(keyMaterial)
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%t", "%10s", "%#x"}
	for _, verb := range verbs {
		for _, arg := range []any{b, &b, any(b), reflect.ValueOf(b)} {
			got := fmt.Sprintf(verb, arg)
			if got != secret.Redacted {
				t.Errorf("Sprintf(%q, %T) = %q, want %q", verb, arg, got, secret.Redacted)
			}
		}
	}
	if b.String() != secret.Redacted || b.GoString() != secret.Redacted {
		t.Error("String/GoString")
	}
	if got := fmt.Sprintf("%T", b); got != "secret.Bytes" {
		t.Errorf("%%T = %q", got)
	}
	others := map[string]string{
		"Sprint":       fmt.Sprint(b),
		"Sprint slice": fmt.Sprint([]secret.Bytes{b}),
		"Sprint map":   fmt.Sprint(map[uint32]secret.Bytes{1: b}),
		"exported %v":  fmt.Sprintf("%v", exposedBytes{b}),
		"exported %+v": fmt.Sprintf("%+v", exposedBytes{b}),
		"exported %#v": fmt.Sprintf("%#v", exposedBytes{b}),
	}
	for label, out := range others {
		assertRedacted(t, label, out, plain)
	}
}

func TestBytesUnexportedFieldReflectiveFormatting(t *testing.T) {
	b := secret.NewBytes(keyMaterial)
	plain := string(keyMaterial)
	h := hiddenBytes{b: b}

	// A keyring-shaped container: unexported map of versions to keys.
	type ring struct {
		active uint32
		keys   map[uint32]secret.Bytes
	}
	r := ring{active: 1, keys: map[uint32]secret.Bytes{1: b}}

	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"}
	for _, verb := range verbs {
		for label, arg := range map[string]any{
			"struct":  h,
			"pointer": &h,
			"ring":    r,
			"slice":   []hiddenBytes{h},
			"map":     map[string]hiddenBytes{"k": h},
		} {
			out := fmt.Sprintf(verb, arg)
			assertNoLeak(t, label+" "+verb, out, plain)
			if out == "" {
				t.Errorf("%s %s: produced no output at all", label, verb)
			}
		}
	}
	if out := fmt.Sprintf("%v", h); strings.Contains(out, secret.Redacted) {
		t.Fatalf("expected reflective walk of unexported field, but fmt reached Format: %q", out)
	}
	// The map inside ring is walked reflectively too (unexported field), so
	// fmt prints the masked bytes of each value, never the plaintext.
	if out := fmt.Sprintf("%+v", r); strings.Contains(out, secret.Redacted) {
		t.Fatalf("expected reflective walk of unexported map, but fmt reached Format: %q", out)
	}
}

func TestBytesMarshalJSONAndText(t *testing.T) {
	b := secret.NewBytes(keyMaterial)
	plain := string(keyMaterial)

	direct, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(direct) != `"[REDACTED]"` {
		t.Errorf("json.Marshal(b) = %s", direct)
	}

	out, err := json.Marshal(exposedBytes{Key: b})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"key":"[REDACTED]"}` {
		t.Errorf("json.Marshal(exposedBytes) = %s", out)
	}

	out, err = json.Marshal(map[string]any{"keys": map[uint32]secret.Bytes{1: b}, "h": hiddenBytes{b}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"h":{},"keys":{"1":"[REDACTED]"}}`; string(out) != want {
		t.Errorf("json.Marshal(map) = %s, want %s", out, want)
	}
	assertNoLeak(t, "json map", string(out), plain)

	txt, err := b.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if string(txt) != secret.Redacted {
		t.Errorf("MarshalText() = %q", txt)
	}
}

func TestBytesSlog(t *testing.T) {
	b := secret.NewBytes(keyMaterial)
	plain := string(keyMaterial)
	h := hiddenBytes{b: b}
	e := exposedBytes{Key: b}

	for _, hc := range []struct {
		name string
		mk   func(w *bytes.Buffer) slog.Handler
	}{
		{"text", func(w *bytes.Buffer) slog.Handler { return slog.NewTextHandler(w, nil) }},
		{"json", func(w *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(w, nil) }},
	} {
		t.Run(hc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(hc.mk(&buf))

			log.Info("direct", slog.Any("k", b))
			assertRedacted(t, "slog.Any(b)", buf.String(), plain)
			buf.Reset()

			log.Info("pointer", slog.Any("k", &b))
			assertRedacted(t, "slog.Any(&b)", buf.String(), plain)
			buf.Reset()

			log.Info("kv", "k", b)
			assertRedacted(t, "key/value", buf.String(), plain)
			buf.Reset()

			log.Info("unexported", slog.Any("s", h))
			assertNoLeak(t, "struct with unexported field", buf.String(), plain)
			buf.Reset()

			log.Info("exported", slog.Any("s", e))
			assertRedacted(t, "struct with exported field", buf.String(), plain)
			buf.Reset()

			log.Info("map", slog.Any("keys", map[uint32]secret.Bytes{1: b}))
			assertRedacted(t, "map of keys", buf.String(), plain)
			buf.Reset()

			log.Error("error", "err", errors.New(fmt.Sprintf("bad key %v", b)))
			assertRedacted(t, "error attr", buf.String(), plain)
		})
	}
}

func TestBytesInErrors(t *testing.T) {
	b := secret.NewBytes(keyMaterial)
	plain := string(keyMaterial)
	cases := map[string]error{
		"errors.New Sprintf %v": errors.New(fmt.Sprintf("bad key %v", b)),
		"Errorf %v":             fmt.Errorf("bad key %v", b),
		"Errorf %x":             fmt.Errorf("bad key %x", b),
		"Errorf %q":             fmt.Errorf("bad key %q", b),
		"Errorf %#v":            fmt.Errorf("bad key %#v", b),
		"Errorf unexported":     fmt.Errorf("bad struct %+v", hiddenBytes{b}),
	}
	for label, err := range cases {
		assertNoLeak(t, label, err.Error(), plain)
	}
}

func TestBytesEqual(t *testing.T) {
	a := secret.NewBytes(keyMaterial)
	b := secret.NewBytes(append([]byte(nil), keyMaterial...))
	c := secret.NewBytes(append(append([]byte(nil), keyMaterial...), 0))
	d := secret.NewBytes(bytes.ToUpper(keyMaterial))
	var zero secret.Bytes

	if !a.Equal(b) || !b.Equal(a) || !a.Equal(a) {
		t.Error("identical values must be Equal")
	}
	if a.Equal(c) || c.Equal(a) {
		t.Error("different lengths must not be Equal")
	}
	if a.Equal(d) || d.Equal(a) {
		t.Error("same length, different content must not be Equal")
	}
	if !zero.Equal(secret.Bytes{}) || !zero.Equal(secret.NewBytes(nil)) || !zero.Equal(secret.NewBytes([]byte{})) {
		t.Error("zero, NewBytes(nil) and NewBytes([]byte{}) must all be Equal")
	}
	if zero.Equal(a) || a.Equal(zero) {
		t.Error("zero must not Equal a non-empty value")
	}
}

func TestBytesZeroValueIsSafe(t *testing.T) {
	var zero secret.Bytes
	if zero.Expose() != nil {
		t.Error("Expose")
	}
	if zero.Len() != 0 || !zero.IsZero() {
		t.Error("Len/IsZero")
	}
	if zero.String() != secret.Redacted || zero.GoString() != secret.Redacted {
		t.Error("String/GoString")
	}
	if got := fmt.Sprintf("%v %+v %#v %s %q %x", zero, zero, zero, zero, zero, zero); got != strings.Repeat(secret.Redacted+" ", 5)+secret.Redacted {
		t.Errorf("formatting zero = %q", got)
	}
	if out, err := zero.MarshalJSON(); err != nil || string(out) != `"[REDACTED]"` {
		t.Errorf("MarshalJSON = %s, %v", out, err)
	}
	if out, err := zero.MarshalText(); err != nil || string(out) != secret.Redacted {
		t.Errorf("MarshalText = %s, %v", out, err)
	}
	if v := zero.LogValue(); v.Kind() != slog.KindString || v.String() != secret.Redacted {
		t.Errorf("LogValue = %v", v)
	}
	if !zero.Equal(secret.Bytes{}) {
		t.Error("Equal")
	}

	var nilBytes *secret.Bytes
	if got := fmt.Sprintf("%v", nilBytes); got != "<nil>" {
		t.Errorf("nil *Bytes %%v = %q", got)
	}
	if out, err := json.Marshal(nilBytes); err != nil || string(out) != "null" {
		t.Errorf("json.Marshal(nil *Bytes) = %s, %v", out, err)
	}
}

func TestBytesConcurrentUse(t *testing.T) {
	b := secret.NewBytes(keyMaterial)
	other := secret.NewBytes(keyMaterial)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if !bytes.Equal(b.Expose(), keyMaterial) {
					t.Error("Expose returned the wrong value")
					return
				}
				if !b.Equal(other) {
					t.Error("Equal returned false")
					return
				}
				_ = fmt.Sprintf("%v %+v", b, hiddenBytes{b})
			}
		}()
	}
	wg.Wait()
}
