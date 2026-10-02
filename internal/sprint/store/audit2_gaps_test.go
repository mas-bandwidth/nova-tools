package store

// Second audit of the trigger rule, at e3a805e66 (the machine merged): every
// thing in every state is final, held by an outside actor with a deadline,
// moved by the tick or the causing step, or named by an open judgment.
//
// Each TestAudit2Gap* is a SEQUENCE that ends with a thing stopped and the
// inbox (read as the coordinator reads it: nova-sprint inbox's deadline and
// stale window, cursor moved past what was shown) naming nothing about it.
// Each PASSES while its gap is open and FAILS once it is fixed. Each
// TestAudit2Defect* shows a behaviour that is not a stall but breaks the spec
// or the inbox (a false alarm, a decision with no command); it too passes
// while the defect is there.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// a2Inbox is the inbox as nova-sprint inbox computes it (its default deadline
// and stale window), at the harness clock.
func (h *harness) a2Inbox() InboxView {
	h.t.Helper()
	v, err := h.st.Inbox(h.ctx, 10*time.Minute, 30*time.Minute, 100000)
	require.NoError(h.t, err)
	return v
}

// a2Named is every inbox group that lists id (its primaries or its members)
// and every open judgment on id or listing it.
func (h *harness) a2Named(id string) []string {
	h.t.Helper()
	v := h.a2Inbox()
	var out []string
	for _, g := range v.Groups {
		if contains(g.Primaries, id) || contains(g.Members, id) {
			out = append(out, fmt.Sprintf("group %s %s %q", g.ID, g.Kind, g.Type))
		}
	}
	for _, o := range v.Open {
		if o.Subject() == id || contains(o.Note.Primaries, id) {
			out = append(out, "open "+o.Note.Type)
		}
	}
	return out
}

// a2Silent fails when the inbox names id: the gap is closed.
func (h *harness) a2Silent(id, when string) {
	h.t.Helper()
	got := h.a2Named(id)
	require.Empty(h.t, got, "%s: the inbox names %s (the gap is closed?): %v", when, id, got)
}

// a2Stale is the stream-stale lines the inbox shows: the one backstop left,
// which names no card.
func (h *harness) a2Stale() []string {
	h.t.Helper()
	var out []string
	for _, g := range h.a2Inbox().Groups {
		if g.Type == sprint.NStreamStale {
			out = append(out, g.ID)
		}
	}
	return out
}

// a2Open is the open judgments of the type (acknowledged holds excluded).
func (h *harness) a2Open(typ string) []sprint.Open {
	h.t.Helper()
	var out []sprint.Open
	for _, o := range h.a2Inbox().Open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// a2Run runs the machine for d of clock time, one tick every step.
func (h *harness) a2Run(d, step time.Duration) {
	h.t.Helper()
	for t := time.Duration(0); t < d; t += step {
		h.tick(step)
		h.machine()
	}
}

// a2Ack acknowledges every open judgment of the type, as a coordinator who
// looked and found nothing to do.
func (h *harness) a2Ack(typ string) {
	h.t.Helper()
	var ids []string
	for _, o := range h.a2Open(typ) {
		if !contains(ids, o.Note.ID) {
			ids = append(ids, o.Note.ID)
		}
	}
	require.NotEmpty(h.t, ids, "no open %q to acknowledge", typ)
	h.must(AckStep(sprint.AckReq{Notes: ids, Reason: "looked", Who: "tester"}))
}

// a2AckRefused is the coordinator's ack of every open judgment of the type,
// which must be refused (ack answers only a judgment that lists it), printing
// the judgment's decisions as commands.
func (h *harness) a2AckRefused(typ string) {
	h.t.Helper()
	var ids []string
	for _, o := range h.a2Open(typ) {
		if !contains(ids, o.Note.ID) {
			ids = append(ids, o.Note.ID)
		}
	}
	require.NotEmpty(h.t, ids, "no open %q to acknowledge", typ)
	res := h.run(AckStep(sprint.AckReq{Notes: ids, Reason: "looked", Who: "tester"}))
	if len(res.Moved) != 0 || len(res.Refused) != len(ids) || !strings.Contains(res.Refused[0].Why, "ack does not answer") || !strings.Contains(res.Refused[0].Why, "nova-sprint ") {
		require.Fail(h.t, fmt.Sprintf("ack of %q: %+v", typ, res))
	}
}

// a2Names fails unless the inbox names id.
func (h *harness) a2Names(id, when string) {
	h.t.Helper()
	require.NotEmpty(h.t, h.a2Named(id), "%s: the inbox names nothing about %s", when, id)
}

// a2ToReview deals, takes and finishes the primary (ok or failed), by hand.
func (h *harness) a2ToReview(id string, failed bool) {
	h.t.Helper()
	if h.state(id) == sprint.Ready {
		h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	h.takeAndFinish(failed, id)
}

// GAP A (the ack family, sentinel). A reached sentinel's judgment offers
// release, do more, drop; ack is accepted all the same. Once acknowledged,
// nothing re-raises it: the tick marks only unreached sentinels, the ack hold
// is written only for tick judgments, and overdue marks only judgments. The
// sentinel waits reached for ever, every card behind it waits for ever, the
// sprint is never done, and no group or judgment names either card. The only
// trace is the stream-stale line (no card, no decision that closes it).
func TestAudit2ClosedAckedSentinelStopsItsStreamForEver(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "tester"))
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"after"}}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	h.readAll()
	h.landAll("s1")
	require.Equal(t, sprint.Landed, h.state("s1-1"), "s1-1 %s, reached %d", h.state("s1-1"), len(h.a2Open(sprint.NSentinelReached)))
	require.Len(t, h.a2Open(sprint.NSentinelReached), 1, "s1-1 %s, reached %d", h.state("s1-1"), len(h.a2Open(sprint.NSentinelReached)))
	h.a2AckRefused(sprint.NSentinelReached)
	h.readInbox()
	h.a2Run(10*time.Hour, 5*time.Minute)
	h.a2Names("stop", "a reached sentinel, its ack refused, ten hours")
}

