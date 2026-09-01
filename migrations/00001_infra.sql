-- +goose Up

-- `rate_limits` is deliberately UNLOGGED.
--
-- The Cloudflare `RateLimitRoom` it replaces held its counters in memory only, on the reasoning
-- that a rate-limit counter is worth less than the write required to persist it, and that losing
-- one fails open for at most a single window. An UNLOGGED table gives that same bargain in
-- Postgres: writes skip the WAL entirely (which matters, because Better-Auth consumes a counter on
-- every auth request), and the table is truncated on crash recovery — the same durability the
-- durable object already chose, not a regression from it.
--
-- Drizzle's schema builder cannot express UNLOGGED, so this is applied here. `drizzle-kit generate`
-- diffs columns and indexes, not table persistence, so this survives future regeneration.
CREATE UNLOGGED TABLE rate_limits (
  key text PRIMARY KEY,
  count integer NOT NULL,
  reset_at timestamptz NOT NULL
);
CREATE INDEX rate_limits_reset_idx ON rate_limits (reset_at);

CREATE TABLE room_events (
  id bigserial PRIMARY KEY,
  room_key text NOT NULL,
  event jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX room_events_room_id_idx ON room_events (room_key, id);
CREATE INDEX room_events_created_idx ON room_events (created_at);

CREATE TABLE room_state (
  room_key text PRIMARY KEY,
  data jsonb NOT NULL DEFAULT '{}'::jsonb,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scheduled_jobs (
  id text PRIMARY KEY,
  kind text NOT NULL,
  room_key text,
  run_at timestamptz NOT NULL,
  payload jsonb,
  attempts integer NOT NULL DEFAULT 0,
  max_attempts integer NOT NULL DEFAULT 5,
  locked_by text,
  locked_at timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scheduled_jobs_due_idx ON scheduled_jobs (run_at);
CREATE UNIQUE INDEX scheduled_jobs_room_kind_idx ON scheduled_jobs (kind, room_key) WHERE room_key IS NOT NULL;

CREATE TABLE ws_presence (
  room_key text NOT NULL,
  replica_id text NOT NULL,
  count integer NOT NULL,
  heartbeat_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (room_key, replica_id)
);
CREATE INDEX ws_presence_heartbeat_idx ON ws_presence (heartbeat_at);

-- +goose Down
DROP TABLE ws_presence;
DROP TABLE scheduled_jobs;
DROP TABLE room_state;
DROP TABLE room_events;
DROP TABLE rate_limits;
