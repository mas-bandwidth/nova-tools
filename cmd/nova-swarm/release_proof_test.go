package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RELEASE PROOF: the binary built WITHOUT the swarmtest tag must ignore every one of the
// five NOVA_SWARM_* injection variables. In the tagged build each of these values would
// kill or pause a process; in the release build the injection functions are no-ops that
// never read the environment, so a pool run with all five set to lethal values completes
// normally and records no kill and no pause.
func TestTheReleaseBuildIgnoresTheInjectionVariables(t *testing.T) {
	b := newBench(t)
	// The release binary is the builtTool default; this test deliberately does NOT opt into
	// the tagged build (b.inject), because the whole point is that the release build reads
	// none of these variables.
	b.binary = builtTool
	mark := filepath.Join(b.dir, "pause-mark")
	b.extraEnv = []string{
		"NOVA_SWARM_KILLPOINT=after-reserve",
		"NOVA_SWARM_KILL_AFTER=" + mark,
		"NOVA_SWARM_PAUSEPOINT=before-identify",
		"NOVA_SWARM_PAUSE_AFTER_ORPHAN=1",
		"NOVA_SWARM_PAUSE_MARK=" + mark,
	}
	id := b.add("a job the injection variables must not touch\nFAKE-FINDINGS 1\n")

	exit, stdout, stderr := b.run()
	if exit != 0 {
		t.Fatalf("the release build ignored the injection variables: exit %d, want 0;\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	mustContain(t, "the run", stdout, "RUN DONE id="+id)
	mustContain(t, "the run", stdout, "RUN OK started=1 done=1 failed=0 killed=0")
	for _, bad := range []string{"RUN KILLED", "RUN VIOLATION", "RUN QUARANTINE", "RUN LAUNCH-FAILED", "RUN ABORTED"} {
		if strings.Contains(stdout+stderr, bad) {
			t.Errorf("the release build recorded %q; the injection variables must be no-ops:\n%s%s", bad, stdout, stderr)
		}
	}
	if _, err := os.Stat(mark); err == nil {
		t.Errorf("the release build wrote the pause mark %s; NOVA_SWARM_PAUSE_MARK must be a no-op", mark)
	}
}
