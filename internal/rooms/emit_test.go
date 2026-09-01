package rooms_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestEmit_RolledBackTxLeavesNoRow(t *testing.T) {
	_, d := testdb.URL(t)
	ctx := context.Background()

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx, "poll:abc", "poll.updated", map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM room_events WHERE room_key = $1", "poll:abc").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("room_events rows after rollback = %d, want 0", n)
	}
}

func TestEmit_CommittedTxRowAndNotify(t *testing.T) {
	url, d := testdb.URL(t)
	ctx := context.Background()

	listener, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connecting listener: %v", err)
	}
	defer func() { _ = listener.Close(context.Background()) }()
	if _, err := listener.Exec(ctx, "LISTEN room_events"); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}

	const roomKey = "poll:xyz"
	const eventType = "poll.updated"
	data := map[string]any{"title": "New title"}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx, roomKey, eventType, data); err != nil {
		t.Fatal(err)
	}

	// Nothing should arrive before commit: NOTIFY is transactional.
	preCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	_, err = listener.WaitForNotification(preCtx)
	cancel()
	if err == nil {
		t.Fatal("received a notification before commit")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting before commit: got %v, want context.DeadlineExceeded", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	postCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	notification, err := listener.WaitForNotification(postCtx)
	if err != nil {
		t.Fatalf("waiting for notification after commit: %v", err)
	}

	var id int64
	var eventJSON []byte
	if err := d.QueryRowContext(ctx,
		"SELECT id, event FROM room_events WHERE room_key = $1", roomKey,
	).Scan(&id, &eventJSON); err != nil {
		t.Fatal(err)
	}

	wantPayload := fmt.Sprintf("%s:%d", roomKey, id)
	if notification.Payload != wantPayload {
		t.Errorf("notification payload = %q, want %q", notification.Payload, wantPayload)
	}

	var got struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(eventJSON, &got); err != nil {
		t.Fatalf("unmarshaling stored event: %v", err)
	}
	if got.Type != eventType {
		t.Errorf("event.type = %q, want %q", got.Type, eventType)
	}
	if got.Data["title"] != "New title" {
		t.Errorf("event.data = %v, want title=New title", got.Data)
	}
}
