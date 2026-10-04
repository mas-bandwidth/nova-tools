package refmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverRefused asserts err is the model's refusal of an action's guard.
func coverRefused(t *testing.T, err error) {
	t.Helper()
	var r *Refusal
	require.ErrorAs(t, err, &r)
}

// coverBadChoice asserts err is the model's refusal of a caller's choice.
func coverBadChoice(t *testing.T, err error) {
	t.Helper()
	var c *ChoiceError
	require.ErrorAs(t, err, &c)
}

// TestActionsCoverAdd covers Add (actions.go): the main path admits one card
// of a waiting stream, scores it, and resolves it to ready; an add that names
// nothing is refused.
func TestActionsCoverAdd(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SWaiting}
	n, err := Add(s, AddArgs{Stream: "s", IDs: []string{"a"}}, map[string]float64{"a": 1})
	require.NoError(t, err)
	assert.Equal(t, Ready, n.Primaries["a"].State)
	assert.Equal(t, KindPrimary, n.Primaries["a"].Kind)
	assert.Equal(t, SWaiting, n.Streams["s"].State)

	_, err = Add(s, AddArgs{}, nil)
	coverRefused(t, err)
}

// TestActionsCoverCheckScore covers checkScore (actions.go): the default
// order, the --before and --after neighbors, and a score that breaks each.
func TestActionsCoverCheckScore(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["b"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Score: 10}
	s.Primaries["c"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Score: 30}

	require.NoError(t, s.checkScore(AddArgs{Stream: "s"}, "a", 40))
	require.NoError(t, s.checkScore(AddArgs{Stream: "s", Before: "b"}, "a", 5))
	require.NoError(t, s.checkScore(AddArgs{Stream: "s", After: "c"}, "a", 40))

	coverBadChoice(t, s.checkScore(AddArgs{Stream: "s"}, "a", 5))
	coverBadChoice(t, s.checkScore(AddArgs{Stream: "s", Before: "b"}, "a", 15))
	coverBadChoice(t, s.checkScore(AddArgs{Stream: "s", After: "b"}, "a", 5))
}

// TestActionsCoverIndexOf covers indexOf (actions.go): a member's position
// and a name that is not there.
func TestActionsCoverIndexOf(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, indexOf([]string{"a", "b"}, "b"))
	assert.Equal(t, -1, indexOf([]string{"a", "b"}, "z"))
}

// TestActionsCoverPlaceSentinel covers placeSentinel (actions.go): a ready
// card behind a newly admitted sentinel goes back to waiting, and a reached
// sentinel behind it is un-reached.
func TestActionsCoverPlaceSentinel(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["x"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 1}
	s.Primaries["y"] = Primary{Stream: "s", Kind: KindPrimary, State: Ready, Score: 2}
	s.Primaries["z"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 3, Reached: true}
	s.Open[Judgment{JReached, "z"}] = true

	s.placeSentinel("x")

	assert.Equal(t, Waiting, s.Primaries["y"].State)
	assert.False(t, s.Primaries["z"].Reached)
	_, open := s.Open[Judgment{JReached, "z"}]
	assert.False(t, open)
}

// TestActionsCoverPlaceBehindSentinel covers placeBehindSentinel
// (actions.go): the first unlanded sentinel after a newly admitted card is
// un-reached.
func TestActionsCoverPlaceBehindSentinel(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["a"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Score: 1}
	s.Primaries["z"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 2, Reached: true}
	s.Open[Judgment{JReached, "z"}] = true

	s.placeBehindSentinel("a")

	assert.False(t, s.Primaries["z"].Reached)
	_, open := s.Open[Judgment{JReached, "z"}]
	assert.False(t, open)
}

// TestActionsCoverWithdrawn covers withdrawn (actions.go): a primary whose
// live work card is withdrawn, and one whose card is not.
func TestActionsCoverWithdrawn(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Working, Attempt: 1}
	s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Member: "m", Place: FWithdrawn}
	assert.True(t, s.withdrawn("p"))

	s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Member: "m", Place: FWorking}
	assert.False(t, s.withdrawn("p"))
}

