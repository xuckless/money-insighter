package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Distinctive secrets. Every one carries the marker LEAKCHECK so that an
// accidental appearance in any output is unambiguous.
const (
	testDBURL      = "postgres://topper:dbpass-LEAKCHECK-hunter2@127.0.0.1:5433/plaidsync?sslmode=disable"
	testReadToken  = "read-token-LEAKCHECK-0123456789abcdef0123456789abcdef"
	testWriteToken = "write-token-LEAKCHECK-0123456789abcdef0123456789abcde"
	testAPIKeys    = "reader:read:" + testReadToken + ",writer:readwrite:" + testWriteToken
)

// minimalEnv is the smallest environment Load accepts: only the required
// variables.
func minimalEnv() map[string]string {
	return map[string]string{
		envDatabaseURL: testDBURL,
		envAPIKeys:     testAPIKeys,
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

func assertNoLeak(t *testing.T, label, out string) {
	t.Helper()
	for _, s := range []string{testDBURL, testReadToken, testWriteToken, "dbpass-LEAKCHECK-hunter2"} {
		if strings.Contains(out, s) {
			t.Errorf("%s leaks a secret:\n%s", label, out)
		}
	}
	if strings.Contains(out, "LEAKCHECK") {
		t.Errorf("%s contains the LEAKCHECK marker:\n%s", label, out)
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg := mustLoad(t, minimalEnv())
	if cfg.BindAddr != defaultBindAddr {
		t.Errorf("BindAddr = %q", cfg.BindAddr)
	}
	if got := cfg.DatabaseURL.Expose(); got != testDBURL {
		t.Errorf("DatabaseURL = %q", got)
	}
	if len(cfg.APIKeys) != 2 {
		t.Fatalf("APIKeys = %+v, want 2", cfg.APIKeys)
	}
	if cfg.APIKeys[0].Name != "reader" || cfg.APIKeys[0].Scope != ScopeRead || cfg.APIKeys[0].Hash != sha256.Sum256([]byte(testReadToken)) {
		t.Errorf("APIKeys[0] = %+v", cfg.APIKeys[0])
	}
	if cfg.APIKeys[1].Name != "writer" || cfg.APIKeys[1].Scope != ScopeReadWrite || cfg.APIKeys[1].Hash != sha256.Sum256([]byte(testWriteToken)) {
		t.Errorf("APIKeys[1] = %+v", cfg.APIKeys[1])
	}
	if cfg.WriteTables == nil || len(cfg.WriteTables) != 0 {
		t.Errorf("WriteTables = %#v, want empty non-nil", cfg.WriteTables)
	}
	if cfg.CORSOrigins == nil || len(cfg.CORSOrigins) != 0 {
		t.Errorf("CORSOrigins = %#v, want empty non-nil", cfg.CORSOrigins)
	}
	if cfg.CacheTTL != defaultCacheTTL || cfg.QueryTimeout != defaultQueryTimeout || cfg.StartupWait != defaultStartupWait {
		t.Errorf("durations = %v %v %v", cfg.CacheTTL, cfg.QueryTimeout, cfg.StartupWait)
	}
	if cfg.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d", cfg.MaxBodyBytes)
	}
	if cfg.Log.Level != slog.LevelInfo || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v", cfg.Log)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
}

func TestLoadExplicitValues(t *testing.T) {
	m := minimalEnv()
	m[envBindAddr] = ":9090"
	m[envWriteTables] = " Tx_Notes , events "
	m[envCacheTTL] = "0"
	m[envCORSOrigins] = "https://App.Example.com,http://localhost:5173"
	m[envQueryTimeout] = "2s"
	m[envStartupWait] = "0"
	m[envMaxBodyBytes] = "1024"
	m[envLogLevel] = "DEBUG"
	m[envLogFormat] = "Text"
	m[envShutdownTimeout] = "5s"
	cfg := mustLoad(t, m)
	if cfg.BindAddr != ":9090" {
		t.Errorf("BindAddr = %q", cfg.BindAddr)
	}
	if want := []string{"tx_notes", "events"}; fmt.Sprint(cfg.WriteTables) != fmt.Sprint(want) {
		t.Errorf("WriteTables = %v, want %v", cfg.WriteTables, want)
	}
	if cfg.CacheTTL != 0 || cfg.StartupWait != 0 {
		t.Errorf("zero durations not honoured: %v %v", cfg.CacheTTL, cfg.StartupWait)
	}
	if want := []string{"https://app.example.com", "http://localhost:5173"}; fmt.Sprint(cfg.CORSOrigins) != fmt.Sprint(want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
	if cfg.QueryTimeout != 2*time.Second || cfg.ShutdownTimeout != 5*time.Second || cfg.MaxBodyBytes != 1024 {
		t.Errorf("values = %v %v %d", cfg.QueryTimeout, cfg.ShutdownTimeout, cfg.MaxBodyBytes)
	}
	if cfg.Log.Level != slog.LevelDebug || cfg.Log.Format != "text" {
		t.Errorf("Log = %+v", cfg.Log)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	m := map[string]string{
		envBindAddr:        "nope",
		envAPIKeys:         "a:b",
		envWriteTables:     "Bad-Name,transactions,views,x,x",
		envCacheTTL:        "-1s",
		envCORSOrigins:     "*,ftp://x,https://a.example.com/path",
		envQueryTimeout:    "0",
		envMaxBodyBytes:    "0",
		envLogLevel:        "loud",
		envLogFormat:       "yaml",
		envShutdownTimeout: "soon",
	}
	cfg, err := Load(lookup(m))
	if cfg != nil {
		t.Fatal("config returned alongside an error")
	}
	got := varErrors(t, err)
	for _, name := range []string{envBindAddr, envDatabaseURL, envAPIKeys, envWriteTables, envCacheTTL, envCORSOrigins, envQueryTimeout, envMaxBodyBytes, envLogLevel, envLogFormat, envShutdownTimeout} {
		if len(got[name]) == 0 {
			t.Errorf("no error reported for %s:\n%v", name, err)
		}
	}
	if n := len(got[envWriteTables]); n != 4 {
		t.Errorf("%s: %d errors, want 4 (bad name, plaid table, reserved, duplicate):\n%v", envWriteTables, n, got[envWriteTables])
	}
	if n := len(got[envCORSOrigins]); n != 3 {
		t.Errorf("%s: %d errors, want 3:\n%v", envCORSOrigins, n, got[envCORSOrigins])
	}
	assertNoLeak(t, "error", err.Error())
}

func TestAPIKeysValidation(t *testing.T) {
	long := strings.Repeat("x", minAPITokenBytes)
	cases := []struct {
		name string
		val  string
		want string // substring of the reason
	}{
		{"short token", "a:read:tooshort", "at least"},
		{"bad scope", "a:admin:" + long, "scope must be"},
		{"bad name", "a b:read:" + long, "name must be"},
		{"dup name", "a:read:" + long + ",a:read:" + long + "y", "name is listed"},
		{"dup token", "a:read:" + long + ",b:read:" + long, "token is listed"},
		{"placeholder", "a:read:<openssl rand -hex 32>", "placeholder"},
		{"missing parts", "a:" + long, "name:scope:token"},
		{"only commas", ",,", "at least one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := minimalEnv()
			m[envAPIKeys] = tc.val
			_, err := Load(lookup(m))
			reasons := varErrors(t, err)[envAPIKeys]
			if len(reasons) == 0 {
				t.Fatalf("no error for %q", tc.val)
			}
			if !strings.Contains(strings.Join(reasons, "\n"), tc.want) {
				t.Errorf("reasons %v do not mention %q", reasons, tc.want)
			}
			if strings.Contains(err.Error(), long) {
				t.Errorf("error quotes the token: %v", err)
			}
		})
	}

	// A token may contain colons; everything after the second colon is it.
	m := minimalEnv()
	colon := "abc:def:" + strings.Repeat("0", minAPITokenBytes)
	m[envAPIKeys] = "svc:readwrite:" + colon
	cfg := mustLoad(t, m)
	if cfg.APIKeys[0].Hash != sha256.Sum256([]byte(colon)) {
		t.Error("token with colons was truncated")
	}
}

func TestPlaceholdersRejected(t *testing.T) {
	m := minimalEnv()
	m[envDatabaseURL] = "<postgres url>"
	_, err := Load(lookup(m))
	if got := varErrors(t, err)[envDatabaseURL]; len(got) == 0 || !strings.Contains(got[0], "placeholder") {
		t.Errorf("placeholder accepted: %v", got)
	}
}

func TestLogValueRedacts(t *testing.T) {
	cfg := mustLoad(t, minimalEnv())
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("start", "config", cfg)
	out := buf.String()
	assertNoLeak(t, "slog", out)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, out)
	}
	c := rec["config"].(map[string]any)
	if c["database_url"] != "[REDACTED]" {
		t.Errorf("database_url = %v", c["database_url"])
	}
	keys := c["api_keys"].([]any)
	if len(keys) != 2 || keys[0].(map[string]any)["name"] != "reader" || keys[0].(map[string]any)["scope"] != "read" {
		t.Errorf("api_keys = %v", keys)
	}
	if fmt.Sprint(keys) != fmt.Sprint(keys) || strings.Contains(out, "hash") {
		t.Errorf("api_keys leak more than name and scope: %v", out)
	}
	if !strings.Contains(fmt.Sprintf("%+v", cfg), "[REDACTED]") {
		t.Errorf("fmt renders the URL: %+v", cfg)
	}
	assertNoLeak(t, "fmt", fmt.Sprintf("%+v %#v %v", cfg, cfg, *cfg))
}

func TestNilLookup(t *testing.T) {
	if _, err := Load(nil); err == nil {
		t.Error("nil lookup accepted")
	}
}
