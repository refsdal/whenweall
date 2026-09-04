-- +goose Up

-- ws_presence_replica_idx (M10): both presence.go's presenceBootSweep (`DELETE FROM ws_presence
-- WHERE replica_id = $1`) and its presenceHeartbeatLoop (`UPDATE ws_presence SET heartbeat_at =
-- now() WHERE replica_id = $1 AND count > 0`) filter by replica_id ALONE. ws_presence's existing
-- primary key is (room_key, replica_id) — a composite index whose leading column is room_key, so
-- it cannot be used to satisfy a lookup that only ever supplies replica_id (Postgres can't skip a
-- composite index's leading column). Every replica runs presenceHeartbeatLoop every 30s
-- (presence.go's own presenceHeartbeatInterval) for as long as it lives, so this was a sequential
-- scan of the whole table on a fixed cadence, forever, once ws_presence held more than a handful
-- of rows across enough concurrently-active rooms.
CREATE INDEX ws_presence_replica_idx ON ws_presence (replica_id);

-- +goose Down
DROP INDEX ws_presence_replica_idx;