// TestActionsCoverInCycle covers inCycle (actions.go): a card that needs
// itself is a cycle, a card with no needs is not.
func TestActionsCoverInCycle(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["a"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Needs: []string{"a"}}
	assert.True(t, s.inCycle("a"))

	s.Primaries["a"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting}
	assert.False(t, s.inCycle("a"))
}

// TestActionsCoverResolve covers Resolve (actions.go): a waiting primary
// whose needs are met goes ready; one that is not waiting is refused.
func TestActionsCoverResolve(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Attempt: 1}
	n, err := Resolve(s, "p")
	require.NoError(t, err)
	assert.Equal(t, Ready, n.Primaries["p"].State)

	_, err = Resolve(n, "p")
	coverRefused(t, err)
}

// TestActionsCoverWaive covers Waive (actions.go): waiving a dropped need
// closes the blocked judgment; a primary not on the table is refused.
func TestActionsCoverWaive(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Needs: []string{"q"}}
	s.Primaries["q"] = Primary{Stream: "s", Kind: KindPrimary, State: Off}
	s.Open[Judgment{JBlocked, "p"}] = true

	n, err := Waive(s, "p", []string{"q"})
	require.NoError(t, err)
	assert.Equal(t, []string{"q"}, n.Primaries["p"].Waived)
	_, open := n.Open[Judgment{JBlocked, "p"}]
	assert.False(t, open)

	_, err = Waive(s, "missing", nil)
	coverRefused(t, err)
}

// TestActionsCoverStart covers Start (actions.go): a ready primary is dealt
// to the next up member; a primary that is not ready is refused.
func TestActionsCoverStart(t *testing.T) {
	t.Parallel()
	s := New(nil, []string{"m"}, "c")
	s.Members["m"] = Up
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Ready, Attempt: 1}

	n, err := Start(s, "p", "m", 0)
	require.NoError(t, err)
	assert.Equal(t, Working, n.Primaries["p"].State)
	assert.Equal(t, FReady, n.Work[WC("p", 1)].Place)

	_, err = Start(n, "p", "m", 0)
	coverRefused(t, err)
}

// TestActionsCoverTake covers Take (actions.go): a ready work card is taken
// at its live generation; a stale generation is refused.
func TestActionsCoverTake(t *testing.T) {
	t.Parallel()
	s := New(nil, []string{"m"}, "c")
	s.Members["m"] = Up
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Working, Attempt: 1}
	s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Member: "m", Place: FReady, Gen: 1}

	n, err := Take(s, "m", WC("p", 1), 1)
	require.NoError(t, err)
	assert.Equal(t, FWorking, n.Work[WC("p", 1)].Place)

	_, err = Take(s, "m", WC("p", 1), 2)
	coverRefused(t, err)
}

// TestActionsCoverFinish covers Finish (actions.go): a live working card
// finishes and its primary moves to review; a stale one is refused.
func TestActionsCoverFinish(t *testing.T) {
	t.Parallel()
	s := New(nil, []string{"m"}, "c")
	s.Members["m"] = Up
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Working, Attempt: 1}
	s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Member: "m", Place: FWorking, Gen: 1}

	n, err := Finish(s, "m", WC("p", 1), 1, true)
	require.NoError(t, err)
	assert.Equal(t, FDone, n.Work[WC("p", 1)].Place)
	assert.Equal(t, Review, n.Primaries["p"].State)
	assert.Equal(t, 1, n.Primaries["p"].Head)

	_, err = Finish(s, "m", WC("p", 1), 2, true)
	coverRefused(t, err)
}

// TestActionsCoverAsk covers Ask (actions.go): a primary in review is asked
// of the next two readers; one not in review is refused.
func TestActionsCoverAsk(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1", "r2"}, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1}

	n, err := Ask(s, "p", []string{"r1", "r2"})
	require.NoError(t, err)
	assert.Equal(t, Asked, n.Reads[RC("p", 1, "r1")].Place)
	assert.Equal(t, Asked, n.Reads[RC("p", 1, "r2")].Place)

	_, err = Ask(n, "p", []string{"r1", "r2"})
	coverRefused(t, err)
}

// TestActionsCoverAskAnother covers AskAnother (actions.go): one more reader
// is added to a primary already asked; one not in review is refused.
func TestActionsCoverAskAnother(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1", "r2", "r3"}, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1}
	s.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: Asked}

	n, err := AskAnother(s, "p", "r2")
	require.NoError(t, err)
	assert.Equal(t, Asked, n.Reads[RC("p", 1, "r2")].Place)

	_, err = AskAnother(s, "missing", "r2")
	coverRefused(t, err)
}

