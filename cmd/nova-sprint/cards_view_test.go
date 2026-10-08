package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedCards is a twin store of two primaries, s1-1 (tier: pro) on m1 and s2-1 (no tier line, so the
// dealer's default flash) on m2, both working.
func (ta *testApp) seedCards() {
	ta.t.Helper()
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	pro := writeBrief(ta.t, "the work (s1) tier: pro")
	none := writeBrief(ta.t, "the work (s1)")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + pro)
	ta.ok("add --stream s2 --count 1 --one --brief-file " + none)
	ta.ok("start")
	ta.ok("tick")
	ta.deal(2)
	ta.ok("take --as m1 --limit 2")
	ta.ok("take --as m2 --limit 2")
}

// view cards --col review --by tier counts the primaries in review by the tier the dealer
// reads: a brief's tier line, flash when it names none; --json is one object.
func TestViewCardsCountsReviewByTier(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.seedCards()
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("finish --as m2 s2-1.w1@1")
	ta.ok("tick")

	out := ta.ok("view cards --col review --by tier")
	assert.Equal(t, "flash=1\npro=1\ntotal=2\n", out)

	var v cardsView
	ta.json("view cards --col review --by tier", &v)
	assert.Equal(t, "cards", v.View)
	assert.Equal(t, viewSchema, v.Schema)
	assert.Equal(t, "review", v.Col)
	assert.Equal(t, "tier", v.By)
	assert.Equal(t, 2, v.Total)
	assert.Equal(t, map[string]int{"flash": 1, "pro": 1}, v.Counts)
	assert.Empty(t, v.Cards, "counting lists nothing")
	assert.Equal(t, 1, strings.Count(ta.ok("view cards --col review --by tier --json"), "\n"), "one object")
}

// Without --by the primaries are listed; --stream and --holder narrow them; no sentinel is a card.
func TestViewCardsListsAndFilters(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.seedCards()
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("tick")

	var all cardsView
	ta.json("view cards", &all)
	require.Equal(t, 2, all.Total, "%+v", all)
	var s2 cardsView
	ta.json("view cards --stream s2", &s2)
	require.Len(t, s2.Cards, 1)
	assert.Equal(t, "s2-1", s2.Cards[0].ID)
	assert.Equal(t, "flash", s2.Cards[0].Tier, "the dealer's default")
	assert.Equal(t, "m2", s2.Cards[0].Holder)
	var mine cardsView
	ta.json("view cards --holder m2 --by stream", &mine)
	assert.Equal(t, map[string]int{"s2": 1}, mine.Counts)
}

func TestViewCardsRefusesWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, _, errs := ta.do("view cards --by colour")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--by is one of")
	code, _, errs = ta.do("view cards --col nowhere")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--col is one of")
}
