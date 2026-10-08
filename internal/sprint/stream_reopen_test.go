package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reopenSnap is an empty observed snapshot: the tables the reopen utilities
// read, with no cards and no clock of its own.
func reopenSnap() *Snapshot {
	s := &Snapshot{Now: time.Unix(0, 0).UTC()}
	s.Work, s.Merge, s.Fleet, s.Readers = NewTable(Work), NewTable(Merge), NewTable(Fleet), NewTable(Readers)
	return s
}

// TestSentinelID pins the stop sentinel's name: `<stream>-stop-<n>`, the ids
// the 2026-10-07 landings used and the reopen makes.
func TestSentinelID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "stream-1-stop-1", SentinelID("stream-1", 1))
	assert.Equal(t, "stream-name-stop-5", SentinelID("stream-name", 5))
	assert.Equal(t, "stream-1", SentinelName("stream-1-stop-1"))
	assert.Equal(t, "stream-name", SentinelName("stream-name-stop-5"))
	assert.Equal(t, "not-a-sentinel", SentinelName("not-a-sentinel"))
	assert.Equal(t, 1, StopNumber("stream-1-stop-1"))
	assert.Equal(t, 5, StopNumber("stream-name-stop-5"))
	assert.Equal(t, 0, StopNumber("stream-1-stop"))
	assert.Equal(t, 0, StopNumber("not-a-sentinel"))
}

// TestIsStopSentinel pins the class the reader found broken: a sentinel id
// ends in a number, so the marker is looked for and not required at the end.
func TestIsStopSentinel(t *testing.T) {
	t.Parallel()

	assert.True(t, IsStopSentinel("stream-1-stop-1", "stream-1"), "a numbered stop sentinel is a sentinel")
	assert.True(t, IsStopSentinel("stream-name-stop-5", "stream-name"))
	assert.False(t, IsStopSentinel("stream-1-stop-1", "stream-2"), "another stream's sentinel is not this one's")
	assert.False(t, IsStopSentinel("stream-1-stop", "stream-1"), "a stop with no number is no numbered sentinel")
	assert.False(t, IsStopSentinel("not-a-sentinel", "stream-1"))
}

// TestIsPushedUnreported pins the mark's parse: a sha a lander pushed and the
// report did not record.
func TestIsPushedUnreported(t *testing.T) {
	t.Parallel()

	assert.True(t, IsPushedUnreported("pushed-unreported abc123"))
	assert.False(t, IsPushedUnreported("abc123"))
	assert.False(t, IsPushedUnreported("regular-card-id"))
	assert.Equal(t, "abc123", PushedUnreportedID("pushed-unreported abc123"))
	assert.Equal(t, "abc123", PushedUnreportedID("abc123"), "a value with no mark is its own sha")
}

// TestStreamStateTextClosed pins the display of a stream whose stop landed:
// closed with a recorded stop, landed with none.
func TestStreamStateTextClosed(t *testing.T) {
	t.Parallel()

	assert.Equal(t, StreamClosed, StreamStateTextClosed(map[string]string{"state": StreamLanded, FieldStop: "1"}))
	assert.Equal(t, StreamLanded, StreamStateTextClosed(map[string]string{"state": StreamLanded}),
		"a sprint that finished every card and recorded no stop still reads landed")
	assert.Equal(t, StreamStopped, StreamStateTextClosed(map[string]string{"state": StreamStopped}))
	assert.Equal(t, StreamWaiting, StreamStateTextClosed(map[string]string{"state": StreamWaiting}))
	assert.Equal(t, StreamMerging, StreamStateTextClosed(map[string]string{"state": StreamMerging}))
}

// TestStreamLandedWithOpen pins the state the lander looped on: landed while a
// card of the stream is still open.
func TestStreamLandedWithOpen(t *testing.T) {
	t.Parallel()

	s := reopenSnap()
	s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Fields: map[string]string{"state": StreamLanded}})
	assert.False(t, StreamLandedWithOpen(s, "s1"), "no open card: nothing to show closed for")

	s.Work.SetRows([]string{"s1"})
	s.Work.Put(&Card{ID: "s1-1", Row: "s1", Col: Waiting, Score: 1, Fields: map[string]string{}})
	assert.True(t, StreamLandedWithOpen(s, "s1"), "a landed stream with an open card is closed")

	s.Merge.Put(&Card{ID: CtlID("s2"), Row: "s2", Col: Ctl, Fields: map[string]string{"state": StreamStopped}})
	assert.False(t, StreamLandedWithOpen(s, "s2"), "a stopped stream is not a landed one")
}

