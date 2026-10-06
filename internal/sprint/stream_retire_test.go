package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// inboxOf is every stream the inbox's groups name, the coordinator's view of
// its judgments.
func inboxOf(w *world) []string {
	var out []string
	for _, g := range Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open}) {
		out = append(out, g.Stream)
	}
	return out
}

// stoppedThenEmptied is a sprint whose stream s1 stopped on a conflict (a
// stream-level merge judgment, open until the stream resumes) and then lost
// its one card: a stream stream remove takes off the tables.
func stoppedThenEmptied(t *testing.T) *world {
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1"}))
	require.Len(t, w.openOn(StreamSubject("s1")), 1, "the conflict's stream judgment: %v", w.s.Open)
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	require.Len(t, w.openOn(StreamSubject("s1")), 1, "the stream judgment outlives its card: %v", w.s.Open)
	return w
}

// A removed stream's open judgments retire with it (stream remove, stream
// archive): each closed with a decided note naming the stream and how it
// left, one Said line a stream, and the view shows none of them; the other
// streams' judgments stay.
func TestRemovingAStreamRetiresItsOpenJudgments(t *testing.T) {
	t.Parallel()
	w := stoppedThenEmptied(t)
	require.Contains(t, inboxOf(w), "s1", "the view before the remove")
	require.Empty(t, StreamRemove(w.s, false, []string{"s1"}), "stream remove refused")
	removeStream(w, "s1")
	require.True(t, StreamGone(w.s, "s1"))
	p := w.must(RetireStreams(w.s, []string{"s1"}, "removed", "coordinator"))
	require.Empty(t, w.openOn(StreamSubject("s1")), "still open: %v", w.s.Open)
	require.NotContains(t, inboxOf(w), "s1", "the view still shows the removed stream")
	require.Len(t, p.Said, 1, "said: %v", p.Said)
	require.Contains(t, p.Said[0], "stream s1 removed: 1 open note retired with it (n")
	var what []string
	for _, n := range p.Notes {
		what = append(what, n.What)
	}
	require.Equal(t, []string{NStreamRetired + ": s1 removed"}, what)
	require.Empty(t, RetireStreams(w.s, []string{"s1"}, "removed", "coordinator").Closes, "a second retire closes nothing")
	w.clean("retired")
}

// A judgment of a stream gone from the tables, raised before its stream was
// removed (a remove that retired nothing), retires on the next tick with its
// note; the tick raises no judgment for a stream the tables lack, and a
// stream that is still a row keeps its judgments.
func TestTheTickRetiresAGoneStreamsJudgments(t *testing.T) {
	t.Parallel()
	w := stoppedThenEmptied(t)
	retired := func() []Open { p, _ := TickRetire(w.s, TickReq{}); return p.Closes }
	require.Empty(t, retired(), "a stream on the tables keeps its judgments")
	removeStream(w, "s1")
	w.tick(time.Minute)
	p, due := TickRetire(w.s, TickReq{Who: MachineActor})
	require.Zero(t, due)
	require.Len(t, p.Closes, 1, "closes: %v", p.Closes)
	require.Equal(t, NStreamRetired+": s1 is gone from the tables", p.Notes[0].What)
	w.must(p)
	require.NotContains(t, inboxOf(w), "s1")
	require.Empty(t, retired(), "retired twice")

	raised := Plan{Notes: []Note{judgment(NStreamStale, "s1", w.s.Now, 0), judgment(NStreamStale, "s2", w.s.Now, 0)},
		Units: []Unit{{Key: "x", Notes: []Note{judgment(NConflict, "s1", w.s.Now, 0)}}}}
	raised.Notes = append(raised.Notes, judgment(NNoRoute, TierSubject("pro"), w.s.Now, 0))
	kept := WithoutGoneStreams(w.s, raised)
	require.Len(t, kept.Notes, 2, "notes: %+v", kept.Notes)
	require.Equal(t, "s2", kept.Notes[0].Stream)
	require.Equal(t, TierSubject("pro"), kept.Notes[1].Stream, "a tier's subject is no stream, and never gone")
	require.Empty(t, kept.Units[0].Notes, "a unit's judgment for the gone stream")
	require.Len(t, raised.Units[0].Notes, 1, "the plan given is not changed")
}
