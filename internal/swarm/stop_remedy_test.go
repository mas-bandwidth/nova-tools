package swarm

import (
	"os"
	"strings"
	"testing"
)

// A STOPPED POOL IS NOT AN OUT-OF-HOURS POOL, AND NOT A DRAINED ONE (issue #180).
//
// Rule 17's recovery and the coordinator handover it enables must preserve a
// person's stop decision: a recovering dispatcher that ends over a stopped pool
// may not advise "run again with more hours" — that is a guess, and a wrong one,
// about a pool whose only problem is that somebody asked it to hold still. Silence
// alone does not identify the cause, so `RUN NOTE` (the one remedy line) names the
// stop and the act that lifts it, and never a second run.
func TestRunNoteNamesAStoppedPool(t *testing.T) {
	dir := t.TempDir()
	p, err := OpenPool(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path(StopFile), []byte("stop\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A stopped pool with work still pending: the remedy names the stop, and does
	// not mis-advise running again with more hours.
	note := remedy(p, 0, 0, 3, 0)
	if !strings.Contains(note, "the pool is stopped") {
		t.Fatalf("a stopped pool with pending work names the stop, got %q", note)
	}
	if strings.Contains(note, "more hours") {
		t.Fatalf("a stopped pool is not out of hours; the remedy must not advise running again with more hours, got %q", note)
	}

	// And a stopped, drained pool says it is stopped and drained, rather than the
	// ordinary drain that reads like the work finished on its own.
	drained := remedy(p, 0, 0, 0, 0)
	if !strings.Contains(drained, "the pool is stopped and drained") {
		t.Fatalf("a stopped, drained pool names the stop, got %q", drained)
	}
}
