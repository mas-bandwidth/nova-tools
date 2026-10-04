package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stream remove (the owner, 2026-10-01: "you should have a verb to remove work
// streams" / "they should only succeed on a STOPPED sprint machine"): a stream
// leaves the work and merge tables only from a STOPPED machine, only when it
// is a row, and only when it holds no card; all or none for the streams named.

// streamsWorld is a world with the streams opened by add, none holding a card.
func streamsWorld(t *testing.T, streams ...string) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	for _, st := range streams {
		w.must(Add(w.s, AddReq{Stream: st, Who: "coordinator"}))
		require.True(t, w.s.Work.HasRow(st) && w.s.Merge.HasRow(st), "add opens %s", st)
		require.NotNil(t, w.s.StreamCtl(st), "add gives %s its control card", st)
	}
	return w
}

// removeStream is the write stream remove makes: the rows leave both tables,
// and the merge row takes the stream's control card off the table with it,
// its record kept, as the table layer's row delete does.
func removeStream(w *world, st string) {
	for _, tb := range []*Table{w.s.Work, w.s.Merge} {
		var rows []string
		for _, r := range tb.Rows() {
			if r != st {
				rows = append(rows, r)
			}
		}
		tb.SetRows(rows)
		for _, c := range tb.Cards() {
			if c.Row == st {
				c.Row, c.Col = "", ""
				c.Rev++
			}
		}
		tb.cells, tb.byPrimary = nil, nil
	}
}

func TestStreamRemoveTakesAnEmptyStreamOffAStoppedMachine(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a", "b", "c")
	assert.Empty(t, StreamRemove(w.s, false, []string{"a", "b", "c"}))
}

func TestStreamRemoveIsRefusedOnARunningMachine(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a", "b")
	got := StreamRemove(w.s, true, []string{"a", "b"})
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Contains(t, r.Why, "the machine is RUNNING")
		assert.Contains(t, r.Why, "run: nova-sprint stop")
	}
}

// A primary placed in any column of the stream's work row holds the stream,
// landed included.
func TestStreamRemoveIsRefusedForAStreamHoldingAPrimary(t *testing.T) {
	t.Parallel()
	for _, col := range []string{Waiting, Ready, Working, Review, Merging, Landed} {
		w := streamsWorld(t, "a")
		w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-1"}, Who: "coordinator"}))
		w.place(w.s.Work, "a-1", "a", col)
		got := StreamRemove(w.s, false, []string{"a"})
		require.Len(t, got, 1, col)
		assert.Contains(t, got[0].Why, "stream a holds 1 primary;", col)
		assert.Contains(t, got[0].Why, "nova-sprint clear --confirm sprint", col)
		assert.Contains(t, got[0].Why, "nova-sprint drop", col)
	}
}

func TestStreamRemoveIsRefusedForAStreamHoldingAMergeCard(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a")
	w.s.Merge.Put(&Card{ID: "a-1", Row: "a", Col: Queued, Rev: 1, Fields: map[string]string{"kind": "merge", PrimaryField: "a-1"}})
	got := StreamRemove(w.s, false, []string{"a"})
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Why, "stream a holds 1 merge card;")
}

func TestStreamRemoveIsRefusedForAStreamHoldingASentinel(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a")
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-stop"}, Sentinel: true, Who: "coordinator"}))
	got := StreamRemove(w.s, false, []string{"a"})
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Why, "stream a holds 1 sentinel;")
}

func TestStreamRemoveIsAllOrNone(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a", "b")
	w.must(Add(w.s, AddReq{Stream: "b", Count: 2, Who: "coordinator"}))
	got := StreamRemove(w.s, false, []string{"a", "b"})
	require.Len(t, got, 2)
	why := map[string]string{}
	for _, r := range got {
		why[r.Key] = r.Why
	}
	assert.Contains(t, why["b"], "stream b holds 2 primaries;")
	assert.Contains(t, why["a"], "removes all or none, and 1 of them was refused")
}

func TestStreamRemoveRefusesAnUnknownStreamByName(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a")
	got := StreamRemove(w.s, false, []string{"a", "zz"})
	require.Len(t, got, 2)
	assert.Equal(t, "zz", got[0].Key)
	assert.Contains(t, got[0].Why, "no stream zz on the work or merge table (streams: a)")
	assert.Equal(t, "a", got[1].Key)
}

// A stream removed in this epoch keeps its control card's record unplaced,
// and the table layer never places a removed member again: add refuses the
// name, naming the clear, until the next epoch.
func TestAStreamRemovedInThisEpochIsRefusedByAddUntilTheNextClear(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a", "b")
	require.Empty(t, StreamRemove(w.s, false, []string{"a"}))
	removeStream(w, "a")
	assert.False(t, w.s.Work.HasRow("a"))
	assert.False(t, w.s.Merge.HasRow("a"))
	assert.Nil(t, w.s.StreamCtl("a"))
	assert.True(t, RemovedStream(w.s, "a"))
	assert.False(t, RemovedStream(w.s, "b"))
	p := Add(w.s, AddReq{Stream: "a", Count: 1, Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "stream a was removed in this epoch")
	assert.Contains(t, p.Refused[0].Why, "nova-sprint clear --confirm sprint")
}
