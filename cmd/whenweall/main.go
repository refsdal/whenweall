// Command whenweall is the application binary: it serves the HTTP API and SPA, runs database
// migrations, and answers the Docker HEALTHCHECK (which has no shell to work with inside the
// scratch image, so the healthcheck is a subcommand of this same binary).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // scratch has no /usr/share/zoneinfo; scheduling needs real tz data.

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/httpserver"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {
	cmd := "serve"
	if len(args) > 1 {
		cmd = args[1]
	}
	switch cmd {
	case "serve":
		return serve()
	case "migrate":
		return migrateCmd()
	case "healthcheck":
		return healthcheck()
	case "version":
		fmt.Println(version)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve|migrate|healthcheck|version)\n", cmd)
		return 2
	}
}

// serve loads configuration, opens the database, migrates on boot if configured to, and runs
// the HTTP server until SIGINT/SIGTERM triggers a graceful shutdown.
func serve() int {
	cfg, warnings, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	sqlDB, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePoolSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer sqlDB.Close()

	if cfg.MigrateOnBoot {
		if err := db.Migrate(ctx, sqlDB); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	srv := httpserver.New(cfg, sqlDB)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// migrateCmd loads configuration, applies pending migrations, and exits — no server started.
func migrateCmd() int {
	cfg, warnings, err := config.FromOS()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	ctx := context.Background()
	sqlDB, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePoolSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer sqlDB.Close()

	if err := db.Migrate(ctx, sqlDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// healthcheck is the Docker HEALTHCHECK entry point: the scratch image has no shell, so `CMD
// ["/whenweall", "healthcheck"]` invokes this instead of curl/wget.
func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
