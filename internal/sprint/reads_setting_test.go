package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint's read count (nova-sprint set --reads; the owner, 2026-10-06: "I'd like to
// waive the second read for the moment"): while it is set, every card in review needs that
// many ok reads whatever its tier (ReadsNeededIn); a card past review is held to the count
// it was accepted on.

// A heavy card with one ok read of its two waits in review on the tier's rule, and is
// accepted on the next tick once the sprint needs one read; past review it keeps the count
// it was accepted on when the setting goes back to default.
func TestAHeavyCardWithOneOkReadIsAcceptedWhenReadsIsOne(t *testing.T) {
	t.Parallel()
	w := tierWorld(t)
	w.s.Work.Card("s1-2").Fields[FieldTierNow] = "heavy"
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	reads := liveReadsAt(w.s, w.s.Work.Card("s1-2"), 1)
	require.Len(t, reads, 2, "default: a heavy card is asked of two readers")
	w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: reads[0].Row, Verdict: "ok", Sel: Sel{IDs: []string{reads[0].ID}}}))

	p, _ := TickAccept(w.s, TickReq{})
	assert.Empty(t, p.Units, "default: one ok read of two accepts nothing")
	assert.Equal(t, Review, w.s.Work.Card("s1-2").Col)

	w.s.Work.SetProp(PropReadsNeeded, "1")
	assert.Equal(t, 1, ReadsNeededIn(w.s, w.s.Work.Card("s1-2")))
	p, _ = TickAccept(w.s, TickReq{})
	require.Len(t, p.Units, 1, "reads 1: the heavy card is accepted on its one ok read")
	w.must(p)
	pr := w.s.Work.Card("s1-2")
	assert.Equal(t, Merging, pr.Col)
	assert.Equal(t, "1", pr.F(FieldReadsNeeded), "the count it was accepted on")
	w.clean("accepted on the sprint's one read")

	w.s.Work.SetProp(PropReadsNeeded, ReadTierDefault)
	assert.Equal(t, 1, ReadsNeededIn(w.s, pr), "past review: never judged again")
	w.clean("the setting back to default does not re-judge a card in merging")
}

// reads 0: a primary whose work finished LAND is accepted on the next tick with no read
// asked and no read card cut; reads 1: it waits for one.
func TestAFinishedPrimaryIsAcceptedWithNoReadWhenReadsIsZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		reads    string
		accepted bool
	}{{"0", true}, {"1", false}} {
		t.Run("reads "+tc.reads, func(t *testing.T) {
			t.Parallel()
			w := tierWorld(t)
			w.s.Work.SetProp(PropReadsNeeded, tc.reads)
			pr := w.s.Work.Card("s1-2")
			wantReads := map[bool]int{true: 0, false: 1}[tc.accepted]
			assert.Equal(t, wantReads, ReadsWanted(w.s, pr), "reads asked by the readers table")
			assert.Equal(t, wantReads, readCardsWanted(w.s, pr, nil), "read cards cut")
			p, _ := TickAccept(w.s, TickReq{})
			if !tc.accepted {
				assert.Empty(t, p.Units, "reads 1: it waits for one read")
				assert.Equal(t, Review, w.s.Work.Card("s1-2").Col)
				return
			}
			require.Len(t, p.Units, 2, "reads 0: both finished primaries are accepted")
			w.must(p)
			pr = w.s.Work.Card("s1-2")
			assert.Equal(t, Merging, pr.Col)
			assert.Empty(t, pr.F("readers"))
			assert.Equal(t, "0", pr.F(FieldReadsNeeded))
			assert.Empty(t, w.s.Readers.Of("s1-2"), "no read was asked")
			w.clean("accepted on its own work with no read")
		})
	}
}

// set --reads writes the property, reads back, resets with default, and is refused for
// any other count or a stream.
func TestSetReadsRoundTripsAndResets(t *testing.T) {
	t.Parallel()
	p := Set(settingsSnapshot(nil, "s1"), SetReq{Reads: "1", Who: "coord"})
	require.Empty(t, p.Refused)
	require.Len(t, p.Props, 1)
	assert.Equal(t, PropWrite{Table: Work, Name: PropReadsNeeded, Value: "1", WasAbsent: true}, p.Props[0])
	assert.Equal(t, "sprint reads-needed 1", p.Units[0].Moved)
	n, ok := ReadsSetting(map[string]string{PropReadsNeeded: "1"})
	assert.True(t, ok)
	assert.Equal(t, 1, n)

	p = Set(settingsSnapshot(map[string]string{PropReadsNeeded: "1"}, "s1"), SetReq{Reads: ReadTierDefault, Who: "coord"})
	require.Empty(t, p.Refused)
	assert.Equal(t, PropWrite{Table: Work, Name: PropReadsNeeded, Value: ReadTierDefault, Was: "1"}, p.Props[0])
	assert.Equal(t, "sprint reads-needed default (one for a flash card, two above)", p.Units[0].Moved)
	_, ok = ReadsSetting(map[string]string{PropReadsNeeded: ReadTierDefault})
	assert.False(t, ok, "default: each card's own rule")
	_, ok = ReadsSetting(nil)
	assert.False(t, ok)

	assert.Contains(t, Set(settingsSnapshot(nil, "s1"), SetReq{Reads: "3", Who: "coord"}).Refused[0].Why, "--reads wants 0, 1, 2 or default")
	assert.Contains(t, Set(settingsSnapshot(nil, "s1"), SetReq{Streams: []string{"s1"}, Reads: "1", Who: "coord"}).Refused[0].Why, "--reads is the sprint's")
}
