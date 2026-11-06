// Package migrations embeds Desk's Goose SQL migrations into the binary so
// cmd/migrate can run them without a separate goose CLI or filesystem copy.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
