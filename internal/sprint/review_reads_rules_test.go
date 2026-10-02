package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cold review of the late read (TickAsk's one more reader), the level's
// move-once mark and a returned read's fresh route (commits 3b847054,
// 4a7e7476, e485c4b5). Each test states a claim the commits make and fails
// where the code does not hold it.

// lateWorld is one primary in review asked of two readers of n: the first has
// read it ok, the second has begun it and holds it; the clock is past the late
// bound, so the tick may ask one more reader.
func lateWorld(t *testing.T, readers ...string) (*world, *Card, *Card) {
	t.Helper()
	w := setup(t, 1)
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), readers...))
	toReview(w, "s1-1")
	w.must(Ask(w.s, AskReq{}))
	two := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, two, 2)
	w.must(Read(w.s, ReadReq{As: two[0].Row, Verdict: "ok", Sel: Sel{IDs: []string{two[0].ID}}}))
	w.must(Read(w.s, ReadReq{As: two[1].Row, Begin: true, Sel: Sel{IDs: []string{two[1].ID}}}))
	w.tick(LateReadDefault + time.Second)
	return w, two[0], two[1]
}

// cardsAt is every read card of the primary at the attempt, placed or retired.
func cardsAt(s *Snapshot, primary string, attempt int) []*Card {
	var out []*Card
	for _, c := range s.Readers.Cards() {
		if c.F("primary") == primary && c.Int("attempt") == attempt {
			out = append(out, c)
		}
	}
	return out
}

// Claim (e485c4b5, rule a): "At most two extra readers per primary per
// attempt" (MaxAnotherReads). The bound is counted on the reads that stand
// (liveReadsAt), and a returned read does not stand: when the one more reader
// hands its read back with no verdict, the very next tick asks another, at
// once (only the hung read is timed, and it is long past the bound), and
// again, until the readers run out. The returned extra read is never asked
// again either: the primary is never on the ask's plain path once two reads
// stand, and the More path keeps a returned read where it is.
func TestAReturnedExtraReadDoesNotOpenAThirdExtraReader(t *testing.T) {
	t.Parallel()
	w, ok, hung := lateWorld(t, "reader-d", "reader-e", "reader-f")
	for i := 0; i < 4; i++ {
		p, _ := TickAsk(w.s, TickReq{})
		w.must(p)
		for _, rc := range readsAt(w.s, w.s.Work.Card("s1-1"), 1) {
			if rc.ID != ok.ID && rc.ID != hung.ID && rc.Col == Asked && !returnedRead(rc) {
				w.must(Read(w.s, ReadReq{As: rc.Row, Return: true, Reason: "no verdict (ran=false)", Sel: Sel{IDs: []string{rc.ID}}}))
			}
		}
	}
	extra := len(cardsAt(w.s, "s1-1", 1)) - 2
	assert.LessOrEqual(t, extra, MaxAnotherReads, "the tick asked %d more readers of one attempt", extra)
}

// Claim (e485c4b5, rule a): the one more reader is "the one the level would
// give a read (round.levelTo), else the next free one, so the next tick's
// level has nothing to move (errata 3 amendment 12)". The fallback breaks it:
// the reader with no load (reader-a) holds the primary's ok read, so it is not
// free; the free readers (reader-c, reader-d) are each above the mean, levelTo
// finds none, the rotation picks one, and the very next level moves the read
// to the other.
func TestTheLateReadsOneMoreReaderLeavesTheLevelNothingToMove(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-d"))
	toReview(w, "s1-1", "s1-2")
	a := putRead(w, "s1-1", 1, "reader-a", OK)
	a.Fields["asked"], a.Fields["read"] = stamp(t0), stamp(t0.Add(10*time.Second))
	b := putRead(w, "s1-1", 1, "reader-b", Reading)
	b.Fields["asked"], b.Fields["begun"] = stamp(t0), stamp(t0)
	w.tick(LateReadDefault + time.Second)
	for _, rd := range []string{"reader-c", "reader-d"} {
		c := putRead(w, "s1-2", 1, rd, Reading)
		c.Fields["asked"], c.Fields["begun"] = stamp(w.s.Now), stamp(w.s.Now)
	}
	p, _ := TickLevelReads(w.s, TickReq{})
	require.Empty(t, p.Units, "nothing to level before the ask")
	p, _ = TickAsk(w.s, TickReq{})
	require.Len(t, p.Units, 1, "one more reader of the late read: %+v", p.Refused)
	w.must(p)
	p, _ = TickLevelReads(w.s, TickReq{})
	var moved []string
	for _, u := range p.Units {
		moved = append(moved, u.Moved)
	}
	assert.Empty(t, moved, "the tick right after the ask moved the read it asked")
}

