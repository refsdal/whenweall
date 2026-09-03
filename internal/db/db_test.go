package db_test

import (
	"context"
	"testing"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/testdb"
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
