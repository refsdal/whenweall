package admin_test

// Ports the behavioral cases from src/server/admin/__tests__/audit.workers.test.ts's
// `recordAdminAction` describe block (the write+read round-trip; "exports no way to change or
// remove a recorded action" is a TS-only reflection check with no Go analogue, so it is not
// ported — Go's admin package simply has no such exported function, which `go vet`/the compiler
// already guarantee statically). The "audit choke point" describe block (driving the raw
// Better-Auth HTTP endpoint) belongs to Task 3/4's mutations, not this port.
//
// List's filters and cursor pagination have no TS analogue at all (audit-query.ts's
// listAdminAuditLog took only a limit) — this plan's Go interface adds them, so their tests below
// are new rather than ported.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/admin"
	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/testdb"
)

var seedSeq atomic.Int64

// seedUser inserts one Limen-shaped user row directly (users(email, updated_at), BIGSERIAL-keyed
// — migrations/00002_auth.sql) and returns its id stringified plus its email, matching the
// auth.Session shape Record expects.
func seedUser(t *testing.T, d *sql.DB) (userID, email string) {
	t.Helper()
	n := seedSeq.Add(1)
	email = fmt.Sprintf("staff-%d@example.com", n)
	var uid int64
	if err := d.QueryRowContext(context.Background(),
		`INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id`, email,
	).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return fmt.Sprint(uid), email
}

func TestRecord_WritesAnAuditableRow(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userID, email := seedUser(t, d)
	actor := &auth.Session{UserID: userID, Email: email, Staff: true}

	err := admin.Record(ctx, d, actor, "set-user-password", "user", "target-9", "ticket 12",
		map[string]any{"fields": []string{"password"}})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	var (
		actorUserID sql.NullInt64
		actorEmail  string
		action      string
		targetType  string
		targetID    sql.NullString
		reason      sql.NullString
		metadata    []byte
	)
	err = d.QueryRowContext(ctx, `
		SELECT actor_user_id, actor_email, action, target_type, target_id, reason, metadata
		FROM admin_audit_log WHERE target_id = 'target-9'
	`).Scan(&actorUserID, &actorEmail, &action, &targetType, &targetID, &reason, &metadata)
	if err != nil {
		t.Fatalf("querying the row Record wrote: %v", err)
	}

	if !actorUserID.Valid || fmt.Sprint(actorUserID.Int64) != userID {
		t.Errorf("actor_user_id = %v, want %s", actorUserID, userID)
	}
	if actorEmail != email {
		t.Errorf("actor_email = %q, want %q", actorEmail, email)
	}
	if action != "set-user-password" {
		t.Errorf("action = %q, want set-user-password", action)
	}
	if targetType != "user" {
		t.Errorf("target_type = %q, want user", targetType)
	}
	if !reason.Valid || reason.String != "ticket 12" {
		t.Errorf("reason = %v, want ticket 12", reason)
	}
	var meta map[string]any
	if err := json.Unmarshal(metadata, &meta); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}
	fields, _ := meta["fields"].([]any)
	if len(fields) != 1 || fields[0] != "password" {
		t.Errorf("metadata.fields = %v, want [password]", meta["fields"])
	}
}

func TestRecord_NilMetadataAndReasonPersistAsNull(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	userID, email := seedUser(t, d)
	actor := &auth.Session{UserID: userID, Email: email, Staff: true}

	if err := admin.Record(ctx, d, actor, "ban-user", "user", "target-empty", "", nil); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var targetID, reason sql.NullString
	var metadata []byte
	err := d.QueryRowContext(ctx, `
		SELECT target_id, reason, metadata FROM admin_audit_log WHERE action = 'ban-user'
	`).Scan(&targetID, &reason, &metadata)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if reason.Valid {
		t.Errorf("reason = %q, want NULL", reason.String)
	}
	if metadata != nil {
		t.Errorf("metadata = %s, want NULL", metadata)
	}
}

func TestRecord_RequiresANonNilActor(t *testing.T) {
	d := testdb.New(t)
	if err := admin.Record(context.Background(), d, nil, "ban-user", "user", "x", "", nil); err == nil {
		t.Fatal("Record with a nil actor: want an error, got nil")
	}
}

// insertAuditRow writes one row directly, bypassing Record, so the filter/cursor tests below can
// control created_at precisely instead of racing the wall clock (Record always uses now()).
func insertAuditRow(t *testing.T, d *sql.DB, id, actorEmail, action, targetType, targetID string, createdAt time.Time) {
	t.Helper()
	_, err := d.ExecContext(context.Background(), `
		INSERT INTO admin_audit_log (id, actor_email, action, target_type, target_id, created_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
	`, id, actorEmail, action, targetType, targetID, createdAt)
	if err != nil {
		t.Fatalf("seeding audit row %s: %v", id, err)
	}
}

