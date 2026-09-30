package store

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fleetRow is a fleet row's display cells.
func (h *harness) fleetRow(member string) map[string]string {
	h.t.Helper()
	shapes, err := h.m.Shapes(h.ctx, []string{h.st.Names.Table(sprint.Fleet)})
	if err != nil {
		h.t.Fatal(err)
	}
	for _, r := range shapes[0].Rows {
		if r.Key == member {
			return r.Texts
		}
	}
	h.t.Fatalf("no fleet row %s", member)
	return nil
}

// setLive sets the members that beat.
func (h *harness) setLive(members ...string) {
	h.mu.Lock()
	h.live = members
	h.mu.Unlock()
}

// beatAt is one beat of member at the load given.
func (h *harness) beatAt(member string, pct float64) sprint.Beat {
	h.t.Helper()
	b, err := h.st.Beat(h.ctx, member, &pct, hostload.Source{})
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

// memberNotes is the happened notifications of a type naming member.
func (h *harness) memberNotes(typ, member string) []string {
	h.t.Helper()
	var notes []sprint.Note
	var err error
	if h.m != nil {
		notes, _, err = h.m.NotesSince(h.ctx, "", 100000)
	} else {
		notes, _, err = h.st.B.NotesSince(h.ctx, "", 100000)
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, n := range notes {
		if n.Type == typ && strings.HasPrefix(n.What, member+" ") {
			out = append(out, n.What)
		}
	}
	return out
}

// dealtTo is how many unfinished work cards each member holds.
func (h *harness) dealtTo() map[string]int {
	s := h.snap()
	out := map[string]int{}
	for _, c := range s.Fleet.Column(sprint.Ready, sprint.Working) {
		out[c.Row]++
	}
	return out
}

// TestABeatIsOneRecordMeasuredOrGiven: a beat with no load measures the
// machine through its source; a given load is written as given; the same
// beat twice in one second writes the same record; a load out of range and
// a machine that cannot measure are refused.
func TestABeatIsOneRecordMeasuredOrGiven(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	src := hostload.Source{NCPU: 4, Load1: func() (float64, bool) { return 1, true }}
	b, err := h.st.Beat(h.ctx, "m3", nil, src)
	if err != nil || b.How != hostload.HowLoad1 || b.Load != 25 || !b.At.Equal(t0) {
		t.Fatalf("measured: %+v %v, want 25%% by load1 at %s", b, err, t0)
	}
	stat := "cpu  100 0 100 700 100 0 0 0 0 0\n"
	src.ProcStat = func() (string, error) { return stat, nil }
	h.tick(time.Second)
	stat = "cpu  400 0 200 900 100 0 0 0 0 0\n"
	if _, err := h.st.Beat(h.ctx, "m3", nil, src); err != nil {
		t.Fatal(err)
	}
	h.tick(time.Second)
	stat = "cpu  500 0 300 1700 100 0 0 0 0 0\n"
	b, err = h.st.Beat(h.ctx, "m3", nil, src)
	// the last interval: busy +200 of total +1000 is 20%; the window's
	// highest is the 25% of the first beat
	if err != nil || b.How != hostload.HowCPU || b.Samples[len(b.Samples)-1].Pct != 20 || b.Load != 25 {
		t.Fatalf("cpu: %+v %v", b, err)
	}
	kv, _ := h.st.rootKV()
	first := h.beatAt("m4", 42)
	raw1, _, _ := kv.GetKey(h.ctx, beatKey("m4"))
	second := h.beatAt("m4", 42)
	raw2, _, _ := kv.GetKey(h.ctx, beatKey("m4"))
	if raw1 != raw2 || first.Load != 42 || second.How != sprint.HowGiven {
		t.Fatalf("the same beat twice in one second:\n%s\n%s", raw1, raw2)
	}
	bad := -1.0
	if _, err := h.st.Beat(h.ctx, "m4", &bad, hostload.Source{}); err == nil {
		t.Fatal("a negative load must be refused")
	}
	if _, err := h.st.Beat(h.ctx, "m4", nil, hostload.Source{}); err == nil || !strings.Contains(err.Error(), "--load") {
		t.Fatalf("a machine that cannot measure: %v, want a refusal naming --load", err)
	}
	if _, err := h.st.Beat(h.ctx, "no such", nil, src); err == nil {
		t.Fatal("a member name with a blank must be refused")
	}
}

// TestASilentMemberGoesDownAndItsCardsAreDealt: a member whose last beat is
// past BeatDeadline goes down at the tick: its unfinished work cards are
// dealt to the member up, with one happened notification; and when it beats
// again it comes up and the ready queues are levelled.
func TestASilentMemberGoesDownAndItsCardsAreDealt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	if d := h.dealtTo(); d["m1"] != 2 || d["m2"] != 2 {
		t.Fatalf("dealt %v, want two each", d)
	}
	h.setLive("m2")
	h.tick(sprint.BeatDeadline)
	h.machine()
	if d := h.dealtTo(); d["m1"] != 2 || h.snap().MemberCtl("m1").F("status") != sprint.Up {
		t.Fatalf("at the deadline m1 is still up with its cards: %v", d)
	}
	h.tick(time.Second)
	h.machine()
	if d := h.dealtTo(); d["m1"] != 0 || d["m2"] != 4 {
		t.Fatalf("after the deadline: dealt %v, want every card on m2", d)
	}
	if st := h.snap().MemberCtl("m1").F("status"); st != sprint.Down {
		t.Fatalf("m1 %s, want down", st)
	}
	if n := h.memberNotes(sprint.NMemberDown, "m1"); len(n) != 1 || !strings.Contains(n[0], "no beat for 15s") {
		t.Fatalf("down notifications: %q, want one saying why", n)
	}
	if row := h.fleetRow("m1"); row[sprint.Status] != sprint.Down || row[sprint.Load] != "" {
		t.Fatalf("m1's row: %v, want down with no load", row)
	}
	h.machine()
	if n := h.memberNotes(sprint.NMemberDown, "m1"); len(n) != 1 {
		t.Fatalf("a second tick wrote again: %q", n)
	}

	h.setLive("m1", "m2")
	h.tick(time.Second)
	h.machine()
	if st := h.snap().MemberCtl("m1").F("status"); st != sprint.Up {
		t.Fatalf("m1 beats again and is %s, want up", st)
	}
	if d := h.dealtTo(); d["m1"] != 2 || d["m2"] != 2 {
		t.Fatalf("after m1 came up: dealt %v, want the queues levelled", d)
	}
	if n := h.memberNotes(sprint.NMemberUp, "m1"); len(n) != 2 || !strings.Contains(n[1], "it beats") {
		t.Fatalf("up notifications: %q, want the setup's and the beat's", n)
	}
	if row := h.fleetRow("m1"); row[sprint.Status] != sprint.Up || row[sprint.Load] != "0.0%" {
		t.Fatalf("m1's row: %v, want up at 0.0%%", row)
	}
	h.clean("after the member came back")
}

// TestNoMemberUpWithdrawsTheCards: when the last member up falls silent its
// cards are withdrawn, as fleet down withdraws them.
func TestNoMemberUpWithdrawsTheCards(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.setLive()
	h.tick(sprint.BeatDeadline + time.Second)
	h.machine()
	h.machine()
	s := h.snap()
	if n := len(s.Fleet.Column(sprint.Withdrawn)); n != 2 || len(s.UpMembers()) != 0 {
		t.Fatalf("withdrawn %d, up %v; want both cards withdrawn and nobody up", n, s.UpMembers())
	}
	h.clean("every member silent")
}

// TestTheHoldKeepsABeatingMemberDown: the coordinator's hold takes a member
// down whatever it beats and it stays down tick after tick; releasing the
// hold brings it up at once when its beat is fresh, and by the tick when it
// is not yet.
func TestTheHoldKeepsABeatingMemberDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1", Why: "held by tester"}))
	for i := 0; i < 3; i++ {
		h.tick(5 * time.Second)
		h.machine()
	}
	s := h.snap()
	if ctl := s.MemberCtl("m1"); ctl.F("status") != sprint.Down || ctl.F("held") == "" {
		t.Fatalf("m1 held: %v", ctl.Fields)
	}
	if d := h.dealtTo(); d["m1"] != 0 {
		t.Fatalf("a held member holds cards: %v", d)
	}
	if row := h.fleetRow("m1"); row[sprint.Status] != sprint.Held || row[sprint.Load] != "0.0%" {
		t.Fatalf("m1's row: %v, want held and its load", row)
	}
	if n := h.memberNotes(sprint.NMemberDown, "m1"); len(n) != 1 || !strings.Contains(n[0], "held by tester") {
		t.Fatalf("down notifications: %q", n)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "release", Member: "m1", Fresh: true}))
	if ctl := h.snap().MemberCtl("m1"); ctl.F("status") != sprint.Up || ctl.F("held") != "" {
		t.Fatalf("released with a fresh beat: %v, want up at once", ctl.Fields)
	}
	if d := h.dealtTo(); d["m1"] != 2 {
		t.Fatalf("after the release: dealt %v, want the queues levelled", d)
	}

	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	h.must(FleetStep(sprint.FleetReq{Op: "release", Member: "m2"}))
	if ctl := h.snap().MemberCtl("m2"); ctl.F("status") != sprint.Down || ctl.F("held") != "" {
		t.Fatalf("released without a fresh beat: %v, want down and not held", ctl.Fields)
	}
	h.tick(time.Second)
	h.machine()
	if st := h.snap().MemberCtl("m2").F("status"); st != sprint.Up {
		t.Fatalf("m2 released and beating is %s after a tick, want up", st)
	}
	h.clean("after the holds")
}