// TestActionsCoverReadStart covers ReadStart (actions.go): a reader begins
// its own asked read; another reader's card is refused.
func TestActionsCoverReadStart(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1"}, nil, "c")
	s.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: Asked}

	n, err := ReadStart(s, "r1", RC("p", 1, "r1"))
	require.NoError(t, err)
	assert.Equal(t, Reading, n.Reads[RC("p", 1, "r1")].Place)

	_, err = ReadStart(s, "r2", RC("p", 1, "r1"))
	coverRefused(t, err)
}

// TestActionsCoverRead covers Read (actions.go): a reading reader records ok
// and the read that leaves none outstanding opens reads exhausted; a report
// on no card is refused.
func TestActionsCoverRead(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1", "r2"}, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Head: 1}
	s.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: Reading}

	n, err := Read(s, "r1", RC("p", 1, "r1"), true)
	require.NoError(t, err)
	assert.Equal(t, OK, n.Reads[RC("p", 1, "r1")].Place)
	assert.True(t, n.Open[Judgment{JReads, "p"}])

	_, err = Read(s, "r1", RC("p", 1, "missing"), true)
	coverRefused(t, err)
}

// TestActionsCoverExhaust covers exhaust (actions.go): an asked primary with
// no read outstanding opens reads exhausted; one never asked opens stranded
// in review.
func TestActionsCoverExhaust(t *testing.T) {
	t.Parallel()
	asked := New([]string{"r1"}, nil, "c")
	asked.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1}
	asked.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: Retired}
	asked.exhaust("p")
	assert.True(t, asked.Open[Judgment{JReads, "p"}])

	stranded := New(nil, nil, "c")
	stranded.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1}
	stranded.exhaust("p")
	assert.True(t, stranded.Open[Judgment{JStranded, "p"}])
}

// TestActionsCoverAccept covers Accept (actions.go): an acceptable primary is
// accepted and queued; an empty set is refused.
func TestActionsCoverAccept(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1", "r2"}, nil, "c")
	s.Streams["s"] = Stream{State: SWaiting}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Head: 1}
	s.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: OK}
	s.Reads[RC("p", 1, "r2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r2", Place: OK}

	n, err := Accept(s, []string{"p"})
	require.NoError(t, err)
	assert.Equal(t, Queued, n.Merge["p"].Place)
	assert.Equal(t, Merging, n.Primaries["p"].State)
	assert.Equal(t, SMerging, n.Streams["s"].State)

	_, err = Accept(s, nil)
	coverRefused(t, err)
}

// TestActionsCoverRework covers Rework (actions.go): a primary in review is
// reworked to the next up member at a new attempt; one not in review is
// refused.
func TestActionsCoverRework(t *testing.T) {
	t.Parallel()
	s := New(nil, []string{"m"}, "c")
	s.Members["m"] = Up
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1}

	n, err := Rework(s, "p", "m")
	require.NoError(t, err)
	assert.Equal(t, Working, n.Primaries["p"].State)
	assert.Equal(t, 2, n.Primaries["p"].Attempt)
	assert.Equal(t, FReady, n.Work[WC("p", 2)].Place)

	_, err = Rework(n, "p", "m")
	coverRefused(t, err)
}

// TestActionsCoverDrop covers Drop (actions.go): an open primary leaves the
// table; one already off it is refused.
func TestActionsCoverDrop(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SWaiting}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Attempt: 1}

	n, err := Drop(s, "p")
	require.NoError(t, err)
	assert.Equal(t, Off, n.Primaries["p"].State)

	_, err = Drop(n, "p")
	coverRefused(t, err)
}