// GAP A2 (the ack family, stranded). Failed work, acked; the stranded
// judgment that follows, acked: the spec keeps it from being written again,
// the tick never asks failed work, and the primary sits in review for ever.
func TestAudit2ClosedAckedStrandedPrimaryIsSilentForEver(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.a2ToReview("s1-1", true)
	h.a2AckRefused(sprint.NWorkFailed)
	h.readInbox()
	h.startMachine()
	h.a2Run(10*time.Hour, 5*time.Minute)
	h.a2Names("s1-1", "failed work, its ack refused, ten hours")
}

// GAP A3 (the ack family, ready to accept). Two ok reads, the judgment
// acknowledged: nothing writes it again; only accept moves the primary.
func TestAudit2ClosedAckedReadyToAcceptIsSilentForEver(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.a2ToReview("s1-1", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.nReadAll("s1-1", "ok")
	h.a2AckRefused(sprint.NReadyToAccept)
	h.readInbox()
	h.startMachine()
	h.a2Run(10*time.Hour, 5*time.Minute)
	h.a2Names("s1-1", "ready to accept, its ack refused, ten hours")
}

// GAP A4 (the ack family, reads exhausted). One ok, one broken; the broken
// read's judgment acked writes reads exhausted; that acked, silence.
func TestAudit2ClosedAckedReadsExhaustedIsSilentForEver(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.a2ToReview("s1-1", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	rc := h.snap().Readers.Of("s1-1")
	h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	h.must(ReadStep(sprint.ReadReq{As: rc[1].Row, Verdict: "broken", Finding: "x", Sel: sprint.Sel{IDs: []string{rc[1].ID}}}))
	h.a2AckRefused(sprint.NReadBroken)
	h.readInbox()
	h.startMachine()
	h.a2Run(10*time.Hour, 5*time.Minute)
	h.a2Names("s1-1", "a broken read, its ack refused, ten hours")
}

// GAP A5 (the ack family, tick judgments). A worker dies holding a card; the
// deadline judgment is acked: the ack hold keeps the tick from writing it
// again while the condition holds, which is for ever; the hold is no
// judgment, so it is never overdue. Same for no member up.
func TestAudit2ClosedAckedTickJudgmentHoldsForEver(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	c := h.snap().Fleet.Card("s1-1.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.a2Run(2*time.Hour+5*time.Minute, 5*time.Minute)
	h.a2AckRefused(sprint.NWorkLate)
	late := h.a2Open(sprint.NWorkLate)
	_, held, err := h.st.Wait(h.ctx, late[0].Note.ID, h.now.Add(30*time.Minute))
	require.NoError(t, err, "wait on the deadline: %v %v", held, err)
	require.True(t, held, "wait on the deadline: %v %v", held, err)
	h.readInbox()
	// the rest of the stream moves, so the stale line is masked
	for i := 0; i < 30; i++ {
		h.a2Run(20*time.Minute, 5*time.Minute)
		h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Run: fmt.Sprint("r", i), Red: i%2 == 1}))
		h.readInbox()
	}
	s := h.snap()
	require.Equal(t, sprint.Working, s.StateOf("s1-1"), "s1-1 %s", s.StateOf("s1-1"))
	require.Equal(t, sprint.Working, s.Fleet.Placed(c.ID).Col, "s1-1 %s", s.StateOf("s1-1"))
	h.a2Names("s1-1", "work card taken twelve hours ago, its deadline waited on for 30 minutes")
	n := h.written(sprint.NWorkLate)
	require.GreaterOrEqual(t, n, 2, "the deadline was not raised again after the wait: written %d", n)
}

