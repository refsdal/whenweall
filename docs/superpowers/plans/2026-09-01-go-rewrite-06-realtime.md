# Go Rewrite Plan 6/8 — Realtime Rooms

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The websocket hub: LISTEN/NOTIFY fan-out of `room_events` (plan 4's `Emit` is the write half), reconnect catch-up, cross-replica presence, bounded send queues, and the three ws endpoints (poll, booking, stats) with the stats room's throttled counters.

**Architecture:** One `Hub` per process. A dedicated pgx connection (never from the pool — a listener owns its session) LISTENs on `room_events`; notifications are `<roomKey>:<id>` pointers; the hub fetches rows and fans out to local subscribers. Clients track the last event id and send it back on reconnect for replay. Design references: `src/rooms/{events,presence,state,lock}.ts` and the client protocols in `src/do/protocol.ts`, `booking-protocol.ts`, `stats-protocol.ts` — read all five; the wire messages must match what the existing frontend components expect (plan 8 ports them as-is).

**Tech Stack:** `github.com/coder/websocket`, `github.com/jackc/pgx/v5` (raw conn for LISTEN), stdlib.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§4)

## Global Constraints

Plan 1's Global Constraints apply. Plus:
- Per-connection send queue is bounded (64 messages); a full queue closes that connection (the `fix/ws-unbounded-do` lesson — carry this comment).
- Auth happens **before** upgrade: poll rooms accept a session or a guest token or nothing (public polls are viewable), booking rooms require org membership, stats is public.
- Wire format (all rooms): server → client JSON `{"type": string, "seq": int64, "data": object}`; the first message is `{"type":"snapshot","seq":<latest id>,"data":<room snapshot>}`; catch-up replays events with `id > since` before going live. Client → server is only `{"type":"ping"}` (answered `{"type":"pong"}`).

---

### Task 1: The Hub — subscribe, fan-out, catch-up

**Files:**
- Create: `internal/rooms/hub.go`, `internal/rooms/listener.go`
- Test: `internal/rooms/hub_test.go`

**Interfaces:**

```go
package rooms

type Event struct { ID int64; RoomKey string; Type string; Data json.RawMessage }

type Hub struct { /* mu, subs map[string]map[*subscriber]struct{}, db *sql.DB, listenURL string, log, replicaID string */ }

func NewHub(listenURL string, sqlDB *sql.DB, log *slog.Logger) *Hub
// Run owns the LISTEN connection: connect (pgx.Connect on listenURL), LISTEN room_events,
// loop WaitForNotification; on "<roomKey>:<id>" fetch that row (via the pool) and Dispatch.
// Reconnects with backoff on connection loss; after a reconnect, every subscribed room's
// clients get a resync nudge (see below) because notifications may have been missed.
func (h *Hub) Run(ctx context.Context) error

// Subscribe registers a local subscriber; returns a buffered channel (cap 64) of marshalled
// frames and an unsubscribe func. Internal — ServeWS (Task 2) is the public face.
// Dispatch delivers an event to local subscribers of its room; a subscriber whose channel is
// full is closed and removed (bounded-queue rule).
// EventsSince(ctx, roomKey, sinceID) ([]Event, error) — the catch-up query over room_events.
```

- Resync nudge: after LISTEN reconnect, send every live subscriber `{"type":"resync"}`; clients respond by re-requesting snapshot (protocol addition; frontend handles it in plan 8 by refetching).

- [ ] **Step 1: Failing tests** (all against `testdb.URL(t)` so LISTEN is real):

```go
func TestEmitReachesSubscriber(t *testing.T)         // Run hub; subscribe "poll:p1"; Emit in a tx via the pool; frame arrives with matching type/seq
func TestEmitOtherRoomNotDelivered(t *testing.T)
func TestTwoHubsBothDeliver(t *testing.T)            // two Hub instances on the same DB (simulated replicas); both subscribers get the event
func TestCatchUpReplaysMissedEvents(t *testing.T)    // 3 events; EventsSince(after first) returns exactly the last 2 in order
func TestSlowSubscriberIsDropped(t *testing.T)       // subscriber that never reads; emit 100 events; its channel is closed; other subscriber got all
```

- [ ] **Step 2: run to verify failure → implement → green** (`go test ./internal/rooms/ -race`).
- [ ] **Step 3: Commit** — `git commit -m "feat(rooms): hub with LISTEN/NOTIFY fan-out and catch-up"`

---

### Task 2: ServeWS + presence

**Files:**
- Create: `internal/rooms/ws.go`, `internal/rooms/presence.go`
- Test: `internal/rooms/ws_test.go`

**Interfaces:**

