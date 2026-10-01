package store

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
	require.Equal(h.t, state, h.state(id), "%s after twelve ticks", id)
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
	require.Equal(t, sprint.Review, h.state("s1-1"), "five ticks after the return")
	assert.Empty(t, h.snap().Merge.Cell("s1", sprint.Queued), "five ticks after the return: queued to merge")
	returned := h.openOf(sprint.NReturned)
	require.Len(t, returned, 1, "the returned judgment")
	assert.Contains(t, returned[0].Note.Decisions, "accept")
	h.clean("returned, held")
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "rebase it", Answers: []string{returned[0].Note.ID}}))
	h.driveTo("s1-1", sprint.Merging)
	assert.Equal(t, 2, h.snap().Work.Card("s1-1").Int("attempt"), "accepted at its new attempt")
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
	require.Equal(t, sprint.Review, h.state("s1-1"), "five ticks with its CI red")
	assert.Len(t, h.openOf(sprint.NReadyToAccept), 1, "ready to accept beside the red")
	assert.Len(t, h.openOf(sprint.NCIRed), 1, "ci red")
	h.clean("ci red, held")
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Run: "2", Source: "test"}))
	h.ticks(1)
	assert.Equal(t, sprint.Merging, h.state("s1-1"), "green at its head")
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
		c := card()
		assert.Equal(t, [2]string{other, sprint.Ready}, [2]string{c.Row, c.Col}, "lap %d, %s silent with the card ready", lap, holder)
		assert.Equal(t, 0, c.Int("redeals"), "lap %d, %s silent with the card ready", lap, holder)
		h.setLive("m1", "m2")
		h.tick(time.Second)
		h.machine()
	}
	assert.Empty(t, h.openOf(sprint.NBound), "bound judgments after members flapped")
	redealLines := func() []string {
		var out []string
		for _, l := range h.lines() {
			if l.Card == "s1-1.w1" && strings.Contains(sprint.Render(l), "redeal ") {
				out = append(out, sprint.Render(l))
			}
		}
		return out
	}
	assert.Empty(t, redealLines(), "redeal lines with no take ended")

	c := card()
	holder := c.Row
	h.run(TakeStep(sprint.TakeReq{As: holder, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: holder}))
	h.setLive(map[string]string{"m1": "m2", "m2": "m1"}[holder])
	for range 3 {
		h.tick(10 * time.Second)
		h.machine()
	}
	assert.Equal(t, sprint.Ready, card().Col, "its take ended")
	assert.Equal(t, 1, card().Int("redeals"), "its take ended")
	lines := redealLines()
	require.Len(t, lines, 1, "the redeal lines")
	assert.Contains(t, lines[0], "redeal 1 of 3")
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
			_, known := dClassify(f)
			require.True(t, known, "a difference between the engine and the model:\n%s", f)
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
		require.Equal(t, state, h.observe().Primaries[p].State, "%s: %s in the engine", when, p)
		require.Equal(t, state, h.model.Primaries[p].State, "%s: %s in the model", when, p)
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
	assert.True(t, h.observe().Open[accept], "its CI judgment acknowledged: ready to accept open in the engine")
	assert.True(t, h.model.Open[accept], "its CI judgment acknowledged: ready to accept open in the model")
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
	assert.Equal(t, refmodel.FReady, h.observe().Work["b1.w1"].Place, "members flapping with the card ready")
	assert.Equal(t, [2]int{0, 0}, [2]int{h.observe().Work["b1.w1"].Redeals, h.model.Work["b1.w1"].Redeals}, "members flapping with the card ready: redeals in the engine and the model")
	c := h.observe().Work["b1.w1"]
	do(dAction{Kind: "take", Member: c.Member, Card: "b1.w1", Gen: c.Gen})
	do(dAction{Kind: "fleet", Op: "down", Member: c.Member})
	assert.Equal(t, [2]int{1, 1}, [2]int{h.observe().Work["b1.w1"].Redeals, h.model.Work["b1.w1"].Redeals}, "its take ended: redeals in the engine and the model")
}

// Primaries returned to review stay held however many more reads stand at
// their attempt; the coordinator's accept takes one, which the running
// machine then leaves merging; a rework's new attempt is accepted.
func TestReturnedPrimariesStayHeldThroughMoreReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.driveTo("s1-1", sprint.Merging)
	h.driveTo("s1-2", sprint.Merging)
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "the stream branch went red"}))
	c := h.snap().Work.Card("s1-1")
	require.Equal(t, c.F("attempt"), c.F(sprint.FieldReturnedAttempt), "marked at its attempt")
	for range 4 {
		h.readAll()
		h.ticks(2)
	}
	assert.Equal(t, sprint.Review, h.state("s1-1"))
	assert.Equal(t, sprint.Review, h.state("s1-2"))
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
	h.ticks(2)
	assert.Equal(t, sprint.Merging, h.state("s1-2"), "the coordinator's accept of a returned primary")
	var answers []string
	for _, o := range h.openOf(sprint.NReturned) {
		answers = append(answers, o.Note.ID)
	}
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "rebase it", Answers: answers}))
	h.driveTo("s1-1", sprint.Merging)
	assert.Equal(t, 2, h.snap().Work.Card("s1-1").Int("attempt"))
	h.clean("returned, held, accepted")
}

