// Package migrations embeds the goose SQL migrations that define the topper
// schema. Never edit a migration that has shipped; add a new one.
//
// Every statement is schema-qualified (topper.<object>): the service never
// relies on search_path, and the goose version table lives at
// topper.goose_db_version so it cannot collide with plaidsync's.
package migrations

import "embed"

// FS holds every *.sql migration in this directory, in version order.
//
//go:embed *.sql
var FS embed.FS
