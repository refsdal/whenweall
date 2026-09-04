# internal/rooms wire protocol

This is the one place the realtime WS wire format is documented in full — every frame shape, every
query parameter, every per-route auth rule, and the client-side rules a consumer MUST follow to
stay correct under at-least-once delivery. `hub.go`, `ws.go`, `endpoints.go`, and `stats.go` each
point their own doc comments here instead of re-deriving pieces of this independently; if this file
and a doc comment ever disagree, this file wins — update the code comment, not this file, unless
the code itself is what's wrong.

## The three routes

| Route | Auth | Room key | Presence |
| --- | --- | --- | --- |
| `GET /api/v1/polls/{id}/ws` | public (snapshot always anonymous) | `poll:{id}` | on |
| `GET /api/v1/booking-pages/{pageId}/ws` | public (Snapshot data withheld unless the caller manages the page) | `booking:{pageId}` | off |
| `GET /api/v1/stats/ws` | public, no gate at all | `stats:global` | off |

The stats room additionally has a plain REST read, `GET /api/v1/stats`, returning the same
`UsageStats` object the stats route's snapshot frame nests under `data`, with
`Cache-Control: no-store`. The landing route's loader uses it for first paint (and it is the only
source of real numbers behind a proxy that does not forward WebSocket upgrades); the socket
remains the live source once connected. Like the WS route, it is public but not unmetered: it sits
behind its own `PublicRateLimit` bucket (`rooms.stats_read`, 60/min per IP — generous headroom for
the once-per-page-load loader call, separate from `ws_connect`'s 30/min budget above), since it
costs the same DB read the WS route's own snapshot frame does.

The booking route's path is `/api/v1/booking-pages/{pageId}/ws` (renamed from
`/api/v1/bookings/{pageId}/ws` to match the REST surface's own `/api/v1/booking-pages/*` naming —
see the M5 hardening pass). Presence is off for booking (M4: this route has no presence UI to
feed) and for stats (a global anonymous counter has no per-viewer identity worth counting) — poll
is the only route that broadcasts a live viewer count.

- **polls**: anonymous, a guest edit token, or a signed-in session may all connect. A missing or
  soft-deleted poll id is a 404 (`not_found`), rejected during the WS handshake itself (before any
  upgrade is attempted) via the standard JSON error envelope.
- **booking-pages**: same connect gate as polls — anonymous, guest, or signed-in, all connect; a
  missing or soft-deleted page id is a 404 (`not_found`), same as polls. Originally gated
  session-required + manager-only, on the theory that this route only served the organiser's own
  dashboard — it doesn't: its one actual caller,
  `web/src/routes/book/$handle/$slug.tsx`'s `useLivePage`, is the PUBLIC `/book/{org}/{page}` page,
  run by an anonymous visitor (go-rewrite-08 T7's review fix — the old gate meant that visitor's
  own socket 401'd immediately, silently killing the "watch this page for live availability
  changes" feature the frontend actually shipped). The *connection* is public; the *data* isn't —
  Snapshot only returns the real (PII-bearing) `BookingSnapshot` payload to a caller whose session
  manages `pageId` (the same check `GET /api/v1/booking-pages/{id}/bookings` uses); every other
  caller gets `"data":null`, which the frontend already treats identically to a real payload (see
  `use-live-page.ts`'s own doc comment — it never reads `data` at all, only that a frame arrived).
- **stats**: no gate. Every caller — signed in, guest, or fully anonymous — connects.

Every route's connect attempt is rejected (404, per the table above — no route in this list can
ever 401/403 a connect anymore) with the SAME handshake-time JSON error envelope
`internal/httpserver.Err` writes for a REST call — the WS upgrade is only attempted once Authorize
succeeds.

All three routes now sit behind the same 30-connects-per-minute-per-IP budget
(`internal/httpserver.PublicRateLimit`); exceeding it is a `429` with `code: "rate_limited"` and a
`Retry-After` header, same as any other rate-limited REST route in this codebase. booking-pages
was originally exempt (reasoning: gated behind a signed-in manager, a meaningfully higher bar than
an anonymous per-IP budget) — that reasoning no longer holds now that an anonymous caller is the
expected, common case for this route rather than an edge case.

The budget (like every `PublicRateLimit` bucket in this codebase) is a pass-through when the
server runs with `ENABLE_TEST_ROUTES=true`, so the Playwright harness's single shared client IP
never trips it.

## Query parameters

- `?since=<seq>` — optional. Requests a backfill of every room event with `seq` (an event's
  `room_events.id`) strictly greater than the given value, delivered in order right after the
  snapshot frame and before any live frame. Omit it (or send a non-numeric/negative value) for an
  ordinary first connect — that is NOT the same as `?since=0` ("replay everything"), which is a
  real, different request. See "Recovery: snapshot is primary, `?since=` is belt-and-braces" below
  for why this is never the only thing a reconnecting client should rely on.
- There is **no identity parameter**. The poll route's snapshot is always the anonymous `PollView`
  (what `GET /api/v1/polls/{id}` returns to a caller with no session and no token); `GetView` never
  reads a guest edit token at all (`polls.Service.GetView` is called with only `viewer.UserID` —
  `Viewer.GuestParticipantID` is read by the participant-mutating endpoints, Claim/Unclaim/
  AddParticipant/..., never by this one), so there is no header a guest could send to get anything
  other than the same anonymous view every other caller gets. A client MUST NOT put a guest edit
  token on the WS URL regardless — query strings land in reverse-proxy access logs, and nothing
  server-side would read it there either.

## Server → client frames

Every frame is a single JSON text message. There are exactly five shapes.

### 1. Snapshot — always the very first frame, every route

```json
{"type": "snapshot", "seq": 58, "data": { /* route-specific, or null */ }}
```

- `seq` is the room's current `max(room_events.id)` at the moment this frame was built (0 for a
  room with no events yet) — the cursor a client that wants to use `?since=` on a later reconnect
  should remember.
