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

	"plaidsync/internal/secret"
)

// Compile-time interface checks.
var (
	_ fmt.Stringer             = secret.Token{}
	_ fmt.GoStringer           = secret.Token{}
	_ fmt.Formatter            = secret.Token{}
	_ json.Marshaler           = secret.Token{}
	_ json.Unmarshaler         = (*secret.Token)(nil)
	_ encoding.TextMarshaler   = secret.Token{}
	_ encoding.TextUnmarshaler = (*secret.Token)(nil)
	_ slog.LogValuer           = secret.Token{}
)

// hidden holds a Token in an unexported field so fmt cannot call its methods
// and has to walk it reflectively.
type hidden struct {
	t secret.Token
}

// exposed holds a Token in an exported field.
type exposed struct {
	Token secret.Token `json:"token"`
}

func TestTokenExposeRoundTrip(t *testing.T) {
	long := strings.Repeat("0123456789abcdef", 100) // 1600 bytes, longer than the pad
	cases := []string{
		"",
		"a",
		"abc",
		plaintext,
		"unicode: héllo wörld ✓ 日本語",
		"with\x00nul\x00bytes",
		"\xff\xfe\xfd invalid utf-8",
		long,
	}
	for _, s := range cases {
		tok := secret.NewToken(s)
		if got := tok.Expose(); got != s {
			t.Errorf("NewToken(%q).Expose() = %q", s, got)
		}
		// Exposing twice must be stable (masking is an involution).
		if got := tok.Expose(); got != s {
			t.Errorf("second Expose() = %q, want %q", got, s)
		}
	}
}

func TestTokenIsZero(t *testing.T) {
	var zero secret.Token
	if !zero.IsZero() {
		t.Error("zero value: IsZero() = false")
	}
	if !secret.NewToken("").IsZero() {
		t.Error(`NewToken(""): IsZero() = false`)
	}
	if secret.NewToken("x").IsZero() {
		t.Error(`NewToken("x"): IsZero() = true`)
	}
	if got := zero.Expose(); got != "" {
		t.Errorf("zero value: Expose() = %q, want empty", got)
	}
}

func TestTokenNotComparable(t *testing.T) {
	// A comparable Token would let == and map keys silently bypass Equal.
	if reflect.TypeOf(secret.Token{}).Comparable() {
		t.Error("Token must not be comparable with ==")
	}
}

func TestTokenStringAndGoString(t *testing.T) {
	tok := secret.NewToken(plaintext)
	if got := tok.String(); got != secret.Redacted {
		t.Errorf("String() = %q", got)
	}
	if got := tok.GoString(); got != secret.Redacted {
		t.Errorf("GoString() = %q", got)
	}
}

func TestTokenEveryVerbIsRedacted(t *testing.T) {
	tok := secret.NewToken(plaintext)
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%t", "%10s", "%-10v", "%#x"}
	for _, verb := range verbs {
		for _, arg := range []any{tok, &tok, any(tok), reflect.ValueOf(tok)} {
			got := fmt.Sprintf(verb, arg)
			if got != secret.Redacted {
				t.Errorf("Sprintf(%q, %T) = %q, want %q", verb, arg, got, secret.Redacted)
			}
		}
	}

	combined := fmt.Sprintf("%v/%+v/%#v/%s/%q/%x", tok, tok, tok, tok, tok, tok)
	if want := strings.Repeat(secret.Redacted+"/", 5) + secret.Redacted; combined != want {
		t.Errorf("combined verbs = %q, want %q", combined, want)
	}
	assertNoLeak(t, "combined", combined, plaintext)

	if got := fmt.Sprintf("%T", tok); got != "secret.Token" {
		t.Errorf("%%T = %q", got)
	}

	others := map[string]string{
		"Sprint":         fmt.Sprint(tok),
		"Sprintln":       strings.TrimSpace(fmt.Sprintln(tok)),
		"Sprint slice":   fmt.Sprint([]secret.Token{tok}),
		"Sprint any":     fmt.Sprint([]any{tok}),
		"Sprint map":     fmt.Sprint(map[string]secret.Token{"k": tok}),
		"Sprint ptr map": fmt.Sprint(map[string]*secret.Token{"k": &tok}),
		"exported %v":    fmt.Sprintf("%v", exposed{tok}),
		"exported %+v":   fmt.Sprintf("%+v", exposed{tok}),
		"exported %#v":   fmt.Sprintf("%#v", exposed{tok}),
	}
	for label, out := range others {
		assertRedacted(t, label, out, plaintext)
	}
}

