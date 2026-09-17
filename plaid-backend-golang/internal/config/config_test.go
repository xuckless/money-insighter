package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"plaidsync/internal/secret"
)

// Distinctive secrets. Every one carries the marker LEAKCHECK so that an
// accidental appearance in any output is unambiguous.
const (
	testDBURL       = "postgres://plaidsync:dbpass-LEAKCHECK-hunter2@127.0.0.1:5433/plaidsync?sslmode=disable"
	testAPIToken    = "api-token-LEAKCHECK-0123456789abcdef0123456789abcdef"
	testPlaidSecret = "plaid-secret-LEAKCHECK-9f3c1e7b"
	testClientID    = "5f1c0test-client-id" // not a secret; it is logged
)

// testKEK returns a 32-byte key for version v that is recognisable in any
// rendering.
func testKEK(v int) []byte {
	k := []byte(fmt.Sprintf("kek-v%d-LEAKCHECK0123456789abcdef", v))
	if len(k) != kekBytes {
		panic(fmt.Sprintf("testKEK: %d bytes", len(k)))
	}
	return k
}

func b64(b []byte) string    { return base64.StdEncoding.EncodeToString(b) }
func rawB64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

// minimalEnv is the smallest environment Load accepts: only the required
// variables.
func minimalEnv() map[string]string {
	return map[string]string{
		envDatabaseURL:   testDBURL,
		envAPIToken:      testAPIToken,
		envKEK:           b64(testKEK(1)),
		envPlaidClientID: testClientID,
		envPlaidSecret:   testPlaidSecret,
	}
}

// lookup adapts a map to LookupFunc.
func lookup(m map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func mustLoad(t *testing.T, m map[string]string) *Config {
	t.Helper()
	cfg, err := Load(lookup(m))
	if err != nil {
		t.Fatalf("Load: unexpected error:\n%v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil config without error")
	}
	return cfg
}

// varErrors flattens a joined Load error into reasons keyed by variable
// name. It fails the test if err contains anything that is not a *VarError.
func varErrors(t *testing.T, err error) map[string][]string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	out := map[string][]string{}
	var walk func(error)
	walk = func(e error) {
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range j.Unwrap() {
				walk(inner)
			}
			return
		}
		var ve *VarError
		if !errors.As(e, &ve) {
			t.Errorf("error is not a *VarError: %v", e)
			return
		}
		out[ve.Var] = append(out[ve.Var], ve.Reason)
	}
	walk(err)
	return out
}

// leakedForms names every rendering of secret that appears in out: raw,
// hex (both cases), base64 (padded and raw), and fmt's []byte list.
func leakedForms(out string, secret []byte) []string {
	forms := []struct{ name, text string }{
		{"plaintext", string(secret)},
		{"hex", hex.EncodeToString(secret)},
		{"HEX", strings.ToUpper(hex.EncodeToString(secret))},
		{"base64", b64(secret)},
		{"raw base64", rawB64(secret)},
		{"byte list", fmt.Sprint(secret)},
	}
	var found []string
	for _, f := range forms {
		if strings.Contains(out, f.text) {
			found = append(found, f.name)
		}
	}
	return found
}

// allTestSecrets lists every secret the tests put into a Config.
func allTestSecrets() [][]byte {
	return [][]byte{
		[]byte(testDBURL), []byte(testAPIToken), []byte(testPlaidSecret),
		testKEK(1), testKEK(2), testKEK(3),
	}
}

func assertNoLeak(t *testing.T, label, out string) {
	t.Helper()
	for _, s := range allTestSecrets() {
		if found := leakedForms(out, s); len(found) > 0 {
			t.Errorf("%s leaks a secret as %v:\n%s", label, found, out)
		}
	}
	if strings.Contains(out, "LEAKCHECK") {
		t.Errorf("%s contains the LEAKCHECK marker:\n%s", label, out)
	}
}

func TestLeakDetectorControl(t *testing.T) {
	k := testKEK(1)
	for name, text := range map[string]string{
		"plaintext":  "x" + string(k) + "y",
		"hex":        hex.EncodeToString(k),
		"base64":     b64(k),
		"raw base64": rawB64(k),
		"byte list":  fmt.Sprintf("{%v}", k),
	} {
		if !slices.Contains(leakedForms(text, k), name) {
			t.Errorf("detector missed the %s rendering", name)
		}
	}
}

