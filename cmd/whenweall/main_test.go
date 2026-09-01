package main

import "testing"

func TestRunDispatch(t *testing.T) {
	if got := run([]string{"whenweall", "definitely-not-a-command"}); got == 0 {
		t.Error("unknown command should exit non-zero")
	}
}

func TestRunDispatchVersion(t *testing.T) {
	if got := run([]string{"whenweall", "version"}); got != 0 {
		t.Errorf("version should exit 0, got %d", got)
	}
}