// TestActionsCoverStreamAfter covers streamAfter (actions.go): a stopped
// stream stays stopped, a queued merge makes it merging, and a landed
// primary makes it landed.
func TestActionsCoverStreamAfter(t *testing.T) {
	t.Parallel()
	stopped := New(nil, nil, "c")
	stopped.Streams["s"] = Stream{State: SStopped}
	assert.Equal(t, SStopped, stopped.streamAfter("s", SStopped, nil, nil))

	merging := New(nil, nil, "c")
	merging.Primaries["q"] = Primary{Stream: "s", Kind: KindPrimary, State: Merging, Score: 1}
	merging.Merge["q"] = MergeCard{Place: Queued}
	assert.Equal(t, SMerging, merging.streamAfter("s", SWaiting, nil, nil))

	landed := New(nil, nil, "c")
	landed.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Landed, Score: 1}
	assert.Equal(t, SLanded, landed.streamAfter("s", SWaiting, nil, nil))

	empty := New(nil, nil, "c")
	assert.Equal(t, SWaiting, empty.streamAfter("s", SWaiting, nil, nil))
}

// TestActionsCoverSprintDone covers sprintDone (actions.go): every primary
// landed or off is done, one still open is not, and an empty table is not.
func TestActionsCoverSprintDone(t *testing.T) {
	t.Parallel()
	done := New(nil, nil, "c")
	done.Primaries["p"] = Primary{Stream: "s", State: Landed}
	assert.True(t, done.sprintDone())

	open := New(nil, nil, "c")
	open.Primaries["p"] = Primary{Stream: "s", State: Waiting}
	assert.False(t, open.sprintDone())

	assert.False(t, New(nil, nil, "c").sprintDone())
}

// TestActionsCoverRank covers Rank (actions.go): a primary is ranked first
// with a score below every other of its stream; a landed primary is refused.
func TestActionsCoverRank(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Score: 10}
	s.Primaries["q"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Score: 20}

	n, err := Rank(s, "p", 5)
	require.NoError(t, err)
	assert.Equal(t, 5.0, n.Primaries["p"].Score)

	_, err = Rank(s, "p", 25)
	coverBadChoice(t, err)

	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Landed, Score: 10}
	_, err = Rank(s, "p", 5)
	coverRefused(t, err)
}

// TestActionsCoverReturn covers Return (actions.go): a merging primary
// returns to review; one not merging is refused.
func TestActionsCoverReturn(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SMerging}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Merging, Attempt: 1}

	n, err := Return(s, "p")
	require.NoError(t, err)
	assert.Equal(t, Review, n.Primaries["p"].State)
	assert.Equal(t, Returned, n.Merge["p"].Place)
	assert.True(t, n.Open[Judgment{JReturned, "p"}])

	_, err = Return(s, "missing")
	coverRefused(t, err)
}

// TestActionsCoverMergeGreen covers MergeGreen (actions.go): the first queued
// card lands; a stream that is not merging is refused.
func TestActionsCoverMergeGreen(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SMerging}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Merging, Score: 1, Attempt: 1}
	s.Merge["p"] = MergeCard{Place: Queued}

	n, err := MergeGreen(s, "s", 1)
	require.NoError(t, err)
	assert.Equal(t, Landed, n.Primaries["p"].State)
	assert.Equal(t, Merged, n.Merge["p"].Place)

	s.Streams["s"] = Stream{State: SWaiting}
	_, err = MergeGreen(s, "s", 1)
	coverRefused(t, err)
}

// TestActionsCoverResolveAll covers resolveAll (actions.go): a waiting
// primary whose needs are met goes ready, and a sentinel whose needs are met
// is marked reached.
func TestActionsCoverResolveAll(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s1", Kind: KindPrimary, State: Waiting, Score: 1}
	s.Primaries["q"] = Primary{Stream: "s2", Kind: KindPrimary, State: Landed, Score: 0}
	s.Primaries["x"] = Primary{Stream: "s2", Kind: KindSentinel, State: Waiting, Needs: []string{"q"}, Score: 2}

	s.resolveAll()

	assert.Equal(t, Ready, s.Primaries["p"].State)
	assert.True(t, s.Primaries["x"].Reached)
	assert.True(t, s.Open[Judgment{JReached, "x"}])
}