func TestLoadMinimalEnvAppliesDefaults(t *testing.T) {
	cfg := mustLoad(t, minimalEnv())

	if cfg.BindAddr != "127.0.0.1:8080" {
		t.Errorf("BindAddr = %q, want 127.0.0.1:8080", cfg.BindAddr)
	}
	if cfg.DatabaseURL.Expose() != testDBURL {
		t.Error("DatabaseURL does not round-trip")
	}
	if cfg.APIToken.Expose() != testAPIToken {
		t.Error("APIToken does not round-trip")
	}
	if cfg.KEKActiveVersion != 1 {
		t.Errorf("KEKActiveVersion = %d, want 1", cfg.KEKActiveVersion)
	}
	if got := slices.Sorted(maps.Keys(cfg.KEKs)); !slices.Equal(got, []uint32{1}) {
		t.Errorf("KEK versions = %v, want [1]", got)
	}
	if !bytes.Equal(cfg.KEKs[1].Expose(), testKEK(1)) {
		t.Error("KEKs[1] does not round-trip")
	}

	p := cfg.Plaid
	if p.ClientID != testClientID {
		t.Errorf("Plaid.ClientID = %q", p.ClientID)
	}
	if p.Secret.Expose() != testPlaidSecret {
		t.Error("Plaid.Secret does not round-trip")
	}
	if p.Env != PlaidEnvSandbox {
		t.Errorf("Plaid.Env = %q, want sandbox", p.Env)
	}
	if p.RedirectURI != "" || p.WebhookURL != "" {
		t.Errorf("RedirectURI/WebhookURL = %q/%q, want empty", p.RedirectURI, p.WebhookURL)
	}
	if !slices.Equal(p.CountryCodes, []string{"CA"}) {
		t.Errorf("CountryCodes = %v, want [CA]", p.CountryCodes)
	}
	if !slices.Equal(p.Products, []string{"transactions"}) {
		t.Errorf("Products = %v, want [transactions]", p.Products)
	}
	if p.RequiredIfSupportedProducts == nil || len(p.RequiredIfSupportedProducts) != 0 {
		t.Errorf("RequiredIfSupportedProducts = %#v, want empty non-nil", p.RequiredIfSupportedProducts)
	}
	if p.OptionalProducts == nil || len(p.OptionalProducts) != 0 {
		t.Errorf("OptionalProducts = %#v, want empty non-nil", p.OptionalProducts)
	}
	if p.TransactionsDaysRequested != 730 {
		t.Errorf("TransactionsDaysRequested = %d, want 730", p.TransactionsDaysRequested)
	}
	if p.LinkClientName != "plaidsync" || p.LinkLanguage != "en" {
		t.Errorf("LinkClientName/LinkLanguage = %q/%q", p.LinkClientName, p.LinkLanguage)
	}

	s := cfg.Sync
	want := SyncConfig{
		MinInterval: 15 * time.Minute, Interval: 6 * time.Hour, SchedulerEnabled: true,
		MaxAttempts: 5, RetryBase: 2 * time.Second, RetryMax: 2 * time.Minute, Concurrency: 2,
	}
	if s != want {
		t.Errorf("Sync = %+v, want %+v", s, want)
	}
	if cfg.Log.Level != slog.LevelInfo || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v, want info/json", cfg.Log)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 30s", cfg.ShutdownTimeout)
	}
}

func TestLoadDefaultSlicesAreCopies(t *testing.T) {
	a := mustLoad(t, minimalEnv())
	a.Plaid.CountryCodes[0] = "XX"
	a.Plaid.Products[0] = "mutated"
	b := mustLoad(t, minimalEnv())
	if b.Plaid.CountryCodes[0] != "CA" || b.Plaid.Products[0] != "transactions" {
		t.Errorf("defaults were shared between loads: %v %v", b.Plaid.CountryCodes, b.Plaid.Products)
	}
}

