// Package config loads the plaidsync service configuration from environment
// variables.
//
// [Load] reads every variable through an injected [LookupFunc] (os.LookupEnv
// via [FromEnv]), applies defaults, validates, and reports every problem at
// once as a single joined error so an operator can fix everything in one pass.
// Each problem is a [VarError] that names the offending variable and the
// violated constraint; messages never include a variable's value.
//
// Values are trimmed of surrounding whitespace, and a variable that is set to
// an empty string is treated as unset. Enumerated values (PLAID_ENV,
// PLAIDSYNC_LOG_LEVEL, PLAIDSYNC_LOG_FORMAT, country codes, product names,
// the Link language) are matched case-insensitively and stored normalised.
//
// Secrets are held as [secret.Token] / [secret.Bytes], so a loaded [Config]
// can be formatted with fmt, marshalled to JSON, or logged without leaking
// them. [Config.LogValue] produces a startup-log summary that lists KEK
// versions but never key material.
package config

import (
	"log/slog"
	"time"

	"plaidsync/internal/secret"
)

// PlaidEnv identifies the Plaid environment the service talks to.
type PlaidEnv string

const (
	// PlaidEnvSandbox is Plaid's Sandbox environment: test institutions and
	// synthetic data only.
	PlaidEnvSandbox PlaidEnv = "sandbox"
	// PlaidEnvProduction is Plaid's Production environment: real
	// institutions and real account data.
	PlaidEnvProduction PlaidEnv = "production"
)

// Config is the complete, validated service configuration. Build it with
// [Load] or [FromEnv]; the zero Config is not valid.
//
// The comment on each field names the environment variable it comes from and
// its default; fields without a default are required.
type Config struct {
	// BindAddr is the host:port the HTTP server listens on; the port must
	// be numeric. PLAIDSYNC_BIND_ADDR, default "127.0.0.1:8080".
	BindAddr string

	// DatabaseURL is the Postgres connection string handed to the store.
	// PLAIDSYNC_DATABASE_URL, required.
	DatabaseURL secret.Token

	// APIToken is the static bearer token that authenticates /v1/* callers.
	// PLAIDSYNC_API_TOKEN, required, at least 32 bytes.
	APIToken secret.Token

	// KEKActiveVersion is the version under which new access tokens are
	// encrypted. PLAIDSYNC_KEK_VERSION, default 1, must be greater than 0.
	KEKActiveVersion uint32

	// KEKs holds every key encryption key by version: PLAIDSYNC_KEK (base64
	// of 32 bytes, required) at KEKActiveVersion, plus each
	// "version:base64" pair from PLAIDSYNC_KEK_PREVIOUS for older versions
	// that are still needed to decrypt existing rows.
	KEKs map[uint32]secret.Bytes

	// Plaid configures the Plaid client and Link.
	Plaid PlaidConfig

	// Sync configures the sync engine and scheduler.
	Sync SyncConfig

	// Log configures the process logger.
	Log LogConfig

	// ShutdownTimeout bounds graceful shutdown of the HTTP server.
	// PLAIDSYNC_SHUTDOWN_TIMEOUT, default 30s, must be positive.
	ShutdownTimeout time.Duration
}

// PlaidConfig holds the Plaid credentials and Link token settings.
type PlaidConfig struct {
	// ClientID is the Plaid client identifier. PLAID_CLIENT_ID, required.
	ClientID string

	// Secret is the Plaid API secret for Env. PLAID_SECRET, required.
	Secret secret.Token

	// Env selects the Plaid environment. PLAID_ENV, default sandbox.
	Env PlaidEnv

	// RedirectURI is the OAuth redirect URI registered in the Plaid
	// dashboard. PLAIDSYNC_REDIRECT_URI, optional; when set it must be an
	// absolute https URL (http is accepted only for loopback hosts).
	RedirectURI string

	// WebhookURL is the public URL Plaid delivers webhooks to.
	// PLAIDSYNC_WEBHOOK_URL, optional; when set it must be an absolute URL,
	// and https when Env is production.
	WebhookURL string

	// CountryCodes are the ISO 3166-1 alpha-2 codes passed to Link.
	// PLAIDSYNC_COUNTRY_CODES, comma-separated, default ["CA"]; stored
	// uppercase.
	CountryCodes []string

	// Products are the Plaid products Link must obtain consent for.
	// PLAIDSYNC_PRODUCTS, comma-separated, default ["transactions"];
	// stored lowercase.
	Products []string

	// RequiredIfSupportedProducts are products used only when the selected
	// institution supports them. PLAIDSYNC_REQUIRED_IF_SUPPORTED_PRODUCTS,
	// comma-separated, default empty. Never nil.
	RequiredIfSupportedProducts []string

	// OptionalProducts are products the user may decline.
	// PLAIDSYNC_OPTIONAL_PRODUCTS, comma-separated, default empty. Never nil.
	OptionalProducts []string

	// TransactionsDaysRequested is the transaction history depth requested
	// at Link time. PLAIDSYNC_TRANSACTIONS_DAYS_REQUESTED, default 730,
	// range 1..730.
	TransactionsDaysRequested int

	// LinkClientName is the application name Link displays.
	// PLAIDSYNC_LINK_CLIENT_NAME, default "plaidsync".
	LinkClientName string

	// LinkLanguage is the two-letter language code for Link.
	// PLAIDSYNC_LINK_LANGUAGE, default "en"; stored lowercase.
	LinkLanguage string
}

// SyncConfig holds the sync engine and scheduler settings.
type SyncConfig struct {
	// MinInterval is the debounce window for manually triggered syncs: a
	// trigger arriving sooner than this after a successful sync is answered
	// from the database without calling Plaid. PLAIDSYNC_SYNC_MIN_INTERVAL,
	// default 15m; zero disables the debounce.
	MinInterval time.Duration

	// Interval is the scheduler period. PLAIDSYNC_SYNC_INTERVAL, default 6h,
	// must be positive.
	Interval time.Duration

	// SchedulerEnabled turns the in-process scheduler on or off.
	// PLAIDSYNC_SCHEDULER_ENABLED, default true.
	SchedulerEnabled bool

	// MaxAttempts caps retries of a retryable Plaid error within one run.
	// PLAIDSYNC_SYNC_MAX_ATTEMPTS, default 5, at least 1.
	MaxAttempts int

	// RetryBase is the initial backoff between attempts.
	// PLAIDSYNC_SYNC_RETRY_BASE, default 2s, must be positive.
	RetryBase time.Duration

	// RetryMax caps the backoff between attempts. PLAIDSYNC_SYNC_RETRY_MAX,
	// default 2m, must be at least RetryBase.
	RetryMax time.Duration

	// Concurrency is how many items the scheduler syncs in parallel.
	// PLAIDSYNC_SYNC_CONCURRENCY, default 2, at least 1.
	Concurrency int
}

// LogConfig holds the logger settings.
type LogConfig struct {
	// Level is the minimum level that is logged. PLAIDSYNC_LOG_LEVEL:
	// debug, info, warn or error; default info.
	Level slog.Level

	// Format selects the slog handler. PLAIDSYNC_LOG_FORMAT: json or text;
	// default json.
	Format string
}
