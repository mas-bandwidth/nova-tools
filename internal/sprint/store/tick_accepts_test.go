package store

// The tick accepts (2026-10-06, 8:24 PM ET: "There should be no manual step you
// need to remember to do. Just a notification."): a primary whose reads at its
// attempt all came back ok moves review -> merging queued in the tick's pump, the
// move accept --read-ok makes, the readers named in its record, RUNNING or
// STOPPED when the read closed (at the first pump after start). No "ready to
// accept" judgment opens for it; the seat is told what was accepted, once a
// tick, a notice with nothing to answer. The model is tla/DirtyTick.tla AcceptAll
// (the pump takes every okd card nothing holds; CoordAccept is for a held one).

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heldByRedCI makes the pump hold the primary (sprint.AcceptHeld): its CI red
// at its head, the ci red judgment acknowledged as a flaky runner. An
// acceptable primary held so is the one "ready to accept" is still for: the
// hold is a mind's.
func (h *harness) heldByRedCI(id string) {
	h.t.Helper()
	run := "red-at-" + h.snap().Work.Card(id).F("head") // one run a head
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{id}}, Red: true, Run: run, Who: "tester"}))
	var ids []string
	for _, o := range h.openOf(sprint.NCIRed) {
		if o.Subject() == id {
			ids = append(ids, o.Note.ID)
		}
	}
	require.Len(h.t, ids, 1, "%s: the ci red judgment", id)
	h.must(AckStep(sprint.AckReq{Notes: ids, Reason: "a flaky runner", Who: "tester"}))
}

// notesOfType is every notification of the type written so far, in order.
func (h *harness) notesOfType(typ string) []sprint.Note {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range notes {
		if n.Type == typ && n.Kind != sprint.Decided && n.Kind != sprint.Acknowledged {
			out = append(out, n)
		}
	}
	return out
}

// friendsRead sets the machine readers away and brings two friends up, so the
// tick's ask asks every primary in review of the friends (sprint.friendReadAsk).
func (h *harness) friendsRead() []string {
	h.t.Helper()
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		require.NoError(h.t, h.st.SetReaderAway(h.ctx, rd, true, "coordinator"))
	}
	friends := []string{"stella", "johnny"}
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{
		{Name: "stella", Width: 32, Class: "flash,frontier,heavy,pro"},
		{Name: "johnny", Width: 16, Class: "flash,pro"},
	})
	require.NoError(h.t, err)
	for _, f := range friends {
		h.up(f)
	}
	return friends
}

// A primary whose reads are all ok is accepted by the tick, with no judgment and
// no hand step, whoever read it (a reader on the readers table, a friend on her
// fleet row) and whether the machine was RUNNING or STOPPED when the last read
// closed: STOPPED, the first tick after start accepts it. The record names the
// readers it was accepted on, as accept --read-ok's does, and accept --read-ok
// after it finds nothing waiting. (Read cards, PR 5392, close through the
// friend's close, friendReadCloseUnit, and stand as hers do: not yet landed here.)
func TestTheTickAcceptsAPrimaryWhoseReadsAreAllOk(t *testing.T) {
	t.Parallel()
	ids := []string{"s1-1", "s1-2"}
	for _, path := range []string{"reader table", "friend"} {
		for _, stopped := range []bool{false, true} {
			name := path + " running"
			if stopped {
				name = path + " stopped"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				h := inReview(t, len(ids))
				var friends []string
				if path == "friend" {
					friends = h.friendsRead()
				}
				h.machine() // asks every primary its reads
				if stopped {
					h.stopMachine()
				}
				switch path {
				case "reader table":
					h.readAll()
				case "friend":
					s := h.snap()
					for _, id := range ids {
						got := friendReadsOf(s, id)
						require.NotEmpty(t, got, "%s asked of the friends", id)
						for _, f := range got {
							h.friendRead(f, id, "Verdict: LAND\n")
						}
					}
				}
				require.Empty(t, h.openOf(sprint.NReadyToAccept), "the last ok read opened a ready to accept judgment")
				if stopped {
					for _, id := range ids {
						require.Equal(t, sprint.Review, h.table().StateOf(id), "%s: a STOPPED machine moves nothing", id)
					}
					h.startMachine()
				}
				h.tick(time.Second)
				for _, f := range friends {
					h.up(f)
				}
				h.machine()
				s := h.table()
				for _, id := range ids {
					pr := s.Work.Card(id)
					require.Equal(t, sprint.Merging, pr.Col, "%s after the tick", id)
					assert.NotEmpty(t, pr.F("readers"), "%s: the readers it was accepted on", id)
					assert.NotEmpty(t, pr.F("accepted"), "%s: accepted, stamped", id)
					require.NotNil(t, s.Merge.Placed(id), "%s in its stream's merge queue", id)
					assert.Equal(t, sprint.Queued, s.Merge.Placed(id).Col, "%s in its stream's merge queue", id)
				}
				assert.Zero(t, h.written(sprint.NReadyToAccept), "a ready to accept judgment was written")
				assert.Empty(t, h.openOf(sprint.NInvariant), "the tick's check found the accept broke a rule")
				res := h.run(AcceptStep(sprint.AcceptReq{ReadOK: true, Who: "coordinator"}))
				assert.Empty(t, res.Moved, "accept --read-ok after the tick")
				assert.Contains(t, res.Said, sprint.NothingWaits, "accept --read-ok after the tick says nothing waits")
				h.clean("accepted by the tick")
			})
		}
	}
}

