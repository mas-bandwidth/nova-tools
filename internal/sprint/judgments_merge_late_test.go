package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The judgment of a stream with no merge step past its deadline prints the act first, land
// --stream, and never the merge report: on 2026-10-05 its printed first decision, merge
// --stream, recorded a card landed whose head was not on the base
// (no-merge-step-prints-land-and-merge-refuses-a-head-off-the-base).
func TestNoMergeStepJudgmentPrintsLand(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	ctl := w.s.StreamCtl("s1")
	require.NotNil(t, ctl)
	ctl.Fields["state"], ctl.Fields["since"] = StreamMerging, stamp(w.s.Now)
	w.tick(DeadlineMergeIdle + time.Minute)
	p, _ := TickDeadlines(w.s, TickReq{})
	var late []Note
	for _, n := range p.Notes {
		if n.Type == NMergeLate {
			late = append(late, n)
		}
	}
	require.Len(t, late, 1, "notes: %+v", p.Notes)
	for _, ds := range [][]string{late[0].Decisions, TickDecisions[NMergeLate]} {
		require.NotEmpty(t, ds)
		assert.True(t, strings.HasPrefix(ds[0], "land --stream "), "the first decision is the act: %v", ds)
		for _, d := range ds {
			assert.False(t, strings.HasPrefix(d, "merge"), "a report verb is offered: %v", ds)
		}
	}
	assert.Equal(t, "land --stream s1", late[0].Decisions[0])
	g := Group{ID: late[0].ID, Kind: Judgment, Type: NMergeLate, Stream: "s1", Size: 1, Notes: []string{late[0].ID}, Decisions: late[0].Decisions}
	var lines []string
	for _, c := range commands(g, late[0], "dev-") {
		lines = append(lines, c.Lines...)
	}
	assert.Contains(t, lines, "nova-sprint land --stream s1")
	for _, l := range lines {
		assert.NotContains(t, l, "nova-sprint merge", "a printed command fakes a landing")
	}
}
