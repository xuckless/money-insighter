package config

import (
	"errors"
	"log/slog"
	"os"
	"strings"

	"plaidsync/internal/secret"
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
//
// Values are trimmed of surrounding whitespace, and a variable set to an
// empty (or whitespace-only) string is treated as unset, so a ".env" line
// like "PLAIDSYNC_REDIRECT_URI=" leaves the default in place.
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
	cfg.APIToken = r.apiToken()

	cfg.KEKActiveVersion = r.positiveUint32(envKEKVersion, defaultKEKVersion)
	cfg.KEKs = r.keks(cfg.KEKActiveVersion)

	cfg.Plaid = r.plaid()
	cfg.Sync = r.sync()
	cfg.Log = r.log()
	cfg.ShutdownTimeout = r.positiveDuration(envShutdownTimeout, defaultShutdownTimeout)

	if err := r.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// apiToken reads the bearer token and enforces its minimum length.
func (r *reader) apiToken() secret.Token {
	v, ok := r.required(envAPIToken)
	if !ok {
		return secret.Token{}
	}
	if len(v) < minAPITokenBytes {
		r.failf(envAPIToken, "must be at least %d bytes", minAPITokenBytes)
		return secret.Token{}
	}
	return secret.NewToken(v)
}

// plaid reads the Plaid credentials and Link settings.
func (r *reader) plaid() PlaidConfig {
	var p PlaidConfig

	p.ClientID, _ = r.required(envPlaidClientID)
	p.Secret = r.requiredToken(envPlaidSecret)
	p.Env = PlaidEnv(r.choice(envPlaidEnv, string(defaultPlaidEnv),
		string(PlaidEnvSandbox), string(PlaidEnvProduction)))

	if v, ok := r.value(envRedirectURI); ok {
		if reason := checkRedirectURI(v); reason != "" {
			r.fail(envRedirectURI, reason)
		} else {
			p.RedirectURI = v
		}
	}
	if v, ok := r.value(envWebhookURL); ok {
		if reason := checkWebhookURL(v, p.Env); reason != "" {
			r.fail(envWebhookURL, reason)
		} else {
			p.WebhookURL = v
		}
	}

	p.CountryCodes = r.countryCodes()
	p.Products = r.products(envProducts, defaultProducts)
	p.RequiredIfSupportedProducts = r.products(envRequiredIfSupp, nil)
	p.OptionalProducts = r.products(envOptionalProds, nil)
	p.AdditionalConsentedProducts = r.products(envAdditionalProds, nil)
	r.checkProductsDisjoint(p)

	p.TransactionsDaysRequested = r.intInRange(envTxnDays, defaultTxnDays, minTxnDays, maxTxnDays)
	p.LinkClientName = r.optional(envLinkClientName, defaultLinkClientName)
	p.LinkLanguage = r.linkLanguage()
	p.LinkClientUserID = r.optional(envLinkClientUser, defaultLinkClientUser)
	return p
}

// countryCodes reads PLAIDSYNC_COUNTRY_CODES as uppercase ISO 3166-1
// alpha-2 codes, rejecting anything that is not two letters and any
// repetition.
func (r *reader) countryCodes() []string {
	codes := r.list(envCountryCodes, defaultCountryCodes)
	seen := make(map[string]bool, len(codes))
	for i, c := range codes {
		if !isASCIILetters(c, 2) {
			r.failf(envCountryCodes, "entry %d: must be a two-letter ISO 3166-1 country code", i+1)
			continue
		}
		codes[i] = strings.ToUpper(c)
		if seen[codes[i]] {
			r.failf(envCountryCodes, "entry %d: listed more than once", i+1)
		}
		seen[codes[i]] = true
	}
	return codes
}

// products reads one comma-separated product list as lowercase Plaid
// product names, rejecting malformed names and repetition within the list.
func (r *reader) products(key string, def []string) []string {
	items := r.list(key, def)
	seen := make(map[string]bool, len(items))
	for i, p := range items {
		items[i] = strings.ToLower(p)
		if !isProductName(items[i]) {
			r.failf(key, "entry %d: must be a Plaid product name such as transactions or liabilities", i+1)
			continue
		}
		if seen[items[i]] {
			r.failf(key, "entry %d: listed more than once", i+1)
		}
		seen[items[i]] = true
	}
	return items
}

// checkProductsDisjoint rejects a product that appears in more than one of
// the four product lists; Plaid treats that as an invalid request, and it
// is always a configuration mistake.
func (r *reader) checkProductsDisjoint(p PlaidConfig) {
	lists := []struct {
		key   string
		items []string
	}{
		{envProducts, p.Products},
		{envRequiredIfSupp, p.RequiredIfSupportedProducts},
		{envOptionalProds, p.OptionalProducts},
		{envAdditionalProds, p.AdditionalConsentedProducts},
	}
	owner := make(map[string]string)
	for _, l := range lists {
		for _, item := range l.items {
			if first, ok := owner[item]; ok && first != l.key {
				r.failf(l.key, "a product is also listed in %s; each product may appear in only one list", first)
				continue
			}
			owner[item] = l.key
		}
	}
}

// linkLanguage reads PLAIDSYNC_LINK_LANGUAGE as a lowercase two-letter code.
func (r *reader) linkLanguage() string {
	v, ok := r.value(envLinkLanguage)
	if !ok {
		return defaultLinkLanguage
	}
	if !isASCIILetters(v, 2) {
		r.fail(envLinkLanguage, "must be a two-letter language code such as en or fr")
		return defaultLinkLanguage
	}
	return strings.ToLower(v)
}

// sync reads the sync engine and scheduler settings.
func (r *reader) sync() SyncConfig {
	var s SyncConfig
	s.MinInterval = r.nonNegativeDuration(envSyncMinInterval, defaultSyncMinInterval)
	s.Interval = r.positiveDuration(envSyncInterval, defaultSyncInterval)
	s.SchedulerEnabled = r.boolean(envSchedulerOn, defaultSchedulerOn)
	s.MaxAttempts = r.intAtLeast(envSyncMaxAttempts, defaultSyncMaxAttempts, 1)
	s.RetryBase = r.positiveDuration(envSyncRetryBase, defaultSyncRetryBase)
	s.RetryMax = r.positiveDuration(envSyncRetryMax, defaultSyncRetryMax)
	if s.RetryMax < s.RetryBase {
		r.fail(envSyncRetryMax, "must be at least "+envSyncRetryBase)
	}
	s.Concurrency = r.intAtLeast(envSyncConcurrency, defaultSyncConcurrency, 1)
	return s
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
