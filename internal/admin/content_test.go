package admin_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/refsdal/whenweall/internal/admin"
	"github.com/refsdal/whenweall/internal/testdb"
)

// seedReportedContent inserts one poll and one booking page (under handle "reported-org") for the
// content-moderation tests, returning their ids.
func seedReportedContent(t *testing.T, d *sql.DB) (pollID, pageID string) {
	t.Helper()
	ctx := context.Background()
	var orgID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO organizations (name, slug) VALUES ('Reported Org', 'reported-org') RETURNING id`,
	).Scan(&orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO polls (id, organization_id, type, title, timezone, created_at, updated_at)
		VALUES ('scam-poll', $1, 'dates', 'Claim your prize', 'UTC', now(), now())`, orgID); err != nil {
		t.Fatalf("seed poll: %v", err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO booking_pages (id, organization_id, slug, title, timezone, slot_duration_min,
			buffer_before_min, buffer_after_min, min_notice_min, max_days_ahead, availability, created_at, updated_at)
		VALUES ('scam-page', $1, 'free-money', 'Free money call', 'UTC', 30, 0, 0, 0, 30, '{}'::jsonb, now(), now())`,
		orgID); err != nil {
		t.Fatalf("seed booking page: %v", err)
	}
	return "scam-poll", "scam-page"
}

func suspendedAt(t *testing.T, d *sql.DB, table, id string) sql.NullTime {
	t.Helper()
	var at sql.NullTime
	if err := d.QueryRowContext(context.Background(),
		`SELECT suspended_at FROM `+table+` WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read %s.suspended_at: %v", table, err)
	}
	return at
}

// TestHandleContentLookup: staff paste whatever the report gave them — a poll link, a booking
// link, or the bare id from a "Report: poll <id>" subject — and get the one item it names.
func TestHandleContentLookup(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-lookup@example.com")
	seedReportedContent(t, d)

	for _, tc := range []struct {
		q, wantKind, wantID, wantTitle string
	}{
		{"https://whenweall.com/p/scam-poll", "poll", "scam-poll", "Claim your prize"},
		{"scam-poll", "poll", "scam-poll", "Claim your prize"},
		{"https://whenweall.com/book/reported-org/free-money?tz=UTC", "booking-page", "scam-page", "Free money call"},
		{"scam-page", "booking-page", "scam-page", "Free money call"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/content?q="+urlQuery(tc.q), nil)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			var item admin.ContentItem
			if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
				t.Fatal(err)
			}
			if item.Kind != tc.wantKind || item.ID != tc.wantID || item.Title != tc.wantTitle {
				t.Errorf("got %+v, want %s %s %q", item, tc.wantKind, tc.wantID, tc.wantTitle)
			}
			if item.OrganizationName != "Reported Org" {
				t.Errorf("OrganizationName = %q, want Reported Org", item.OrganizationName)
			}
		})
	}

	resp := h.requestJSON(t, client, http.MethodGet, "/api/v1/admin/content?q=nothing-here", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown id: status = %d, want 404", resp.StatusCode)
	}
}

// TestHandleSuspendAndLiftContent (#48): suspending and lifting through the console sets and
// clears suspended_at and writes an audit row each time, with the reason; doing either twice in a
// row is a 409 rather than a second, misleading audit row.
func TestHandleSuspendAndLiftContent(t *testing.T) {
	d := testdb.New(t)
	h := newAdminHTTPHarness(t, d)
	client := staffClient(t, h, "staff-suspend@example.com")
	pollID, pageID := seedReportedContent(t, d)

	for _, tc := range []struct{ kind, id, table string }{
		{"poll", pollID, "polls"},
		{"booking-page", pageID, "booking_pages"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			base := "/api/v1/admin/content/" + tc.kind + "/" + tc.id

			post := func(action string, body map[string]any) int {
				resp := h.requestJSON(t, client, http.MethodPost, base+"/"+action, body)
				defer func() { _ = resp.Body.Close() }()
				return resp.StatusCode
			}

			if got := post("suspend", map[string]any{"reason": "  "}); got != http.StatusBadRequest {
				t.Errorf("suspend without reason: status %d, want 400", got)
			}
			if suspendedAt(t, d, tc.table, tc.id).Valid {
				t.Fatal("suspended without a reason")
			}

			if got := post("suspend", map[string]any{"reason": "phishing report #12"}); got != http.StatusOK {
				t.Fatalf("suspend: status %d, want 200", got)
			}
			if !suspendedAt(t, d, tc.table, tc.id).Valid {
				t.Error("suspended_at not set after suspend")
			}
			if !auditRowExists(t, d, admin.ActionSuspendContent, tc.id) {
				t.Error("suspend left no audit row")
			}
			if got := post("suspend", map[string]any{"reason": "again"}); got != http.StatusConflict {
				t.Errorf("second suspend: status %d, want 409", got)
			}

			if got := post("unsuspend", map[string]any{"reason": "report was mistaken"}); got != http.StatusOK {
				t.Fatalf("unsuspend: status %d, want 200", got)
			}
			if suspendedAt(t, d, tc.table, tc.id).Valid {
				t.Error("suspended_at still set after unsuspend")
			}
			if !auditRowExists(t, d, admin.ActionUnsuspendContent, tc.id) {
				t.Error("unsuspend left no audit row")
			}
			if got := post("unsuspend", map[string]any{"reason": "again"}); got != http.StatusConflict {
				t.Errorf("second unsuspend: status %d, want 409", got)
			}
		})
	}

	t.Run("unknown kind or id", func(t *testing.T) {
		resp := h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/content/user/1/suspend", map[string]any{"reason": "x"})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("unknown kind: status %d, want 404", resp.StatusCode)
		}
		resp = h.requestJSON(t, client, http.MethodPost, "/api/v1/admin/content/poll/missing/suspend", map[string]any{"reason": "x"})
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("unknown id: status %d, want 404", resp.StatusCode)
		}
	})
}

// TestLookupShowsSuspendedAndDeletedContent: the console must still find what it has taken down —
// that is the only way to lift a suspension — and show when the owner has deleted it too.
func TestLookupShowsSuspendedAndDeletedContent(t *testing.T) {
	d := testdb.New(t)
	pollID, _ := seedReportedContent(t, d)
	if _, err := d.ExecContext(context.Background(),
		`UPDATE polls SET suspended_at = now(), deleted_at = now() WHERE id = $1`, pollID); err != nil {
		t.Fatal(err)
	}
	item, err := admin.LookupContent(context.Background(), d, pollID)
	if err != nil || item == nil {
		t.Fatalf("LookupContent = %v, %v", item, err)
	}
	if item.SuspendedAt == nil || item.DeletedAt == nil {
		t.Errorf("got %+v, want both suspendedAt and deletedAt", item)
	}
}

func urlQuery(s string) string { return url.QueryEscape(s) }
