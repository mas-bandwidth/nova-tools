package wake

import (
	"strings"
	"testing"
)

// TestTheInnerWaitsOwnTimeoutIsNotABusChange is the consumer half of #1434,
// dogfood round 5, receipt 9b12c7fb98ae: under --refresh the source runs
// `nova-bus wait` and classifies every line it printed, and the inner wait's
// own WAIT / WAIT TIMEOUT / WAIT DONE reason=timeout lines are bookkeeping --
// the words that say nothing arrived -- yet the default case relayed them as a
// change in the world and the verb could not wait at all.
//
// The three bookkeeping shapes are COUNTED in read= and suppressed= and are
// never relayed and never standing. A test that only checked they were gone
// would pass over a tool that had also gone quiet about a bus refusing every
// read, so this transcript carries an INBOX REFUSED, a real note and a
// WAIT DONE with another reason, and requires all three to still arrive.
func TestTheInnerWaitsOwnTimeoutIsNotABusChange(t *testing.T) {
	b := &Bus{}
	res := b.Classify(strings.Join([]string{
		"WAIT as=Rowan timeout=20s interval=10s cursor=73ca511e",
		"WAIT TIMEOUT after=27.975s polls=3 cursor=73ca511e",
		"WAIT DONE reason=timeout rearm=required next=nova-bus wait",
		"WAIT DONE reason=note rearm=none next=-",
		"INBOX NOTE id=stella-aaaaaaaaaaaa from=Stella addr=to at=2026-09-18T11:00:00Z path=from-stella/a.md: a real note",
		"INBOX REFUSED: the bus is not a git checkout",
	}, "\n") + "\n")

	for _, it := range res.Items {
		if strings.Contains(it.Key, "bus:line:WAIT as=") ||
			strings.Contains(it.Key, "bus:line:WAIT TIMEOUT") ||
			strings.Contains(it.Key, "bus:line:WAIT DONE reason=timeout") {
			t.Errorf("the inner wait's own bookkeeping was relayed as a change in the world:\n%s", it.Key)
		}
	}
	for _, line := range res.Standing {
		if strings.Contains(line, "WAIT as=") ||
			strings.Contains(line, "WAIT TIMEOUT") ||
			strings.Contains(line, "WAIT DONE reason=timeout") {
			t.Errorf("the inner wait's own bookkeeping stood as a bus line:\n%s", line)
		}
	}

	// The note, the refusal and a WAIT DONE for any other reason still come
	// through: the fix drops the inner wait's timeout and nothing else. A test
	// that only checked the three were gone would pass over a tool that had
	// gone quiet about a refusing bus, which is the stale allow-list failure
	// this package exists to remember.
	var sawNote, sawRefusal, sawNoteDone bool
	for _, it := range res.Items {
		switch {
		case it.Kind == KindBus && it.ID == "stella-aaaaaaaaaaaa":
			sawNote = true
		case it.Kind == KindBusLine && strings.Contains(it.Key, "INBOX REFUSED"):
			sawRefusal = true
		case it.Kind == KindBusLine && strings.Contains(it.Key, "WAIT DONE reason=note"):
			sawNoteDone = true
		}
	}
	if !sawNote {
		t.Error("the note was not relayed")
	}
	if !sawRefusal {
		t.Error("INBOX REFUSED was not relayed; the default case PRINTS, and a refusal on stderr is the reason this tool reads both streams")
	}
	if !sawNoteDone {
		t.Error("WAIT DONE reason=note was not relayed; only reason=timeout is the inner wait's own bookkeeping")
	}

	// Rule 7's sum over the six lines: three bookkeeping suppressed, and the
	// note, the refusal and the reason=note done relayed. The bookkeeping is
	// counted, not merely dropped.
	read, suppress, relay, standing := b.Counts()
	if read != 6 {
		t.Errorf("read=%d, want 6: every line the inner wait printed is a line of the protocol this poll read", read)
	}
	if suppress != 3 {
		t.Errorf("suppress=%d, want 3: the three timeout bookkeeping lines are counted", suppress)
	}
	if relay != 3 {
		t.Errorf("relay=%d, want 3", relay)
	}
	if standing != 0 {
		t.Errorf("standing=%d, want 0", standing)
	}
	if read != suppress+relay+standing {
		t.Errorf("rule 7's sum does not hold: read=%d suppressed=%d relayed=%d standing=%d", read, suppress, relay, standing)
	}
}
