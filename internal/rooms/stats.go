// This file is Task 3's stats room: the landing page's global, anonymous usage counters — a
// straight port of src/do/StatsRoom.ts's counters + broadcast-throttle behavior onto room_state
// (room_key "stats:global") plus this package's own Hub/Emit machinery, rather than a Durable
// Object's in-memory delta + storage alarm. See UsageStats's own doc comment for the field
// vocabulary (ported field-for-field from stats-protocol.ts) and maybeBroadcast's for the
// throttle design (deliberately NOT a byte-for-byte port of StatsRoom's own leading-edge-only
// throttle — see that doc comment for why).
package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// RoomKeyStats is the one global stats room's key — both the room_state row this file owns and
// the WS room a connecting client subscribes to (endpoints.go's stats route).
const RoomKeyStats = "stats:global"

// statsBroadcastThrottle mirrors StatsRoom.ts's BROADCAST_THROTTLE_MS: a burst of votes should
// tick the landing page smoothly, not flood every connected browser with one frame per vote.
const statsBroadcastThrottle = 2 * time.Second

// statsEventType is this room's one live event's "type" — kept distinct from stats-protocol.ts's
// own message shape (`{type:'stats', stats: UsageStats}`) deliberately: Register's Authorize
// wraps every WS route (poll/booking/stats alike) in the same {"type","seq",<data fields
// flattened>} envelope (hub.go's buildFrameFromParts), so this room's live frame is
// {"type":"stats","seq":N,"pollsCreated":...,...} — UsageStats's own fields flattened at the top
// level rather than nested under a second "stats" key, for parity with this room's OWN snapshot
// frame (Snapshot below returns the same UsageStats value, nested once under "data" by
// snapshotFrame) — plan 8's frontend reads one shape whether it's the connection's first frame
// or a live update, never two.
const statsEventType = "stats"

// UsageStats is the landing page's global, anonymous usage counters — ported field-for-field from
// stats-protocol.ts's UsageStats so plan 8's frontend needs no translation layer. Every field is
// a monotonic lifetime count of *submissions*, not a snapshot of current state (see that file's
// own doc comment: someone who answers "yes" and later changes it to "no" adds one to each; a
// counter that ticks downward while a visitor watches reads as a bug).
type UsageStats struct {
	PollsFinalized    int64 `json:"pollsFinalized"`
	PollsCreated      int64 `json:"pollsCreated"`
	ResponsesYes      int64 `json:"responsesYes"`
	ResponsesIfNeedBe int64 `json:"responsesIfNeedBe"`
	ResponsesNo       int64 `json:"responsesNo"`
}

// The five counter field names Record accepts — the jsonb keys of room_state's "stats:global"
// row, identical to UsageStats's own json tags above.
const (
	StatsPollsCreated      = "pollsCreated"
	StatsPollsFinalized    = "pollsFinalized"
	StatsResponsesYes      = "responsesYes"
	StatsResponsesIfNeedBe = "responsesIfNeedBe"
	StatsResponsesNo       = "responsesNo"
)

// StatsFieldForAnswer maps one poll vote's answer ("yes"/"ifneedbe"/"no" — polls' own Answer
// vocabulary) to the counter field it increments, mirroring stats-protocol.ts's ANSWER_FIELD.
// ok is false for anything else (a caller should treat that as "don't count this").
func StatsFieldForAnswer(answer string) (field string, ok bool) {
	switch answer {
	case "yes":
		return StatsResponsesYes, true
	case "ifneedbe":
		return StatsResponsesIfNeedBe, true
	case "no":
		return StatsResponsesNo, true
	default:
		return "", false
	}
}

// statsRecordSQL atomically bumps all five counter fields by their own delta (0 for any field a
// given Record call doesn't touch) and returns the row's full, current counters — ONE INSERT ...
// ON CONFLICT DO UPDATE statement regardless of how many fields actually changed, which is what
// lets a call site with several different deltas to apply at once (e.g. AddParticipant's own
// multi-option vote) do it in a single round trip rather than one upsert per field. Safe under
// concurrent Record calls with no separate advisory lock, exactly like the old per-field Increment
// before it: two transactions racing for the same room_state row simply serialize on Postgres's
// own row lock (the second blocks until the first commits, then re-reads the row it just waited
// on) — the same pattern presence.go's presenceJoin/presenceLeave use for ws_presence.
//
// The nested jsonb_set calls each read room_state.data (the row's value from BEFORE this
// statement, unqualified — never an intermediate result of an earlier jsonb_set in this same
// chain), so the five fields' COALESCE reads are mutually independent regardless of nesting order;
// jsonb_set's 4th argument (create_missing) defaults to true, so a field that has never been
// touched before simply springs into existence the first time it is — the row need not be seeded
// with all five keys up front, and UsageStats's json.Unmarshal below treats an absent key as its
// zero value regardless.
const statsRecordSQL = `
INSERT INTO room_state (room_key, data, updated_at)
VALUES ($1, jsonb_build_object(
	'pollsCreated', $2::bigint,
	'pollsFinalized', $3::bigint,
	'responsesYes', $4::bigint,
	'responsesIfNeedBe', $5::bigint,
	'responsesNo', $6::bigint
), now())
ON CONFLICT (room_key) DO UPDATE SET
	data = jsonb_set(jsonb_set(jsonb_set(jsonb_set(jsonb_set(
		room_state.data,
		ARRAY['pollsCreated'], to_jsonb(COALESCE((room_state.data->>'pollsCreated')::bigint, 0) + $2::bigint)),
		ARRAY['pollsFinalized'], to_jsonb(COALESCE((room_state.data->>'pollsFinalized')::bigint, 0) + $3::bigint)),
		ARRAY['responsesYes'], to_jsonb(COALESCE((room_state.data->>'responsesYes')::bigint, 0) + $4::bigint)),
		ARRAY['responsesIfNeedBe'], to_jsonb(COALESCE((room_state.data->>'responsesIfNeedBe')::bigint, 0) + $5::bigint)),
		ARRAY['responsesNo'], to_jsonb(COALESCE((room_state.data->>'responsesNo')::bigint, 0) + $6::bigint)),
	updated_at = now()
RETURNING data
`