// The seat is told what the tick accepted, not asked to accept it: one notice a
// tick ("ready to merge", happened, addressed to the coordinator, no decisions)
// naming every primary accepted, across streams, with each stream's land
// command; no judgment is open on any of them.
func TestTheSeatIsToldWhatWasAcceptedNotAskedToAccept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 1}))
	h.startMachine()
	h.machine() // deals
	h.work("m1")
	h.work("m2")
	h.machine() // review, asked
	h.readAll()
	require.Empty(t, h.openOf(sprint.NReadyToAccept), "the seat is asked to accept")
	h.machine() // the pump accepts all three
	all := []string{"s1-1", "s1-2", "s2-1"}
	for _, id := range all {
		require.Equal(t, sprint.Merging, h.table().StateOf(id), "%s after the tick", id)
	}
	notes := h.notesOfType(sprint.NReadyToMerge)
	require.Len(t, notes, 1, "one notice for the tick's accepts: %+v", notes)
	n := notes[0]
	assert.Equal(t, sprint.Happened, n.Kind, "a notice, not a decision")
	assert.Empty(t, n.Decisions, "nothing to answer")
	assert.Equal(t, h.table().Coordinator, n.To, "addressed to the seat")
	got := slices.Clone(n.Primaries)
	slices.Sort(got)
	assert.Equal(t, all, got, "every primary accepted, named")
	assert.Equal(t, 3, n.Count)
	assert.Contains(t, n.What, "3 accepted and queued to merge")
	assert.Contains(t, n.What, "nova-sprint land --stream s1")
	assert.Contains(t, n.What, "nova-sprint land --stream s2")
	assert.Empty(t, n.Stream, "two streams: the notice is the tick's, not one stream's")
	for _, id := range all {
		for _, o := range h.openOn(id) {
			assert.Fail(t, "a judgment open on an accepted primary", "%s: %s", id, o.Note.Type)
		}
	}
	assert.Zero(t, h.written(sprint.NReadyToAccept), "a ready to accept judgment was written")
	h.machine()
	assert.Len(t, h.notesOfType(sprint.NReadyToMerge), 1, "a quiet tick tells the seat nothing more")
	var line string
	for _, x := range strings.Split(n.What, "; ") {
		if strings.HasPrefix(x, "run: ") {
			line = x
		}
	}
	assert.NotEmpty(t, line, "the notice names the command: %s", n.What)
	h.clean("told")
}

// The reference model and the engine agree, step by step, that the tick accepts
// a primary whose reads are all ok with no hand step: read ok on a STOPPED
// machine, no ready to accept opens in either (refmodel acceptNote, sprint's
// reviewJudgment); started, the first tick moves it to merging in both
// (refmodel tickAccept, sprint.TickAccept). A primary the pump holds (its CI red
// at its head) is still ready to accept in both, once its CI judgment is
// acknowledged: the hold is a mind's (tla/DirtyTick.tla AcceptAll, CoordAccept).
func TestTheModelAndTheEngineAgreeTheTickAcceptsWithNoHandStep(t *testing.T) {
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
		require.True(t, h.observe().Acceptable(p), "%s: its reads all ok", p)
	}
	both := func(p, state, when string) {
		t.Helper()
		require.Equal(t, state, h.observe().Primaries[p].State, "%s: %s in the engine", when, p)
		require.Equal(t, state, h.model.Primaries[p].State, "%s: %s in the model", when, p)
	}
	open := func(p string, want bool, when string) {
		t.Helper()
		j := refmodel.Judgment{Type: refmodel.JAccept, Subject: p}
		assert.Equal(t, want, h.observe().Open[j], "%s: ready to accept open on %s in the engine", when, p)
		assert.Equal(t, want, h.model.Open[j], "%s: ready to accept open on %s in the model", when, p)
	}

	do(dAction{Kind: "fleet", Op: "up", Member: "m1"})
	do(dAction{Kind: "fleet", Op: "up", Member: "m2"})
	do(dAction{Kind: "start"})
	do(dAction{Kind: "add", Stream: "s1", IDs: []string{"a1", "a2"}})
	do(dAction{Kind: "tick"})
	work("a1")
	work("a2")
	do(dAction{Kind: "tick"}) // both asked their reads together
	do(dAction{Kind: "ci", IDs: []string{"a2"}, OK: false, Run: 1})
	do(dAction{Kind: "ack", Type: refmodel.JCI, Subject: "a2"})
	do(dAction{Kind: "stop"})
	readOK("a1")
	readOK("a2")
	open("a1", false, "read ok on a STOPPED machine")
	open("a2", true, "read ok, held by its CI red")
	both("a1", refmodel.Review, "STOPPED")
	do(dAction{Kind: "start"})
	do(dAction{Kind: "tick"})
	both("a1", refmodel.Merging, "the first tick after start")
	both("a2", refmodel.Review, "held by its CI red")
	open("a1", false, "accepted by the tick")
}