// TestTokenUnexportedFieldReflectiveFormatting is the contract's key case:
// fmt cannot reach methods on an unexported field, so it walks the struct
// reflectively; the XOR-masked storage must keep the plaintext out of the
// output for every verb.
func TestTokenUnexportedFieldReflectiveFormatting(t *testing.T) {
	tok := secret.NewToken(plaintext)
	h := hidden{t: tok}

	type nested struct {
		inner hidden
		list  []hidden
		m     map[string]hidden
		ptr   *hidden
	}
	n := nested{inner: h, list: []hidden{h}, m: map[string]hidden{"k": h}, ptr: &h}

	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"}
	for _, verb := range verbs {
		for label, arg := range map[string]any{
			"struct":         h,
			"pointer":        &h,
			"nested":         n,
			"nested pointer": &n,
			"slice":          []hidden{h, h},
			"array":          [1]hidden{h},
			"map":            map[string]hidden{"k": h},
		} {
			out := fmt.Sprintf(verb, arg)
			assertNoLeak(t, label+" "+verb, out, plaintext)
			if out == "" {
				t.Errorf("%s %s: produced no output at all", label, verb)
			}
		}
	}

	// The reflective output must not be [REDACTED] either (fmt cannot call
	// Format here); this guards against the test silently exercising the
	// wrong path.
	if out := fmt.Sprintf("%v", h); strings.Contains(out, secret.Redacted) {
		t.Fatalf("expected reflective walk of unexported field, but fmt reached Format: %q", out)
	}
	for _, other := range []string{fmt.Sprint(h), fmt.Sprintln(h)} {
		assertNoLeak(t, "Sprint/Sprintln", other, plaintext)
	}
}

