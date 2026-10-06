package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A stream judgment written before its stream left the tables (a remove that
// retired nothing, as every remove did before it retired its stream's notes)
// is retired by the next RUNNING tick, with its decided note, and the inbox
// shows it no more; a stream still on the tables keeps its own.
func TestTheTickRetiresTheJudgmentsOfARemovedStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "gone", Who: h.st.Actor}))
	for _, st := range []string{"gone", "s1"} {
		h.must(Step{Verb: "merge", Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Judgment, Type: sprint.NRejected, Stream: st, StreamLevel: true, At: s.Now,
				Decisions: sprint.Decisions[sprint.NRejected]}}}
		}})
	}
	for _, tb := range []string{sprint.Work, sprint.Merge} {
		require.NoError(t, h.m.RowsDel(h.ctx, h.st.Names.Table(tb), []string{"gone"}))
	}
	open := func() []string {
		all, err := h.m.OpenNotes(h.ctx)
		require.NoError(t, err)
		var out []string
		for _, o := range all {
			out = append(out, o.Note.Stream)
		}
		return out
	}
	require.ElementsMatch(t, []string{"gone", "s1"}, open())
	h.startMachine()
	h.machine()
	require.Equal(t, []string{"s1"}, open(), "the tick did not retire the gone stream's judgment")
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(t, err)
	var decided []string
	for _, n := range notes {
		if n.Kind == sprint.Decided {
			decided = append(decided, n.Stream+": "+n.What)
		}
	}
	require.Equal(t, []string{"gone: " + sprint.NStreamRetired + ": gone is gone from the tables"}, decided, "the retire's decided note")
	v, err := h.st.Inbox(h.ctx, 0, 0, 100)
	require.NoError(t, err)
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment {
			require.NotEqual(t, "gone", g.Stream, "the inbox shows the gone stream's judgment: %+v", g)
		}
	}
	h.machine()
	require.Equal(t, []string{"s1"}, open())
}