// Claim (e485c4b5, rule c): a read is levelled when "a card of its attempt
// [was] retired by level at its asked stamp". Stamps have one-second
// resolution and ticks run several times a second: a read the ask placed in
// the same second as the level moved its sibling carries the same stamp, and
// is taken for levelled though it never moved. Here reader-b holds the
// never-moved read behind two reads begun, reader-d is idle, and the level
// leaves it where it is.
func TestAReadNeverMovedIsNotTakenForLevelledByAStampItShares(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-d"))
	toReview(w, "s1-1", "s1-2", "s1-3")
	// the ask placed s1-1 on reader-a and reader-b at t0; in the same second
	// the level moved reader-a's read to reader-c
	gone := putRead(w, "s1-1", 1, "reader-a", "")
	gone.Fields["asked"], gone.Fields["retired"], gone.Fields["retired_by"] = stamp(t0), stamp(t0), RetiredByLevel
	movedTo := putRead(w, "s1-1", 1, "reader-c", Asked)
	movedTo.Fields["asked"] = stamp(t0)
	never := putRead(w, "s1-1", 1, "reader-b", Asked)
	never.Fields["asked"] = stamp(t0)
	for _, p := range []string{"s1-2", "s1-3"} {
		c := putRead(w, p, 1, "reader-b", Reading)
		c.Fields["asked"], c.Fields["begun"] = stamp(t0), stamp(t0)
	}
	w.tick(time.Second)
	require.True(t, levelled(w.s, movedTo), "the moved read is levelled")
	assert.False(t, levelled(w.s, never), "the read the ask placed was never moved")
	p, _ := TickLevelReads(w.s, TickReq{})
	w.must(p)
	assert.Nil(t, w.s.Readers.Placed(never.ID), "reader-b's read, never moved, is levelled to reader-d (loads 3 and 0)")
	assert.NotNil(t, w.s.Readers.Placed(ReadCardID("s1-1", 1, "reader-d")))
}

// Claim (e485c4b5, rule b) and the route index's form ("a unit the plan
// dropped moves it no more", roundWrites): a returned read asked again in place
// now draws a route before the ask knows the primary can be asked at all. When
// the primary is then refused (its other read taken back from a reader away,
// no reader free), its draw stays in the index the next primary draws from,
// while the index written counts only the kept units: the kept reads are
// drawn one entry on from where the written index says they were, and the
// next draw repeats the last route drawn.
func TestARefusedReturnedReadMovesNoRouteTheNextPrimaryDraws(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: "flash", Provider: "p", Model: "a", Tokens: 1000, Deadline: 600, Enabled: true},
		{Name: "flash-b", Tier: "flash", Provider: "p", Model: "b", Tokens: 1000, Deadline: 600, Enabled: true},
		{Name: "flash-c", Tier: "flash", Provider: "p", Model: "c", Tokens: 1000, Deadline: 600, Enabled: true},
	}
	toReview(w, "s1-1", "s1-2")
	// s1-1: asked of reader-a, now away; returned by reader-b; reader-c read
	// it already (retired). No reader is free for it: refused.
	away := putRead(w, "s1-1", 1, "reader-a", Asked)
	away.Fields["asked"] = stamp(t0)
	back := putRead(w, "s1-1", 1, "reader-b", Asked)
	back.Fields["asked"], back.Fields[FieldReturned], back.Fields[FieldRoute] = stamp(t0), stamp(t0), "flash-c"
	old := putRead(w, "s1-1", 1, "reader-c", "")
	old.Fields["retired"], old.Fields["retired_by"] = stamp(t0), "returned"
	w.s.ReaderStates = map[string]string{"reader-a": ReaderAway, "reader-b": ReaderUp, "reader-c": ReaderUp}
	p := Ask(w.s, AskReq{})
	require.Len(t, p.Refused, 1, "s1-1 has no reader free")
	require.Equal(t, "s1-1", p.Refused[0].Key)
	w.do(p)
	var drawn []string
	for _, rd := range []string{"reader-b", "reader-c"} {
		rc := w.s.Readers.Placed(ReadCardID("s1-2", 1, rd))
		require.NotNil(t, rc, "s1-2 asked of %s", rd)
		drawn = append(drawn, rc.F(FieldRoute))
	}
	v, _ := w.s.Fleet.Prop(PropRouteIndex("flash"))
	arr := w.s.tierArray("flash")
	n := roundCount(nil, v)
	last := arr[(n+uint64(len(arr))-1)%uint64(len(arr))]
	assert.Contains(t, drawn, last, "the index written (%s) is past a route s1-2 drew (%v)", v, drawn)
	assert.NotContains(t, drawn, arr[n%uint64(len(arr))], "the next draw (index %s) repeats a route s1-2 just drew (%v)", v, drawn)
}