// A6, as decided: a reminder's failure lists ack. Acked, it is not judged
// again while the route keeps failing; the failure stays on the goal record.
func TestAudit2ClosedAckedReminderFailureStaysOnTheGoal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	dir := t.TempDir()
	blocker := dir + "/file"
	require.NoError(t, writeFileA2(blocker))
	// the route's directory is a file: every delivery fails
	_, _, err := h.st.SetGoal(h.ctx, "rowan", strp("keep going"), "file:"+blocker+"/reminder")
	require.NoError(t, err)
	h.startMachine()
	h.machine()
	require.Len(t, h.a2Open(sprint.NRemindFailed), 1, "no reminder judgment")
	h.a2Ack(sprint.NRemindFailed)
	h.readInbox()
	h.a2Run(2*time.Hour, 5*time.Minute)
	g := h.goalA2("rowan")
	require.NotEmpty(t, g.Fail, "the route recovered")
	// A reminder's failure is information: ack lists it, and the failure
	// stays on the person's goal, which goal show shows, until a delivery arrives.
	n := len(h.a2Open(sprint.NRemindFailed))
	require.Equal(t, 0, n, "the acknowledged failure is judged again while the route fails: %d", n)
}

// GAP B (not ack). After a clear, a repair's skip judgment names the stored
// id (s1-1~1), not the primary: drop, rework and return of the primary never
// close it, its group's commands act on a card that does not exist, and it
// stays open after the primary is gone. At epoch 0 the ids agree.
func TestAudit2ClosedRepairSkipNamesAStoredIDAfterAClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	outside := &a2Racer{racer: &racer{Backend: h.m, at: "apply t-work"}}
	outside.do = func() {
		s := h.snap()
		p := s.Work.Card("s1-1")
		_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: fmt.Sprint(s.Epoch), ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
			OperationID: "outside", Members: []ntable.BatchMemberEntry{{ID: sprint.StoredID(p.ID, s.Epoch), Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: p.Row, Col: p.Col}}, Set: map[string]string{"brief": "outside"}}}})
		assert.NoError(t, err)
	}
	st := *h.st
	st.B = outside
	var cut *CutError
	_, err = st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	require.ErrorAs(t, err, &cut, "not cut: %v", err)
	h.tick(time.Hour)
	rr, err := h.st.Repair(h.ctx)
	require.NoError(t, err, "repair %+v %v", rr, err)
	require.Len(t, rr, 1, "repair %+v %v", rr, err)
	require.Equal(t, RepairSkipped, rr[0].Done, "repair %+v %v", rr, err)
	var subjects []string
	for _, o := range h.a2Open(sprint.NRepairSkipped) {
		subjects = append(subjects, o.Subject())
	}
	require.True(t, contains(subjects, "s1-1"), "skip judgment subjects %v", subjects)
	require.False(t, contains(subjects, "s1-1~1"), "skip judgment subjects %v", subjects)
	noStoredIDs(t, h.a2Inbox())
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "gone"}))
	n := len(h.a2Open(sprint.NRepairSkipped))
	require.Equal(t, 0, n, "drop of the primary left the skip judgment open: %d", n)
}

