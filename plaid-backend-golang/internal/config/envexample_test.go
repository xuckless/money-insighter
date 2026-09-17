package config

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const envExamplePath = "../../.env.example"

// The database URL .env.example must carry, matching docker-compose.yml.
const composeDatabaseURL = "postgres://plaidsync:plaidsync@127.0.0.1:5433/plaidsync?sslmode=disable"

// envVarNames extracts every environment variable name declared as an env*
// string constant in vars.go, so the test cannot drift from the loader.
func envVarNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "vars.go", nil, 0)
	if err != nil {
		t.Fatalf("parse vars.go: %v", err)
	}
	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, ident := range vs.Names {
				if !strings.HasPrefix(ident.Name, "env") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				names = append(names, v)
			}
		}
	}
	if len(names) < 20 {
		t.Fatalf("found only %d env* constants in vars.go; parser is broken", len(names))
	}
	return names
}

// envExampleEntry is one KEY=value line of .env.example.
type envExampleEntry struct {
	key, value string
	line       int
	commented  bool // a comment line immediately precedes the assignment
}

// parseEnvExample reads .env.example the way `make run` sources it: blank
// lines and #-comments are skipped, and a value wrapped in double quotes is
// unwrapped.
func parseEnvExample(t *testing.T) []envExampleEntry {
	t.Helper()
	f, err := os.Open(envExamplePath)
	if err != nil {
		t.Fatalf("open %s: %v", envExamplePath, err)
	}
	defer f.Close()

	var entries []envExampleEntry
	prevComment := false
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			prevComment = false
			continue
		case strings.HasPrefix(line, "#"):
			prevComment = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("%s:%d: not KEY=value: %q", envExamplePath, n, line)
			continue
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		entries = append(entries, envExampleEntry{key: key, value: value, line: n, commented: prevComment})
		prevComment = false
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", envExamplePath, err)
	}
	return entries
}

func envExampleMap(t *testing.T) map[string]string {
	t.Helper()
	m := make(map[string]string)
	for _, e := range parseEnvExample(t) {
		m[e.key] = e.value
	}
	return m
}

func TestEnvExampleListsEveryVariableOnce(t *testing.T) {
	want := envVarNames(t)
	entries := parseEnvExample(t)

	seen := map[string]int{}
	for _, e := range entries {
		seen[e.key]++
		if seen[e.key] > 1 {
			t.Errorf("%s:%d: %s is listed more than once", envExamplePath, e.line, e.key)
		}
		if !e.commented {
			t.Errorf("%s:%d: %s has no comment line above it", envExamplePath, e.line, e.key)
		}
		if !slices.Contains(want, e.key) {
			t.Errorf("%s:%d: %s is not read by internal/config", envExamplePath, e.line, e.key)
		}
	}
	for _, name := range want {
		if seen[name] == 0 {
			t.Errorf("%s does not list %s", envExamplePath, name)
		}
	}
}

func TestEnvExampleDatabaseURLMatchesCompose(t *testing.T) {
	if got := envExampleMap(t)[envDatabaseURL]; got != composeDatabaseURL {
		t.Errorf("%s = %q, want %q", envDatabaseURL, got, composeDatabaseURL)
	}
}

func TestEnvExamplePlaceholdersAreRejected(t *testing.T) {
	// An unedited copy must not start, and every forgotten placeholder must
	// be named: the loader rejects <angle-bracket> values in required
	// variables, so PLAID_CLIENT_ID and PLAID_SECRET fail at startup rather
	// than at the first Plaid call.
	cfg, err := Load(lookup(envExampleMap(t)))
	if cfg != nil || err == nil {
		t.Fatalf("Load(.env.example as-is) = %v, %v; want an error", cfg, err)
	}
	got := varErrors(t, err)
	for _, name := range []string{envAPIToken, envKEK, envPlaidClientID, envPlaidSecret} {
		if len(got[name]) == 0 {
			t.Errorf("placeholder for %s was accepted:\n%v", name, err)
		}
	}
	// The database URL in the example is the real Compose value.
	if len(got[envDatabaseURL]) > 0 {
		t.Errorf("%s in .env.example is rejected: %v", envDatabaseURL, got[envDatabaseURL])
	}
}

func TestEnvExampleValuesAreTheDefaults(t *testing.T) {
	// With the secret placeholders replaced, .env.example must load, and
	// every explicit value in it must equal the loader's default, so the
	// file documents the real defaults.
	example := envExampleMap(t)
	example[envAPIToken] = testAPIToken
	example[envKEK] = b64(testKEK(1))
	example[envPlaidClientID] = testClientID
	example[envPlaidSecret] = testPlaidSecret
	fromExample := mustLoad(t, example)

	minimal := minimalEnv()
	minimal[envDatabaseURL] = example[envDatabaseURL]
	fromDefaults := mustLoad(t, minimal)

	if !reflect.DeepEqual(fromExample, fromDefaults) {
		t.Errorf(".env.example values differ from the defaults:\n example:  %+v\n defaults: %+v", fromExample, fromDefaults)
	}
}

func TestEnvExampleHasNoRealSecrets(t *testing.T) {
	for _, e := range parseEnvExample(t) {
		switch e.key {
		case envAPIToken, envKEK, envPlaidClientID, envPlaidSecret:
			if !strings.HasPrefix(e.value, "<") || !strings.HasSuffix(e.value, ">") {
				t.Errorf("%s:%d: %s must be an <angle-bracket placeholder>, not a value", envExamplePath, e.line, e.key)
			}
		case envKEKPrevious:
			if e.value != "" {
				t.Errorf("%s:%d: %s must be empty in the example", envExamplePath, e.line, e.key)
			}
		}
	}
}
