package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// landCol moves a placed card to col. The tests build a landed stop without a store.
func landCol(w *world, table *Table, id, col string) {
	w.t.Helper()
	c := table.Placed(id)
	require.NotNil(w.t, c, id)
	c.Col = col
	table.Put(c)
}

func TestAddAfterALandedStopReopensTheStream(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-stop-1"}, Sentinel: true, Who: "coordinator"}))
	landCol(w, w.s.Work, "s1-1", Landed)
	landCol(w, w.s.Work, "s1-stop-1", Landed)
	w.s.StreamCtl("s1").Fields["state"] = StreamLanded

	p := w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Who: "coordinator"}))
	assert.Contains(t, p.Said, ReopenLine("s1", "s1-stop-2"))
	assert.Equal(t, StreamWorking, w.s.StreamCtl("s1").F("state"))
	stop := w.s.Work.Placed("s1-stop-2")
	require.NotNil(t, stop, "the new stop is on the table")
	assert.Equal(t, "sentinel", stop.F("kind"))
	assert.Equal(t, Waiting, stop.Col)
	assert.Contains(t, Split(stop.F("needs")), "s1-2")
	assert.Greater(t, stop.Score, w.s.Work.Placed("s1-2").Score)
	var said bool
	for _, n := range w.notes {
		if n.Type == noteStreamReopened && n.What == ReopenLine("s1", "s1-stop-2") {
			said = true
		}
	}
	assert.True(t, said, "the timeline notes the reopen")

	// a later card, while this stop has not landed, does not open a second one
	p2 := w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-3"}, Who: "coordinator"}))
	for _, line := range p2.Said {
		assert.NotContains(t, line, "STREAM REOPENED")
	}
	assert.Equal(t, StreamWorking, w.s.StreamCtl("s1").F("state"))
	assert.Nil(t, w.s.Work.Card("s1-stop-3"))
}

func TestAddToALandedStreamWithoutAStopStaysWaiting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	landCol(w, w.s.Work, "s1-1", Landed)
	w.s.StreamCtl("s1").Fields["state"] = StreamLanded
	p := w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Who: "coordinator"}))
	assert.Equal(t, StreamWaiting, w.s.StreamCtl("s1").F("state"))
	for _, line := range p.Said {
		assert.NotContains(t, line, "STREAM REOPENED")
	}
	assert.Nil(t, w.s.Work.Card("s1-stop-1"))
}

func TestAddOfASentinelReopensWithThatStop(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-stop-1"}, Sentinel: true, Who: "coordinator"}))
	landCol(w, w.s.Work, "s1-1", Landed)
	landCol(w, w.s.Work, "s1-stop-1", Landed)
	w.s.StreamCtl("s1").Fields["state"] = StreamLanded
	p := w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-stop-2"}, Sentinel: true, Who: "coordinator"}))
	assert.Contains(t, p.Said, ReopenLine("s1", "s1-stop-2"))
	assert.Equal(t, StreamWorking, w.s.StreamCtl("s1").F("state"))
	assert.Nil(t, w.s.Work.Card("s1-stop-3"), "the named sentinel is the stop")
}

func TestNextStopID(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	assert.Equal(t, "s1-stop-1", NextStopID(w.s, "s1", nil))
	w.s.Work.Put(&Card{ID: "s1-stop", Row: "s1", Col: Landed, Fields: map[string]string{"kind": "sentinel"}})
	assert.Equal(t, "s1-stop-2", NextStopID(w.s, "s1", nil))
	w.s.Work.Put(&Card{ID: "s1-stop-4", Row: "s1", Col: Landed, Fields: map[string]string{"kind": "sentinel"}})
	assert.Equal(t, "s1-stop-5", NextStopID(w.s, "s1", nil))
	assert.Equal(t, "s1-stop-6", NextStopID(w.s, "s1", []string{"s1-stop-5"}))
}

func TestMergeRecordsALandingTheStreamDealt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1", "s1-2"}, Who: "coordinator"}))
	pr := w.s.Work.Placed("s1-1")
	pr.Col = Merging
	pr.Fields["head"] = "abc1234"
	pr.Fields["attempt"] = "1"
	w.s.Work.Put(pr)
	w.s.Merge.Put(&Card{ID: "s1-1", Row: "s1", Col: Queued, Score: 1})
	w.must(MarkPushedUnreported(w.s, "s1", "abc1234", []PushedPin{{ID: "s1-1", Head: "abc1234", Attempt: "1"}}))
	w.s.StreamCtl("s1").Fields["state"] = StreamLanded
	require.True(t, StreamOwesLanding(w.s, "s1"))
	require.Equal(t, []string{"s1-1"}, PushedUnreportedIDs(w.s, "s1"))
	require.Equal(t, "abc1234", PushedUnreportedSHA(w.s, "s1-1"))

	p := MergeStep(w.s, MergeReq{Stream: "s1", Who: "coordinator"})
	require.Empty(t, p.Refused, "refused: %v", p.Refused)
	var moved bool
	var state string
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table == Work && c.Entry.ID == "s1-1" && c.Entry.Move != nil && c.Entry.Move.Col == Landed {
				moved = true
			}
			if c.Entry.ID == CtlID("s1") && c.Entry.Set["state"] != "" {
				state = c.Entry.Set["state"]
			}
		}
	}
	assert.True(t, moved, "the owed card is recorded landed")
	assert.Equal(t, StreamWaiting, state, "the stream leaves landed while another card is open")
}