```go
// SnapshotFunc builds the initial payload for a room (latest seq comes from room_events max id).
type SnapshotFunc func(ctx context.Context, roomKey string) (data any, err error)

type WSOptions struct {
    Snapshot   SnapshotFunc
    // Authorize runs before upgrade; return the roomKey or an error (mapped to 401/403/404).
    Authorize  func(r *http.Request) (roomKey string, err error)
    // Presence: when true, joins/leaves adjust ws_presence and broadcast {"type":"presence","data":{"count":N}}.
    Presence   bool
}
func (h *Hub) ServeWS(opts WSOptions) http.HandlerFunc
```

- Flow per connection: Authorize → `websocket.Accept` (only same-origin per `CheckOrigin` logic; compression on) → presence++ → send snapshot (+ replay `?since=`) → pump: hub channel → `conn.Write` with 5 s write timeout; read loop only for ping/close. On close: presence--, unsubscribe.
- Presence (port `src/rooms/presence.ts` — read it): `INSERT ... ON CONFLICT (room_key, replica_id) DO UPDATE SET count = ws_presence.count + 1, heartbeat_at = now()` on join, decrement on leave (floor 0), total = `SELECT COALESCE(sum(count),0)`; a heartbeat goroutine refreshes this replica's rows every 30 s; presence changes broadcast through `Emit` so all replicas see them. Boot sweep: on `Run` start, delete this replica's stale rows.

- [ ] **Step 1: Failing tests** — use `httptest.NewServer` + `websocket.Dial`:
  - connect → first frame is snapshot with current seq
  - emit → frame arrives over the wire
  - reconnect with `?since=` → missed events replayed in order before live ones
  - two connections → presence event with count 2 on both; close one → count 1
  - Authorize error → HTTP 403 before upgrade (no ws handshake)
- [ ] **Step 2: implement → green → commit** — `git commit -m "feat(rooms): websocket serving with presence and replay"`

---

### Task 3: The three endpoints + stats room

**Files:**
- Create: `internal/rooms/endpoints.go`, `internal/rooms/stats.go`
- Test: `internal/rooms/endpoints_test.go`, `internal/rooms/stats_test.go`

**Source to port:** snapshot shapes from `src/do/PollRoom.ts` / `BookingRoom.ts` / `StatsRoom.ts` (what each sends on join), throttle behavior from StatsRoom.

**Interfaces:**

```go
// Register mounts:
//   GET /api/v1/polls/{id}/ws     — Authorize: poll exists & not deleted; session, guest token, or anonymous. Snapshot: polls.Service.GetView.
//   GET /api/v1/bookings/{pageId}/ws — Authorize: session + org owns page. Snapshot: bookings day listing.
//   GET /api/v1/stats/ws          — public. Snapshot: current counters. Presence off.
func Register(mux *http.ServeMux, h *Hub, a Auth, p *polls.Service, b *bookings.Service, s *StatsService)

// StatsService: counters for the landing page (port StatsRoom).
// Counters live in room_state under room_key 'stats:global' as {"polls": n, "votes": n};
// polls/participants code from plan 4 already Emits to "stats:global" on create/vote —
// wire those Increment calls here if plan 4 left TODO hooks (check; add if missing).
// Broadcast throttling: the hub coalesces "stats:global" dispatches to at most one per 2s,
// sending the latest counters (read from room_state), not each delta (spec §4).
type StatsService struct{ /* db */ }
func (s *StatsService) Increment(ctx context.Context, tx db.DBTX, field string) error // UPDATE room_state jsonb counter + Emit
func (s *StatsService) Snapshot(ctx context.Context, _ string) (any, error)
```

- [ ] **Step 1: Failing tests** — endpoint auth matrix (booking ws 403 for non-member; poll ws open for anonymous on public poll; 404 unknown poll); stats: 10 rapid Increments produce ≤ a few frames but the last one carries the final count (assert coalescing by counting frames over a 3 s window).
- [ ] **Step 2: implement → green.**
- [ ] **Step 3: Wire into serve():** construct Hub, `go hub.Run(ctx)`, `rooms.Register(...)`; plan-4/5 Emit calls need no change (they already write the right rooms). Add `StatsService.Increment` calls in polls create/vote paths if Task 3 found them missing — same tx as the domain write.
- [ ] **Step 4: `go test ./... -race` green. Commit** — `git commit -m "feat(rooms): poll/booking/stats websocket endpoints"`

---

### Task 4: Replica integration proof

**Files:**
- Test: `internal/rooms/replica_test.go`

One test tying the architecture together (this is the "replaces Durable Objects" proof):

- [ ] **Step 1:** Two full stacks (two Hubs + servers) over one `testdb` database. Client A on stack 1, client B on stack 2, same poll room. A vote written through stack 1's polls service arrives at **both** clients with the same seq; a booking on stack 2 reaches stack 1's watcher; presence totals count both replicas' connections.
- [ ] **Step 2:** green → commit — `git commit -m "test(rooms): cross-replica fan-out and presence proof"`
