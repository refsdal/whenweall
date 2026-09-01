// Package migrations embeds the goose SQL migrations so the binary migrates itself.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