// TestAMemberThatNeverBeatIsDown: a member the coordinator adds with no beat
// is down, with no load cell, and the tick does not bring it up.
func TestAMemberThatNeverBeatIsDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.must(FleetStep(sprint.FleetReq{Op: "release", Member: "m3"}))
	h.machine()
	h.tick(time.Second)
	h.machine()
	if ctl := h.snap().MemberCtl("m3"); ctl == nil || ctl.F("status") != sprint.Down {
		t.Fatalf("m3: %+v, want a member down", ctl)
	}
	if row := h.fleetRow("m3"); row[sprint.Status] != sprint.Down || row[sprint.Load] != "" {
		t.Fatalf("m3's row: %v, want down with an empty load, not a zero", row)
	}
	if n := h.memberNotes(sprint.NMemberUp, "m3"); len(n) != 0 {
		t.Fatalf("m3 came up: %q", n)
	}
	if d := h.dealtTo(); d["m3"] != 0 {
		t.Fatalf("m3 was dealt: %v", d)
	}
}

// TestAStoppedMachineTakesBeatsAndMovesNothing: while STOPPED a member's
// beats are taken and its row shows the status they say, but nothing is
// dealt again; the first tick after start applies it.
func TestAStoppedMachineTakesBeatsAndMovesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	h.stopMachine()
	h.setLive("m2")
	h.tick(sprint.BeatDeadline + 5*time.Second)
	h.beatAt("m2", 64)
	res := h.machine()
	if res.State != Stopped || len(res.Parts) > 0 {
		t.Fatalf("a STOPPED tick: %+v", res)
	}
	if d := h.dealtTo(); d["m1"] != 2 || h.snap().MemberCtl("m1").F("status") != sprint.Up {
		t.Fatalf("while STOPPED m1's cards moved: %v", d)
	}
	if row := h.fleetRow("m1"); row[sprint.Status] != sprint.Down {
		t.Fatalf("while STOPPED m1's row: %v, want the derived status down", row)
	}
	if row := h.fleetRow("m2"); row[sprint.Status] != sprint.Up || row[sprint.Load] != "64.0%" {
		t.Fatalf("while STOPPED m2's row: %v, want up at 64.0%%", row)
	}
	h.startMachine()
	h.machine()
	if d := h.dealtTo(); d["m1"] != 0 || d["m2"] != 4 {
		t.Fatalf("the first tick after start: dealt %v, want m1's cards on m2", d)
	}
	if n := h.memberNotes(sprint.NMemberDown, "m1"); len(n) != 1 {
		t.Fatalf("down notifications: %q", n)
	}
	h.clean("after start")
}