func TestMergeStillRefusesALandedStreamThatOwesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	w.s.StreamCtl("s1").Fields["state"] = StreamLanded
	require.False(t, StreamOwesLanding(w.s, "s1"))
	p := MergeStep(w.s, MergeReq{Stream: "s1", Who: "coordinator"})
	require.NotEmpty(t, p.Refused)
	assert.Contains(t, p.Refused[0].Why, "landed")
	assert.Empty(t, p.Units)
}

func TestPushedUnreportedMarkAndReportReason(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	pr := w.s.Work.Placed("s1-1")
	pr.Col = Merging
	pr.Fields["head"] = "head-a"
	pr.Fields["attempt"] = "1"
	w.s.Work.Put(pr)
	w.s.Merge.Put(&Card{ID: "s1-1", Row: "s1", Col: Queued, Score: 1})
	pin := []PushedPin{{ID: "s1-1", Head: "head-a", Attempt: "1"}}
	p := w.must(MarkPushedUnreported(w.s, "s1", "abc1234", pin))
	assert.Equal(t, "abc1234", w.s.Work.Placed("s1-1").F(FieldPushedUnreported))
	assert.Equal(t, "abc1234", w.s.Merge.Placed("s1-1").F(FieldPushedUnreported))
	require.NotEmpty(t, p.Units)
	require.NotEmpty(t, p.Units[0].Notes)
	assert.Equal(t, PushedUnreportedMark("abc1234"), p.Units[0].Notes[0].Type)
	assert.Empty(t, p.Units[0].Notes[0].What)
	assert.Contains(t, p.Units[0].Notes[0].Primaries, "s1-1")
	again := MarkPushedUnreported(w.s, "s1", "abc1234", pin)
	assert.Empty(t, again.Units, "the same sha is not marked twice")
	why := ReportRefusedReason("sprint/base", "abc1234", "s1: landed")
	assert.Contains(t, why, "s1: landed")
	assert.Contains(t, why, "pushed-unreported abc1234")
	assert.Contains(t, why, "sprint/base")
}

func TestPushedReceiptCannotFollowAReworkedCard(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	pr := w.s.Work.Placed("s1-1")
	pr.Col = Merging
	pr.Fields["head"], pr.Fields["attempt"] = "head-a", "1"
	w.s.Work.Put(pr)
	w.s.Merge.Put(&Card{ID: "s1-1", Row: "s1", Col: Queued, Score: 1})
	old := []PushedPin{{ID: "s1-1", Head: "head-a", Attempt: "1"}}
	w.must(MarkPushedUnreported(w.s, "s1", "merge-a", old))
	require.Equal(t, []string{"s1-1"}, PushedUnreportedIDs(w.s, "s1"))
	pr = w.s.Work.Placed("s1-1")
	pr.Fields["head"], pr.Fields["attempt"] = "head-b", "2"
	w.s.Work.Put(pr)
	assert.Empty(t, PushedUnreportedIDs(w.s, "s1"), "the old receipt must not recover the new head")
	assert.Empty(t, MarkPushedUnreported(w.s, "s1", "merge-a", old).Units, "obsolete head cannot receive a new receipt")
	w.must(ClearObsoletePushedUnreported(w.s, "s1"))
	assert.Empty(t, PushedUnreportedSHA(w.s, "s1-1"))
}

func TestShownStreamStateIsClosedWhileACardIsOpen(t *testing.T) {
	t.Parallel()
	landed := map[string]string{"state": StreamLanded}
	assert.Equal(t, StreamClosed, ShownStreamState(landed, 1))
	assert.Equal(t, StreamLanded, ShownStreamState(landed, 0))
	assert.Equal(t, Held, ShownStreamState(map[string]string{"state": StreamLanded, FieldHeld: "t"}, 2))
	assert.Equal(t, StreamWaiting, ShownStreamState(map[string]string{"state": StreamWaiting}, 3))
	assert.Equal(t, "", ShownStreamState(nil, 1))

	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Who: "coordinator"}))
	w.s.StreamCtl("s1").Fields["state"] = StreamLanded
	v := StreamsOf(w.s, StreamsReq{})
	require.Len(t, v.Streams, 1)
	assert.Equal(t, StreamClosed, v.Streams[0].State)
	assert.Greater(t, v.Streams[0].Open, 0)
	landCol(w, w.s.Work, "s1-1", Landed)
	v = StreamsOf(w.s, StreamsReq{})
	assert.Equal(t, StreamLanded, v.Streams[0].State)
	assert.Equal(t, 0, v.Streams[0].Open)
}
