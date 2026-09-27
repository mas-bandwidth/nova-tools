package main

import (
	"strings"
	"testing"
)

func TestAwakeRefusesNonBus(t *testing.T) {
	t.Parallel()

	r := wakeRun(t, "awake")
	if r.exit != 2 || !strings.Contains(r.stderr, "AWAKE REFUSED") {
		t.Fatalf("missing --bus: exit=%d stderr=%s, want 2 and AWAKE REFUSED", r.exit, r.stderr)
	}

	dir := t.TempDir()
	r = wakeRun(t, "awake", "--bus", dir)
	if r.exit != 2 || !strings.Contains(r.stderr, "AWAKE REFUSED") {
		t.Fatalf("non-git --bus: exit=%d stderr=%s, want 2 and AWAKE REFUSED", r.exit, r.stderr)
	}
}
