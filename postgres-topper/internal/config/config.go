// Package config loads and validates the topper configuration from the
// environment.
//
// Every setting comes from a TOPPER_* variable (see .env.example). Values
// are trimmed, an empty value counts as unset, enumerated values are matched
// case-insensitively, and every problem is reported at once as one joined
// error so a failed start lists everything to fix. Error messages name the
// variable, never its value; API tokens are reduced to a SHA-256 digest
// before they leave [Load].
package config

import (
	"log/slog"
	"time"

	"postgres-topper/internal/secret"
)

// Scope is what an API key may do.
type Scope string

const (
	// ScopeRead allows GET and HEAD.
	ScopeRead Scope = "read"
	// ScopeReadWrite additionally allows POST and DELETE.
	ScopeReadWrite Scope = "readwrite"
)

// APIKey is one entry of TOPPER_API_KEYS. The token itself is not retained:
// Hash is its SHA-256 digest, which is all the auth layer needs to compare
// a presented token in constant time.
type APIKey struct {
	// Name identifies the caller in logs. It is not secret.
	Name string
	// Scope is read or readwrite.
	Scope Scope
	// Hash is sha256(token).
	Hash [32]byte
}

// Config is the validated configuration of one topper process.
type Config struct {
	// BindAddr is the host:port the HTTP server listens on.
	// TOPPER_BIND_ADDR, default "127.0.0.1:8080".
	BindAddr string

	// DatabaseURL is the Postgres connection string for the topper role.
	// TOPPER_DATABASE_URL, required.
	DatabaseURL secret.Token

	// APIKeys are the callers allowed through /v1. TOPPER_API_KEYS,
	// required, at least one entry.
	APIKeys []APIKey

	// WriteTables are the tables in schema topper exposed read-write.
	// TOPPER_WRITE_TABLES, default none. Never nil.
	WriteTables []string

	// CacheTTL is how long a read of a read-only table or view is served
	// from memory. TOPPER_CACHE_TTL, default 30s; zero disables the cache.
	CacheTTL time.Duration

	// CORSOrigins are the exact origins allowed to call the API from a
	// browser. TOPPER_CORS_ORIGINS, default none (CORS headers disabled).
	// Never nil.
	CORSOrigins []string

	// QueryTimeout bounds every database call made for one request.
	// TOPPER_QUERY_TIMEOUT, default 15s.
	QueryTimeout time.Duration

	// StartupWait is how long startup waits for plaidsync's tables to
	// exist before giving up. TOPPER_STARTUP_WAIT, default 60s; zero
	// checks once.
	StartupWait time.Duration

	// MaxBodyBytes caps the size of a POST body. TOPPER_MAX_BODY_BYTES,
	// default 5 MiB.
	MaxBodyBytes int64

	// Log configures the process logger.
	Log LogConfig

	// ShutdownTimeout bounds graceful shutdown of the HTTP server.
	// TOPPER_SHUTDOWN_TIMEOUT, default 30s.
	ShutdownTimeout time.Duration
}

// LogConfig is the logger configuration.
type LogConfig struct {
	// Level is the minimum level written. TOPPER_LOG_LEVEL, default info.
	Level slog.Level
	// Format is "json" or "text". TOPPER_LOG_FORMAT, default json.
	Format string
}