func TestLoadFullEnvParsesAndNormalises(t *testing.T) {
	m := minimalEnv()
	m[envBindAddr] = " 0.0.0.0:9090 "
	m[envKEKVersion] = "3"
	m[envKEK] = b64(testKEK(3))
	m[envKEKPrevious] = "1:" + b64(testKEK(1)) + " , 2 : " + rawB64(testKEK(2)) + ","
	m[envPlaidEnv] = "Production"
	m[envRedirectURI] = "https://app.example.com/oauth/plaid"
	m[envWebhookURL] = "https://hooks.example.com/v1/webhook"
	m[envCountryCodes] = "ca, us"
	m[envProducts] = "Transactions"
	m[envRequiredIfSupp] = "liabilities"
	m[envOptionalProds] = "investments, identity"
	m[envTxnDays] = "90"
	m[envLinkClientName] = "Money Insighter"
	m[envLinkLanguage] = "FR"
	m[envSyncMinInterval] = "1m"
	m[envSyncInterval] = "1h30m"
	m[envSchedulerOn] = "FALSE"
	m[envSyncMaxAttempts] = "3"
	m[envSyncRetryBase] = "500ms"
	m[envSyncRetryMax] = "30s"
	m[envSyncConcurrency] = "4"
	m[envLogLevel] = "Debug"
	m[envLogFormat] = "TEXT"
	m[envShutdownTimeout] = "5s"

	cfg := mustLoad(t, m)

	if cfg.BindAddr != "0.0.0.0:9090" {
		t.Errorf("BindAddr = %q (whitespace should be trimmed)", cfg.BindAddr)
	}
	if cfg.KEKActiveVersion != 3 {
		t.Errorf("KEKActiveVersion = %d", cfg.KEKActiveVersion)
	}
	if got := slices.Sorted(maps.Keys(cfg.KEKs)); !slices.Equal(got, []uint32{1, 2, 3}) {
		t.Fatalf("KEK versions = %v, want [1 2 3]", got)
	}
	for v := 1; v <= 3; v++ {
		if !bytes.Equal(cfg.KEKs[uint32(v)].Expose(), testKEK(v)) {
			t.Errorf("KEKs[%d] holds the wrong key", v)
		}
	}

	p := cfg.Plaid
	if p.Env != PlaidEnvProduction {
		t.Errorf("Env = %q", p.Env)
	}
	if p.RedirectURI != m[envRedirectURI] || p.WebhookURL != m[envWebhookURL] {
		t.Errorf("URLs = %q / %q", p.RedirectURI, p.WebhookURL)
	}
	if !slices.Equal(p.CountryCodes, []string{"CA", "US"}) {
		t.Errorf("CountryCodes = %v", p.CountryCodes)
	}
	if !slices.Equal(p.Products, []string{"transactions"}) {
		t.Errorf("Products = %v", p.Products)
	}
	if !slices.Equal(p.RequiredIfSupportedProducts, []string{"liabilities"}) {
		t.Errorf("RequiredIfSupportedProducts = %v", p.RequiredIfSupportedProducts)
	}
	if !slices.Equal(p.OptionalProducts, []string{"investments", "identity"}) {
		t.Errorf("OptionalProducts = %v", p.OptionalProducts)
	}
	if p.TransactionsDaysRequested != 90 {
		t.Errorf("TransactionsDaysRequested = %d", p.TransactionsDaysRequested)
	}
	if p.LinkClientName != "Money Insighter" || p.LinkLanguage != "fr" {
		t.Errorf("LinkClientName/LinkLanguage = %q/%q", p.LinkClientName, p.LinkLanguage)
	}

	want := SyncConfig{
		MinInterval: time.Minute, Interval: 90 * time.Minute, SchedulerEnabled: false,
		MaxAttempts: 3, RetryBase: 500 * time.Millisecond, RetryMax: 30 * time.Second, Concurrency: 4,
	}
	if cfg.Sync != want {
		t.Errorf("Sync = %+v, want %+v", cfg.Sync, want)
	}
	if cfg.Log.Level != slog.LevelDebug || cfg.Log.Format != "text" {
		t.Errorf("Log = %+v", cfg.Log)
	}
	if cfg.ShutdownTimeout != 5*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
}

func TestLoadEachRequiredVarMissing(t *testing.T) {
	required := []string{envDatabaseURL, envAPIToken, envKEK, envPlaidClientID, envPlaidSecret}
	for _, name := range required {
		for _, mode := range []string{"unset", "empty", "whitespace", "placeholder"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				m := minimalEnv()
				wantReason := "required but not set"
				switch mode {
				case "unset":
					delete(m, name)
				case "empty":
					m[name] = ""
				case "whitespace":
					m[name] = " \t\n"
				case "placeholder":
					// The .env.example convention; a forgotten one must fail
					// at startup, not at the first Plaid call.
					m[name] = "<your " + name + " LEAKCHECK>"
					wantReason = "still holds the <placeholder> from .env.example"
				}
				cfg, err := Load(lookup(m))
				if cfg != nil {
					t.Error("config returned alongside an error")
				}
				got := varErrors(t, err)
				if len(got) != 1 || len(got[name]) != 1 {
					t.Fatalf("errors = %v, want exactly one for %s", got, name)
				}
				if got[name][0] != wantReason {
					t.Errorf("reason = %q, want %q", got[name][0], wantReason)
				}
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error text %q does not name %s", err, name)
				}
				if strings.Contains(err.Error(), "LEAKCHECK") {
					t.Errorf("error text %q echoes the value", err)
				}
			})
		}
	}
}

