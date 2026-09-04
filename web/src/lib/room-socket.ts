/**
 * Reconnecting client for the plan-6/plan-8 realtime wire protocol — see
 * `internal/rooms/PROTOCOL.md` (the binding wire contract; this file mirrors it, not the other way
 * around) for every frame shape and the delivery rules this implementation exists to satisfy.
 *
 * Three routes share this one client (poll, booking-page, stats — `connectRoom`'s `path` picks
 * which): every connection gets a fresh `snapshot` frame first (nested `{"type","seq","data"}`),
 * then zero or more backfill frames (only when reconnecting with `?since=`), then live frames —
 * backfill and live frames are byte-for-byte the same FLATTENED shape
 * (`{"type","seq",...fields}`, fields at the top level, never nested under `data`).
 */

const FIRST_DELAY_MS = 1000
const MAX_DELAY_MS = 30_000

/** Every field on a flattened frame except the two protocol envelope fields — the domain payload
 * `onEvent` gets as `data`. */
function omitTypeAndSeq(frame: Record<string, unknown>): unknown {
  const data: Record<string, unknown> = {}
  for (const key of Object.keys(frame)) {
    if (key !== 'type' && key !== 'seq') data[key] = frame[key]
  }
  return data
}

export type ConnectRoomOptions = {
  /** e.g. `/api/v1/polls/${id}/ws`. */
  path: string
  /** Whether a reconnect requests `?since=<max seq observed>` backfill (PROTOCOL.md "Recovery").
   * Default `true`. A consumer that already treats every snapshot as ground truth and refetches
   * its full state on it (`useLivePoll`, `useLivePage`) passes `false`: for it, each replayed
   * frame would only trigger one more redundant refetch — N loader round trips after a brief
   * outage — for no correctness gain. */
  backfill?: boolean
  /** Called once per connection (first connect AND every reconnect) with the snapshot's own
   * `data` (route-specific, may be `null`) and its `seq` (the room's `max(room_events.id)` at
   * that moment — the cursor a caller may remember, though `connectRoom` already tracks this
   * itself for its own `?since=` reconnects). */
  onSnapshot(data: unknown, seq: number): void
  /** Called for every backfill/live frame after dedup — `type` is the frame's own `"type"` field
   * (`"poll.changed"`, `"page.changed"`, `"stats"`, `"presence"`, ...), `data` is every OTHER
   * top-level field on the frame (i.e. the frame with `type`/`seq` stripped), and `seq` is its
   * `room_events.id`. */
  onEvent(type: string, data: unknown, seq: number): void
  /** The hub's own LISTEN session recovered after being lost — an arbitrary, unknowable set of
   * this room's events were dropped while it was down. A caller MUST treat this exactly like a
   * fresh connect: refetch a full snapshot from the ordinary REST endpoint this connection
   * covers. Never call anything `?since=`-shaped in response — see PROTOCOL.md's "Delivery rules"
   * §3 for why no `seq`-ordered query can be trusted to know what was missed. */
  onResync(): void
}

export type RoomSocket = { close(): void }

/**
 * Dedupes by a SET of every `seq` already applied (never `seq <= lastSeen` — a lower `seq` can
 * legitimately arrive after a higher one; see PROTOCOL.md's "late committer" hazard) and, on a
 * clean disconnect, reconnects with `?since=<max seq observed>` so the fresh snapshot every
 * reconnect gets doesn't have to throw away a UI that was already showing recent history. `?since=`
 * is never the correctness mechanism — the snapshot is — so it is purely a smoothing optimization,
 * and is dropped entirely (not merely stale) once a `resync` is seen: PROTOCOL.md is explicit that
 * `?since=` is actively wrong to lean on across a lost-LISTEN gap.
 */
export function connectRoom(opts: ConnectRoomOptions): RoomSocket {
  if (typeof window === 'undefined' || typeof WebSocket === 'undefined') {
    return { close() {} }
  }

  let disposed = false
  let socket: WebSocket | null = null
  let retry: ReturnType<typeof setTimeout> | undefined
  let delay = FIRST_DELAY_MS

  // Every `seq` this connection (across reconnects) has already delivered to `onEvent`. Room
  // event ids are never reused, so this only ever grows — that is correct, not a leak concern at
  // any scale this product runs at.
  const seenSeqs = new Set<number>()
  // The cursor for this connection's own next `?since=`, or `undefined` for "send none" (a first
  // connect, or any connect right after a `resync`). Advanced by the MAXIMUM seq observed so far,
  // never "the last one that arrived" (PROTOCOL.md's delivery rule §2).
  let cursor: number | undefined

  const buildUrl = (): string => {
    const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const url = new URL(`${protocol}://${window.location.host}${opts.path}`)
    if (opts.backfill !== false && cursor !== undefined) url.searchParams.set('since', String(cursor))
    return url.toString()
  }

  const advanceCursor = (seq: number) => {
    if (cursor === undefined || seq > cursor) cursor = seq
  }

  const handleMessage = (raw: unknown) => {
    if (typeof raw !== 'string') return
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      return // Not a JSON frame this protocol ever sends — nothing to do with it.
    }
    if (typeof parsed !== 'object' || parsed === null) return
    const frame = parsed as Record<string, unknown>
    const type = frame.type
    if (typeof type !== 'string') return

    if (type === 'pong') return // The one reply to our own keepalive; carries no data.

    if (type === 'resync') {
      // No `seq` at all (PROTOCOL.md §5). The gap it represents makes `?since=` actively wrong to
      // use on this connection's next reconnect, so the cursor is dropped, not merely left stale.
      cursor = undefined
      opts.onResync()
      return
    }

    if (type === 'snapshot') {
      // Nested — the one frame shape that isn't a `room_events` row at all.
      const seq = typeof frame.seq === 'number' ? frame.seq : 0
      advanceCursor(seq)
      opts.onSnapshot(frame.data, seq)
      return
    }

    // Every other type is a backfill or live entity frame — flattened, dedup by seq membership.
    const seq = frame.seq
    if (typeof seq !== 'number') return
    if (seenSeqs.has(seq)) return
    seenSeqs.add(seq)
    advanceCursor(seq)

    opts.onEvent(type, omitTypeAndSeq(frame), seq)
  }

  const connect = () => {
    const ws = new WebSocket(buildUrl())
    socket = ws

    ws.onopen = () => {
      delay = FIRST_DELAY_MS
    }

    ws.onmessage = (event: MessageEvent) => handleMessage(event.data)

    ws.onerror = () => {
      try {
        ws.close()
      } catch {
        // Already closing; `onclose` still schedules the retry below.
      }
    }

    ws.onclose = () => {
      if (disposed) return
      retry = setTimeout(connect, delay)
      delay = Math.min(delay * 2, MAX_DELAY_MS)
    }
  }

  connect()

  return {
    close() {
      disposed = true
      if (retry !== undefined) clearTimeout(retry)
      try {
        socket?.close()
      } catch {
        // Nothing to clean up if the socket never opened.
      }
    },
  }
}