// noStoredIDs is the class rule: no group's members or primaries, and no
// word of a printed command but a notification id, carries an epoch suffix
// (id~n): a stored id never reaches the coordinator.
func noStoredIDs(t *testing.T, v InboxView) {
	t.Helper()
	for _, g := range v.Groups {
		for _, x := range append(append([]string(nil), g.Members...), g.Primaries...) {
			require.NotContains(t, x, "~", "group %s (%s) names the stored id %s", g.ID, g.Type, x)
		}
		ok := map[string]bool{g.ID: true}
		for _, n := range g.Notes {
			ok[n] = true
		}
		for _, c := range g.Commands {
			for _, line := range c.Lines {
				for _, w := range strings.Fields(line) {
					if !strings.Contains(w, "~") {
						continue
					}
					for _, part := range strings.Split(strings.Trim(w, "'"), ",") {
						require.True(t, ok[part], "group %s (%s) prints the stored id %s: %s", g.ID, g.Type, part, line)
					}
				}
			}
		}
	}
	for _, o := range v.Open {
		require.NotContains(t, o.Subject(), "~", "an open judgment's subject is the stored id %s", o.Subject())
	}
}

// GAP C (not ack, the machine). A RUNNING machine whose run loop died: a
// ready primary is never dealt; no group or judgment names it. The inbox's
// last line says "machine: STOPPED" and the stream is
// shown stale, naming no card; there is no judgment and no decision.
func TestAudit2ClosedDeadRunLoopIsNoJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.readInbox()
	h.tick(time.Hour)
	require.Equal(t, sprint.Ready, h.state("s1-1"), "s1-1 %s", h.state("s1-1"))
	var silent *sprint.Group
	for _, g := range h.a2Inbox().Groups {
		if g.Type == sprint.NMachineSilent {
			silent = &g
		}
	}
	require.NotNil(t, silent, "a RUNNING machine an hour without a tick: %+v", silent)
	require.NotEmpty(t, silent.Commands, "a RUNNING machine an hour without a tick: %+v", silent)
	require.Equal(t, "nova-sprint run", silent.Commands[0].Lines[0], "a RUNNING machine an hour without a tick: %+v", silent)
	// three failed ticks in a row are a group too
	h.m.Fail = func(p string) error {
		if p == "fence" {
			return errors.New("the store went away")
		}
		return nil
	}
	for i := 0; i < 3; i++ {
		h.tick(time.Second)
		_, _ = h.st.Tick(h.ctx)
	}
	h.m.Fail = nil
	failing := false
	for _, g := range h.a2Inbox().Groups {
		failing = failing || g.Type == sprint.NTickFailing && strings.Contains(g.What, "the store went away")
	}
	require.True(t, failing, "three failed ticks: no group")
}

// GAP D (not ack, clear). clear leaves the machine STOPPED; its "machine
// stopped" note is written at the old epoch. Work added at the new epoch is
// never dealt, and the new epoch's inbox is empty: no group at all, however
// long (STOPPED time does not count, so not even the stale line).
func TestAudit2ClosedClearLeavesTheMachineStoppedSilently(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	h.a2Run(6*time.Hour, 10*time.Minute)
	require.Equal(t, sprint.Ready, h.state("s1-1"), "s1-1 %s", h.state("s1-1"))
	var due, note bool
	for _, g := range h.a2Inbox().Groups {
		due = due || g.Type == sprint.NStoppedWithDue && strings.Contains(g.What, "3 moves are due") && g.Commands[0].Lines[0] == "nova-sprint start"
		note = note || g.Type == sprint.NMachineStopped
	}
	require.True(t, due, "the new epoch's inbox: moves due %v, stopped note %v: %+v", due, note, h.a2Inbox().Groups)
	require.True(t, note, "the new epoch's inbox: moves due %v, stopped note %v: %+v", due, note, h.a2Inbox().Groups)
}