// A late CI result for a head the primary has moved past, green or red, is
// labelled and leaves the red at its current head standing: the running
// machine keeps holding it (tla/DirtyTick.tla W19).
func TestALateCIResultForAnOlderHeadKeepsTheHold(t *testing.T) {
	t.Parallel()
	for _, red := range []bool{false, true} {
		h := newHarness(t)
		h.setup(1)
		h.startMachine()
		h.driveTo("s1-1", sprint.Review)
		h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1", Source: "test"}))
		h.machine()
		h.readAll()
		h.ticks(3)
		require.Equal(t, sprint.Review, h.state("s1-1"), "red at its head")
		h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: red, Head: "an-older-head", Run: "0", Source: "test"}))
		h.ticks(2)
		c := h.snap().Work.Card("s1-1")
		assert.Equal(t, sprint.Review, h.state("s1-1"), "a late result (red %v) for an older head released the hold", red)
		assert.Equal(t, "red", c.F("ci"), "the record of its head")
		assert.Equal(t, c.F("head"), c.F("ci_head"), "the record of its head")
		assert.Equal(t, "0", c.F("ci_run"), "the last run reported")
		again := h.run(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: red, Head: "an-older-head", Run: "0", Source: "test"}))
		assert.Len(t, again.Refused, 1, "a retried report of the run is recorded once")
		h.clean("a late result for an older head")
	}
}

// A primary in review whose accept was cut after its merge card and repaired
// (an orphan merge card), returned by the coordinator, is held by the running
// machine as any returned primary is.
func TestAnOrphanReturnIsHeldByTheMachine(t *testing.T) {
	t.Parallel()
	p := newProbe(t)
	p.setup(1)
	p.toReview("h", "s1-1")
	p.do("ask", AskStep(sprint.AskReq{Sel: ids("s1-1")}))
	for _, rc := range p.snap().Readers.Of("s1-1") {
		p.read(rc.F("reader"), rc.ID, "ok")
	}
	p.m.Fail = func(pt string) error {
		if pt == "apply t-work before" {
			return errors.New("cut")
		}
		return nil
	}
	_, err := p.st.Run(p.ctx, AcceptStep(sprint.AcceptReq{Sel: ids("s1-1")}))
	require.Error(t, err, "the accept is cut before its work entry")
	p.m.Fail = nil
	s := p.snap()
	pr := s.Work.Card("s1-1")
	_, err = p.m.Apply(p.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: strconv.FormatUint(s.Work.Revision, 10),
		OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: "s1-1", Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: pr.Row, Col: pr.Col}}, Set: map[string]string{"outside": "1"}}}})
	require.NoError(t, err)
	_, err = p.st.Repair(p.ctx)
	require.NoError(t, err)
	require.Equal(t, sprint.Review, p.state("s1-1"), "the repair skipped the accept's work entry")
	p.do("return", ReturnStep(sprint.ReturnReq{Sel: ids("s1-1")}))
	c := p.snap().Work.Card("s1-1")
	assert.Equal(t, c.F("attempt"), c.F(sprint.FieldReturnedAttempt), "the orphan return marks its attempt")
	p.startMachine()
	p.ticks(3)
	assert.Equal(t, sprint.Review, p.state("s1-1"), "the running machine took the returned orphan back")
	assert.NotEmpty(t, p.openOf(sprint.NReadyToAccept), "the coordinator is told it is ready to accept")
}

// Every member going down and up while the card sits ready withdraws it and
// deals it again with its count kept, however often; then ended takes count,
// by the direct path and by the withdrawn one alike, and the fourth ended
// take retires it (MaxRedeals, 3: dealt again after each of the first three).
func TestWithdrawalsWithoutATakeKeepTheCountAndTheFourthEndedTakeRetires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	wc := func() *sprint.Card { return h.snap().Fleet.Card("s1-1.w1") }
	require.Equal(t, sprint.Ready, wc().Col, "dealt")
	allDown := func() {
		h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
		h.setLive()
		h.machine()
	}
	allUp := func() {
		h.setLive("m1", "m2")
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
		h.tick(time.Second)
		h.machine()
	}
	for lap := range 5 {
		allDown()
		assert.Equal(t, sprint.Withdrawn, wc().Col, "lap %d", lap)
		assert.Empty(t, wc().F(sprint.FieldTakeEnded), "lap %d: withdrawn untaken", lap)
		allUp()
		assert.Equal(t, sprint.Ready, wc().Col, "lap %d", lap)
		assert.Equal(t, 0, wc().Int("redeals"), "lap %d", lap)
	}
	assert.Empty(t, h.openOf(sprint.NBound))
	ended := 0
	for ended < 6 && wc().Col == sprint.Ready {
		c := wc()
		h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Who: c.Row}))
		if ended%2 == 0 {
			h.must(FleetStep(sprint.FleetReq{Op: "down", Member: c.Row}))
			h.must(FleetStep(sprint.FleetReq{Op: "up", Member: c.Row}))
			h.tick(time.Second)
			h.machine()
		} else {
			allDown()
			allUp()
		}
		ended++
	}
	assert.Equal(t, 4, ended, "ended takes until retired")
	assert.Equal(t, sprint.Withdrawn, wc().Col, "retired")
	assert.Equal(t, sprint.MaxRedeals, wc().Int("redeals"))
	assert.Len(t, h.openOf(sprint.NBound), 1, "the bound's judgment")
	h.clean("retired at the fourth ended take")
}