func TestTokenMarshalJSON(t *testing.T) {
	tok := secret.NewToken(plaintext)

	direct, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	if string(direct) != `"[REDACTED]"` {
		t.Errorf("json.Marshal(tok) = %s", direct)
	}

	out, err := json.Marshal(exposed{Token: tok})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"token":"[REDACTED]"}` {
		t.Errorf("json.Marshal(exposed) = %s", out)
	}

	type ptrField struct {
		Token *secret.Token `json:"token"`
	}
	out, err = json.Marshal(ptrField{Token: &tok})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"token":"[REDACTED]"}` {
		t.Errorf("json.Marshal(ptrField) = %s", out)
	}
	out, err = json.Marshal(ptrField{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"token":null}` {
		t.Errorf("json.Marshal(nil ptrField) = %s", out)
	}

	out, err = json.Marshal(map[string]any{"a": tok, "b": []secret.Token{tok}, "c": hidden{tok}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":"[REDACTED]","b":["[REDACTED]"],"c":{}}`; string(out) != want {
		t.Errorf("json.Marshal(map) = %s, want %s", out, want)
	}

	// Pretty-printing goes through the same path.
	out, err = json.MarshalIndent(exposed{Token: tok}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	assertRedacted(t, "MarshalIndent", string(out), plaintext)
}

func TestTokenMarshalText(t *testing.T) {
	tok := secret.NewToken(plaintext)
	b, err := tok.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != secret.Redacted {
		t.Errorf("MarshalText() = %q", b)
	}

	var parsed secret.Token
	if err := parsed.UnmarshalText([]byte(plaintext)); err != nil {
		t.Fatal(err)
	}
	if parsed.Expose() != plaintext {
		t.Errorf("UnmarshalText round trip = %q", parsed.Expose())
	}
	if err := parsed.UnmarshalText(nil); err != nil {
		t.Fatal(err)
	}
	if !parsed.IsZero() {
		t.Error("UnmarshalText(nil) should yield the zero Token")
	}
}

func TestTokenUnmarshalJSONRoundTrip(t *testing.T) {
	type req struct {
		PublicToken secret.Token `json:"public_token"`
	}

	var r req
	if err := json.Unmarshal([]byte(`{"public_token":"abc"}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.PublicToken.Expose(); got != "abc" {
		t.Errorf("Expose() = %q, want %q", got, "abc")
	}

	// Escapes are decoded like any JSON string.
	if err := json.Unmarshal([]byte(`{"public_token":"a\"b\\cé\n"}`), &r); err != nil {
		t.Fatal(err)
	}
	if got, want := r.PublicToken.Expose(), "a\"b\\cé\n"; got != want {
		t.Errorf("escaped Expose() = %q, want %q", got, want)
	}

	// Decoding via json.Decoder (how an HTTP handler reads a body) behaves the same.
	r = req{}
	dec := json.NewDecoder(strings.NewReader(`{"public_token": "` + plaintext + `"}`))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.PublicToken.Expose() != plaintext {
		t.Errorf("Decoder round trip = %q", r.PublicToken.Expose())
	}

	// Re-encoding the decoded struct must not echo the value back.
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"public_token":"[REDACTED]"}` {
		t.Errorf("re-marshal = %s", out)
	}

	// Empty string decodes to the zero Token.
	if err := json.Unmarshal([]byte(`{"public_token":""}`), &r); err != nil {
		t.Fatal(err)
	}
	if !r.PublicToken.IsZero() {
		t.Error(`"" should decode to the zero Token`)
	}

	// Top-level string and whitespace around the literal.
	var top secret.Token
	if err := json.Unmarshal([]byte(`  "xyz"  `), &top); err != nil {
		t.Fatal(err)
	}
	if top.Expose() != "xyz" {
		t.Errorf("top-level Expose() = %q", top.Expose())
	}
	if err := top.UnmarshalJSON([]byte("\t\"padded\"\n")); err != nil {
		t.Fatal(err)
	}
	if top.Expose() != "padded" {
		t.Errorf("padded Expose() = %q", top.Expose())
	}
}

func TestTokenUnmarshalJSONNullIsNoOp(t *testing.T) {
	type req struct {
		PublicToken secret.Token `json:"public_token"`
	}
	r := req{PublicToken: secret.NewToken("keep")}
	if err := json.Unmarshal([]byte(`{"public_token":null}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.PublicToken.Expose(); got != "keep" {
		t.Errorf("null overwrote the token: Expose() = %q", got)
	}
	// Absent field is also untouched.
	if err := json.Unmarshal([]byte(`{}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := r.PublicToken.Expose(); got != "keep" {
		t.Errorf("absent field overwrote the token: Expose() = %q", got)
	}
}

func TestTokenUnmarshalJSONRejectsNonStrings(t *testing.T) {
	type req struct {
		PublicToken secret.Token `json:"public_token"`
	}
	inputs := []string{
		`{"public_token":123}`,
		`{"public_token":true}`,
		`{"public_token":{"nested":"secret-in-object-7f1e"}}`,
		`{"public_token":["secret-in-array-7f1e"]}`,
	}
	for _, in := range inputs {
		r := req{PublicToken: secret.NewToken("keep")}
		err := json.Unmarshal([]byte(in), &r)
		if err == nil {
			t.Errorf("Unmarshal(%s): expected error", in)
			continue
		}
		if !errors.Is(err, secret.ErrNotJSONString) {
			t.Errorf("Unmarshal(%s): error %v is not ErrNotJSONString", in, err)
		}
		if strings.Contains(err.Error(), "7f1e") {
			t.Errorf("Unmarshal(%s): error text echoes the input: %q", in, err)
		}
		if got := r.PublicToken.Expose(); got != "keep" {
			t.Errorf("Unmarshal(%s): failed decode modified the token: %q", in, got)
		}
	}

	// Direct calls with malformed input never echo it back either.
	var tok secret.Token
	for _, in := range []string{``, `"`, `"unterminated-8c2d`, `"bad\escape-8c2d"`, `x`} {
		err := tok.UnmarshalJSON([]byte(in))
		if !errors.Is(err, secret.ErrNotJSONString) {
			t.Errorf("UnmarshalJSON(%q): got %v, want ErrNotJSONString", in, err)
		}
		if err != nil && strings.Contains(err.Error(), "8c2d") {
			t.Errorf("UnmarshalJSON(%q): error text echoes the input: %q", in, err)
		}
	}
}

func TestTokenSlog(t *testing.T) {
	tok := secret.NewToken(plaintext)
	h := hidden{t: tok}
	e := exposed{Token: tok}

	type handlerCase struct {
		name string
		mk   func(w *bytes.Buffer) slog.Handler
	}
	handlers := []handlerCase{
		{"text", func(w *bytes.Buffer) slog.Handler { return slog.NewTextHandler(w, nil) }},
		{"json", func(w *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(w, nil) }},
		{"text debug", func(w *bytes.Buffer) slog.Handler {
			return slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true})
		}},
	}
	for _, hc := range handlers {
		t.Run(hc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(hc.mk(&buf))

			log.Info("direct", slog.Any("t", tok))
			assertRedacted(t, "slog.Any(tok)", buf.String(), plaintext)
			buf.Reset()

			log.Info("pointer", slog.Any("t", &tok))
			assertRedacted(t, "slog.Any(&tok)", buf.String(), plaintext)
			buf.Reset()

			log.Info("kv", "t", tok)
			assertRedacted(t, "key/value", buf.String(), plaintext)
			buf.Reset()

			log.Info("stringer", slog.String("t", tok.String()))
			assertRedacted(t, "slog.String", buf.String(), plaintext)
			buf.Reset()

			log.Info("unexported field", slog.Any("s", h))
			assertNoLeak(t, "struct with unexported field", buf.String(), plaintext)
			buf.Reset()

			log.Info("unexported field pointer", slog.Any("s", &h))
			assertNoLeak(t, "pointer to struct with unexported field", buf.String(), plaintext)
			buf.Reset()

			log.Info("exported field", slog.Any("s", e))
			assertRedacted(t, "struct with exported field", buf.String(), plaintext)
			buf.Reset()

			log.Info("map", slog.Any("m", map[string]secret.Token{"k": tok}))
			assertRedacted(t, "map of tokens", buf.String(), plaintext)
			buf.Reset()

			log.Info("group", slog.Group("g", slog.Any("t", tok), slog.Any("s", h)))
			assertRedacted(t, "group", buf.String(), plaintext)
			buf.Reset()

			log.With("t", tok).Info("with")
			assertRedacted(t, "With", buf.String(), plaintext)
			buf.Reset()

			log.Error("error", "err", fmt.Errorf("bad token %v", tok))
			assertRedacted(t, "error attr", buf.String(), plaintext)
			buf.Reset()

			// A nil *Token must not take the logger down.
			var nilTok *secret.Token
			log.Info("nil", slog.Any("t", nilTok))
			assertNoLeak(t, "nil pointer", buf.String(), plaintext)
		})
	}
}

func TestTokenInErrors(t *testing.T) {
	tok := secret.NewToken(plaintext)
	h := hidden{t: tok}

	cases := map[string]error{
		"errors.New Sprintf %v": errors.New(fmt.Sprintf("bad token %v", tok)),
		"Errorf %v":             fmt.Errorf("bad token %v", tok),
		"Errorf %s":             fmt.Errorf("bad token %s", tok),
		"Errorf %q":             fmt.Errorf("bad token %q", tok),
		"Errorf %+v":            fmt.Errorf("bad token %+v", tok),
		"Errorf %#v":            fmt.Errorf("bad token %#v", tok),
		"Errorf %w":             fmt.Errorf("bad token %v: %w", tok, errors.New("inner")),
		"Errorf pointer":        fmt.Errorf("bad token %v", &tok),
		"Errorf unexported":     fmt.Errorf("bad struct %+v", h),
		"Errorf unexported %#v": fmt.Errorf("bad struct %#v", h),
	}
	for label, err := range cases {
		assertNoLeak(t, label, err.Error(), plaintext)
		if strings.HasPrefix(label, "Errorf unexported") {
			continue
		}
		if !strings.Contains(err.Error(), secret.Redacted) {
			t.Errorf("%s: %q does not contain %s", label, err, secret.Redacted)
		}
	}
	if err := cases["Errorf %w"]; !errors.Is(err, errors.Unwrap(err)) {
		t.Error("%w wrapping broken by Token argument")
	}
}

func TestTokenEqual(t *testing.T) {
	a := secret.NewToken(plaintext)
	b := secret.NewToken(plaintext)
	c := secret.NewToken(plaintext + "x")
	d := secret.NewToken(strings.ToUpper(plaintext))
	var zero secret.Token

	if !a.Equal(b) || !b.Equal(a) {
		t.Error("identical values must be Equal")
	}
	if !a.Equal(a) {
		t.Error("a value must Equal itself")
	}
	if a.Equal(c) || c.Equal(a) {
		t.Error("values of different length must not be Equal")
	}
	if a.Equal(d) || d.Equal(a) {
		t.Error("values of equal length but different content must not be Equal")
	}
	if !zero.Equal(secret.Token{}) {
		t.Error("two zero Tokens must be Equal")
	}
	if !zero.Equal(secret.NewToken("")) || !secret.NewToken("").Equal(zero) {
		t.Error(`zero Token and NewToken("") must be Equal`)
	}
	if zero.Equal(a) || a.Equal(zero) {
		t.Error("zero Token must not Equal a non-empty Token")
	}
	// Prefix / single-byte differences.
	if secret.NewToken("abc").Equal(secret.NewToken("abd")) {
		t.Error("single-byte difference must not be Equal")
	}
	if secret.NewToken("abc").Equal(secret.NewToken("ab")) {
		t.Error("prefix must not be Equal")
	}
}

func TestTokenZeroValueIsSafe(t *testing.T) {
	var zero secret.Token

	if zero.Expose() != "" {
		t.Error("Expose")
	}
	if !zero.IsZero() {
		t.Error("IsZero")
	}
	if zero.String() != secret.Redacted || zero.GoString() != secret.Redacted {
		t.Error("String/GoString")
	}
	if got := fmt.Sprintf("%v %+v %#v %s %q %x", zero, zero, zero, zero, zero, zero); got != strings.Repeat(secret.Redacted+" ", 5)+secret.Redacted {
		t.Errorf("formatting zero = %q", got)
	}
	if b, err := zero.MarshalJSON(); err != nil || string(b) != `"[REDACTED]"` {
		t.Errorf("MarshalJSON = %s, %v", b, err)
	}
	if b, err := zero.MarshalText(); err != nil || string(b) != secret.Redacted {
		t.Errorf("MarshalText = %s, %v", b, err)
	}
	if v := zero.LogValue(); v.Kind() != slog.KindString || v.String() != secret.Redacted {
		t.Errorf("LogValue = %v", v)
	}
	if !zero.Equal(secret.Token{}) {
		t.Error("Equal")
	}
	if err := zero.UnmarshalJSON([]byte(`"v"`)); err != nil || zero.Expose() != "v" {
		t.Errorf("UnmarshalJSON on zero: %v, %q", err, zero.Expose())
	}
	zero = secret.Token{}
	if err := zero.UnmarshalText([]byte("w")); err != nil || zero.Expose() != "w" {
		t.Errorf("UnmarshalText on zero: %v, %q", err, zero.Expose())
	}

	// A nil pointer formats as <nil> (fmt recovers the nil dereference) and
	// marshals as null; neither panics.
	var nilTok *secret.Token
	if got := fmt.Sprintf("%v", nilTok); got != "<nil>" {
		t.Errorf("nil *Token %%v = %q", got)
	}
	if b, err := json.Marshal(nilTok); err != nil || string(b) != "null" {
		t.Errorf("json.Marshal(nil *Token) = %s, %v", b, err)
	}
}

func TestTokenConcurrentUse(t *testing.T) {
	tok := secret.NewToken(plaintext)
	other := secret.NewToken(plaintext)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if tok.Expose() != plaintext {
					t.Error("Expose returned the wrong value")
					return
				}
				if !tok.Equal(other) {
					t.Error("Equal returned false")
					return
				}
				_ = fmt.Sprintf("%v %+v", tok, hidden{tok})
				_, _ = json.Marshal(exposed{tok})
			}
		}()
	}
	wg.Wait()
}
