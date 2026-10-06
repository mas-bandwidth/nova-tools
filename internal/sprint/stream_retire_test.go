package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The coordinator view of 2026-10-06 00:11 held six "stream stopped" judgments
// of streams stream remove had taken off the tables: each printed next step,
// nova-sprint resume --stream <s>, was refused "no such stream", and they
// counted against the coordinator (j=19, max 8 behind). Removing a stream
// retires what names it; the tick retires what was open before, and raises
// nothing for a stream the tables lack.

// stoppedRed is a rig whose stream s2 stopped red on its one card, the card then
// dropped on a STOPPED machine: the stream holds no card, and its stopped
// judgment, stream-level, stays open until a resume that can no longer come
// once the stream is removed.
func stoppedRed(t *testing.T) *conflictRig {
	t.Helper()
	r := newConflictRig(t)
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 1, Brief: "c: the other work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.toMerging("s2-1")
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s2", Batch: 1, Red: true, Suspects: []string{"s2-1"}}))
	require.Equal(t, sprint.StreamStopped, r.snap().StreamCtl("s2").F("state"))
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	r.must(store.DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s2-1"}}, Reason: "obsolete"}))
	require.Len(t, r.openOf("s2"), 1, "the stopped judgment outlives the drop")
	require.Empty(t, sprint.StreamRemove(r.snap(), false, []string{"s2"}), "s2 holds no card")
	return r
}

// openOf is the open judgments and held conditions that name the stream.
func (r *conflictRig) openOf(stream string) []sprint.Open {
	r.t.Helper()
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(r.t, err)
	var out []sprint.Open
	for _, o := range open {
		if sprint.NamedStream(o.Note) == stream {
			out = append(out, o)
		}
	}
	return out
}

// removeRows takes the streams' rows off the work and merge tables, as stream
// remove does before it retires.
func (r *conflictRig) removeRows(streams ...string) {
	r.t.Helper()
	for _, tb := range []string{sprint.Work, sprint.Merge} {
		require.NoError(r.t, r.st.B.RowsDel(r.ctx, r.st.Names.Table(tb), streams))
	}
}

// inboxStreams is the streams of the groups the inbox, and so the coordinator
// view, shows.
func (r *conflictRig) inboxStreams() []string {
	s := r.snap()
	var out []string
	for _, g := range sprint.Inbox(sprint.InboxReq{Now: s.Now, Open: s.Open}) {
		out = append(out, g.Stream)
	}
	return out
}

// retired is the decided notes that retired a judgment of the stream.
func (r *conflictRig) retired(stream string) []sprint.Note {
	r.t.Helper()
	all, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Kind == sprint.Decided && n.Stream == stream && strings.HasPrefix(n.What, "retired: stream "+stream+" ") {
			out = append(out, n)
		}
	}
	return out
}

func TestRemovingAStreamRetiresItsOpenJudgments(t *testing.T) {
	t.Parallel()
	t.Run("the remove retires them", func(t *testing.T) {
		t.Parallel()
		r := stoppedRed(t)
		j := r.openOf("s2")[0].Note
		require.Equal(t, sprint.NRed, j.Type)
		require.Contains(t, r.inboxStreams(), "s2", "the view shows the judgment before")

		r.removeRows("s2")
		res, err := r.st.Run(r.ctx, store.Step{Verb: "stream remove", Load: []string{sprint.Work, sprint.Merge}, Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.RetireStreams(s, []string{"s2"}, "was removed (stream remove), so nothing can act on this")
		}})
		require.NoError(t, err)
		require.Len(t, res.Said, 1, "one NOTE line: %v", res.Said)
		assert.Contains(t, res.Said[0], j.ID)
		assert.Contains(t, res.Said[0], "("+sprint.NRed+") retired: stream s2 was removed")

		assert.Empty(t, r.openOf("s2"), "nothing names s2")
		assert.NotContains(t, r.inboxStreams(), "s2", "the view without it")
		require.Len(t, r.retired("s2"), 1)
		assert.Equal(t, j.ID, r.retired("s2")[0].Answers)

		// a tick after raises nothing for s2, and retires nothing more
		_, _, _, err = r.st.SetMachine(r.ctx, true)
		require.NoError(t, err)
		r.tick()
		assert.Empty(t, r.openOf("s2"))
		assert.Len(t, r.retired("s2"), 1, "retired once")
	})

	t.Run("a judgment open before the change retires on the next tick", func(t *testing.T) {
		t.Parallel()
		r := stoppedRed(t)
		j := r.openOf("s2")[0].Note
		r.removeRows("s2") // a remove of before this change: rows only
		require.Len(t, r.openOf("s2"), 1)
		_, _, _, err := r.st.SetMachine(r.ctx, true)
		require.NoError(t, err)
		r.tick()
		assert.Empty(t, r.openOf("s2"), "the tick retired it")
		assert.NotContains(t, r.inboxStreams(), "s2")
		got := r.retired("s2")
		require.Len(t, got, 1)
		assert.Equal(t, j.ID, got[0].Answers)
		assert.Contains(t, got[0].What, sprint.WhyGone, "with a note saying why")
		assert.Equal(t, sprint.MachineActor, got[0].Who)
	})

	t.Run("the tick raises no judgment for a stream the tables lack", func(t *testing.T) {
		t.Parallel()
		r := stoppedRed(t)
		r.removeRows("s2")
		s := r.snap()
		require.True(t, sprint.StreamGone(s, "s2"))
		require.False(t, sprint.StreamGone(s, "s1"))
		raised := []sprint.Note{
			{Kind: sprint.Judgment, Type: sprint.NRed, Stream: "s2", StreamLevel: true},
			{Kind: sprint.Acknowledged, Type: sprint.NOverdue, Stream: "s2"},
			{Kind: sprint.Judgment, Type: sprint.NRed, Stream: "s1", StreamLevel: true},
			{Kind: sprint.Judgment, Type: sprint.NOverloaded, Stream: sprint.MemberSubject("m1"), StreamLevel: true},
			{Kind: sprint.Happened, Type: sprint.NStreamLanded, Stream: "s2"},
		}
		p := sprint.ForTables(s, sprint.Plan{Notes: raised, Units: []sprint.Unit{{Key: "x", Notes: raised[:1]}}})
		assert.Equal(t, raised[2:], p.Notes, "only the gone stream's judgment and held condition are left out")
		assert.Empty(t, p.Units[0].Notes)
		p = sprint.ForTables(s, sprint.Plan{Notes: raised[:1], Rows: []sprint.RowAdd{{Table: sprint.Merge, Row: "s2"}}})
		assert.Len(t, p.Notes, 1, "a plan that adds the stream's row keeps it")
	})
}