// TestActionsCoverMergeRed covers MergeRed (actions.go): a red branch stops
// the stream, and the rejected flag stops it with the rejected cause.
func TestActionsCoverMergeRed(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SMerging}

	n, err := MergeRed(s, "s", false)
	require.NoError(t, err)
	assert.Equal(t, SStopped, n.Streams["s"].State)
	assert.Equal(t, CRed, n.Streams["s"].Cause)
	assert.True(t, n.Open[Judgment{JRed, StreamSubject("s")}])

	r, err := MergeRed(s, "s", true)
	require.NoError(t, err)
	assert.Equal(t, CRejected, r.Streams["s"].Cause)
	assert.True(t, r.Open[Judgment{JRejected, StreamSubject("s")}])

	s.Streams["s"] = Stream{State: SWaiting}
	_, err = MergeRed(s, "s", false)
	coverRefused(t, err)
}

// TestActionsCoverResume covers Resume (actions.go): a stopped stream's stuck
// cards go back to queued; a stream that is not stopped is refused.
func TestActionsCoverResume(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SStopped, Cause: CCross}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Merging, Score: 1}
	s.Merge["p"] = MergeCard{Place: Stuck}

	n, err := Resume(s, "s", "")
	require.NoError(t, err)
	assert.Equal(t, Queued, n.Merge["p"].Place)

	_, err = Resume(s, "missing", "")
	coverRefused(t, err)
}

// TestActionsCoverResumed covers resumed (actions.go): the stuck cards of a
// stopped stream go back to queued and the stream's judgment closes.
func TestActionsCoverResumed(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Streams["s"] = Stream{State: SStopped, Cause: CCross}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Merging, Score: 1}
	s.Merge["p"] = MergeCard{Place: Stuck}
	s.Open[Judgment{JCross, StreamSubject("s")}] = true

	n := s.resumed("s")

	assert.Equal(t, Queued, n.Merge["p"].Place)
	_, open := n.Open[Judgment{JCross, StreamSubject("s")}]
	assert.False(t, open)
}

// TestActionsCoverFleetUp covers FleetUp (actions.go): a member comes up and
// the ready queues are levelled.
func TestActionsCoverFleetUp(t *testing.T) {
	t.Parallel()
	s := New(nil, []string{"m"}, "c")
	n, err := FleetUp(s, "m", nil)
	require.NoError(t, err)
	assert.Equal(t, Up, n.Members["m"])
}