- `data` is nested (unlike every other frame below, which flattens) because a snapshot is not a
  `room_events` row at all — there is no envelope to unwrap. Per route:
  - polls: the same `*PollView` JSON `GET /api/v1/polls/{id}` returns to a fully anonymous caller
    (no session, no token), or `null` if PollExists's own Authorize gate already ruled out a
    missing poll (in practice `data` is never null for polls, since Authorize would have 404'd
    first — see PollService's own doc comment for why Snapshot is queried completely fresh rather
    than reusing anything Authorize saw). A guest's own claim/participant identity never comes back
    from this endpoint by token (see the query-parameters section above) — the SPA resolves it
    entirely client-side, by matching the participant ids in this same anonymous `PollView` against
    whatever edit tokens it already holds in `localStorage` (`web/src/lib/edit-tokens.ts`).
  - booking-pages: an array of booking views across the page's own visible horizon (now through
    `MaxDaysAhead`).
  - stats: the full `UsageStats` object — `{"pollsCreated": 12, "pollsFinalized": 3, "responsesYes":
    40, "responsesIfNeedBe": 5, "responsesNo": 8}` — never null (an unseeded room reads as
    all-zero, not absent).

### 2. Backfill — zero or more, only when `?since=` was given, before any live frame

Byte-for-byte the same shape as a live frame (below) for the same underlying row — a client
handling one generic "frame" type per `type` value never needs to know whether a given message
arrived via backfill or live.

### 3. Live entity frames — flattened, one per `room_events` row

```json
{"type": "poll.changed", "seq": 61, "entity": "vote"}
```

The event's own domain payload fields (`entity`, or whatever a given `rooms.Emit` call's data
carried) sit at the TOP level, alongside the protocol fields `type` and `seq` — never nested under
a `data` key. `type` and `seq` always win over a same-named domain field. A payload with no data at
all (`Emit(..., nil)`) yields just `{"type": "...", "seq": N}`.

