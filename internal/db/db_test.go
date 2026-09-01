package db_test

import (
	"context"
	"testing"

	"github.com/refsdal/whenweall/internal/testdb"
)

func TestMigrationsCreateInfraTables(t *testing.T) {
	d := testdb.New(t)
	for _, table := range []string{"rate_limits", "room_events", "room_state", "scheduled_jobs", "ws_presence"} {
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
