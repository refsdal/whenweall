// Package db opens the application's Postgres connection pool and applies embedded migrations.
package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"

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
	d.SetMaxOpenConns(poolSize)
	d.SetMaxIdleConns(poolSize)
	if err := d.PingContext(ctx); err != nil {
		d.Close()
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
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLock); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLock)

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
