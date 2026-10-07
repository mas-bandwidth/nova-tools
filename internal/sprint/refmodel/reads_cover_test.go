package refmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readsSprint is a sprint of three members up, each a reader, and p in review at attempt
// 1, worked by m1 and finished at its head.
func readsSprint() State {
	s := New([]string{"m1", "m2", "m3"}, []string{"m1", "m2", "m3"}, "c")
	for _, m := range s.Order {
		s.Members[m] = Up
	}
	s.Primaries["p"] = Primary{Stream: "s", Kind: KindPrimary, State: Review, Attempt: 1, Head: 1}
	s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Member: "m1", Place: FDone, Gen: 1, OK: "ok"}
	return s
}

// TestReadsCoverIdentities covers ReadIDs, nextReadID and itoa (reads.go): a reader's read
// card of an attempt has the plain identity and MaxReadGen generations, the next unused
// one first, none once every one is made.
func TestReadsCoverIdentities(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"p.r1.m2", "p.r1.m2.g1", "p.r1.m2.g2"}, ReadIDs("p", 1, "m2"))
	s := readsSprint()
	assert.Equal(t, "p.r1.m2", s.nextReadID("p", 1, "m2"))
	for _, id := range ReadIDs("p", 1, "m2") {
		s.Reads[id] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, By: ByAway}
	}
	assert.Empty(t, s.nextReadID("p", 1, "m2"), "every generation used")
	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "120", itoa(120))
}

