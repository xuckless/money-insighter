package config

import (
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
)

// LookupFunc reports the value of one environment variable and whether it is
// set, in the shape of os.LookupEnv. [Load] reads every variable through it,
// so tests can supply a map instead of touching the process environment.
type LookupFunc func(key string) (string, bool)

// FromEnv loads the configuration from the process environment. It is
// Load(os.LookupEnv).
func FromEnv() (*Config, error) {
	return Load(os.LookupEnv)
}

// Load reads every configuration variable through lookup, applies defaults,
// validates, and returns the resulting Config.
//
// Validation does not stop at the first problem: every missing or malformed
// variable is reported as a [VarError], and Load returns all of them joined
// with errors.Join so an operator can fix the whole environment in one pass.
// Error messages name the variable and the violated constraint and never
// include the variable's value. When err is non-nil the returned Config is
// nil.
func Load(lookup LookupFunc) (*Config, error) {
	if lookup == nil {
		return nil, errors.New("config: Load requires a non-nil lookup function")
	}
	r := &reader{lookup: lookup}
	cfg := &Config{}

	cfg.BindAddr = r.optional(envBindAddr, defaultBindAddr)
	if reason := checkBindAddr(cfg.BindAddr); reason != "" {
		r.fail(envBindAddr, reason)
	}

	cfg.DatabaseURL = r.requiredToken(envDatabaseURL)
	cfg.APIKeys = r.apiKeys()
	cfg.WriteTables = r.writeTables()
	cfg.CacheTTL = r.nonNegativeDuration(envCacheTTL, defaultCacheTTL)
	cfg.CORSOrigins = r.corsOrigins()
	cfg.QueryTimeout = r.positiveDuration(envQueryTimeout, defaultQueryTimeout)
	cfg.StartupWait = r.nonNegativeDuration(envStartupWait, defaultStartupWait)
	cfg.MaxBodyBytes = int64(r.intAtLeast(envMaxBodyBytes, defaultMaxBodyBytes, 1))
	cfg.Log = r.log()
	cfg.ShutdownTimeout = r.positiveDuration(envShutdownTimeout, defaultShutdownTimeout)

	if err := r.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// apiKeys parses TOPPER_API_KEYS: comma-separated "name:scope:token"
// entries. The token is the remainder after the second colon, so it may
// itself contain colons. Each token is reduced to its SHA-256 digest; the
// plaintext is not kept and never appears in an error.
func (r *reader) apiKeys() []APIKey {
	raw, ok := r.required(envAPIKeys)
	if !ok {
		return nil
	}
	var keys []APIKey
	names := map[string]bool{}
	hashes := map[[32]byte]bool{}
	for i, entry := range strings.Split(raw, ",") {
		n := i + 1
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) != 3 {
			r.failf(envAPIKeys, "entry %d: must be name:scope:token", n)
			continue
		}
		name, scope, token := strings.TrimSpace(parts[0]), strings.ToLower(strings.TrimSpace(parts[1])), strings.TrimSpace(parts[2])
		if !isKeyName(name) {
			r.failf(envAPIKeys, "entry %d: name must be 1..%d letters, digits, hyphens or underscores", n, maxKeyNameLen)
			continue
		}
		if names[name] {
			r.failf(envAPIKeys, "entry %d: name is listed more than once", n)
			continue
		}
		if scope != string(ScopeRead) && scope != string(ScopeReadWrite) {
			r.failf(envAPIKeys, "entry %d: scope must be read or readwrite", n)
			continue
		}
		if isPlaceholder(token) {
			r.failf(envAPIKeys, "entry %d: token still holds the <placeholder> from .env.example", n)
			continue
		}
		if len(token) < minAPITokenBytes {
			r.failf(envAPIKeys, "entry %d: token must be at least %d bytes", n, minAPITokenBytes)
			continue
		}
		h := sha256.Sum256([]byte(token))
		if hashes[h] {
			r.failf(envAPIKeys, "entry %d: token is listed more than once", n)
			continue
		}
		names[name] = true
		hashes[h] = true
		keys = append(keys, APIKey{Name: name, Scope: Scope(scope), Hash: h})
	}
	if len(keys) == 0 && len(r.errs) == 0 {
		r.fail(envAPIKeys, "must list at least one name:scope:token entry")
	}
	return keys
}

// writeTables parses TOPPER_WRITE_TABLES as lowercase table names in the
// topper schema. A name that is not a plain identifier, is repeated, is a
// reserved path segment, or collides with a plaidsync table is rejected.
func (r *reader) writeTables() []string {
	items := r.list(envWriteTables, nil)
	seen := map[string]bool{}
	for i, name := range items {
		n := i + 1
		name = strings.ToLower(name)
		items[i] = name
		switch {
		case !isIdentifier(name):
			r.failf(envWriteTables, "entry %d: must be a lowercase identifier such as tx_notes", n)
		case seen[name]:
			r.failf(envWriteTables, "entry %d: listed more than once", n)
		case slices.Contains(reservedTableNames, name):
			r.failf(envWriteTables, "entry %d: the name is reserved", n)
		case slices.Contains(PlaidTables, name):
			r.failf(envWriteTables, "entry %d: collides with a plaidsync table, which is read-only", n)
		}
		seen[name] = true
	}
	return items
}

// corsOrigins parses TOPPER_CORS_ORIGINS as exact origins, stored
// lowercased so the middleware can compare them case-insensitively.
func (r *reader) corsOrigins() []string {
	items := r.list(envCORSOrigins, nil)
	seen := map[string]bool{}
	for i, o := range items {
		n := i + 1
		if reason := checkOrigin(o); reason != "" {
			r.failf(envCORSOrigins, "entry %d: %s", n, reason)
			continue
		}
		items[i] = strings.ToLower(o)
		if seen[items[i]] {
			r.failf(envCORSOrigins, "entry %d: listed more than once", n)
		}
		seen[items[i]] = true
	}
	return items
}

// log reads the logger settings.
func (r *reader) log() LogConfig {
	var l LogConfig
	switch r.choice(envLogLevel, logLevelInfo, logLevelDebug, logLevelInfo, logLevelWarn, logLevelError) {
	case logLevelDebug:
		l.Level = slog.LevelDebug
	case logLevelWarn:
		l.Level = slog.LevelWarn
	case logLevelError:
		l.Level = slog.LevelError
	default:
		l.Level = defaultLogLevel
	}
	l.Format = r.choice(envLogFormat, defaultLogFormat, logFormatJSON, logFormatText)
	return l
}