// TestAnIdleTickShowsTheLoadAndStaysIdle: a load that changes is written by
// an idle tick, which stays idle, and so does the next.
func TestAnIdleTickShowsTheLoadAndStaysIdle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.machine()
	h.machine()
	h.beatAt("m1", 50)
	if res := h.machine(); !res.Idle {
		t.Fatalf("a new load: the tick is not idle: %+v", res)
	}
	if row := h.fleetRow("m1"); row[sprint.Load] != "50.0%" {
		t.Fatalf("m1's load: %q, want 50.0%%", row[sprint.Load])
	}
	if res := h.machine(); !res.Idle {
		t.Fatalf("the tick after the load was shown is not idle: %+v", res)
	}
}

// A beat from a machine the sprint does not know writes one happened
// notification, naming it and fleet up; teardown removes its beat record.
func TestAnUnknownMachineBeatingIsToldOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	zero := 0.0
	for i := 0; i < 3; i++ {
		if _, err := h.st.Beat(h.ctx, "m9", &zero, hostload.Source{}); err != nil {
			t.Fatal(err)
		}
		h.tick(time.Second)
		h.machine()
	}
	notes, _, _ := h.m.NotesSince(h.ctx, "", 1000)
	told := 0
	for _, n := range notes {
		if n.Type == sprint.NUnknownMachine {
			told++
			if !strings.Contains(n.What, "m9") || !strings.Contains(n.What, "nova-sprint fleet up m9") {
				t.Fatalf("the note: %+v", n)
			}
		}
	}
	if told != 1 {
		t.Fatalf("told %d times", told)
	}
	if _, err := h.st.Teardown(h.ctx); err != nil {
		t.Fatal(err)
	}
	if keys := h.m.Keys(h.st.Names); len(keys) != 0 {
		t.Fatalf("teardown left %v", keys)
	}
}

