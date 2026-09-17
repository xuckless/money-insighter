package config

import (
	"log/slog"
	"time"
)

// Names of the environment variables read by [Load], kept in one place so
// the loader, its tests, and .env.example can be cross-checked. The tests
// parse this file to verify that .env.example documents every variable.
const (
	envBindAddr        = "PLAIDSYNC_BIND_ADDR"
	envDatabaseURL     = "PLAIDSYNC_DATABASE_URL"
	envAPIToken        = "PLAIDSYNC_API_TOKEN"
	envKEK             = "PLAIDSYNC_KEK"
	envKEKVersion      = "PLAIDSYNC_KEK_VERSION"
	envKEKPrevious     = "PLAIDSYNC_KEK_PREVIOUS"
	envPlaidClientID   = "PLAID_CLIENT_ID"
	envPlaidSecret     = "PLAID_SECRET"
	envPlaidEnv        = "PLAID_ENV"
	envRedirectURI     = "PLAIDSYNC_REDIRECT_URI"
	envWebhookURL      = "PLAIDSYNC_WEBHOOK_URL"
	envCountryCodes    = "PLAIDSYNC_COUNTRY_CODES"
	envProducts        = "PLAIDSYNC_PRODUCTS"
	envRequiredIfSupp  = "PLAIDSYNC_REQUIRED_IF_SUPPORTED_PRODUCTS"
	envOptionalProds   = "PLAIDSYNC_OPTIONAL_PRODUCTS"
	envAdditionalProds = "PLAIDSYNC_ADDITIONAL_CONSENTED_PRODUCTS"
	envTxnDays         = "PLAIDSYNC_TRANSACTIONS_DAYS_REQUESTED"
	envLinkClientName  = "PLAIDSYNC_LINK_CLIENT_NAME"
	envLinkLanguage    = "PLAIDSYNC_LINK_LANGUAGE"
	envLinkClientUser  = "PLAIDSYNC_LINK_CLIENT_USER_ID"
	envSyncMinInterval = "PLAIDSYNC_SYNC_MIN_INTERVAL"
	envSyncInterval    = "PLAIDSYNC_SYNC_INTERVAL"
	envSchedulerOn     = "PLAIDSYNC_SCHEDULER_ENABLED"
	envSyncMaxAttempts = "PLAIDSYNC_SYNC_MAX_ATTEMPTS"
	envSyncRetryBase   = "PLAIDSYNC_SYNC_RETRY_BASE"
	envSyncRetryMax    = "PLAIDSYNC_SYNC_RETRY_MAX"
	envSyncConcurrency = "PLAIDSYNC_SYNC_CONCURRENCY"
	envRecurringOn     = "PLAIDSYNC_RECURRING_ENABLED"
	envLogLevel        = "PLAIDSYNC_LOG_LEVEL"
	envLogFormat       = "PLAIDSYNC_LOG_FORMAT"
	envShutdownTimeout = "PLAIDSYNC_SHUTDOWN_TIMEOUT"
)

// Defaults applied when the corresponding variable is unset or empty.
const (
	defaultBindAddr        = "127.0.0.1:8080"
	defaultKEKVersion      = uint32(1)
	defaultPlaidEnv        = PlaidEnvSandbox
	defaultTxnDays         = 730
	defaultLinkClientName  = "plaidsync"
	defaultLinkLanguage    = "en"
	defaultLinkClientUser  = "plaidsync"
	defaultSyncMinInterval = 15 * time.Minute
	defaultSyncInterval    = 6 * time.Hour
	defaultSchedulerOn     = true
	defaultSyncMaxAttempts = 5
	defaultSyncRetryBase   = 2 * time.Second
	defaultSyncRetryMax    = 2 * time.Minute
	defaultSyncConcurrency = 2
	defaultRecurringOn     = false
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
	kekBytes         = 32
	minTxnDays       = 1
	maxTxnDays       = 730
)

// Slice defaults cannot be constants. [Load] hands out copies, never these
// slices themselves.
var (
	defaultCountryCodes = []string{"CA"}
	defaultProducts     = []string{"transactions"}
)
