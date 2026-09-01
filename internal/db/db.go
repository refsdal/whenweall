// Package db opens the application's Postgres connection pool and applies embedded migrations.
package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/refsdal/whenweall/migrations"
)

// DBTX is the query surface shared by *sql.DB and *sql.Tx (same shape sqlc generates against).
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Open(ctx context.Context, databaseURL string, poolSize int) (*sql.DB, error) {
	d, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	// Migrate holds the advisory lock on one connection while goose executes migrations on
	// another; a pool capped at 1 would have goose wait forever for a connection Migrate is
	// holding. Floor it at 2 so Migrate can never starve itself.
	if poolSize < 2 {
		poolSize = 2
	}
	d.SetMaxOpenConns(poolSize)
	d.SetMaxIdleConns(poolSize)
	// Recycle connections periodically so a long-lived pool doesn't keep using connections
	// that predate a Postgres failover or restart.
	d.SetConnMaxLifetime(30 * time.Minute)
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("database unreachable: %w", err)
	}
	return d, nil
}

// migrationLock is an arbitrary fixed key: replicas booting together must not race goose.
const migrationLock = 727272

func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return err
	}
	defer func() {
		// The connection is about to be released back to the pool (or closed) regardless, so a
		// failed unlock can't be retried here — but it's worth knowing about: it means the
		// advisory lock stays held until Postgres notices the session end.
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock); err != nil {
			slog.Warn("failed to release migration advisory lock", "error", err)
		}
	}()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, sqlDB, ".")
}

const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

// NewID returns a 21-char nanoid-style identifier drawn from crypto/rand.
func NewID() string {
	b := make([]byte, 21)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing means the host is broken
	}
	for i := range b {
		b[i] = idAlphabet[int(b[i])&63]
	}
	return string(b)
}
