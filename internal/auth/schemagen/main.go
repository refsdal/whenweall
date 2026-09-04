// Command schemagen is a dev-only tool, never built into the application image: it builds the
// exact Limen configuration internal/auth.New builds (see auth.GenerateSchemas), with Limen's
// CLI schema serialization turned on, against a throwaway Postgres database. Constructing Limen
// with that config is what makes Limen write .limen/schemas.json (see Limen's
// Config.prepareCLIConfig) — the file github.com/thecodearcher/limen/cmd/limen's own
// `generate migrations` subcommand reads to introspect the live database and emit DDL.
//
// Regeneration steps (including the CLI invocation and how the output gets folded into a goose
// migration) are in docs/limen-migrations.md — this program is only step 1 of that process.
//
// Usage: from the repo root, with the compose db up (`docker compose up -d db`, with
// POSTGRES_PASSWORD exported first):
//
//	go run ./internal/auth/schemagen
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dsn := os.Getenv("SCHEMAGEN_DSN")
	if dsn == "" {
		// The compose db (compose.yaml's "db" service) on its host-mapped port, per
		// docs/limen-migrations.md.
		dsn = "postgres://whenweall:whenweall@localhost:5433/whenweall?sslmode=disable"
	}

	cfg := &config.Config{
		AppURL:      "http://localhost:3000",
		DatabaseURL: dsn,
		LimenSecret: make([]byte, 32), // any 32 bytes — schema shape never depends on the secret's value
		Capabilities: config.Capabilities{
			// Google forced on (with placeholder credentials) so the oauth plugin — and the
			// "accounts" table it owns — is part of the generated schema regardless of whether a
			// real deployment ever configures Google or OIDC. OIDC is deliberately left off:
			// oauth-generic's WithDiscoveryURL fetches the issuer's discovery document at
			// provider-construction time (see plugins/oauth-generic/options.go's
			// resolveDiscovery), which would need a live, reachable issuer to run here — and the
			// oauth plugin's schema is identical regardless of which provider is registered, so
			// enabling just Google is sufficient to cover it.
			Google: true,
		},
		GoogleClientID:     "schemagen-placeholder",
		GoogleClientSecret: "schemagen-placeholder",
	}

	ctx := context.Background()
	sqlDB, err := db.Open(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return fmt.Errorf("schemagen: opening database: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := auth.GenerateSchemas(cfg, sqlDB); err != nil {
		return fmt.Errorf("schemagen: %w", err)
	}

	fmt.Println("wrote .limen/schemas.json")
	return nil
}