// TestReadsCoverMayRead covers MayRead, spent and worker (reads.go): the worker never reads
// its own attempt; a reader holding a card, or one that closed or handed back its read, is
// spent; one the machine took back is not; a member down, or no reader, may not.
func TestReadsCoverMayRead(t *testing.T) {
	t.Parallel()
	s := readsSprint()
	assert.Equal(t, "m1", s.worker("p"))
	assert.False(t, s.MayRead("p", "m1"), "its worker")
	assert.True(t, s.MayRead("p", "m2"))
	s.Reads[RC("p", 1, "m2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, By: ByAway}
	assert.True(t, s.MayRead("p", "m2"), "a card the machine took back spends nothing")
	for _, c := range []ReadCard{{Place: FReady}, {Place: Retired, Verdict: OK, By: ByRead}, {Place: Retired, By: ByReturned}, {Place: Retired, By: ByLate}} {
		x := s.Clone()
		c.Primary, c.Attempt, c.Reader = "p", 1, "m3"
		x.Reads[RC("p", 1, "m3")] = c
		assert.False(t, x.MayRead("p", "m3"), "%+v spends m3", c)
	}
	down := s.Clone()
	down.Members["m3"] = Down
	assert.False(t, down.MayRead("p", "m3"), "a member down")
	assert.False(t, s.MayRead("p", "m9"), "no reader")
}

// TestReadsCoverWanted covers readsOf, standing, ReadsWanted, OutOf, OkReaders, Acceptable,
// AskedNow and ReadChoice (reads.go): a primary in review wants the reads it needs less
// those that stand, none once one is broken or its work failed; two oks at its head make it
// acceptable; the choice is the readers that may read it, in name order.
func TestReadsCoverWanted(t *testing.T) {
	t.Parallel()
	s := readsSprint()
	assert.Equal(t, 2, s.ReadsWanted("p"))
	assert.False(t, s.AskedNow("p"))
	may, n := s.ReadChoice("p")
	assert.Equal(t, []string{"m2", "m3"}, may)
	assert.Equal(t, 2, n)
	s.Reads[RC("p", 1, "m2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: FReady}
	assert.Equal(t, 1, s.ReadsWanted("p"))
	assert.Equal(t, []string{RC("p", 1, "m2")}, s.OutOf("p"))
	assert.True(t, s.AskedNow("p"))
	s.Reads[RC("p", 1, "m2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, Verdict: OK, By: ByRead}
	s.Reads[RC("p", 1, "m3")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m3", Place: Retired, Verdict: OK, By: ByRead}
	assert.Equal(t, []string{"m2", "m3"}, s.OkReaders("p"))
	assert.True(t, s.Acceptable("p"))
	assert.Zero(t, s.ReadsWanted("p"))
	broken := readsSprint()
	broken.Reads[RC("p", 1, "m2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, Verdict: Broken, By: ByRead}
	assert.Zero(t, broken.ReadsWanted("p"), "a broken read: none more")
	failed := readsSprint()
	w := failed.Work[WC("p", 1)]
	w.OK = "failed"
	failed.Work[WC("p", 1)] = w
	assert.Zero(t, failed.ReadsWanted("p"), "failed work is not read")
}

// TestReadsCoverCutReads covers cutReads, takeBack and retireReads (reads.go): the tick's
// deal takes back a card whose primary moved on, then cuts every read wanted at once, by
// the choice given or the first that may; a choice of the wrong count, or of a reader that
// may not read it, is refused; the cut closes the stranded judgment.
func TestReadsCoverCutReads(t *testing.T) {
	t.Parallel()
	s := readsSprint()
	s.Open[Judgment{JStranded, "p"}] = true
	s.Primaries["q"] = Primary{Stream: "s", Kind: KindPrimary, State: Merging, Attempt: 1, Head: 1}
	s.Reads[RC("q", 1, "m2")] = ReadCard{Primary: "q", Attempt: 1, Reader: "m2", Place: FReady}
	n := s.Clone()
	require.NoError(t, n.cutReads(nil))
	assert.Equal(t, ReadCard{Primary: "q", Attempt: 1, Reader: "m2", Place: Retired, By: ByPrimary}, n.Reads[RC("q", 1, "m2")])
	assert.Equal(t, FReady, n.Reads[RC("p", 1, "m2")].Place)
	assert.Equal(t, FReady, n.Reads[RC("p", 1, "m3")].Place)
	assert.False(t, n.Open[Judgment{JStranded, "p"}])

	x := s.Clone()
	coverBadChoice(t, x.cutReads(map[string][]string{"p": {"m2"}}))
	x = s.Clone()
	coverBadChoice(t, x.cutReads(map[string][]string{"p": {"m1", "m2"}}))

	n.retireReads("p", ByRework)
	assert.Empty(t, n.OutOf("p"))
	assert.Equal(t, ByRework, n.Reads[RC("p", 1, "m2")].By)
}

// TestReadsCoverCannotAsk covers cannotAsk and readersSpent (reads.go): a primary that wants
// reads no reader may ever give it at its attempt is a judgment; acknowledged, it is not
// opened again while it holds; it closes once a reader that may read it exists, even down.
func TestReadsCoverCannotAsk(t *testing.T) {
	t.Parallel()
	s := readsSprint()
	for _, m := range []string{"m2", "m3"} {
		s.Reads[RC("p", 1, m)] = ReadCard{Primary: "p", Attempt: 1, Reader: m, Place: Retired, By: ByReturned}
	}
	s.cannotAsk()
	assert.True(t, s.Open[Judgment{JCannotAsk, "p"}])
	delete(s.Open, Judgment{JCannotAsk, "p"})
	s.Acked[Judgment{JCannotAsk, "p"}] = true
	s.cannotAsk()
	assert.False(t, s.Open[Judgment{JCannotAsk, "p"}], "acknowledged while it holds")
	s.Members["m4"], s.Readers, s.Order = Down, append(s.Readers, "m4"), append(s.Order, "m4")
	s.cannotAsk()
	assert.Empty(t, s.Acked, "a reader that may, down: closed (it waits for it)")
}

// TestReadsCoverRead covers Read and TakeRead (reads.go): a member takes its read card and
// closes it with its verdict; a broken verdict opens the broken judgment; two oks leave the
// primary the tick's to accept; a card not on the reader's row, or of an attempt the
// primary has left, is refused.
func TestReadsCoverRead(t *testing.T) {
	t.Parallel()
	s := readsSprint()
	require.NoError(t, s.cutReads(nil))
	a, b := RC("p", 1, "m2"), RC("p", 1, "m3")
	n, err := TakeRead(s, "m2", a)
	require.NoError(t, err)
	assert.Equal(t, FWorking, n.Reads[a].Place)
	_, err = TakeRead(s, "m3", a)
	coverRefused(t, err)

	n, err = Read(n, "m2", a, true)
	require.NoError(t, err)
	assert.Equal(t, ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, Verdict: OK, By: ByRead}, n.Reads[a])
	assert.Empty(t, n.Open, "one ok of two: the other stands")
	n, err = Read(n, "m3", b, true)
	require.NoError(t, err)
	assert.True(t, n.Acceptable("p"))
	assert.Empty(t, n.Open, "acceptable: the tick accepts it")

	br, err := Read(s, "m3", b, false)
	require.NoError(t, err)
	assert.True(t, br.Open[Judgment{JBroken, "p"}])

	_, err = Read(s, "m2", b, true)
	coverRefused(t, err)
	moved := s.Clone()
	moved.setPrimary("p", func(x *Primary) { x.Attempt = 2 })
	_, err = Read(moved, "m2", a, true)
	coverRefused(t, err)
}

// TestReadsCoverJudgeReview covers judgeReview (reads.go): reads that stand exhausted (one
// broken whose judgment was acknowledged, none outstanding) are reads exhausted; failed
// work with nothing open is stranded; no read at all is stranded only when the step closed
// a judgment and no reader may yet read it; one ok of the two needed is the deal's.
func TestReadsCoverJudgeReview(t *testing.T) {
	t.Parallel()
	ex := readsSprint()
	ex.Reads[RC("p", 1, "m2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, Verdict: Broken, By: ByRead}
	ex.judgeReview("p", true)
	assert.True(t, ex.Open[Judgment{JReads, "p"}])

	failed := readsSprint()
	w := failed.Work[WC("p", 1)]
	w.OK = "failed"
	failed.Work[WC("p", 1)] = w
	failed.judgeReview("p", false)
	assert.True(t, failed.Open[Judgment{JStranded, "p"}])

	none := readsSprint()
	none.judgeReview("p", false)
	assert.Empty(t, none.Open)
	none.judgeReview("p", true)
	assert.Empty(t, none.Open, "no read yet, a reader that may read it there: the deal's")
	none.Readers = nil
	none.judgeReview("p", true)
	assert.True(t, none.Open[Judgment{JStranded, "p"}])

	one := readsSprint()
	one.Reads[RC("p", 1, "m2")] = ReadCard{Primary: "p", Attempt: 1, Reader: "m2", Place: Retired, Verdict: OK, By: ByRead}
	one.judgeReview("p", false)
	assert.Empty(t, one.Open, "the deal cuts the second")
}
