package main

import (
	"strconv"
	"strings"
	"testing"
)

// gatedStreams adds streams a and c, each three runs of ten cards with a gate
// after each run, and a-dep-1 before a-gate-2 needing c-25 (behind c-gate-2).
func gatedStreams(na *npApp) {
	na.ok("init")
	for _, s := range []string{"a", "c"} {
		for k := 1; k <= 3; k++ {
			na.ok("add --stream " + s + " --count 10")
			na.ok("add --stream " + s + " --sentinel " + s + "-gate-" + strconv.Itoa(k))
		}
	}
	na.ok("add --stream a a-dep-1 --needs c-25 --after a-15")
}

// TestNewPathAddRefusesALoopThroughTheGates (the 4806 read, M1; errata 3
// amendment 7): an add whose needs reach, through the named needs and the
// gates' implicit ones, a sentinel of its own stream after the place it is put
// is refused, naming the loop, and nothing is written; the same need placed
// past that sentinel closes no loop and is admitted.
func TestNewPathAddRefusesALoopThroughTheGates(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	gatedStreams(na)
	before := na.image()
	code, out, errs := na.do("add --stream c c-dep-1 --needs a-dep-1 --after c-5")
	if code != exitRefused || !strings.Contains(out+errs, "code=CYCLE") || strings.Count(out+errs, "nothing was changed") > 1 ||
		!strings.Contains(out+errs, "a-dep-1 needs c-25") || !strings.Contains(out+errs, "c-gate-") {
		t.Fatalf("the add that closes a loop through the gates: exit %d\n%s%s", code, out, errs)
	}
	if na.image() != before {
		t.Errorf("the refused add wrote")
	}
	na.ok("add --stream c c-dep-2 --needs a-dep-1 --after c-28")
}

// TestNewPathCheckReportsACycle (the 4806 read, M1): check reads the work
// table whole and names a cycle through the gates that a rank made (rank is
// not checked: errata 3 amendment 7), refused CYCLE (exit 2); a clean table
// says so and exits 0.
func TestNewPathCheckReportsACycle(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	gatedStreams(na)
	na.ok("add --stream c c-dep-2 --needs a-dep-1 --after c-28")
	if out := na.ok("check"); !strings.Contains(out, "no cycle of needs in 2 streams") {
		t.Fatalf("check on a clean table:\n%s", out)
	}
	na.ok("rank c-dep-2 --after c-5")
	code, out, errs := na.do("check")
	if code != exitRefused || !strings.Contains(out+errs, "code=CYCLE") || !strings.Contains(out+errs, "a cycle through a-dep-1 needs c-25") ||
		!strings.Contains(out+errs, "cards can never be reached") {
		t.Fatalf("check on a table with a cycle: exit %d\n%s%s", code, out, errs)
	}
}

// TestNewPathAddNeedsAcrossManyGates (the cold read at cacfd32cc, 2): a long
// gated stream, 33 runs of 30 cards with a gate after each, and a need on its
// tail from another stream: no loop, and the needs walk stays linear (a gate
// waits for the cards back to the gate before it, a card for the nearest gate
// before it), so the add is admitted. Before, each gate's place read went
// back to the head of the stream, the cost grew with the square of the gates,
// and the walk ran out its bound.
func TestNewPathAddNeedsAcrossManyGates(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	na.ok("init")
	for k := 1; k <= 33; k++ {
		na.ok("add --stream a --count 30")
		na.ok("add --stream a --sentinel a-gate-" + strconv.Itoa(k))
	}
	na.ok("add --stream b --count 3")
	if code, out, errs := na.do("add --stream b b-dep-1 --needs a-900"); code != 0 {
		t.Fatalf("a need on the tail of a long gated stream: exit %d\n%s%s", code, out, errs)
	}
}