func TestList_FiltersByEveryField(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()

	insertAuditRow(t, d, "row-1", "alice@example.com", "ban-user", "user", "u1", base.Add(1*time.Second))
	insertAuditRow(t, d, "row-2", "alice@example.com", "unban-user", "user", "u2", base.Add(2*time.Second))
	insertAuditRow(t, d, "row-3", "bob@example.com", "ban-user", "user", "u3", base.Add(3*time.Second))
	insertAuditRow(t, d, "row-4", "bob@example.com", "delete-poll", "poll", "p1", base.Add(4*time.Second))

	cases := []struct {
		name   string
		filter admin.AuditFilter
		wantID string // wantIDs would be more general, but every case below matches exactly one row
	}{
		{"by action", admin.AuditFilter{Action: "delete-poll"}, "row-4"},
		{"by actor email", admin.AuditFilter{ActorEmail: "bob@example.com", Action: "ban-user"}, "row-3"},
		{"by target type", admin.AuditFilter{TargetType: "poll"}, "row-4"},
		{"by target id", admin.AuditFilter{TargetID: "u2"}, "row-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entries, _, err := admin.List(ctx, d, c.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(entries) != 1 || entries[0].ID != c.wantID {
				ids := make([]string, len(entries))
				for i, e := range entries {
					ids[i] = e.ID
				}
				t.Errorf("List(%+v) = %v, want exactly [%s]", c.filter, ids, c.wantID)
			}
		})
	}

	t.Run("combining filters can exclude everything", func(t *testing.T) {
		entries, _, err := admin.List(ctx, d, admin.AuditFilter{ActorEmail: "alice@example.com", TargetType: "poll"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("List = %d entries, want 0", len(entries))
		}
	})
}

// TestCountAuditLog_MatchesFilterIndependentOfLimit proves Count reports the full filtered set's
// size, not one page of it — the reason handleAuditList's own "total" exists at all.
func TestCountAuditLog_MatchesFilterIndependentOfLimit(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()

	for i := 0; i < 5; i++ {
		insertAuditRow(t, d, fmt.Sprintf("count-%d", i), "counter@example.com", "ban-user", "user",
			fmt.Sprintf("u%d", i), base.Add(time.Duration(i)*time.Second))
	}
	// A row that must NOT be counted, to prove the filter is actually applied.
	insertAuditRow(t, d, "count-other", "someone-else@example.com", "ban-user", "user", "u-other", base)

	total, err := admin.CountAuditLog(ctx, d, admin.AuditFilter{ActorEmail: "counter@example.com", Limit: 2})
	if err != nil {
		t.Fatalf("CountAuditLog: %v", err)
	}
	if total != 5 {
		t.Errorf("CountAuditLog = %d, want 5 (Limit must not shrink the count)", total)
	}

	entries, _, err := admin.List(ctx, d, admin.AuditFilter{ActorEmail: "counter@example.com", Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("List returned %d entries, want 2 (the page, not the total)", len(entries))
	}
}

func TestList_NewestFirst(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()

	insertAuditRow(t, d, "older", "a@example.com", "ban-user", "user", "u1", base)
	insertAuditRow(t, d, "newer", "a@example.com", "ban-user", "user", "u2", base.Add(time.Minute))

	entries, _, err := admin.List(ctx, d, admin.AuditFilter{ActorEmail: "a@example.com"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "newer" || entries[1].ID != "older" {
		t.Fatalf("List order = %+v, want [newer, older]", entries)
	}
}

// TestList_CursorWalksFullSetNoDupesNoGaps seeds a set of rows sharing one created_at instant
// (per keyset-pagination convention, ties are broken by id) plus rows at distinct instants, then
// walks the whole table two rows at a time and asserts the walk visits every row exactly once, in
// strictly non-increasing (created_at, id) order.
// TestList_NextCursorEmptyOnExactBoundaryLastPage is M10's own regression test: when a filtered
// set's remaining rows exactly equal Limit, nextCursor must still be "" (no more rows), not a
// cursor pointing at an empty next page — the bug the naive "len(entries) == limit" check had.
func TestList_NextCursorEmptyOnExactBoundaryLastPage(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()

	insertAuditRow(t, d, "boundary-1", "boundary@example.com", "ban-user", "user", "u1", base.Add(1*time.Second))
	insertAuditRow(t, d, "boundary-2", "boundary@example.com", "ban-user", "user", "u2", base.Add(2*time.Second))

	entries, nextCursor, err := admin.List(ctx, d, admin.AuditFilter{ActorEmail: "boundary@example.com", Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	if nextCursor != "" {
		t.Errorf("nextCursor = %q, want \"\" — exactly Limit rows matched, there is no next page", nextCursor)
	}
}

func TestList_CursorWalksFullSetNoDupesNoGaps(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).UTC()

	const total = 23
	wantIDs := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("walk-%02d", i)
		// Every third row shares an instant with its neighbours, to exercise the id tie-break.
		ts := base.Add(time.Duration(i/3) * time.Second)
		insertAuditRow(t, d, id, "walker@example.com", "ban-user", "user", db.NewID(), ts)
		wantIDs[id] = true
	}

	seen := make(map[string]bool, total)
	var order []admin.AuditEntry
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > total { // a stuck cursor would otherwise loop forever
			t.Fatalf("List did not terminate after %d pages", pages)
		}
		entries, next, err := admin.List(ctx, d, admin.AuditFilter{ActorEmail: "walker@example.com", Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, e := range entries {
			if seen[e.ID] {
				t.Fatalf("List revisited %s — cursor produced a duplicate", e.ID)
			}
			seen[e.ID] = true
			order = append(order, e)
		}
		if next == "" {
			break
		}
		cursor = next
	}

	if len(seen) != total {
		missing := 0
		for id := range wantIDs {
			if !seen[id] {
				missing++
			}
		}
		t.Fatalf("walked %d of %d rows (%d missing) — cursor left a gap", len(seen), total, missing)
	}

	for i := 1; i < len(order); i++ {
		prev, cur := order[i-1], order[i]
		if cur.CreatedAt > prev.CreatedAt {
			t.Fatalf("row %d (%s, %s) sorts after row %d (%s, %s) — not newest-first",
				i, cur.ID, cur.CreatedAt, i-1, prev.ID, prev.CreatedAt)
		}
	}
}
