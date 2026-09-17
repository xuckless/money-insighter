package config

import (
	"log/slog"
	"time"
)

// Names of the environment variables read by [Load], kept in one place so
// the loader, its tests, and .env.example can be cross-checked. The tests
// parse this file to verify that .env.example documents every variable.
const (
	envBindAddr        = "TOPPER_BIND_ADDR"
	envDatabaseURL     = "TOPPER_DATABASE_URL"
	envAPIKeys         = "TOPPER_API_KEYS"
	envWriteTables     = "TOPPER_WRITE_TABLES"
	envCacheTTL        = "TOPPER_CACHE_TTL"
	envCORSOrigins     = "TOPPER_CORS_ORIGINS"
	envQueryTimeout    = "TOPPER_QUERY_TIMEOUT"
	envStartupWait     = "TOPPER_STARTUP_WAIT"
	envMaxBodyBytes    = "TOPPER_MAX_BODY_BYTES"
	envLogLevel        = "TOPPER_LOG_LEVEL"
	envLogFormat       = "TOPPER_LOG_FORMAT"
	envShutdownTimeout = "TOPPER_SHUTDOWN_TIMEOUT"
)

// Defaults applied when the corresponding variable is unset or empty.
const (
	defaultBindAddr        = "127.0.0.1:8080"
	defaultCacheTTL        = 30 * time.Second
	defaultQueryTimeout    = 15 * time.Second
	defaultStartupWait     = 60 * time.Second
	defaultMaxBodyBytes    = 5 << 20
	defaultLogLevel        = slog.LevelInfo
	defaultLogFormat       = logFormatJSON
	defaultShutdownTimeout = 30 * time.Second
)

// Accepted values for the enumerated variables. They are compared
// case-insensitively and stored in this canonical lowercase form.
const (
	logFormatJSON = "json"
	logFormatText = "text"

	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
)

// Limits enforced by [Load].
const (
	minAPITokenBytes = 32
	maxKeyNameLen    = 64
	maxIdentifierLen = 63 // Postgres NAMEDATALEN - 1
)

// PlaidTables are the plaidsync tables the topper exposes read-only. A
// write table may not reuse one of these names.
var PlaidTables = []string{"plaid_items", "plaid_accounts", "transactions", "sync_jobs", "sync_runs"}

// reservedTableNames are path segments under /v1 that are not tables.
var reservedTableNames = []string{"views"}
