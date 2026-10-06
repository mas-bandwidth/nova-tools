package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadsBackedUpRaisesOneJudgmentAtTheEdge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, column, backed, clear string }{
		{"reads", Review, "reads are backed up", "reads are clear"},
		{"merges", Merging, "merges are backed up", "merges are clear"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := setup(t, 3)
			// Invoke the registered observer only: no work pump, external store or clock.
			observe := func() Plan {
				for _, part := range TickEndWith(false, false) {
					if part.Name == "backups" {
						return w.part(part.Fn, TickReq{})
					}
				}
				return Plan{}
			}
			assert.Empty(t, observe().Notes, "empty pipeline has no initial clear edge")
			w.place(w.s.Work, "s1-1", "s1", tc.column)
			w.s.Work.Card("s1-1").Fields[FieldFinishedAt] = stamp(w.s.Now.Add(-time.Hour))
			w.s.Work.Card("s1-1").Fields["accepted"] = stamp(w.s.Now.Add(-time.Hour))
			p := observe()
			require.Len(t, p.Notes, 1, "crossing above the threshold raises one judgment")
			assert.Equal(t, tc.backed, p.Notes[0].Type)
			assert.Equal(t, Judgment, p.Notes[0].Kind)
			assert.True(t, p.Notes[0].SprintLevel)
			assert.True(t, TickEndCounts(p.Notes[0]), "the seat receives the regular tick-end push")
			assert.Contains(t, p.Notes[0].What, "oldest=s1-1")
			assert.Contains(t, p.Notes[0].What, "age=1h0m0s")
			assert.Empty(t, observe().Notes, "a stable backup never repeats")
			edgeID := w.s.Open[len(w.s.Open)-1].Note.ID
			w.must(Ack(w.s, AckReq{Who: w.s.Coordinator, Notes: []string{edgeID}, Reason: "seen"})) // acknowledgment does not reset observation
			assert.Empty(t, observe().Notes, "acknowledgment never reopens the edge")
			w.place(w.s.Work, "s1-2", "s1", Working)
			assert.Empty(t, observe().Notes, "equality preserves the previous side")
			w.place(w.s.Work, "s1-3", "s1", Working)
			p = observe()
			require.Len(t, p.Notes, 1)
			assert.Equal(t, tc.clear, p.Notes[0].Type)
			assert.Empty(t, observe().Notes)
			w.place(w.s.Work, "s1-2", "s1", Ready)
			w.place(w.s.Work, "s1-3", "s1", Ready)
			p = observe()
			require.Len(t, p.Notes, 1, "another crossing is a new judgment")
			assert.Equal(t, tc.backed, p.Notes[0].Type)
			require.Len(t, p.Closes, 1, "new backup closes its old clear judgment")
			assert.Equal(t, tc.clear, p.Closes[0].Note.Type)
		})
	}
}

func TestBackupObserversCountThePipelineAndKeepIndependentEdges(t *testing.T) {
	t.Parallel()
	w := setup(t, 5)
	w.place(w.s.Work, "s1-1", "s1", Review)
	for _, id := range []string{"s1-2", "s1-3", "s1-4"} {
		w.place(w.s.Work, id, "s1", Merging)
	}
	w.s.Work.SetHidden("s1") // add can revive an archived row before its next redraw
	assert.Equal(t, PipelineCounts{Review: 1, Merging: 3}, Pipeline(w.s))
	p := w.part(TickBackups, TickReq{})
	require.Len(t, p.Notes, 2, "both thresholds can cross at the same tick")
	assert.Equal(t, "merges", Pipeline(w.s).Backup(w.s.Work.Props()))
	assert.Contains(t, p.Notes[0].What, "working=0 review=1 merging=3")
	assert.Contains(t, p.Notes[0].What, "reading=0 width=unbounded")
	assert.Contains(t, p.Notes[0].What, "reader set <reader> --tiers")
	assert.Contains(t, p.Notes[0].What, "fleet up <member> --width")
	assert.Contains(t, p.Notes[0].What, "route set <route> --enabled true")
	assert.Contains(t, p.Notes[1].What, "lander last_landing=none stopped_streams=[]")
	assert.Contains(t, p.Notes[1].What, "resume --stream")
	assert.Contains(t, p.Notes[1].What, "age=unknown")
	assert.Empty(t, w.part(TickBackups, TickReq{}).Notes)
	// A fresh snapshot object retains the persisted properties across restart.
	restarted := *w.s
	restarted.Work = w.s.Work.Frozen()
	p, _ = TickBackups(&restarted, TickReq{})
	assert.True(t, p.Empty())
}

func TestPipelineBackupReportsThresholdsAndEquality(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		counts PipelineCounts
		props  map[string]string
		want   string
	}{
		{"empty", PipelineCounts{}, nil, "none"},
		{"reads", PipelineCounts{Working: 1, Review: 2}, nil, "reads"},
		{"merges", PipelineCounts{Working: 1, Review: 1, Merging: 3}, nil, "merges"},
		{"merge priority", PipelineCounts{Review: 2, Merging: 3}, nil, "merges"},
		{"reads equality retains backup", PipelineCounts{Working: 1, Review: 1}, map[string]string{propBackupReads: "true"}, "reads"},
		{"merge equality retains backup", PipelineCounts{Working: 1, Review: 1, Merging: 2}, map[string]string{propBackupMerges: "true"}, "merges"},
		{"clear below", PipelineCounts{Working: 2, Review: 1, Merging: 1}, map[string]string{propBackupReads: "true", propBackupMerges: "true"}, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, tc.counts.Backup(tc.props)) })
	}
}

func TestBackupJudgmentsCarryOldestAgeAndActualCapacity(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "a", Width: 4}))
	for _, id := range []string{"s1-1", "s1-2"} {
		w.place(w.s.Work, id, "s1", Review)
	}
	w.s.Work.Card("s1-1").Fields[FieldFinishedAt] = stamp(w.s.Now.Add(-time.Hour))
	w.s.Work.Card("s1-2").Fields[FieldFinishedAt] = stamp(w.s.Now.Add(-2 * time.Hour))
	putRead(w, "s1-1", 1, "reader-a", Reading)
	p := w.part(TickBackups, TickReq{})
	require.Len(t, p.Notes, 1)
	assert.Contains(t, p.Notes[0].What, "oldest=s1-2 age=2h0m0s")
	assert.Contains(t, p.Notes[0].What, "reader-a reading=1 width=4")
	for _, id := range []string{"s1-1", "s1-2"} {
		w.place(w.s.Work, id, "s1", Merging)
		w.s.Work.Card(id).Fields["accepted"] = stamp(w.s.Now.Add(-time.Hour))
	}
	w.place(w.s.Work, "s1-3", "s1", Landed)
	w.s.Work.Card("s1-3").Fields["landed"] = stamp(w.s.Now.Add(-30 * time.Minute))
	w.s.StreamCtl("s1").Fields["state"] = StreamStopped
	p = w.part(TickBackups, TickReq{})
	require.Len(t, p.Notes, 1)
	assert.Contains(t, p.Notes[0].What, "oldest=s1-1 age=1h0m0s")
	assert.Contains(t, p.Notes[0].What, "last_landing="+stamp(w.s.Now.Add(-30*time.Minute)))
	assert.Contains(t, p.Notes[0].What, "stopped_streams=[s1]")
}
