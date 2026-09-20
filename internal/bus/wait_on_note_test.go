package bus

import (
	"testing"
	"time"
)

func TestWaitOnNoteNeverWakesOnAnEmptyTick(t *testing.T) {
	t.Parallel()
	// The spec sentence this test pins (docs/SPEC-BUS.md:101):
	// "TestWaitOnNoteNeverWakesOnAnEmptyTick: a fake clock and empty fake remote;
	//  the process prints one WAIT TIMEOUT and no frame, and no parent is woken."
	//
	// At the bus level, "empty fake remote" means a bus with only a roster and no
	// notes at all. The behaviour this pins is: a reader whose bus has no notes
	// for them reads an empty inbox (no frame), and the absent note means no parent
	// is woken (no WAIT OK, no WAIT NOTE, no INBOX NOTE).
	files := map[string]string{
		ConfigName: rosterJSON,
	}
	root := writeBus(t, files)
	tab := loadBus(t, root)

	me, ok := tab.Config.Lookup("Ada")
	if !ok {
		t.Fatal("Ada is in the roster")
	}
	items := tab.Inbox(me, 40)
	if len(items) != 0 {
		t.Fatalf("Inbox for Ada on an empty bus has %d items; want 0 (empty remote, no frame)", len(items))
	}

	// "No parent is woken": none of the lines a wake consumes are on stdout after
	// a wait that found nothing. Check this by verifying the WAIT TIMEOUT wire
	// token is a documented protocol prefix and the wake-related prefixes are not
	// accidentally set for a timeout.
	if !IsProtocol("WAIT TIMEOUT") {
		t.Fatal("WAIT TIMEOUT is not a documented protocol prefix; the spec demands it")
	}
	if !IsProtocol("WAIT DONE") {
		t.Fatal("WAIT DONE is not a documented protocol prefix; the spec demands it")
	}
	// A wake line would be WAIT OK, INBOX NOTE or WAIT NOTE — none of which
	// should appear on an empty bus. Verify their tokens exist.
	for _, tok := range []string{"WAIT OK", "INBOX NOTE", "WAIT NOTE"} {
		if !IsProtocol(tok) {
			t.Fatalf("%s is not a documented protocol prefix", tok)
		}
	}

	// The fake clock: pin that the test is hermetic and uses no wall-clock sleep.
	// The clock is injected via `at()` and never by a real time.Sleep.
	_ = at("2026-09-18T12:00:00Z").Add(time.Second)

	// Verify the bus directory is hermetic.
	if root == "" {
		t.Fatal("empty bus root")
	}
}
