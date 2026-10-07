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

// A removal is not a tombstone (2026-10-06: the roadmap re-add of 844 cards
// was refused for 384 of them, each under a removed stream's name): adding a
// card under a stream removed in this epoch places its control card again,
// the record the removal kept, with the rows, and says the stream came back.
func TestARemovedStreamComesBackWhenACardIsAdded(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a", "b")
	require.Empty(t, StreamRemove(w.s, false, []string{"a"}))
	removeStream(w, "a")
	require.False(t, w.s.Work.HasRow("a"))
	require.False(t, w.s.Merge.HasRow("a"))
	require.Nil(t, w.s.StreamCtl("a"))
	assert.True(t, RemovedStream(w.s, "a"))
	assert.False(t, RemovedStream(w.s, "b"))
	rev := w.s.Merge.Card(CtlID("a")).Rev

	p := w.must(Add(w.s, AddReq{Stream: "a", Count: 1, Who: "coordinator"}))
	require.Len(t, p.Places, 1)
	assert.Equal(t, PlaceAgain{Table: Merge, Row: "a", Col: Ctl, ID: CtlID("a"), Said: "stream a was removed in this epoch and comes back: its control card is placed again"}, p.Places[0])
	assert.True(t, w.s.Work.HasRow("a"), "the work row is back")
	assert.True(t, w.s.Merge.HasRow("a"), "the merge row is back")
	ctl := w.s.StreamCtl("a")
	require.NotNil(t, ctl, "the stream's control card is on the table again")
	assert.Equal(t, CtlID("a"), ctl.ID, "the record the removal kept, not a new one")
	assert.Greater(t, ctl.Rev, rev)
	assert.Equal(t, StreamWaiting, ctl.F("state"))
	assert.Equal(t, Ready, w.state("a-1"), "the card is on the table")
	assert.False(t, RemovedStream(w.s, "a"))
	w.clean("a stream back")

	// once back, the stream is as any other: a second add places nothing again
	p = w.must(Add(w.s, AddReq{Stream: "a", Count: 1, Who: "coordinator"}))
	assert.Empty(t, p.Places)
	assert.Equal(t, Ready, w.state("a-2"))
}

// A removed stream whose control card had landed comes back waiting.
func TestARemovedLandedStreamComesBackWaiting(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a")
	ctl := w.s.Merge.Card(CtlID("a"))
	ctl.Fields["state"] = StreamLanded
	removeStream(w, "a")
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-1"}, Who: "coordinator"}))
	require.NotNil(t, w.s.StreamCtl("a"))
	assert.Equal(t, StreamWaiting, w.s.StreamCtl("a").F("state"))
	assert.Equal(t, Ready, w.state("a-1"))
}

// A stream removed and added again in one many-stream add comes back once.
func TestAddEachPlacesARemovedStreamAgainOnce(t *testing.T) {
	t.Parallel()
	w := streamsWorld(t, "a", "b")
	removeStream(w, "a")
	p := w.must(AddEach(w.s, []AddReq{{Stream: "a", IDs: []string{"a-1"}, Who: "coordinator"}, {Stream: "b", IDs: []string{"b-1"}, Who: "coordinator"}}))
	require.Len(t, p.Places, 1)
	assert.Equal(t, CtlID("a"), p.Places[0].ID)
	assert.Equal(t, Ready, w.state("a-1"))
	assert.Equal(t, Ready, w.state("b-1"))
}