// DEFECT E. A sprint set up and never started has no STOPPED span (init
// writes no machine record): its setup time counts as running time. A
// judgment written during setup is overdue in the inbox, and every stream is
// stale, before the machine ever ran; the first tick marks it overdue at once.
func TestAudit2ClosedSetupTimeCountsAsRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	h.tick(2 * time.Hour)
	overdue := false
	for _, g := range h.a2Inbox().Groups {
		overdue = overdue || g.Type == sprint.NCIRed && g.Overdue
	}
	require.False(t, overdue, "setup time counted as running: overdue %v stale %v", overdue, h.a2Stale())
	require.Empty(t, h.a2Stale(), "setup time counted as running: overdue %v stale %v", overdue, h.a2Stale())
	h.startMachine()
	h.machine()
	require.Equal(t, 0, h.written(sprint.NOverdue), "the first tick marked a setup judgment overdue")
}

// DEFECT F. wait's review time is a clock time: a judgment put off for 30
// minutes and a stop of two hours is overdue at the first tick after start,
// though the deadlines count running time.
func TestAudit2ClosedWaitCountsStoppedTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	id := h.a2Open(sprint.NCIRed)[0].Note.ID
	require.NoError(t, h.st.SetReview(h.ctx, id, h.st.now().Add(30*time.Minute)))
	h.stopMachine()
	h.tick(2 * time.Hour)
	h.startMachine()
	h.machine()
	require.Equal(t, 0, h.written(sprint.NOverdue), "overdue after 0 minutes of running time: STOPPED time counted")
	h.a2Run(31*time.Minute, time.Minute)
	notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
	lines := 0
	for _, n := range notes {
		if n.Type == sprint.NOverdue && n.Kind == sprint.Happened && strings.HasPrefix(n.What, id+" ") {
			lines++
		}
	}
	require.Equal(t, 1, lines, "the waited judgment after 31 minutes of running time: %d overdue lines", lines)
}

// DEFECT G. A stream with no open primary that has not landed (every
// primary dropped, or a new epoch's restored stream) is shown stale for
// ever: a "look" line naming no card that no verb closes (ack refuses it).
func TestAudit2ClosedEmptyStreamIsStaleForEver(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "not needed"}))
	h.startMachine()
	h.a2Run(10*time.Hour, 30*time.Minute)
	st := h.a2Stale()
	require.Empty(t, st, "an empty stream is stale: %v", st)
	c := h.snap().StreamCtl("s1")
	require.Equal(t, sprint.StreamWaiting, c.F("state"), "the empty stream: %v", c.Fields)
	require.Equal(t, "", c.F("since"), "the empty stream: %v", c.Fields)
	// a clear restores every stream empty: never stale either
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.startMachine()
	h.a2Run(2*time.Hour, 30*time.Minute)
	st = h.a2Stale()
	require.Empty(t, st, "a restored stream is stale: %v", st)
}

// DEFECT H. The reminder judgment's decisions (goal set, goal drop) have no
// commands in the inbox.
func TestAudit2ClosedReminderDecisionsHaveNoCommands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	blocker := t.TempDir() + "/file"
	require.NoError(t, writeFileA2(blocker))
	_, _, err := h.st.SetGoal(h.ctx, "rowan", strp("keep going"), "file:"+blocker+"/reminder")
	require.NoError(t, err)
	h.startMachine()
	h.machine()
	for _, g := range h.a2Inbox().Groups {
		if g.Type == sprint.NRemindFailed {
			require.Len(t, g.Commands, len(g.Decisions), "decisions %v commands %v", g.Decisions, g.Commands)
			return
		}
	}
	require.FailNow(t, "no reminder group")
}

// DEFECT I. ask --another after accept retired a slow reader's card and the
// primary was returned: the only free reader is that one, whose card id
// exists (retired), so the create is refused by the table layer on every
// plan and the verb ends "the sprint kept changing ... run it again", which
// can never succeed.
func TestAudit2ClosedAskAnotherHitsARetiredCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // readers a, b, c
	h.setup(1)
	h.a2ToReview("s1-1", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	rc := h.snap().Readers.Of("s1-1")
	h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	for _, c := range h.snap().Readers.Of("s1-1") {
		if c.Col == sprint.Asked && c.ID != rc[1].ID {
			h.must(ReadStep(sprint.ReadReq{As: c.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{c.ID}}}))
		}
	}
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "look again"}))
	res := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	require.Len(t, res.Refused, 1, "ask --another: %+v", res)
	require.Contains(t, res.Refused[0].Why, "already read attempt", "ask --another: %+v", res)
	require.Contains(t, res.Refused[0].Why, "reader add", "ask --another: %+v", res)
	require.Equal(t, 1, res.Attempts, "ask --another: %+v", res)
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-d"}))
	h.beat()
	res = h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	require.Len(t, res.Moved, 1, "ask --another with a new reader: %+v", res)
}

