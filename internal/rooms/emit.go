// Package rooms holds the write half of the realtime room design (spec §4): appending events
// to the room_events log and waking the room's WS hub via Postgres NOTIFY. The listening hub
// itself (the LISTEN side, the fan-out to WS connections) arrives in plan 6.
package rooms

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/refsdal/whenweall/internal/db"
)

// event is the jsonb shape written to room_events.event: {"type": ..., "data": ...}.
type event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Emit appends one event to room_events and notifies the "room_events" channel with
// "<roomKey>:<id>" — a pointer, never a payload (spec §4). Call it INSIDE the same transaction
// as the domain write; NOTIFY is transactional, so the notification only fires once that
// transaction commits, and never fires at all if it rolls back.
func Emit(ctx context.Context, tx db.DBTX, roomKey, eventType string, data any) error {
	payload, err := json.Marshal(event{Type: eventType, Data: data})
	if err != nil {
		return fmt.Errorf("rooms: marshal event: %w", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO room_events (room_key, event) VALUES ($1, $2) RETURNING id`,
		roomKey, payload,
	).Scan(&id); err != nil {
		return fmt.Errorf("rooms: insert room_events: %w", err)
	}

	pointer := fmt.Sprintf("%s:%d", roomKey, id)
	if _, err := tx.ExecContext(ctx, `SELECT pg_notify('room_events', $1)`, pointer); err != nil {
		return fmt.Errorf("rooms: notify room_events: %w", err)
	}
	return nil
}