func TestLoadJoinsEveryError(t *testing.T) {
	m := minimalEnv()
	delete(m, envDatabaseURL)
	delete(m, envPlaidSecret)
	m[envAPIToken] = "short-LEAKCHECK"
	m[envSyncInterval] = "six hours"
	m[envLogLevel] = "loud"
	m[envTxnDays] = "731"

	cfg, err := Load(lookup(m))
	if cfg != nil {
		t.Error("config returned alongside an error")
	}
	got := varErrors(t, err)
	want := []string{envDatabaseURL, envPlaidSecret, envAPIToken, envSyncInterval, envLogLevel, envTxnDays}
	for _, name := range want {
		if len(got[name]) == 0 {
			t.Errorf("no error for %s in:\n%v", name, err)
		}
	}
	if len(got) != len(want) {
		t.Errorf("errors for %d variables, want %d:\n%v", len(got), len(want), err)
	}

	// The joined error is walkable with the standard helpers.
	var ve *VarError
	if !errors.As(err, &ve) {
		t.Error("errors.As could not find a *VarError")
	}
	if j, ok := err.(interface{ Unwrap() []error }); !ok || len(j.Unwrap()) != len(want) {
		t.Errorf("expected an errors.Join of %d errors", len(want))
	}
	// One line per problem, each naming its variable.
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	if len(lines) != len(want) {
		t.Errorf("error has %d lines, want %d:\n%v", len(lines), len(want), err)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "config: PLAID") {
			t.Errorf("line %q does not start with the config: VAR prefix", l)
		}
	}
	assertNoLeak(t, "joined error", err.Error())
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	// value is what the variable is set to; extra sets other variables the
	// case depends on. leak, when set, is a substring of the value that
	// must not appear in the error (values like "0" are too short to check).
	type tc struct {
		name  string
		key   string
		value string
		extra map[string]string
		leak  string
	}
	kek31 := b64(testKEK(1)[:31])
	kek33 := b64(append(testKEK(1), 'x'))
	cases := []tc{
		{name: "bind addr without port", key: envBindAddr, value: "127.0.0.1"},
		{name: "bind addr port out of range", key: envBindAddr, value: "127.0.0.1:99999", leak: "99999"},
		{name: "bind addr named port", key: envBindAddr, value: "127.0.0.1:http"},
		{name: "api token 31 bytes", key: envAPIToken, value: strings.Repeat("a", 31), leak: strings.Repeat("a", 31)},
		{name: "kek 31 bytes", key: envKEK, value: kek31, leak: kek31},
		{name: "kek 33 bytes", key: envKEK, value: kek33, leak: kek33},
		{name: "kek not base64", key: envKEK, value: "not*base64*LEAKCHECK", leak: "not*base64"},
		{name: "kek version zero", key: envKEKVersion, value: "0"},
		{name: "kek version negative", key: envKEKVersion, value: "-1"},
		{name: "kek version text", key: envKEKVersion, value: "one-LEAKCHECK", leak: "one-"},
		{name: "kek version overflow", key: envKEKVersion, value: "4294967296", leak: "4294967296"},
		{name: "kek previous no colon", key: envKEKPrevious, value: b64(testKEK(2)), leak: b64(testKEK(2))},
		{name: "kek previous version zero", key: envKEKPrevious, value: "0:" + b64(testKEK(2)), leak: b64(testKEK(2))},
		{name: "kek previous version text", key: envKEKPrevious, value: "v2:" + b64(testKEK(2)), leak: b64(testKEK(2))},
		{name: "kek previous equals active", key: envKEKPrevious, value: "1:" + b64(testKEK(2)), leak: b64(testKEK(2))},
		{name: "kek previous duplicate version", key: envKEKPrevious, value: "2:" + b64(testKEK(2)) + ",2:" + b64(testKEK(3)), leak: b64(testKEK(3))},
		{name: "kek previous wrong length", key: envKEKPrevious, value: "2:" + kek31, leak: kek31},
		{name: "kek previous bad base64", key: envKEKPrevious, value: "2:%%%LEAKCHECK", leak: "%%%"},
		{name: "plaid env retired", key: envPlaidEnv, value: "development", leak: "development"},
		{name: "redirect http non-loopback", key: envRedirectURI, value: "http://example.com/cb", leak: "example.com"},
		{name: "redirect relative", key: envRedirectURI, value: "/oauth/cb", leak: "/oauth/cb"},
		{name: "redirect no host", key: envRedirectURI, value: "https:///cb"},
		{name: "redirect wrong scheme", key: envRedirectURI, value: "ftp://example.com/cb", leak: "ftp://"},
		{name: "redirect unparsable", key: envRedirectURI, value: "https://exa mple.com/%zz", leak: "exa mple"},
		{name: "webhook relative", key: envWebhookURL, value: "/v1/webhook", leak: "/v1/webhook"},
		{name: "webhook wrong scheme", key: envWebhookURL, value: "wss://hooks.example.com", leak: "wss://"},
		{name: "webhook http in production", key: envWebhookURL, value: "http://hooks.example.com/v1/webhook",
			extra: map[string]string{envPlaidEnv: "production"}, leak: "hooks.example.com"},
		{name: "country code three letters", key: envCountryCodes, value: "CAN", leak: "CAN"},
		{name: "country code digit", key: envCountryCodes, value: "C1"},
		{name: "country code duplicate", key: envCountryCodes, value: "CA,ca"},
		{name: "product with space", key: envProducts, value: "transactions sync", leak: "transactions sync"},
		{name: "product duplicate", key: envProducts, value: "transactions,Transactions"},
		{name: "required-if-supported malformed", key: envRequiredIfSupp, value: "liabilities!", leak: "liabilities!"},
		{name: "optional malformed", key: envOptionalProds, value: "-identity", leak: "-identity"},
		{name: "product in two lists", key: envOptionalProds, value: "transactions"},
		{name: "days zero", key: envTxnDays, value: "0"},
		{name: "days too many", key: envTxnDays, value: "731"},
		{name: "days text", key: envTxnDays, value: "ninety-LEAKCHECK", leak: "ninety"},
		{name: "language three letters", key: envLinkLanguage, value: "eng", leak: "eng"},
		{name: "min interval bad", key: envSyncMinInterval, value: "15 minutes", leak: "15 minutes"},
		{name: "min interval negative", key: envSyncMinInterval, value: "-1m"},
		{name: "interval bad", key: envSyncInterval, value: "6", leak: ""},
		{name: "interval zero", key: envSyncInterval, value: "0s"},
		{name: "interval negative", key: envSyncInterval, value: "-6h"},
		{name: "scheduler enabled bad", key: envSchedulerOn, value: "maybe", leak: "maybe"},
		{name: "max attempts zero", key: envSyncMaxAttempts, value: "0"},
		{name: "max attempts text", key: envSyncMaxAttempts, value: "five", leak: "five"},
		{name: "retry base zero", key: envSyncRetryBase, value: "0s"},
		{name: "retry base bad", key: envSyncRetryBase, value: "2sec", leak: "2sec"},
		{name: "retry max below base", key: envSyncRetryMax, value: "1s", extra: map[string]string{envSyncRetryBase: "5s"}},
		{name: "retry max zero", key: envSyncRetryMax, value: "0"},
		{name: "concurrency zero", key: envSyncConcurrency, value: "0"},
		{name: "concurrency negative", key: envSyncConcurrency, value: "-2"},
		{name: "log level bad", key: envLogLevel, value: "verbose", leak: "verbose"},
		{name: "log level with offset", key: envLogLevel, value: "INFO+2", leak: "INFO+2"},
		{name: "log format bad", key: envLogFormat, value: "yaml", leak: "yaml"},
		{name: "shutdown zero", key: envShutdownTimeout, value: "0s"},
		{name: "shutdown bad", key: envShutdownTimeout, value: "30", leak: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := minimalEnv()
			maps.Copy(m, c.extra)
			m[c.key] = c.value
			cfg, err := Load(lookup(m))
			if cfg != nil {
				t.Error("config returned alongside an error")
			}
			got := varErrors(t, err)
			if len(got[c.key]) == 0 {
				t.Fatalf("no error for %s in:\n%v", c.key, err)
			}
			if len(got) != 1 {
				t.Errorf("errors for other variables too: %v", got)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.key) {
				t.Errorf("error %q does not name %s", msg, c.key)
			}
			if c.leak != "" && strings.Contains(msg, c.leak) {
				t.Errorf("error %q includes the value", msg)
			}
			assertNoLeak(t, "error", msg)
		})
	}
}