// TestStreamReopen pins the reopen a card's admission makes: the next numbered
// stop sentinel and the line the add prints.
func TestStreamReopen(t *testing.T) {
	t.Parallel()

	s := reopenSnap()
	s.Work.SetRows([]string{"s1"})
	s.Work.Put(&Card{ID: "s1-stop-1", Row: "s1", Col: Landed, Score: 1, Fields: map[string]string{"kind": "sentinel"}})
	s.Merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl, Fields: map[string]string{"state": StreamLanded, FieldStop: "1"}})
	id, said, err := StreamReopen(s, StreamReopenReq{Stream: "s1", Card: "s1-9", Who: "coord"})
	require.NoError(t, err)
	assert.Equal(t, "s1-stop-2", id, "the stop numbers after the highest recorded")
	assert.Equal(t, "STREAM REOPENED s1 stop=s1-stop-2", said)

	s.Merge.Put(&Card{ID: CtlID("s2"), Row: "s2", Col: Ctl, Fields: map[string]string{"state": StreamWaiting}})
	_, _, err = StreamReopen(s, StreamReopenReq{Stream: "s2"})
	assert.Error(t, err, "a stream that is not closed has nothing to reopen")
	_, _, err = StreamReopen(s, StreamReopenReq{Stream: "s3"})
	assert.Error(t, err, "a stream the table lacks is refused")

	// a stream that landed with no stop sentinel has nothing to reopen
	s.Work.SetRows(append(s.Work.Rows(), "s4"))
	s.Merge.Put(&Card{ID: CtlID("s4"), Row: "s4", Col: Ctl, Fields: map[string]string{"state": StreamLanded}})
	_, _, err = StreamReopen(s, StreamReopenReq{Stream: "s4"})
	assert.Error(t, err, "a stream that finished every card and placed no stop is not reopened")
	assert.False(t, StreamStopLanded(s, "s4"))
	assert.True(t, StreamStopLanded(s, "s1"))
}

// TestMarkPushedUnreported pins the mark a failed report leaves: the merge is
// on the base, and the next pass reads the sha from the card. A card reworked
// under the push is not marked: its new head is the next merge's.
func TestMarkPushedUnreported(t *testing.T) {
	t.Parallel()

	s := reopenSnap()
	s.Work.SetRows([]string{"s1"})
	s.Work.Put(&Card{ID: "s1-1", Row: "s1", Col: Merging, Score: 1, Fields: map[string]string{"head": "h1"}})
	s.Work.Put(&Card{ID: "s1-2", Row: "s1", Col: Merging, Score: 2, Fields: map[string]string{"head": "h2"}})
	s.Merge.Put(&Card{ID: "s1-1", Row: "s1", Col: Queued, Score: 1, Fields: map[string]string{}})
	s.Merge.Put(&Card{ID: "s1-2", Row: "s1", Col: Queued, Score: 2, Fields: map[string]string{}})
	p := MarkPushedUnreported(s, PushedUnreportedReq{Stream: "s1", Cards: []string{"s1-1", "s1-2", "s1-3"}, Heads: []string{"h1", "old", ""}, Sha: "abc123", Who: "coord"})
	require.Len(t, p.Units, 1, "the card with no merge card and the one reworked under the push are left alone")
	require.Len(t, p.Units[0].Changes, 1)
	assert.Equal(t, PushedUnreportedPrefix+"abc123", p.Units[0].Changes[0].Entry.Set[FieldPushedUnreported])
	require.Len(t, p.Units[0].Notes, 1)
	assert.Equal(t, NPushedUnreported, p.Units[0].Notes[0].Type)
	assert.Equal(t, PushedUnreportedPrefix+"abc123", p.Units[0].Notes[0].What)
	assert.True(t, IsPushedUnreported(p.Units[0].Changes[0].Entry.Set[FieldPushedUnreported]))
	assert.Equal(t, "abc123", PushedUnreportedID(p.Units[0].Changes[0].Entry.Set[FieldPushedUnreported]))
}

// TestAddReopensALandedStream pins change 1: a card admitted to a stream whose
// stop landed makes a new stop sentinel, says the line, and sets the stream
// working again.
func TestAddReopensALandedStream(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	w.s.Work.Card("s1-1").Col = Landed
	w.s.Work.Put(&Card{ID: "s1-stop-1", Row: "s1", Col: Landed, Score: 2, Fields: map[string]string{"kind": "sentinel"}})
	ctl := w.s.Merge.Card(CtlID("s1"))
	ctl.Fields["state"] = StreamLanded
	ctl.Fields[FieldStop] = "1"
	w.s.Work.cells, w.s.Work.byPrimary = nil, nil

	p := w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}}))
	assert.Contains(t, p.Said, "STREAM REOPENED s1 stop=s1-stop-2")
	ctl = w.s.Merge.Card(CtlID("s1"))
	assert.Equal(t, StreamWaiting, ctl.F("state"), "the stream works again")
	assert.Equal(t, "2", ctl.F(FieldStop), "the stream records the stop it reopened with")
	sent := w.s.Work.Card("s1-stop-2")
	require.NotNil(t, sent, "the add places the new stop sentinel")
	assert.Equal(t, "sentinel", sent.F("kind"))
	assert.Equal(t, Waiting, sent.Col, "the stop waits for the card it was made for")
	assert.Greater(t, sent.Score, w.s.Work.Card("s1-2").Score, "the stop sorts after the card it needs")
}

// TestALandedStreamWithAQueuedCardIsReportedNotRefused pins changes 2 and 3:
// the state the 2026-10-07 lander looped on is recorded, and the stream
// reopens instead of refusing with "landed".
func TestALandedStreamWithAQueuedCardIsReportedNotRefused(t *testing.T) {
	t.Parallel()

	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	w.s.Merge.Card(CtlID("s1")).Fields["state"] = StreamLanded
	p := w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Who: "coord"}))
	require.Equal(t, Landed, w.state("s1-1"), "the queued card is recorded, not refused")
	require.Equal(t, Merging, w.state("s1-2"), "the card after it is untouched")
	require.Equal(t, StreamWaiting, w.s.Merge.Card(CtlID("s1")).F("state"), "the stream reopens: %+v", p.Units)
}