// StatsService owns the "stats:global" room_state row: Record (called AFTER the domain write's
// own tx has committed — Create/Finalize/AddParticipant/UpdateParticipant/Claim, see
// cmd/whenweall/main.go's wiring) and Snapshot (this room's WS route's connect-time payload,
// endpoints.go).
//
// The throttle state below (mu/lastEmitAt/pending) is plain in-process memory, deliberately NOT
// persisted to room_state — see maybeBroadcast's doc comment for why that's an accepted,
// documented trade-off rather than an oversight.
type StatsService struct {
	sqlDB *sql.DB
	log   *slog.Logger

	mu         sync.Mutex
	lastEmitAt time.Time
	pending    bool
}

// NewStatsService builds a StatsService bound to sqlDB — the pool used both for Snapshot's plain
// reads and for maybeBroadcast's trailing-edge flush (which runs on its own timer goroutine, long
// after any caller's own transaction has committed or rolled back, so it can never reuse that
// transaction's tx).
func NewStatsService(sqlDB *sql.DB, log *slog.Logger) *StatsService {
	if log == nil {
		log = slog.Default()
	}
	return &StatsService{sqlDB: sqlDB, log: log}
}

// Record atomically applies deltas — a subset of the five StatsXxx field constants, each mapped to
// how much to bump it by (statsRecordSQL, ONE upsert regardless of how many fields are present) —
// then decides whether this update should actually broadcast live (maybeBroadcast).
//
// Best-effort: every error (an unrecognized field, any DB failure) is logged and swallowed, never
// returned. This replaced the old field-at-a-time Increment, which ran inside the SAME transaction
// as the domain write it counted and propagated its own errors like every other rooms.Emit call
// site in this codebase — a documented deviation, at the time, from stats-client.ts's own
// recordPollCreated/recordResponses (which catch and log rather than ever failing their caller).
// That shape held every mutating poll/participant write's transaction open across an extra jsonb
// upsert against a single, hot, shared room_state row — real, avoidable lock contention for a
// landing-page counter that is not part of any write's integrity. Record is always called AFTER
// the caller's own domain-write tx has committed (internal/polls's Create/Duplicate/Finalize/
// Claim/AddParticipant/UpdateParticipant — see each call site's own comment), using s.sqlDB
// directly, matching the TS source's best-effort stance at last.
//
// A no-op (no DB round trip at all) when deltas has no nonzero, recognized entry — nothing to
// record.
func (s *StatsService) Record(ctx context.Context, deltas map[string]int64) {
	var pollsCreated, pollsFinalized, responsesYes, responsesIfNeedBe, responsesNo int64
	touched := false
	for field, delta := range deltas {
		if delta == 0 {
			continue
		}
		switch field {
		case StatsPollsCreated:
			pollsCreated = delta
		case StatsPollsFinalized:
			pollsFinalized = delta
		case StatsResponsesYes:
			responsesYes = delta
		case StatsResponsesIfNeedBe:
			responsesIfNeedBe = delta
		case StatsResponsesNo:
			responsesNo = delta
		default:
			s.log.Error("rooms: record stats: unknown field", "field", field)
			continue
		}
		touched = true
	}
	if !touched {
		return
	}

	var raw []byte
	if err := s.sqlDB.QueryRowContext(ctx, statsRecordSQL, RoomKeyStats,
		pollsCreated, pollsFinalized, responsesYes, responsesIfNeedBe, responsesNo,
	).Scan(&raw); err != nil {
		s.log.Error("rooms: record stats", "error", err)
		return
	}

	var stats UsageStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		s.log.Error("rooms: unmarshal stats counters", "error", err)
		return
	}

	s.maybeBroadcast(ctx, stats)
}

