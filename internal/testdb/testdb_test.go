package testdb

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recorder is a stand-in for *testing.T that records which of Skipf/Fatalf unavailable() picked,
// instead of actually skipping or failing this test.
type recorder struct {
	skipped, fatal string
}

func (r *recorder) Skipf(format string, args ...any)  { r.skipped = fmt.Sprintf(format, args...) }
func (r *recorder) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }

func TestUnavailableSkipsLocallyButFailsUnderCI(t *testing.T) {
	cause := errors.New("docker daemon not reachable")

	local := &recorder{}
	unavailable(local, false, "postgres testcontainer", cause)
	if local.fatal != "" {
		t.Fatalf("outside CI unavailable() must skip, not fail: %q", local.fatal)
	}
	if !strings.Contains(local.skipped, "docker daemon not reachable") {
		t.Fatalf("skip message = %q, want it to carry the cause", local.skipped)
	}

	ci := &recorder{}
	unavailable(ci, true, "postgres testcontainer", cause)
	if ci.skipped != "" {
		t.Fatalf("under CI unavailable() must not skip: %q", ci.skipped)
	}
	if !strings.Contains(ci.fatal, "CI") || !strings.Contains(ci.fatal, "docker daemon not reachable") {
		t.Fatalf("fatal message = %q, want it to mention CI and the cause", ci.fatal)
	}
}
