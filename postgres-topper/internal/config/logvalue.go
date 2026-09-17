package config

import (
	"log/slog"

	"postgres-topper/internal/secret"
)

// LogValue implements [slog.LogValuer] with a summary that is safe for the
// startup log: the database URL is redacted and API keys are described by
// name and scope only. A nil receiver yields an empty group.
func (c *Config) LogValue() slog.Value {
	if c == nil {
		return slog.GroupValue()
	}
	// A plain struct slice, not slog groups: the JSON handler renders
	// nested slog.Value groups inside slog.Any as empty objects.
	type keySummary struct {
		Name  string `json:"name"`
		Scope Scope  `json:"scope"`
	}
	keys := make([]keySummary, 0, len(c.APIKeys))
	for _, k := range c.APIKeys {
		keys = append(keys, keySummary{Name: k.Name, Scope: k.Scope})
	}
	return slog.GroupValue(
		slog.String("bind_addr", c.BindAddr),
		slog.String("database_url", secret.Redacted),
		slog.Any("api_keys", keys),
		slog.Any("write_tables", c.WriteTables),
		slog.Duration("cache_ttl", c.CacheTTL),
		slog.Any("cors_origins", c.CORSOrigins),
		slog.Duration("query_timeout", c.QueryTimeout),
		slog.Duration("startup_wait", c.StartupWait),
		slog.Int64("max_body_bytes", c.MaxBodyBytes),
		slog.Group("log",
			slog.String("level", c.Log.Level.String()),
			slog.String("format", c.Log.Format),
		),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
	)
}
