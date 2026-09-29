package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// sentinelsBy is add --stream <streams> --count n --sentinel-every k
// [--sentinel-last] as one step.
func (h *harness) sentinelsBy(streams []string, n, k int, last bool) Result {
	h.t.Helper()
	var rs []sprint.AddReq
	for _, s := range streams {
		rs = append(rs, sprint.AddReq{Stream: s, Count: n, Every: k, Last: last, Who: "tester"})
	}
	return h.must(AddEachStep(rs))
}

// A stream in stops, several streams in one step: the sentinels block by
// their place in line and nothing about position is written on any card; the
// first block of each stream is ready, the rest wait; the log has a line per
// set, not per card.
func TestSentinelsByPositionStoreNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	before := len(h.lines())
	h.sentinelsBy([]string{"a", "b", "c"}, 300, 100, true)
	s := h.snap()
	for _, st := range []string{"a", "b", "c"} {
		ready, waiting, gates := 0, 0, 0
		for _, c := range s.Work.Column(sprint.States...) {
			if c.Row != st {
				continue
			}
			if c.F("needs") != "" {
				t.Fatalf("%s stores needs %q", c.ID, c.F("needs"))
			}
			switch {
			case sprint.IsSentinel(c):
				gates++
			case c.Col == sprint.Ready:
				ready++
			case c.Col == sprint.Waiting:
				waiting++
			}
		}
		if ready != 100 || waiting != 200 || gates != 3 {
			t.Fatalf("stream %s: ready %d waiting %d gates %d", st, ready, waiting, gates)
		}
	}
	g1 := s.Work.Card("a-gate-1")
	if w := sprint.WaitsFor(s, g1, nil); len(w) != 100 || w[0] != "a-1" || w[99] != "a-100" {
		t.Fatalf("a-gate-1 waits for %d cards", len(w))
	}
	if w := sprint.WaitsFor(s, s.Work.Card("a-150"), nil); strings.Join(w, ",") != "a-gate-1" {
		t.Fatalf("a-150 waits for %v", w)
	}
	if n := len(h.lines()) - before; n > 12 {
		t.Fatalf("the add of 900 cards and 9 stops wrote %d log lines", n)
	}
	h.clean("three streams in stops")
	// dropping the cards before a stop frees it: reached, released, and what
	// is behind it goes to ready as one set, up to the next stop
	var first []string
	for i := 1; i <= 100; i++ {
		first = append(first, fmt.Sprintf("a-%d", i))
	}
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: first}, Reason: "done elsewhere"}))
	h.must(SentinelsDueStep("tester"))
	if h.snap().Work.Card("a-gate-1").F("reached") == "" {
		t.Fatalf("a-gate-1 not reached with the cards before it dropped")
	}
	mark := len(h.lines())
	res := h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"a-gate-1"}, Reason: "go", Coordinator: "tester", Who: "tester"}))
	s = h.snap()
	if st := s.StateOf("a-101"); st != sprint.Ready || s.StateOf("a-200") != sprint.Ready || s.StateOf("a-201") != sprint.Waiting {
		t.Fatalf("released a-gate-1: a-101 %s a-200 %s a-201 %s (%v)", st, s.StateOf("a-200"), s.StateOf("a-201"), res.Moved[:1])
	}
	moves := 0
	for _, l := range h.lines()[mark:] {
		if l.Note == nil && l.Table == sprint.Work {
			moves++
		}
	}
	if moves > 3 {
		t.Fatalf("the release wrote %d work lines for its set", moves)
	}
	h.clean("released")
}

// A stop inserted into the middle of a line of ready cards: the ready cards
// behind it go back to waiting as one set, one log line; the stop names
// nothing.
func TestAStopInsertedIntoTheMiddleOfALine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1000}))
	mark := len(h.lines())
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, After: "s1-500"}))
	s := h.snap()
	if s.StateOf("s1-500") != sprint.Ready || s.StateOf("s1-501") != sprint.Waiting || s.StateOf("s1-1000") != sprint.Waiting || s.Work.Card("stop").F("needs") != "" {
		t.Fatalf("inserted: s1-500 %s s1-501 %s stop needs %q", s.StateOf("s1-500"), s.StateOf("s1-501"), s.Work.Card("stop").F("needs"))
	}
	var sets []sprint.Line
	for _, l := range h.lines()[mark:] {
		if l.Note == nil && l.Table == sprint.Work {
			sets = append(sets, l)
		}
	}
	if len(sets) != 2 || len(sets[1].Cards) != 500 {
		t.Fatalf("the insert wrote %d work lines (%v)", len(sets), sets)
	}
	h.clean("inserted")
}
