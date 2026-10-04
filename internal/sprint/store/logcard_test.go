package store

import (
	"context"
	"errors"
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

// indexLost is a log index whose catch-up write loses.
type indexLost struct {
	*logReads
	writes int
}

func (x *indexLost) LogIndex(context.Context) error {
	x.writes++
	return errors.New("the log index was not written: the log kept changing")
}

// When the index is behind, LogAbout returns the lines About would keep, from
// the epoch log, and a lost index write is not an error and is not attempted.
func TestLogAboutReadsTheEpochLogWhenTheIndexIsBehind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.m.appendLine(sprint.Line{Kind: sprint.LineMove, Table: sprint.Fleet, Card: "s1-1.w1", Primary: "s1-1", To: "m1:ready"})
	note := sprint.Note{Kind: sprint.Judgment, Type: "needs-dropped", Primaries: []string{"s1-2"}, Card: "s1-2"}
	h.m.appendLine(sprint.Line{Kind: sprint.Judgment, Note: &note})
	h.m.DropLogIndex()
	all, err := h.st.Log(h.ctx)
	require.NoError(t, err)
	lost := &indexLost{logReads: &logReads{Mem: h.m}}
	st := *h.st
	st.B = lost
	for _, id := range []string{"s1-1", "s1-1.w1", "s1-2", "no-such"} {
		lost.n = 0
		got, err := st.LogAbout(h.ctx, id)
		require.NoError(t, err)
		var want []sprint.Line
		for _, line := range all {
			if line.About(id) {
				want = append(want, line)
			}
		}
		assert.Equal(t, want, got, id)
		assert.Positive(t, lost.n, "epoch-log reads for %s", id)
		assert.Zero(t, lost.writes, "index writes for %s", id)
	}
	_, indexed, err := h.m.LogCard(h.ctx, "s1-1")
	require.NoError(t, err)
	assert.False(t, indexed)
}

// A twin's LogCard matches the indexed store it was snapshotted from, with
// no later read writing the index back.
func TestSnapshotRestoresTheLogIndex(t *testing.T) {
	t.Parallel()
	m := NewMem()
	m.appendLine(sprint.Line{Kind: sprint.LineMove, At: t0, Card: "s1-1", Primary: "s1-1", Table: sprint.Work, To: "s1:ready"})
	want, indexed, err := m.LogCard(context.Background(), "s1-1")
	require.NoError(t, err)
	require.True(t, indexed)
	require.NotEmpty(t, want)
	doc, err := m.Snapshot()
	require.NoError(t, err)
	twin := NewMem()
	require.NoError(t, twin.Restore(doc))
	got, indexed, err := twin.LogCard(context.Background(), "s1-1")
	require.NoError(t, err)
	assert.True(t, indexed)
	assert.Equal(t, want, got)
	again, err := twin.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, doc, again)
}

// A log that was not indexed stays not indexed across a twin. Restoring it
// does not invent an index.
func TestSnapshotKeepsALogThatWasNotIndexed(t *testing.T) {
	t.Parallel()
	m := NewMem()
	m.appendLine(sprint.Line{Kind: sprint.LineMove, Card: "s1-1", Primary: "s1-1"})
	m.DropLogIndex()
	doc, err := m.Snapshot()
	require.NoError(t, err)
	twin := NewMem()
	require.NoError(t, twin.Restore(doc))
	_, indexed, err := twin.LogCard(context.Background(), "s1-1")
	require.NoError(t, err)
	assert.False(t, indexed)
	lines, _, err := twin.LogSince(context.Background(), "", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, lines)
}

// LogIndex does not write an earlier epoch.
func TestLogIndexDoesNotWriteAnOldEpoch(t *testing.T) {
	t.Parallel()
	m := NewMem()
	m.appendLine(sprint.Line{Card: "s1-1", Primary: "s1-1"})
	m.DropLogIndex()
	old := m.AtEpoch(0, true).(*Mem)
	require.NoError(t, old.LogIndex(context.Background()))
	_, indexed, err := m.LogCard(context.Background(), "s1-1")
	require.NoError(t, err)
	assert.False(t, indexed)
}