The stats room's own live frame follows this exact same flattening — this is the one place this
codebase used to claim (falsely — since fixed, see stats.go's own doc comment) that snapshot and
live share "one shape": they do NOT. A stats live frame looks like:

```json
{"type": "stats", "seq": 61, "pollsCreated": 12, "pollsFinalized": 3, "responsesYes": 40, "responsesIfNeedBe": 5, "responsesNo": 8}
```

— `UsageStats`'s fields flattened at the top level, exactly like poll/booking's own live frames,
NOT nested under a second `"stats"` key the way `stats-protocol.ts`'s original message shape was.
A client must branch on `"type"` (`"snapshot"` vs `"stats"`) to know whether a given stats message
is nested or flat, same as any other route.

### 4. Presence — poll room only, live, flattened (a `room_events` row like any other)

```json
{"type": "presence", "seq": 62, "count": 3}
```

Broadcast on every join/leave for the room (`presence.go`), and once more, out of band, whenever
`internal/jobs`'s `presence:sweep` housekeeping job deletes a stale replica's row for that room
(the room's true total just changed even though nobody joined or left — see
`rooms.BroadcastPresenceTotal`'s own doc comment).

### 5. Resync — no `seq` at all, sent at most once per reconnect of the HUB's own LISTEN session

```json
{"type": "resync"}
```

This is not a per-client event — it fires for every currently-subscribed room, on every replica,
whenever that replica's own dedicated Postgres LISTEN connection comes back after being lost (see
`listener.go`'s `Run`). See "Recovery" below for what a client MUST do on receiving it.

### Pong — the server's one reply to a client ping

```json
{"type": "pong"}
```

## Client → server

The only message this protocol ever expects FROM a client is:

```json
{"type": "ping"}
```

Anything else received is silently ignored (not an error, not a close) — `readPumpLoop`
(`ws.go`) only ever recognizes this one shape and answers it inline with a pong. There is no
client-side ack, ordering requirement, or dedupe concern on this direction: it exists purely as an
application-level "are you still there" a client may use on its own cadence (independent of the
server's own keepalive pings — see below — which are plain WebSocket-protocol pings/pongs, a
different mechanism from this JSON ping/pong pair).

Server-initiated keepalive (I4): every connection is pinged (the underlying WebSocket protocol
ping, not this JSON message) every 30s; a peer that doesn't answer within 10s is presumed dead and
the connection is torn down server-side. A client library that already answers protocol-level
pings automatically (every normal browser `WebSocket` and `coder/websocket` client does) needs no
code for this at all.

## Delivery rules a client MUST follow

These come from `hub.go`'s own CONTRACT block — restated here for a client implementer who will
never read the Go source.

1. **At-least-once, never exactly-once.** The same `seq` can arrive more than once (a deliberate,
   accepted duplicate in the routine case — two events committed in one transaction can each
   trigger a redundant delivery of the other). A client MUST dedupe by keeping a SET of every
   `seq` it has already applied, and skip anything already in that set.
2. **`seq` is NOT monotonically increasing in delivery order.** A lower `seq` can arrive strictly
   after a higher one (a "late committer" — the row committed after a numerically-higher-id row,
   even though it was allocated first). Never dedupe or filter with `seq <= lastSeenSeq` — that
   silently and permanently discards exactly the events this whole design exists to deliver. Dedupe
   by SET MEMBERSHIP only. If a client keeps a catch-up cursor at all (to pass as its own next
   `?since=`), advance it by the MAXIMUM `seq` observed so far, never "the last one that arrived."
3. **On `{"type":"resync"}`, refetch a full snapshot from the ordinary REST endpoint for whatever
   view this connection covers.** Never call `?since=` (or anything else) to "catch up" a resync:
   while disconnected, an arbitrary and unknown set of the room's events were lost forever
   (Postgres does not queue `NOTIFY`s for an absent listener), so no `seq`-ordered query can be
   trusted to know what was missed — only a fresh read of current state can. Resync means
   "re-snapshot," full stop.

## Recovery: snapshot is primary, `?since=` is belt-and-braces

Every connection — first-time or reconnect — gets a fresh snapshot as its very first frame. That
snapshot alone is ALWAYS sufficient to bring a client fully up to date: it reflects current state
at the moment `Subscribe` registered this connection's own live listener (see `PollService`'s own
doc comment on why `Snapshot` must be queried fresh, after `Subscribe`, never memoized from
`Authorize`-time), and every event after that point arrives live regardless of `?since=`.

`?since=` exists purely to shrink the visible gap for a client that already held a `seq` cursor
from before a brief disconnect — replaying the handful of events between that cursor and the fresh
snapshot's own `seq` so a UI doesn't have to throw away and rebuild everything it was already
showing. It is NEVER the primary correctness mechanism, for two reasons:

- `EventsSince`'s own query (`id > sinceID`) carries a narrower version of the exact same
  cursor-visibility hazard `handleNotify` closes for live delivery: a row whose id is numerically
  at or below `sinceID` but which committed AFTER a client last saw `sinceID` will not be returned
  by it. Subscribing (live) before ever calling `EventsSince` is what actually closes that gap in
  practice — any such row still reaches the connection through the live/late-committer path even
  though the catch-up query alone would miss it — but a client that skipped the snapshot entirely
  and relied on `?since=` alone would have no such guarantee.
- A resync (see above) makes `?since=` actively wrong to use at all for that reconnect: the gap it
  would be backfilling includes events lost to a disconnected LISTEN session, which no id-ordered
  query can recover.

A client that always takes the snapshot as ground truth, treats `?since=`'s backfill (when
requested) as a pure smoothing optimization, and always re-snapshots on resync, is correct under
every one of these hazards without needing to reason about any of them individually.

Corollary for a consumer that answers every snapshot by refetching its full state over REST (the
SPA's `useLivePoll` and `useLivePage` both do): omit `?since=` entirely. Each backfilled frame
would only trigger one more redundant refetch — N loader round trips after a brief outage — for
no correctness gain, since the snapshot that precedes the backfill is already complete.
