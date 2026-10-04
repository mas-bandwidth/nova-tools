package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ids a line is indexed under are the ids Line.About accepts, and no others.
func TestLogCardIDsAreTheIDsAboutAccepts(t *testing.T) {
	t.Parallel()
	note := sprint.Note{Kind: sprint.Judgment, Primaries: []string{"s1-2"}, Card: "s1-2.r1.reader-a", Other: "s1-3"}
	lines := []sprint.Line{
		{Card: "s1-1", Primary: "s1-1"},
		{Card: "s1-1.w1", Primary: "s1-1", Cards: []string{"s1-1.w1", "s1-2.w1"}},
		{Note: &note, Primary: "s1-9"},
		{Primary: "s1-4"},
	}
	probes := []string{"s1-1", "s1-1.w1", "s1-2", "s1-2.w1", "s1-2.r1", "s1-2.r1.reader-a", "s1-3", "s1-4", "s1-9", "nope"}
	for _, l := range lines {
		got := map[string]bool{}
		for _, id := range logCardIDs(l) {
			got[id] = true
			assert.True(t, l.About(id), "indexed %s is not about the line", id)
		}
		for _, id := range probes {
			if l.About(id) {
				assert.True(t, got[id], "about %s is not indexed", id)
			}
		}
	}
}

// LogAbout returns the lines About would keep, including a work card and a
// note, and does not read the epoch log to do it.
func TestLogAboutReturnsTheCardsLinesWithoutReadingTheLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.m.appendLine(sprint.Line{Kind: sprint.LineMove, Table: sprint.Fleet, Card: "s1-1.w1", Primary: "s1-1", To: "m1:ready"})
	note := sprint.Note{Kind: sprint.Judgment, Type: "needs-dropped", Primaries: []string{"s1-2"}, Card: "s1-2"}
	h.m.appendLine(sprint.Line{Kind: sprint.Judgment, Note: &note})
	all, err := h.st.Log(h.ctx)
	require.NoError(t, err)
	l := &logReads{Mem: h.m}
	st := *h.st
	st.B = l
	for _, id := range []string{"s1-1", "s1-1.w1", "s1-2", "s1-3", "no-such"} {
		l.n = 0
		got, err := st.LogAbout(h.ctx, id)
		require.NoError(t, err)
		var want []sprint.Line
		for _, line := range all {
			if line.About(id) {
				want = append(want, line)
			}
		}
		assert.Equal(t, want, got, id)
		assert.Zero(t, l.n, "log reads for %s", id)
		_, err = st.LogAbout(h.ctx, id)
		require.NoError(t, err)
		assert.Zero(t, l.n, "log reads on the second call for %s", id)
	}
}
