package wake

import (
	"strings"
	"testing"
)

// TestProgressIsNeverRelayedAsABusLine is the consumer half of the class this
// lane closes; cmd/nova-bus's TestProgressNeverEntersTheProtocolStream is the
// producer half.
//
// This tool reads nova-bus's stdout and stderr TOGETHER, deliberately: a
// watcher that read stdout only would be the grep of 2026-09-10, which kept
// the lines it knew about and slept through an hour of a bus refusing every
// read. That is also how a stderr-only PROGRESS line reached a classifier whose
// default case PRINTS. On 2026-09-18 `INBOX WALK commits=1/1 notes=0
// elapsed=3ms` was relayed as `WAKE BUS LINE INBOX WALK ...`, counted as a
// change in the world, and the poll that found that "news" returned before the
// mail the watcher was waiting for came down.
//
// The transcript below is what the two streams look like together: progress,
// protocol and a refusal. The progress is dropped and NOT COUNTED -- read is
// the protocol lines this poll read, and a sentence about how far a walk has
// got is not one of them. The refusal and the note are untouched, because the
// allow-list still decides only what is suppressed and never what is shown.
func TestProgressIsNeverRelayedAsABusLine(t *testing.T) {
	b := &Bus{}
	res := b.Classify(strings.Join([]string{
		"INBOX WALK commits=1/1 notes=0 elapsed=3ms",
		`INBOX WALK bounded commits=500 cursor=0edc81b7 behind=more-than-500 notes=0 remedy="raise --max-commits or close --before <instant>"`,
		"INBOX NOTE id=stella-aaaaaaaaaaaa from=Stella addr=to at=2026-09-18T11:00:00Z path=from-stella/a.md: the first note",
		"INBOX OPEN carrying=1 heard=0 large=false remedy=-",
		"INBOX REFUSED: the bus is not a git checkout",
		"INBOX OK as=Rowan carrying=1 open=0 notes=1 receipts=0 heard=0 unaddressed=0 unreadable=0",
	}, "\n") + "\n")

	for _, it := range res.Items {
		if strings.Contains(it.Key, "INBOX WALK") {
			t.Errorf("a progress line was relayed as a bus line, which is what woke a window with a walk's bookkeeping and ended a poll early:\n%s", it.Key)
		}
	}
	for _, line := range res.Standing {
		if strings.Contains(line, "INBOX WALK") {
			t.Errorf("a progress line was carried as a standing line:\n%s", line)
		}
	}

	// The refusal and the note still arrive: the fix drops PROGRESS and nothing
	// else. A test that only checked the walk was gone would pass over a tool
	// that had gone quiet about the bus refusing every read.
	var sawNote, sawRefusal bool
	for _, it := range res.Items {
		switch {
		case it.Kind == KindBus && it.ID == "stella-aaaaaaaaaaaa":
			sawNote = true
		case it.Kind == KindBusLine && strings.Contains(it.Key, "INBOX REFUSED"):
			sawRefusal = true
		}
	}
	if !sawNote {
		t.Error("the note was not relayed")
	}
	if !sawRefusal {
		t.Error("INBOX REFUSED was not relayed; the default case PRINTS, and a refusal on stderr is the reason this tool reads both streams")
	}

	// Rule 7's sum, over the PROTOCOL lines only: four of them here -- NOTE
	// relayed, OPEN and OK suppressed, REFUSED relayed -- and the two progress
	// lines counted nowhere.
	read, suppress, relay, standing := b.Counts()
	if read != 4 {
		t.Errorf("read=%d, want 4: progress is not a line of the protocol this poll read", read)
	}
	if read != suppress+relay+standing {
		t.Errorf("rule 7's sum does not hold: read=%d suppressed=%d relayed=%d standing=%d", read, suppress, relay, standing)
	}
}
