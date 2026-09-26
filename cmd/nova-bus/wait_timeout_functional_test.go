//go:build functional

package main

import (
	"strings"
	"testing"
)

// wait_timeout_functional_test.go holds the wait tests that wait out a real
// wall-clock timeout over a real git bus: a functional test (nova-tools #4328),
// not a unit test paying the clock. Measured 2026-09-26:
// TestWaitQuietBeatsSleepsThroughABeatCommit waits its whole 2 s --timeout and
// ran 2.1 s on run 36261817989's space shard 1/4, 3.0 s on the Studio at load
// 17-30 (nice -n 15, -p 2).

// A beat commit from another line is not a note, so a wait sleeps through it -- with or
// without --quiet-beats. Until #328 (2026-09-17) the flag made a beat a wake worth one WAIT
// line; with six lines beating once a minute that was a poll with extra steps, and every
// wake cost the waiting window a turn. A wake is a note addressed to the reader, nothing
// else; the flag stays accepted so callers that pass it keep working.
func TestWaitQuietBeatsSleepsThroughABeatCommit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	// Bo's beat: a commit touching only from-bo/BEAT, no note.
	writeFile(t, other, "from-bo/BEAT", "2026-09-09T12:35:00Z - until=2026-09-09T12:45:00Z\n")
	gitIn(t, other, "add", "from-bo/BEAT")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "beat bo")
	if err := push(other); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s", "--quiet-beats")...).mustCode(t, 0)

	r.mustContain(t, "stdout", "WAIT DONE reason=timeout")
	if strings.Contains(r.stdout, "WAIT OK") || strings.Contains(r.stdout, "reason=new") {
		t.Fatalf("a beat-only change woke the wait; a beat is not news:\n%s", r.stdout)
	}
}
