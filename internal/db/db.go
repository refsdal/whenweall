// Package db opens the application's Postgres connection pool and applies embedded migrations.
package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/refsdal/whenweall/migrations"
)

// DBTX is the query surface shared by *sql.DB and *sql.Tx — it matches sqlc's generated DBTX
// interface (internal/polls/queries.DBTX) exactly, so a *sql.DB or *sql.Tx can be passed
// directly to queries.New.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
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

// gooseInit guards goose's process-global configuration (base FS, dialect). Migrate can be called
// from several goroutines at once — the concurrent-boot test does exactly that — and two
// unsynchronized writes to goose's package-level state would be a data race even though both
// write the same values.
var (
	gooseInit    sync.Once
	gooseInitErr error
)

func gooseSetup() error {
	gooseInit.Do(func() {
		goose.SetBaseFS(migrations.FS)
		gooseInitErr = goose.SetDialect("postgres")
	})
	return gooseInitErr
}

// withMigrationLock runs fn while holding the advisory lock on a dedicated connection: replicas
// booting together must not race goose.
func withMigrationLock(ctx context.Context, sqlDB *sql.DB, fn func() error) error {
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
	if err := gooseSetup(); err != nil {
		return err
	}
	return fn()
}

// Migrate applies every pending migration (goose up) under the advisory lock.
func Migrate(ctx context.Context, sqlDB *sql.DB) error {
	return withMigrationLock(ctx, sqlDB, func() error {
		return goose.UpContext(ctx, sqlDB, ".")
	})
}

// MigrateDownTo rolls the schema back to version (0 = undo everything) under the same advisory
// lock. Tests and operator tooling only — `serve` and `migrate` never call it; it exists so the
// Down sections actually get exercised (TestMigrationsDownToZeroAndBackUp) instead of being
// trusted blind until an emergency rollback.
func MigrateDownTo(ctx context.Context, sqlDB *sql.DB, version int64) error {
	return withMigrationLock(ctx, sqlDB, func() error {
		return goose.DownToContext(ctx, sqlDB, ".", version)
	})
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
