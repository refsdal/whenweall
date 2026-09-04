package polls

// This file exports the one private internal a _test.go file in this package (and, via the usual
// Go export-test-file convention, package polls_test as well — every other test file in this
// package is polls_test) needs to reach past this package's own exported API: the fault-injection
// seam timers.go's fanOutDigestItems checks. Mirrors internal/rooms/export_test.go's own idiom for
// exposing test-only internals as a deliberate, narrow seam rather than a real exported symbol.

// SetDigestFanOutFailAfterN sets digestFanOutFailAfterN (timers.go) for the duration of a test —
// see that var's own doc comment for what it does. Exported only for
// TestDigestFanOutFailurePreservesItemsForRetry, which needs to force fanOutDigestItems's
// per-recipient loop to fail partway through deterministically. Returns a restore func a test
// should defer (or pass to t.Cleanup) to put the seam back to its previous value, since it's a
// package-level var shared across the whole test binary.
func SetDigestFanOutFailAfterN(n int) (restore func()) {
	prev := digestFanOutFailAfterN
	digestFanOutFailAfterN = n
	return func() { digestFanOutFailAfterN = prev }
}
