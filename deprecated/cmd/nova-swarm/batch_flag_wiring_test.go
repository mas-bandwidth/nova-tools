package main

import (
	"strings"
	"testing"
)

// 2d99edbf wired --max-inflight and --stall-after on batch; 021e9e4b wired
// --harness and --auth. Reverting cmd/nova-swarm/main.go kept this package
// green: the behaviour tests live on BatchInput in internal/swarm, and the
// CLI never named the flags.
func TestBatchAcceptsTheGatherFlagsThatLandedUnguarded(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--max-inflight", "--stall-after", "--harness", "--auth"} {
		exit, _, stderr := runSwarm(t, "batch", flag, "1")
		if exit != 2 {
			t.Fatalf("%s: exit %d, want 2 (a missing required flag, not an unknown flag); stderr=%q", flag, exit, stderr)
		}
		if strings.Contains(stderr, "flag provided but not defined") {
			t.Fatalf("%s is not wired on batch; a revert of main.go would print this:\n%s", flag, stderr)
		}
	}
}
