package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// stoppedJudgment writes a stream-stopped judgment on the stream, open until the
// stream resumes, as the merge step writes one (sprint.NRejected).
func (ta *testApp) stoppedJudgment(stream string) {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: sprint.MachineActor})
	require.NoError(ta.t, err)
	_, err = st.Run(context.Background(), store.Step{Verb: "merge", Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Judgment, Type: sprint.NRejected, Stream: stream, StreamLevel: true, At: s.Now,
			Decisions: sprint.Decisions[sprint.NRejected]}}}
	}})
	require.NoError(ta.t, err)
}

// judgedStreams is the view's judgment items by what they say.
func judgedStreams(v coordinatorView) []string {
	var out []string
	for _, it := range v.Items {
		if it.T == itemJudgment {
			out = append(out, it.W+" "+it.S)
		}
	}
	return out
}

// stream remove and stream archive retire the open judgments of the streams they
// take off the tables, a NOTE line a stream: the coordinator view shows none of
// them, and no next step of theirs is left to be refused "no such stream"; the
// judgments of the other streams stay.
func TestStreamRemoveAndArchiveRetireTheStreamsJudgments(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	for _, st := range []string{"a", "b", "c"} {
		ta.ok("add --stream " + st + " --count 1 --one")
	}
	ta.ok("clear --confirm sprint")
	for _, st := range []string{"a", "b", "c"} {
		ta.stoppedJudgment(st)
	}
	require.Len(t, judgedStreams(ta.coordView("")), 3, "the three stopped streams' judgments")

	out := ta.ok("stream remove a")
	assert.Contains(t, out, "STREAM-REMOVE OK streams=a")
	assert.Contains(t, out, "NOTE stream a removed: 1 open note retired with it (")
	left := judgedStreams(ta.coordView(""))
	require.Len(t, left, 2, "the removed stream's judgment is still in the view: %v", left)
	for _, l := range left {
		assert.NotContains(t, l, "stream a:", "the removed stream's judgment: %v", left)
	}

	out = ta.ok("stream archive b")
	assert.Contains(t, out, "STREAM-ARCHIVE OK streams=b")
	assert.Contains(t, out, "NOTE stream b archived: 1 open note retired with it (")
	require.Len(t, judgedStreams(ta.coordView("")), 1, "the archived stream's judgment is still in the view")

	// a stream with nothing open says nothing more
	out = ta.ok("stream remove c")
	assert.Contains(t, out, "NOTE stream c removed: 1 open note retired with it (")
	assert.Empty(t, judgedStreams(ta.coordView("")))
	ta.ok("add --stream d --count 1 --one")
	ta.ok("clear --confirm sprint")
	out = ta.ok("stream remove d")
	assert.NotContains(t, out, "NOTE")
	ta.clean()
}