// DEFECT J. The tick marked "the sprint is done" overdue, which the inbox and
// Note.Due say is never overdue. Since errata 3 amendment 6 it is no judgment:
// the tick says it once, addressed to the coordinator, and stops the machine,
// so nothing is left to mark overdue or to say again.
func TestAudit2ClosedSprintDoneIsMarkedOverdue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "done"}))
	h.machine()
	require.Empty(t, h.a2Open(sprint.NSprintDone), "not done once, or done as a judgment: open %d, written %d", len(h.a2Open(sprint.NSprintDone)), h.written(sprint.NSprintDone))
	require.Equal(t, 1, h.written(sprint.NSprintDone), "not done once, or done as a judgment: open %d, written %d", len(h.a2Open(sprint.NSprintDone)), h.written(sprint.NSprintDone))
	h.a2Run(15*time.Minute, 5*time.Minute)
	require.Equal(t, 0, h.written(sprint.NOverdue), "the sprint is done marked overdue (%d lines) or said again (%d)", h.written(sprint.NOverdue), h.written(sprint.NSprintDone))
	require.Equal(t, 1, h.written(sprint.NSprintDone), "the sprint is done marked overdue (%d lines) or said again (%d)", h.written(sprint.NOverdue), h.written(sprint.NSprintDone))
}

func writeFileA2(path string) error { return os.WriteFile(path, []byte("x"), 0o644) }

func (h *harness) goalA2(name string) sprint.Goal {
	h.t.Helper()
	g, err := h.st.Goals(h.ctx)
	require.NoError(h.t, err)
	if i := g.Find(name); i >= 0 {
		return g.People[i]
	}
	return sprint.Goal{}
}

// a2Racer is racer kept in place when the store pins an epoch past 0.
type a2Racer struct{ *racer }

func (r *a2Racer) AtEpoch(epoch uint64, old bool) Backend {
	return &a2Racer{&racer{Backend: r.racer.Backend.AtEpoch(epoch, old), at: r.at, do: r.do}}
}

// The class rule over a sprint at a later epoch with a judgment of each kind
// the verbs raise: no stored id reaches a group, an open subject or a
// printed command.
func TestNoStoredIDReachesTheCoordinator(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "tester"))
	h.setup(1)
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 6}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"w"}, Needs: []string{"s1-6"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-6"}}, Reason: "gone"})) // w is blocked
	h.a2ToReview("s1-1", true)                                                               // work failed
	h.a2ToReview("s1-2", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}}))
	h.nReadAll("s1-2", "broken") // a broken read
	h.a2ToReview("s1-3", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-3"}}}))
	h.nReadAll("s1-3", "ok") // ready to accept
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-3"}}}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-3"})) // a stopped stream
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-4"}}, Red: true, Run: "r1"}))
	v := h.a2Inbox()
	types := map[string]bool{}
	for _, g := range v.Groups {
		types[g.Type] = true
	}
	for _, want := range []string{sprint.NWorkFailed, sprint.NReadBroken, sprint.NBlocked, sprint.NConflict, sprint.NCIRed} {
		require.True(t, types[want], "no %q group: %v", want, types)
	}
	noStoredIDs(t, v)
}

// The tick's ask on a returned primary whose reads were all retired by
// accept does not choose a reader who already read its attempt: it asks the
// others, or says it cannot, and never loses every attempt to the create.
func TestTheTickAsksNoReaderWhoAlreadyReadTheAttempt(t *testing.T) {
	t.Parallel()
	h := newHarness(t) // readers a, b, c
	h.setup(1)
	h.a2ToReview("s1-1", false)
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	for _, c := range h.snap().Readers.Of("s1-1") {
		h.must(ReadStep(sprint.ReadReq{As: c.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{c.ID}}}))
	}
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "look again"}))
	h.startMachine()
	for i := 0; i < 3; i++ {
		res := h.machine()
		for _, p := range res.Parts {
			require.False(t, p.Lost, "the %s part lost every attempt: %+v", p.Name, p.Result)
		}
		h.tick(time.Second)
	}
	h.clean("asked")
}
