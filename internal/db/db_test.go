package db_test

import (
	"context"
	"database/sql"
	"io/fs"
	"sync"
	"testing"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/testdb"
	"github.com/refsdal/whenweall/migrations"
)

func TestMigrationsCreateInfraTables(t *testing.T) {
	d := testdb.New(t)
	for _, table := range []string{
		"rate_limits", "room_events", "room_state", "scheduled_jobs", "ws_presence",
		"users", "staff_users", "locked_users", "user_preferences",
		"polls", "poll_options", "participants", "votes", "comments",
		"notification_prefs", "notification_subscriptions",
		"booking_pages", "bookings",
		"admin_audit_log",
	} {
		var n int
		err := d.QueryRowContext(context.Background(),
			"SELECT count(*) FROM information_schema.tables WHERE table_name = $1", table).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
	// rate_limits must be UNLOGGED (relpersistence 'u')
	var persistence string
	if err := d.QueryRowContext(context.Background(),
		"SELECT relpersistence FROM pg_class WHERE relname = 'rate_limits'").Scan(&persistence); err != nil {
		t.Fatal(err)
	}
	if persistence != "u" {
		t.Errorf("rate_limits relpersistence = %q, want u", persistence)
	}
}

func TestClonesAreIsolated(t *testing.T) {
	a, b := testdb.New(t), testdb.New(t)
	ctx := context.Background()
	if _, err := a.ExecContext(ctx, "INSERT INTO room_state (room_key) VALUES ('x')"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRowContext(ctx, "SELECT count(*) FROM room_state").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("clone b sees %d rows from clone a", n)
	}
}

// TestMigrateNeverStarvesOnMinimalPool guards against a deadlock: Migrate holds the advisory
// lock on one connection while goose runs migrations over the same *sql.DB, which needs a
// second connection. A pool floored below 2 would have goose wait forever for a connection
// Migrate is holding. Regression test for that bug.
func TestMigrateNeverStarvesOnMinimalPool(t *testing.T) {
	url, _ := testdb.URL(t)
	ctx := context.Background()

	d, err := db.Open(ctx, url, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := d.Stats().MaxOpenConnections; got != 2 {
		t.Errorf("MaxOpenConnections = %d, want 2 (floored)", got)
	}
}

// TestTwoFactorLeftoversDropped pins migration 00010: Limen's two-factor plugin was unmounted in
// 72a8306, and its schema — declared by the plugin, not by Limen core — must not linger in the
// baseline (sqlc was generating a User.TwoFactorEnabled field nothing read, and admin's
// DeleteUser was clearing a table nothing wrote).
func TestTwoFactorLeftoversDropped(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	var tables int
	if err := d.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_name = 'two_factors'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Error("two_factors table still exists")
	}

	var columns int
	if err := d.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'two_factor_enabled'").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Error("users.two_factor_enabled column still exists")
	}
}

// migrationCount is the number of goose migration files — and, since they are numbered 00001..N
// contiguously, the version_id the schema must sit at when fully migrated.
func migrationCount(t *testing.T) int {
	t.Helper()
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no migration files embedded")
	}
	return len(files)
}

// TestMigrationsDownToZeroAndBackUp is the first time any Down section runs: a broken Down (a
// missing DROP, a wrong dependency order) should surface here, not during an emergency rollback.
func TestMigrationsDownToZeroAndBackUp(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := db.MigrateDownTo(ctx, d, 0); err != nil {
		t.Fatalf("goose down to 0: %v", err)
	}
	var leftover int
	if err := d.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'goose_db_version'",
	).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("%d tables survived a full Down — some migration's Down section is incomplete", leftover)
	}

	if err := db.Migrate(ctx, d); err != nil {
		t.Fatalf("goose up after down: %v", err)
	}
	var version int64
	if err := d.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if int(version) != migrationCount(t) {
		t.Fatalf("version after re-migrating = %d, want %d", version, migrationCount(t))
	}
}

// TestConcurrentMigrateAppliesEachMigrationOnce is spec §6's "auto-run at boot under a PG advisory
// lock" proof: several replicas booting against an empty database at the same instant must each
// return success and leave exactly one version row per migration.
func TestConcurrentMigrateAppliesEachMigrationOnce(t *testing.T) {
	url, d := testdb.URL(t)
	ctx := context.Background()
	if err := db.MigrateDownTo(ctx, d, 0); err != nil {
		t.Fatalf("goose down to 0: %v", err)
	}

	const replicas = 4
	pools := make([]*sql.DB, replicas)
	for i := range pools {
		pool, err := db.Open(ctx, url, 2)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = pool.Close() }()
		pools[i] = pool
	}

	start := make(chan struct{})
	errs := make(chan error, replicas)
	var wg sync.WaitGroup
	for _, pool := range pools {
		wg.Add(1)
		go func(p *sql.DB) {
			defer wg.Done()
			<-start
			errs <- db.Migrate(ctx, p)
		}(pool)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate: %v", err)
		}
	}

	var applied int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != migrationCount(t) {
		t.Fatalf("goose_db_version has %d applied rows, want %d (each migration applied exactly once)", applied, migrationCount(t))
	}
}
