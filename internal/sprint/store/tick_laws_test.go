package store

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// The laws of the machine's accept and of its redeals, on the store with the
// machine RUNNING: docs/SPEC-SPRINT.md sections 2 and 6, tla/DirtyTick.tla
// RedealsAreEndedTakes and RedealBoundHolds.

// driveTo runs the machine, the workers and the readers, a second at a time,
// until the primary is in the state, and fails when it is not within twelve
// ticks.
func (h *harness) driveTo(id, state string) {
	h.t.Helper()
	for range 12 {
		h.machine()
		if h.state(id) == state {
			return
		}
		h.work("m1")
		h.work("m2")
		h.readAll()
		h.tick(time.Second)
	}
	h.t.Fatalf("%s is %s after twelve ticks, want %s", id, h.state(id), state)
}

// ticks runs the machine n times, a second apart.
func (h *harness) ticks(n int) {
	h.t.Helper()
	for range n {
		h.tick(time.Second)
		h.machine()
	}
}

// A primary the coordinator returned to review is not accepted again by the
// running machine on the reads that stand: it stays in review, nothing is
// queued to merge, and the returned judgment offers the coordinator accept. A
// rework is a new attempt, whose own two reads the machine accepts.
func TestTheMachineDoesNotAcceptAReturnedPrimaryAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.driveTo("s1-1", sprint.Merging)
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "the stream branch went red"}))
	h.ticks(5)
	if st := h.state("s1-1"); st != sprint.Review || len(h.snap().Merge.Cell("s1", sprint.Queued)) != 0 {
		t.Fatalf("five ticks after the return: s1-1 %s, queued %d", st, len(h.snap().Merge.Cell("s1", sprint.Queued)))
	}
	returned := h.openOf(sprint.NReturned)
	if len(returned) != 1 || !slices.Contains(returned[0].Note.Decisions, "accept") {
		t.Fatalf("the returned judgment: %+v", returned)
	}
	h.clean("returned, held")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "rebase it", Answers: []string{returned[0].Note.ID}}))
	h.driveTo("s1-1", sprint.Merging)
	if a := h.snap().Work.Card("s1-1").Int("attempt"); a != 2 {
		t.Fatalf("accepted at attempt %d, want 2", a)
	}
	h.clean("accepted at attempt 2")
}

// A primary whose CI is red at its head is not accepted by the running
// machine: the coordinator is told it is ready to accept beside the red, and
// a green at its head lets the machine accept it.
func TestTheMachineDoesNotAcceptAPrimaryWhoseCIIsRed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.driveTo("s1-1", sprint.Review)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1", Source: "test"}))
	h.machine() // asks its readers
	h.readAll()
	h.ticks(5)
	if st := h.state("s1-1"); st != sprint.Review {
		t.Fatalf("five ticks with its CI red: s1-1 %s", st)
	}
	if n := len(h.openOf(sprint.NReadyToAccept)); n != 1 || len(h.openOf(sprint.NCIRed)) != 1 {
		t.Fatalf("ready to accept %d, ci red %d: want one of each", n, len(h.openOf(sprint.NCIRed)))
	}
	h.clean("ci red, held")
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Run: "2", Source: "test"}))
	h.ticks(1)
	if st := h.state("s1-1"); st != sprint.Merging {
		t.Fatalf("green at its head: s1-1 %s", st)
	}
	h.clean("ci green, accepted")
}

// Members flapping while a card sits ready never retire it: its holder going
// silent twice the bound's times deals it round the fleet with its count at
// zero and no redeal line; a take that ends is the first redeal counted.
func TestMembersFlappingNeverRetireAReadyCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	card := func() *sprint.Card { return h.snap().Fleet.Card("s1-1.w1") }
	for lap := range 2 * sprint.MaxRedeals {
		holder := card().Row
		other := map[string]string{"m1": "m2", "m2": "m1"}[holder]
		h.setLive(other)
		for range 3 {
			h.tick(10 * time.Second)
			h.machine()
		}
		if c := card(); c.Col != sprint.Ready || c.Row != other || c.Int("redeals") != 0 {
			t.Fatalf("lap %d, %s silent with the card ready: %s:%s redeals %d", lap, holder, c.Row, c.Col, c.Int("redeals"))
		}
		h.setLive("m1", "m2")
		h.tick(time.Second)
		h.machine()
	}
	if n := len(h.openOf(sprint.NBound)); n != 0 {
		t.Fatalf("%d bound judgments after members flapped", n)
	}
	redealLines := func() []string {
		var out []string
		for _, l := range h.lines() {
			if l.Card == "s1-1.w1" && strings.Contains(sprint.Render(l), "redeal ") {
				out = append(out, sprint.Render(l))
			}
		}
		return out
	}
	if lines := redealLines(); len(lines) != 0 {
		t.Fatalf("redeal lines with no take ended: %q", lines)
	}

	c := card()
	holder := c.Row
	h.run(TakeStep(sprint.TakeReq{As: holder, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: holder}))
	h.setLive(map[string]string{"m1": "m2", "m2": "m1"}[holder])
	for range 3 {
		h.tick(10 * time.Second)
		h.machine()
	}
	if c := card(); c.Col != sprint.Ready || c.Int("redeals") != 1 {
		t.Fatalf("its take ended: %s:%s redeals %d, want 1", c.Row, c.Col, c.Int("redeals"))
	}
	if lines := redealLines(); len(lines) != 1 || !strings.Contains(lines[0], "redeal 1 of 3") {
		t.Fatalf("the redeal lines: %q", lines)
	}
	h.clean("one take ended")
}

