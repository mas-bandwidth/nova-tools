package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cold review of the level's move-once mark and a returned read's fresh
// route (commit e485c4b5). Each test states a claim the commit made and failed
// where the code did not hold it; the late read's one more reader (rule a)
// was taken out, and its two tests with it.

// Claim (e485c4b5, rule c): a read is levelled when "a card of its attempt
// [was] retired by level at its asked stamp". Stamps have one-second
// resolution and ticks run several times a second: a read the ask placed in
// the same second as the level moved its sibling carries the same stamp, and
// was taken for levelled though it never moved. The mark is now exact: the
// card the level asks carries leveled. Here reader-b holds the never-moved
// read behind two reads begun, reader-d is idle, and the level moves it.
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
	movedTo.Fields["asked"], movedTo.Fields[FieldLeveled] = stamp(t0), "1"
	never := putRead(w, "s1-1", 1, "reader-b", Asked)
	never.Fields["asked"] = stamp(t0)
	for _, p := range []string{"s1-2", "s1-3"} {
		c := putRead(w, p, 1, "reader-b", Reading)
		c.Fields["asked"], c.Fields["begun"] = stamp(t0), stamp(t0)
	}
	w.tick(time.Second)
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
	for _, m := range []string{"a", "b", "c"} {
		r := Route{Name: "pro-" + m, Tier: "pro", Provider: "p", Model: m, Tokens: 1000, Enabled: true}
		r.Deadline = 600 // seconds, as the route row holds it
		w.s.Routes = append(w.s.Routes, r)
	}
	for _, id := range []string{"s1-1", "s1-2"} {
		w.s.Work.Card(id).Fields[FieldTierNow] = "pro" // pro cards on pro (flash first: escalated)
	}
	toReview(w, "s1-1", "s1-2")
	// s1-1: asked of reader-a, now away; returned by reader-b; reader-c read
	// it already (retired). No reader is free for it: refused.
	away := putRead(w, "s1-1", 1, "reader-a", Asked)
	away.Fields["asked"] = stamp(t0)
	back := putRead(w, "s1-1", 1, "reader-b", Asked)
	back.Fields["asked"], back.Fields[FieldReturned], back.Fields[FieldRoute] = stamp(t0), stamp(t0), "pro-c"
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
	v, _ := w.s.Fleet.Prop(PropRouteIndex("pro"))
	arr := w.s.tierArray("pro")
	n := roundCount(nil, v)
	last := arr[(n+uint64(len(arr))-1)%uint64(len(arr))]
	assert.Contains(t, drawn, last, "the index written (%s) is past a route s1-2 drew (%v)", v, drawn)
	assert.NotContains(t, drawn, arr[n%uint64(len(arr))], "the next draw (index %s) repeats a route s1-2 just drew (%v)", v, drawn)
}
