// Package migrations embeds the goose SQL migrations that define the
// plaidsync schema. The store package runs them through goose at startup;
// nothing else should read this filesystem.
//
// Migration files are named NNNNN_description.sql and use the goose SQL
// format (-- +goose Up / -- +goose Down). Never edit a migration that has
// shipped; add a new one.
package migrations

import "embed"

// FS holds every *.sql migration file in this directory. Pass it to goose
// as the migration source.
//
//go:embed *.sql
var FS embed.FS
