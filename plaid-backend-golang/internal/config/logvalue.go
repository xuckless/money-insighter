package config

import (
	"log/slog"
	"maps"
	"slices"

	"plaidsync/internal/secret"
)

// LogValue implements [slog.LogValuer] with a summary that is safe for the
// startup log: every non-secret field is included, secrets appear as
// [secret.Redacted], and the keyring is described by its versions only.
// A nil receiver yields an empty group.
func (c *Config) LogValue() slog.Value {
	if c == nil {
		return slog.GroupValue()
	}
	versions := slices.Sorted(maps.Keys(c.KEKs))
	if versions == nil {
		versions = []uint32{}
	}
	return slog.GroupValue(
		slog.String("bind_addr", c.BindAddr),
		slog.String("database_url", secret.Redacted),
		slog.String("api_token", secret.Redacted),
		slog.Uint64("kek_active_version", uint64(c.KEKActiveVersion)),
		slog.Any("kek_versions", versions),
		slog.Group("plaid",
			slog.String("client_id", c.Plaid.ClientID),
			slog.String("secret", secret.Redacted),
			slog.String("env", string(c.Plaid.Env)),
			slog.String("redirect_uri", c.Plaid.RedirectURI),
			slog.String("webhook_url", c.Plaid.WebhookURL),
			slog.Any("country_codes", c.Plaid.CountryCodes),
			slog.Any("products", c.Plaid.Products),
			slog.Any("required_if_supported_products", c.Plaid.RequiredIfSupportedProducts),
			slog.Any("optional_products", c.Plaid.OptionalProducts),
			slog.Any("additional_consented_products", c.Plaid.AdditionalConsentedProducts),
			slog.Int("transactions_days_requested", c.Plaid.TransactionsDaysRequested),
			slog.String("link_client_name", c.Plaid.LinkClientName),
			slog.String("link_language", c.Plaid.LinkLanguage),
			slog.String("link_client_user_id", c.Plaid.LinkClientUserID),
		),
		slog.Group("sync",
			slog.Duration("min_interval", c.Sync.MinInterval),
			slog.Duration("interval", c.Sync.Interval),
			slog.Bool("scheduler_enabled", c.Sync.SchedulerEnabled),
			slog.Int("max_attempts", c.Sync.MaxAttempts),
			slog.Duration("retry_base", c.Sync.RetryBase),
			slog.Duration("retry_max", c.Sync.RetryMax),
			slog.Int("concurrency", c.Sync.Concurrency),
		),
		slog.Group("log",
			slog.String("level", c.Log.Level.String()),
			slog.String("format", c.Log.Format),
		),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
	)
}