// The reference model and the engine agree on the three laws, step by step:
// a primary with its CI red at its head is held, and told ready to accept when
// its CI judgment is acknowledged; a returned primary is held on its standing
// reads; a card whose members flap while it sits ready keeps its count, and a
// take that ends is counted (refmodel tickAccept, AcceptHeld, FleetDown).
func TestTheModelAndTheEngineAgreeOnTheAcceptAndRedealLaws(t *testing.T) {
	t.Parallel()
	h := newDHarness(t)
	do := func(a dAction) {
		t.Helper()
		h.do(a)
		for _, f := range h.findings {
			if _, known := dClassify(f); !known {
				t.Fatalf("a difference between the engine and the model:\n%s", f)
			}
		}
	}
	member := func(card string) string { return h.observe().Work[card].Member }
	work := func(p string) {
		t.Helper()
		c := refmodel.WC(p, h.observe().Primaries[p].Attempt)
		m, g := member(c), h.observe().Work[c].Gen
		do(dAction{Kind: "take", Member: m, Card: c, Gen: g})
		do(dAction{Kind: "finish", Member: m, Card: c, Gen: g, OK: true})
	}
	readOK := func(p string) {
		t.Helper()
		s := h.observe()
		for _, id := range refmodel.Keys(s.Reads) {
			if rc := s.Reads[id]; rc.Primary == p && (rc.Place == refmodel.Asked || rc.Place == refmodel.Reading) {
				do(dAction{Kind: "read", Reader: rc.Reader, Card: id, OK: true})
			}
		}
	}
	both := func(p, state, when string) {
		t.Helper()
		if e, m := h.observe().Primaries[p].State, h.model.Primaries[p].State; e != state || m != state {
			t.Fatalf("%s: %s is %s in the engine and %s in the model, want %s", when, p, e, m, state)
		}
	}

	do(dAction{Kind: "fleet", Op: "up", Member: "m1"})
	do(dAction{Kind: "fleet", Op: "up", Member: "m2"})
	do(dAction{Kind: "start"})
	do(dAction{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2"}})
	do(dAction{Kind: "tick"})
	work("a1")
	work("a2")
	do(dAction{Kind: "tick"})
	do(dAction{Kind: "ci", IDs: []string{"a2"}, OK: false, Run: 1})
	readOK("a1")
	readOK("a2")
	do(dAction{Kind: "tick"})
	both("a1", refmodel.Merging, "accepted")
	both("a2", refmodel.Review, "its CI red at its head")

	do(dAction{Kind: "return", IDs: []string{"a1"}})
	do(dAction{Kind: "tick"})
	do(dAction{Kind: "tick"})
	both("a1", refmodel.Review, "returned, two ticks on")

	do(dAction{Kind: "ack", Type: refmodel.JCI, Subject: "a2"})
	accept := refmodel.Judgment{Type: refmodel.JAccept, Subject: "a2"}
	if !h.observe().Open[accept] || !h.model.Open[accept] {
		t.Fatalf("its CI judgment acknowledged: ready to accept open in the engine %v, in the model %v", h.observe().Open[accept], h.model.Open[accept])
	}
	do(dAction{Kind: "ci", IDs: []string{"a2"}, OK: true, Run: 2})
	do(dAction{Kind: "tick"})
	both("a2", refmodel.Merging, "green at its head")

	do(dAction{Kind: "add", Stream: "s2", IDs: []string{"b1"}})
	do(dAction{Kind: "tick"})
	for range 2 * refmodel.MaxRedeals {
		from := member("b1.w1")
		do(dAction{Kind: "fleet", Op: "down", Member: from})
		do(dAction{Kind: "fleet", Op: "up", Member: from})
	}
	if e, m := h.observe().Work["b1.w1"], h.model.Work["b1.w1"]; e.Redeals != 0 || m.Redeals != 0 || e.Place != refmodel.FReady {
		t.Fatalf("members flapping with the card ready: engine %+v, model %+v", e, m)
	}
	c := h.observe().Work["b1.w1"]
	do(dAction{Kind: "take", Member: c.Member, Card: "b1.w1", Gen: c.Gen})
	do(dAction{Kind: "fleet", Op: "down", Member: c.Member})
	if e, m := h.observe().Work["b1.w1"], h.model.Work["b1.w1"]; e.Redeals != 1 || m.Redeals != 1 {
		t.Fatalf("its take ended: engine %+v, model %+v", e, m)
	}
}
