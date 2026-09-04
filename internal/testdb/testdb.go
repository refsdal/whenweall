// Package testdb provides per-test isolated Postgres databases.
//
// One postgres:18-alpine container serves the whole `go test` run. A template database is
// migrated once; each New(t) clones it, which costs milliseconds. Clones are dropped in
// t.Cleanup, and the container itself is reaped by testcontainers when the run exits.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/refsdal/whenweall/internal/db"
)

var (
	once    sync.Once
	baseURL string  // connection URL to the container's postgres database
	adminDB *sql.DB // one shared admin pool, opened once in setup(), reused by every New(t)
	initErr error
	seq     atomic.Int64
)

const template = "whenweall_template"

func setup() {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		initErr = err
		return
	}
	baseURL, initErr = ctr.ConnectionString(ctx, "sslmode=disable")
	if initErr != nil {
		return
	}

	// One admin pool for the whole test run: it outlives setup() (unlike the template pool
	// below) because every New(t) clone/drop goes through it via URL().
	adminDB, err = db.Open(ctx, baseURL, 10)
	if err != nil {
		initErr = err
		return
	}
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+template); err != nil {
		initErr = err
		return
	}

	tmpl, err := db.Open(ctx, urlFor(template), 2)
	if err != nil {
		initErr = err
		return
	}
	defer func() { _ = tmpl.Close() }()
	initErr = db.Migrate(ctx, tmpl)
}

func urlFor(name string) string {
	// baseURL ends in "/postgres?sslmode=disable" — swap the database name.
	return strings.Replace(baseURL, "/postgres?", "/"+name+"?", 1)
}

// skipperFailer is the slice of testing.TB unavailable needs — narrow so a test can hand it a
// recorder instead of a real *testing.T.
type skipperFailer interface {
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Unavailable reports that a piece of test infrastructure (a container that would not start)
// is missing. Locally that is a Skip — a laptop without Docker should still run the pure-Go
// tests — but under CI (the CI env var GitHub Actions and every common runner set) it is a hard
// failure: nine of the twelve tested packages get their database from this package, so a
// silently skipped container would let `go test ./...` go green having run almost nothing.
func Unavailable(t testing.TB, what string, err error) {
	t.Helper()
	unavailable(t, os.Getenv("CI") != "", what, err)
}

func unavailable(t skipperFailer, ci bool, what string, err error) {
	if ci {
		t.Fatalf("%s unavailable under CI — refusing to skip: %v", what, err)
		return
	}
	t.Skipf("%s unavailable: %v", what, err)
}

// URL returns the connection string of the clone backing db, and the *sql.DB itself — rooms
// tests need the URL for LISTEN, which the database/sql pool abstraction can't express.
func URL(t *testing.T) (string, *sql.DB) {
	t.Helper()
	once.Do(setup)
	if initErr != nil {
		Unavailable(t, "postgres testcontainer", initErr)
	}
	ctx := context.Background()
	name := fmt.Sprintf("wt_%d_%d", time.Now().UnixNano(), seq.Add(1))
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template)); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(ctx, urlFor(name), 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		if _, err := adminDB.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping test database %s: %v", name, err)
		}
	})
	return urlFor(name), d
}

// New returns an isolated *sql.DB on a freshly cloned, fully migrated database.
// Skips the test if Docker is unavailable — or fails it, under CI (see Unavailable). Closes and
// drops on t.Cleanup.
func New(t *testing.T) *sql.DB {
	t.Helper()
	_, d := URL(t)
	return d
}