// TestActionsCoverReaderLoad covers ReaderLoad (actions.go): a reader's reads
// asked and reading, and a reader with none.
func TestActionsCoverReaderLoad(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: Asked}
	s.Reads[RC("p", 1, "r2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r2", Place: Reading}
	s.Reads[RC("p", 1, "r3")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r3", Place: Retired}

	assert.Equal(t, 1, s.ReaderLoad("r1"))
	assert.Equal(t, 1, s.ReaderLoad("r2"))
	assert.Equal(t, 0, s.ReaderLoad("r3"))
}

// TestActionsCoverLevelReads covers levelReads (actions.go): the newest asked
// read of the busiest reader moves to a reader below the mean.
func TestActionsCoverLevelReads(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1", "r2"}, nil, "c")
	s.Primaries["p1"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Score: 1}
	s.Primaries["p2"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Score: 2}
	s.Reads[RC("p1", 1, "r1")] = ReadCard{Primary: "p1", Attempt: 1, Reader: "r1", Place: Asked}
	s.Reads[RC("p2", 1, "r1")] = ReadCard{Primary: "p2", Attempt: 1, Reader: "r1", Place: Asked}

	s.levelReads()

	assert.Equal(t, Retired, s.Reads[RC("p2", 1, "r1")].Place)
	assert.Equal(t, Asked, s.Reads[RC("p2", 1, "r2")].Place)
}

// TestActionsCoverReaderHasAsked covers readerHasAsked (actions.go): a reader
// holding an asked read, and one holding only a retired read.
func TestActionsCoverReaderHasAsked(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Reads[RC("p", 1, "r1")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r1", Place: Asked}
	s.Reads[RC("p", 1, "r2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "r2", Place: Retired}

	assert.True(t, s.readerHasAsked("r1"))
	assert.False(t, s.readerHasAsked("r2"))
}

// TestActionsCoverNextReader covers nextReader (actions.go): the first of a
// set round the readers, and the empty set.
func TestActionsCoverNextReader(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1", "r2", "r3"}, nil, "c")
	assert.Equal(t, "r2", s.nextReader([]string{"r2"}))
	assert.Equal(t, "", s.nextReader(nil))
}

// TestActionsCoverCiRed covers CiRed (actions.go): a placed primary records
// red and opens the ci judgment; one not on the table is refused.
func TestActionsCoverCiRed(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Head: 1}

	n, err := CiRed(s, "p")
	require.NoError(t, err)
	assert.Equal(t, "red", n.Primaries["p"].CI)
	assert.True(t, n.Open[Judgment{JCI, "p"}])

	_, err = CiRed(s, "missing")
	coverRefused(t, err)
}

// TestActionsCoverCiGreen covers CiGreen (actions.go): green resolves an open
// red on the current head; one not on the table is refused.
func TestActionsCoverCiGreen(t *testing.T) {
	t.Parallel()
	s := New([]string{"r1"}, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Head: 1}
	s.Open[Judgment{JCI, "p"}] = true

	n, err := CiGreen(s, "p")
	require.NoError(t, err)
	assert.Equal(t, "green", n.Primaries["p"].CI)
	_, open := n.Open[Judgment{JCI, "p"}]
	assert.False(t, open)

	_, err = CiGreen(s, "missing")
	coverRefused(t, err)
}

// TestActionsCoverAck covers Ack (actions.go): a ci red is answered and the
// review's exhausted judgment written; a blocked judgment waives the named
// dropped need; a type its decisions do not list is refused.
func TestActionsCoverAck(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Head: 1}
	s.Open[Judgment{JCI, "p"}] = true

	n, err := Ack(s, JCI, []string{"p"}, nil)
	require.NoError(t, err)
	assert.True(t, n.Open[Judgment{JStranded, "p"}])

	b := New(nil, nil, "c")
	b.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Needs: []string{"q"}}
	b.Primaries["q"] = Primary{Stream: "s", Kind: KindPrimary, State: Off}
	b.Open[Judgment{JBlocked, "p"}] = true

	bn, err := Ack(b, JBlocked, []string{"p"}, []string{"q"})
	require.NoError(t, err)
	assert.Equal(t, []string{"q"}, bn.Primaries["p"].Waived)

	_, err = Ack(s, "failed", []string{"p"}, nil)
	coverRefused(t, err)
}

// TestActionsCoverCrash covers Crash (actions.go): the work table and
// judgments stay pre's and the operation is left pending.
func TestActionsCoverCrash(t *testing.T) {
	t.Parallel()
	pre := New(nil, nil, "c")
	pre.Primaries["p"] = Primary{Stream: "s", State: Landed}
	pre.Open[Judgment{JFailed, "p"}] = true

	post := New(nil, nil, "c")
	post.Primaries["q"] = Primary{Stream: "s", State: Waiting}
	post.Open[Judgment{JCI, "q"}] = true

	n := Crash(pre, post, "add")

	assert.Equal(t, pre.Primaries, n.Primaries)
	assert.Equal(t, pre.Open, n.Open)
	assert.Equal(t, "add", n.Pending)
}

// TestActionsCoverRepair covers Repair (actions.go): the pending operation
// releases.
func TestActionsCoverRepair(t *testing.T) {
	t.Parallel()
	post := New(nil, nil, "c")
	post.Pending = "add"
	post.Primaries["p"] = Primary{Stream: "s", State: Landed}

	n := Repair(post)

	assert.Equal(t, "", n.Pending)
	assert.Equal(t, Landed, n.Primaries["p"].State)
}

// TestActionsCoverDefaultScore covers DefaultScore (actions.go): a score
// after every card, and a score between the neighbours of --before and
// --after.
func TestActionsCoverDefaultScore(t *testing.T) {
	t.Parallel()
	s := New(nil, nil, "c")
	s.Primaries["b"] = Primary{Stream: "s", Kind: KindPrimary, State: Waiting, Score: 10}

	assert.Equal(t, 11.0, DefaultScore(s, AddArgs{Stream: "s"}, 0, 0))
	assert.Equal(t, 12.0, DefaultScore(s, AddArgs{Stream: "s"}, 0, 11))
	assert.Equal(t, 9.5, DefaultScore(s, AddArgs{Stream: "s", Before: "b"}, 0, 0))
	assert.Equal(t, 10.5, DefaultScore(s, AddArgs{Stream: "s", After: "b"}, 0, 0))
}