func TestLoadAcceptsEdgeValues(t *testing.T) {
	cases := []struct {
		name  string
		set   map[string]string
		check func(t *testing.T, cfg *Config)
	}{
		{"redirect http localhost", map[string]string{envRedirectURI: "http://localhost:3000/cb"},
			func(t *testing.T, cfg *Config) {
				wantStr(t, "RedirectURI", cfg.Plaid.RedirectURI, "http://localhost:3000/cb")
			}},
		{"redirect http 127.0.0.1", map[string]string{envRedirectURI: "http://127.0.0.1:3000/cb"}, nil},
		{"redirect http ::1", map[string]string{envRedirectURI: "http://[::1]:3000/cb"}, nil},
		{"redirect http sub.localhost", map[string]string{envRedirectURI: "http://app.localhost/cb"}, nil},
		{"redirect https", map[string]string{envRedirectURI: "https://example.com/cb"}, nil},
		{"webhook http in sandbox", map[string]string{envWebhookURL: "http://example.com/hook"}, nil},
		{"webhook https in production", map[string]string{envPlaidEnv: "PRODUCTION", envWebhookURL: "https://example.com/hook"},
			func(t *testing.T, cfg *Config) { wantStr(t, "Env", string(cfg.Plaid.Env), "production") }},
		{"kek unpadded base64", map[string]string{envKEK: rawB64(testKEK(1))},
			func(t *testing.T, cfg *Config) {
				if !bytes.Equal(cfg.KEKs[1].Expose(), testKEK(1)) {
					t.Error("KEK mismatch")
				}
			}},
		{"kek version large", map[string]string{envKEKVersion: "4294967295"},
			func(t *testing.T, cfg *Config) {
				if cfg.KEKActiveVersion != 4294967295 || cfg.KEKs[4294967295].IsZero() {
					t.Errorf("KEKActiveVersion = %d", cfg.KEKActiveVersion)
				}
			}},
		{"api token exactly 32 bytes", map[string]string{envAPIToken: strings.Repeat("x", 32)}, nil},
		{"scheduler 0", map[string]string{envSchedulerOn: "0"},
			func(t *testing.T, cfg *Config) {
				if cfg.Sync.SchedulerEnabled {
					t.Error("SchedulerEnabled = true")
				}
			}},
		{"scheduler T", map[string]string{envSchedulerOn: "T"}, nil},
		{"level WARN", map[string]string{envLogLevel: "WARN"},
			func(t *testing.T, cfg *Config) {
				if cfg.Log.Level != slog.LevelWarn {
					t.Errorf("Level = %v", cfg.Log.Level)
				}
			}},
		{"level error", map[string]string{envLogLevel: "error"},
			func(t *testing.T, cfg *Config) {
				if cfg.Log.Level != slog.LevelError {
					t.Errorf("Level = %v", cfg.Log.Level)
				}
			}},
		{"days 1", map[string]string{envTxnDays: "1"}, nil},
		{"days 730", map[string]string{envTxnDays: "730"}, nil},
		{"min interval zero disables", map[string]string{envSyncMinInterval: "0"},
			func(t *testing.T, cfg *Config) {
				if cfg.Sync.MinInterval != 0 {
					t.Errorf("MinInterval = %v", cfg.Sync.MinInterval)
				}
			}},
		{"retry max equals base", map[string]string{envSyncRetryBase: "3s", envSyncRetryMax: "3s"}, nil},
		{"bind all interfaces", map[string]string{envBindAddr: ":8080"}, nil},
		{"bind ipv6", map[string]string{envBindAddr: "[::1]:8080"}, nil},
		{"bind hostname", map[string]string{envBindAddr: "localhost:8080"}, nil},
		{"empty optional lists", map[string]string{envRequiredIfSupp: " , ,", envOptionalProds: ""},
			func(t *testing.T, cfg *Config) {
				if len(cfg.Plaid.RequiredIfSupportedProducts) != 0 || len(cfg.Plaid.OptionalProducts) != 0 {
					t.Errorf("lists not empty: %v %v", cfg.Plaid.RequiredIfSupportedProducts, cfg.Plaid.OptionalProducts)
				}
			}},
		{"empty optional string is unset", map[string]string{envLogLevel: "", envLogFormat: "  ", envBindAddr: ""},
			func(t *testing.T, cfg *Config) {
				if cfg.Log.Level != slog.LevelInfo || cfg.Log.Format != "json" || cfg.BindAddr != "127.0.0.1:8080" {
					t.Errorf("defaults not applied: %+v %q", cfg.Log, cfg.BindAddr)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := minimalEnv()
			maps.Copy(m, c.set)
			cfg := mustLoad(t, m)
			if c.check != nil {
				c.check(t, cfg)
			}
		})
	}
}

func wantStr(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func TestLoadNilLookup(t *testing.T) {
	cfg, err := Load(nil)
	if err == nil || cfg != nil {
		t.Fatalf("Load(nil) = %v, %v; want nil config and an error", cfg, err)
	}
}

func TestFromEnvReadsProcessEnvironment(t *testing.T) {
	for k, v := range minimalEnv() {
		t.Setenv(k, v)
	}
	t.Setenv(envBindAddr, "127.0.0.1:18080")
	t.Setenv(envLogFormat, "text")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.BindAddr != "127.0.0.1:18080" || cfg.Log.Format != "text" {
		t.Errorf("FromEnv did not read the process environment: %q %q", cfg.BindAddr, cfg.Log.Format)
	}
	if cfg.APIToken.Expose() != testAPIToken {
		t.Error("APIToken mismatch")
	}
}

func TestFromEnvReportsMissing(t *testing.T) {
	for k := range minimalEnv() {
		t.Setenv(k, "")
	}
	cfg, err := FromEnv()
	if cfg != nil || err == nil {
		t.Fatalf("FromEnv with an empty environment = %v, %v", cfg, err)
	}
	got := varErrors(t, err)
	for k := range minimalEnv() {
		if len(got[k]) == 0 {
			t.Errorf("missing %s not reported", k)
		}
	}
}

// fullConfig loads a config holding every test secret, including two
// previous KEKs, so leak tests cover each secret-bearing field.
func fullConfig(t *testing.T) *Config {
	t.Helper()
	m := minimalEnv()
	m[envKEKVersion] = "3"
	m[envKEK] = b64(testKEK(3))
	m[envKEKPrevious] = "1:" + b64(testKEK(1)) + ",2:" + b64(testKEK(2))
	return mustLoad(t, m)
}

func TestConfigFormattingDoesNotLeak(t *testing.T) {
	cfg := fullConfig(t)
	renderings := map[string]string{
		"%v ptr":    fmt.Sprintf("%v", cfg),
		"%+v ptr":   fmt.Sprintf("%+v", cfg),
		"%#v ptr":   fmt.Sprintf("%#v", cfg),
		"Sprint":    fmt.Sprint(cfg),
		"%v value":  fmt.Sprintf("%v", *cfg),
		"%+v value": fmt.Sprintf("%+v", *cfg),
		"%#v value": fmt.Sprintf("%#v", *cfg),
		"%v KEKs":   fmt.Sprintf("%v", cfg.KEKs),
		"%+v Plaid": fmt.Sprintf("%+v", cfg.Plaid),
		"error":     fmt.Errorf("startup failed with config %+v", cfg).Error(),
	}
	for label, out := range renderings {
		assertNoLeak(t, label, out)
		if !strings.Contains(out, secret.Redacted) {
			t.Errorf("%s does not show %s:\n%s", label, secret.Redacted, out)
		}
	}
	// Non-secret fields are still visible, so the redaction is targeted.
	if out := renderings["%+v ptr"]; !strings.Contains(out, "BindAddr:127.0.0.1:8080") || !strings.Contains(out, testClientID) {
		t.Errorf("%%+v hides non-secret fields:\n%s", out)
	}

	js, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	assertNoLeak(t, "json.Marshal", string(js))
	if !strings.Contains(string(js), `"DatabaseURL":"[REDACTED]"`) {
		t.Errorf("json.Marshal does not redact DatabaseURL:\n%s", js)
	}
}

func TestConfigSlogDoesNotLeak(t *testing.T) {
	cfg := fullConfig(t)
	handlers := map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	}
	for name, mk := range handlers {
		var buf bytes.Buffer
		logger := slog.New(mk(&buf))
		logger.Info("starting", slog.Any("config", cfg))
		logger.Info("by value", slog.Any("config", *cfg))
		logger.Info("fields", "db", cfg.DatabaseURL, "kek", cfg.KEKs[3], "plaid", cfg.Plaid)
		out := buf.String()
		assertNoLeak(t, name+" handler", out)
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("%s handler output has no [REDACTED]:\n%s", name, out)
		}
		if !strings.Contains(out, "127.0.0.1:8080") || !strings.Contains(out, testClientID) {
			t.Errorf("%s handler output hides non-secret fields:\n%s", name, out)
		}
	}
}