// maybeBroadcast implements this room's throttle: at most one live "stats" frame every
// statsBroadcastThrottle, always carrying the LATEST counters at the moment it actually sends —
// never the delta this particular Record call contributed. It differs from StatsRoom.ts's own
// #broadcastThrottled in one deliberate way: that source is a pure leading-edge throttle (a
// broadcast that arrives just after the window opens goes out immediately; every later one within
// the same window is dropped outright, with no guarantee the window's FINAL state is ever sent
// live at all — only a future connection's snapshot would show it). This port adds a
// TRAILING edge: a Record that arrives inside an active window, finding no trailing flush already
// scheduled, arms exactly one (time.AfterFunc, below) for whatever time remains in the window, so
// the window's last word always reaches every live subscriber, not just its first. This is what
// task-3's own test requirement ("10 rapid Records produce ≤ a few frames but the last one
// carries the final count") needs to hold regardless of timing, rather than only when a burst
// happens to straddle a window boundary just right.
//
// The throttle state itself (lastEmitAt/pending) is plain in-process memory, not persisted to
// room_state or coordinated across replicas: each replica's Hub keeps its own independent
// throttle window. In a multi-replica deployment this means the AGGREGATE broadcast rate across
// every replica can exceed one-per-window (each replica's own Record calls are throttled
// independently), a real but accepted trade-off — the counters themselves are never lost or
// under-counted (Record's own DB write, above, always lands), only the live-broadcast CADENCE
// is coarser than a single global rate-limiter would give, and the at-least-once CONTRACT (hub.go)
// already asks every consumer to tolerate more frames than the theoretical minimum, never fewer
// than the state changes they represent.
//
// Always runs on s.sqlDB, never a caller's tx: Record itself is always called post-commit now (see
// its own doc comment), so there is no longer any caller transaction to share — this is the same
// plain-sqlDB shape flushTrailing (below) already used for its own, always-standalone emit.
func (s *StatsService) maybeBroadcast(ctx context.Context, stats UsageStats) {
	s.mu.Lock()
	now := time.Now()
	if s.lastEmitAt.IsZero() || now.Sub(s.lastEmitAt) >= statsBroadcastThrottle {
		s.lastEmitAt = now
		s.mu.Unlock()

		if err := Emit(ctx, s.sqlDB, RoomKeyStats, statsEventType, stats); err != nil {
			s.log.Error("rooms: emit stats broadcast", "error", err)
		}
		return
	}

	if s.pending {
		s.mu.Unlock()
		return
	}
	s.pending = true
	wait := statsBroadcastThrottle - now.Sub(s.lastEmitAt)
	s.mu.Unlock()

	time.AfterFunc(wait, s.flushTrailing)
}

// flushTrailing is maybeBroadcast's trailing-edge half: fires once, statsBroadcastThrottle after
// the window it was armed for opened, re-reads whatever the counters are AT THAT MOMENT (not
// whatever they were when the triggering Increment ran — further increments may have landed in
// between), and emits them as this window's one trailing broadcast. Runs on its own goroutine
// (time.AfterFunc), entirely outside any caller's request/transaction — Emit here uses s.sqlDB
// directly (a plain, non-tx DBTX, exactly like presence.go's broadcastPresence), never a tx that
// may have long since committed or rolled back.
func (s *StatsService) flushTrailing() {
	s.mu.Lock()
	s.pending = false
	s.lastEmitAt = time.Now()
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stats, err := s.readCurrent(ctx)
	if err != nil {
		s.log.Error("rooms: read stats for trailing broadcast", "error", err)
		return
	}
	if err := Emit(ctx, s.sqlDB, RoomKeyStats, statsEventType, stats); err != nil {
		s.log.Error("rooms: emit trailing stats broadcast", "error", err)
	}
}

// readCurrent reads room_state's "stats:global" row, or all-zero UsageStats if nothing has ever
// incremented it (no row seeded yet) — mirrors StatsRoom.ts's own #seededCounters returning
// EMPTY_STATS before its first D1 seed, minus the D1-seeding step itself: this port has no
// pre-existing Cloudflare-era data to backfill from, so "nothing yet" simply means zero.
func (s *StatsService) readCurrent(ctx context.Context) (UsageStats, error) {
	var raw []byte
	err := s.sqlDB.QueryRowContext(ctx, `SELECT data FROM room_state WHERE room_key = $1`, RoomKeyStats).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageStats{}, nil
	}
	if err != nil {
		return UsageStats{}, fmt.Errorf("rooms: read stats counters: %w", err)
	}

	var stats UsageStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		return UsageStats{}, fmt.Errorf("rooms: unmarshal stats counters: %w", err)
	}
	return stats, nil
}

// Snapshot implements SnapshotFunc for the stats WS route (endpoints.go): the connection's very
// first frame is the current counters, so a socket that connects between broadcasts is never
// stuck showing whatever the page was server-rendered with — the same reasoning StatsRoom.ts's
// own fetch gives for sending its greeting frame immediately on accept.
func (s *StatsService) Snapshot(ctx context.Context, _ string) (any, error) {
	return s.readCurrent(ctx)
}
