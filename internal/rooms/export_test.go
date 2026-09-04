package rooms

// This file exports a few of Hub's private map internals for _test.go files in this same package
// (and, via the usual Go export-test-file convention, for package rooms_test as well — every other
// test file in this package is rooms_test) to assert against directly: the I3 map-lifecycle fix
// (pruneRoomLocked, clearPendingNotify) is a statement about what these maps hold, which a test can
// only observe by reaching past Hub's own exported API.

// PendingNotifyLen reports how many ids are currently recorded in h.pendingNotify for roomKey —
// exported only for tests, to assert the I3 leak fix directly: this must reach (and stay at) zero
// once roomKey's last local subscriber is gone (pruneRoomLocked), and must be zero for every room
// immediately after a reconnect (Run's clearPendingNotify).
func (h *Hub) PendingNotifyLen(roomKey string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pendingNotify[roomKey])
}

// TestWSConnectLimit exposes wsConnectLimit (endpoints.go, I5) so a test can drive exactly that
// many connects before expecting the next one to be rate-limited, without duplicating the literal.
const TestWSConnectLimit = wsConnectLimit

// DispatchLocal exposes Hub.dispatchLocal for tests — exported only so
// TestDroppedRoomIsFullyPruned can exercise the slow-consumer drop path deterministically, purely
// in-process, with no real Postgres LISTEN session running: real NOTIFY timing cannot be
// synchronized precisely enough to also guarantee no FURTHER notify lands for the room afterward
// (which would legitimately, and correctly, re-populate watermark via handleNotify's "no local
// subscribers" branch — a real behavior, not the leak this test targets).
func (h *Hub) DispatchLocal(roomKey string, frame []byte) {
	h.dispatchLocal(roomKey, frame)
}

// SeedPendingNotify records id as pending for roomKey — exported only for
// TestReconnectClearsPendingNotify, which needs a deterministic way to populate pendingNotify.
// The natural path (deliverSince's trackPending, triggered by two Emits in one transaction) races
// the very reconnect that test forces: the routine duplicate NOTIFY it creates is typically
// consumed within milliseconds, well before a deliberately-forced pg_terminate_backend could ever
// land first. Seeding directly proves Run's reconnect path (clearPendingNotify) clears
// pendingNotify wholesale regardless of how an entry got there.
func (h *Hub) SeedPendingNotify(roomKey string, id int64) {
	h.trackPending(roomKey, id)
}

// RoomTracked reports whether roomKey still has ANY bookkeeping at all — a subs entry, a
// watermark, or a pendingNotify entry — exported only for tests asserting pruneRoomLocked's full
// cleanup (I3): once a room's last local subscriber is gone, however that happened (a graceful
// unsubscribe, or dispatchLocal dropping a slow consumer), every one of these three must be gone
// too, not just the subs entry the pre-fix code already handled.
func (h *Hub) RoomTracked(roomKey string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[roomKey]; ok {
		return true
	}
	if _, ok := h.watermark[roomKey]; ok {
		return true
	}
	if _, ok := h.pendingNotify[roomKey]; ok {
		return true
	}
	return false
}