// TestMultipleSilentMembersGoDownInOneTick: when multiple fleet members fall
// silent, a single machine tick brings all of them down together in one batch
// (the design's R2) rather than taking them down one per tick.
func testMultipleSilentMembersGoDownInOneTick(t *testing.T, h *harness) {
	t.Helper()
	members := []string{"m1", "m2", "m3", "m4"}
	for _, m := range members {
		h.must(FleetStep(sprint.FleetReq{Op: "release", Member: m, Who: "tester"}))
	}
	h.setLive(members...)
	h.beat()
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 8}))
	h.startMachine()
	h.machine()
	// All 4 members are up and have cards dealt to them.
	for _, m := range members {
		if st := h.snap().MemberCtl(m).F("status"); st != sprint.Up {
			t.Fatalf("%s not up: %s", m, st)
		}
	}
	// All 4 members stop beating and fall silent past BeatDeadline.
	h.setLive()
	h.tick(sprint.BeatDeadline + time.Second)
	// ONE tick of the machine must take all 4 members down in one batch!
	h.machine()
	snap := h.snap()
	for _, m := range members {
		if st := snap.MemberCtl(m).F("status"); st != sprint.Down {
			t.Fatalf("after one tick %s is %s, want down (downs must be batched)", m, st)
		}
		if n := h.memberNotes(sprint.NMemberDown, m); len(n) != 1 {
			t.Fatalf("%s down notes: %q, want exactly 1", m, n)
		}
	}
	if len(snap.UpMembers()) != 0 {
		t.Fatalf("up members %v, want none", snap.UpMembers())
	}
	h.clean("multiple silent members down")
}

func TestMultipleSilentMembersGoDownInOneTick(t *testing.T) {
	t.Parallel()
	testMultipleSilentMembersGoDownInOneTick(t, newHarness(t))
}