func TestLogValueSummary(t *testing.T) {
	cfg := fullConfig(t)
	cfg.Plaid.RedirectURI = "https://app.example.com/cb"

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting", slog.Any("config", cfg))

	var rec struct {
		Config struct {
			BindAddr         string   `json:"bind_addr"`
			DatabaseURL      string   `json:"database_url"`
			APIToken         string   `json:"api_token"`
			KEKActiveVersion uint32   `json:"kek_active_version"`
			KEKVersions      []uint32 `json:"kek_versions"`
			Plaid            struct {
				ClientID     string   `json:"client_id"`
				Secret       string   `json:"secret"`
				Env          string   `json:"env"`
				RedirectURI  string   `json:"redirect_uri"`
				CountryCodes []string `json:"country_codes"`
				Products     []string `json:"products"`
				Days         int      `json:"transactions_days_requested"`
			} `json:"plaid"`
			Sync struct {
				Interval    int64 `json:"interval"`
				Scheduler   bool  `json:"scheduler_enabled"`
				Concurrency int   `json:"concurrency"`
			} `json:"sync"`
			Log struct {
				Level  string `json:"level"`
				Format string `json:"format"`
			} `json:"log"`
			ShutdownTimeout int64 `json:"shutdown_timeout"`
		} `json:"config"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode log line: %v\n%s", err, buf.String())
	}
	c := rec.Config
	if c.BindAddr != "127.0.0.1:8080" {
		t.Errorf("bind_addr = %q", c.BindAddr)
	}
	if c.DatabaseURL != secret.Redacted || c.APIToken != secret.Redacted || c.Plaid.Secret != secret.Redacted {
		t.Errorf("secrets not redacted: %q %q %q", c.DatabaseURL, c.APIToken, c.Plaid.Secret)
	}
	if c.KEKActiveVersion != 3 || !slices.Equal(c.KEKVersions, []uint32{1, 2, 3}) {
		t.Errorf("kek_active_version/kek_versions = %d/%v", c.KEKActiveVersion, c.KEKVersions)
	}
	if c.Plaid.ClientID != testClientID || c.Plaid.Env != "sandbox" || c.Plaid.RedirectURI != "https://app.example.com/cb" {
		t.Errorf("plaid group = %+v", c.Plaid)
	}
	if !slices.Equal(c.Plaid.CountryCodes, []string{"CA"}) || !slices.Equal(c.Plaid.Products, []string{"transactions"}) || c.Plaid.Days != 730 {
		t.Errorf("plaid lists = %+v", c.Plaid)
	}
	if c.Sync.Interval != int64(6*time.Hour) || !c.Sync.Scheduler || c.Sync.Concurrency != 2 {
		t.Errorf("sync group = %+v", c.Sync)
	}
	if c.Log.Level != "INFO" || c.Log.Format != "json" {
		t.Errorf("log group = %+v", c.Log)
	}
	if c.ShutdownTimeout != int64(30*time.Second) {
		t.Errorf("shutdown_timeout = %d", c.ShutdownTimeout)
	}
	assertNoLeak(t, "LogValue JSON", buf.String())
}

func TestLogValueNilReceiver(t *testing.T) {
	var cfg *Config
	v := cfg.LogValue()
	if v.Kind() != slog.KindGroup || len(v.Group()) != 0 {
		t.Errorf("nil LogValue = %v, want empty group", v)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("nil", slog.Any("config", cfg))
	if buf.Len() == 0 {
		t.Error("logging a nil config produced nothing")
	}
}

func TestVarErrorMessage(t *testing.T) {
	err := &VarError{Var: "PLAIDSYNC_KEK", Reason: "must be base64"}
	if got, want := err.Error(), "config: PLAIDSYNC_KEK: must be base64"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	var target *VarError
	if !errors.As(fmt.Errorf("wrapped: %w", err), &target) || target.Var != "PLAIDSYNC_KEK" {
		t.Error("errors.As does not find a wrapped *VarError")
	}
}
