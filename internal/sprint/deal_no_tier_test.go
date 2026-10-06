package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card whose tier is unset is not dealt to anyone (a-card-without-a-tier-is-not-dealt.w1;
// a flash friend's night of 2026-10-05, width 24: the audit and rework cards carried no
// tier, the dealer took them for flash, and the runner, which hands back a card whose tier it
// can read and is not flash, ran every one; each lane ended with no result or a HOLD). The
// tick deals it to no friend and no machine, raises one judgment per such card naming the
// brief command that sets its tier, and closes it once the tier is set and the card dealt.
func TestACardWithoutATierIsNeverDealt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t,
		"c: an audit with no tier\n\nThe task.",
		"c: a go build tier: pro\n\nThe task.",
		"c: a small fix tier: flash\n\nThe task.")
	fay := FriendSeat{Name: "fay", Width: 24, Status: Up, Class: cardhdr.RouteFlash, Tiers: []string{cardhdr.RouteFlash}}
	require.True(t, TierUnset(w.s.Primary("s1-1")), "line 1 names no tier, nothing pins one")
	require.False(t, TierUnset(w.s.Primary("s1-2")))

	dealWith(w, fay)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"), "a card whose tier is unset waits ready")
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"), "dealt to no friend and no machine")
	judged := w.notesOf(NNoTier)
	require.Len(t, judged, 1, "one judgment for the one card with no tier")
	assert.Equal(t, []string{"s1-1"}, judged[0].Primaries)
	assert.Contains(t, judged[0].What, "s1-1 has no tier")
	assert.Contains(t, judged[0].What, "nova-sprint brief s1-1 --tier")
	assert.Equal(t, TickDecisions[NNoTier], judged[0].Decisions)
	lines := NoteCommands(judged[0], judged[0].Primaries)
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[0].Lines[0], "nova-sprint brief s1-1 --tier", "the decision prints the command that sets the tier")

	// a flash friend is dealt only flash: the pro card goes to a machine, never to her
	pro := w.s.Fleet.Card("s1-2.w1")
	require.NotNil(t, pro, "the pro card is dealt")
	assert.NotEqual(t, FriendRow("fay"), pro.Row, "a flash friend is never dealt a pro card")
	flash := w.s.Fleet.Card("s1-3.w1")
	require.NotNil(t, flash)
	assert.Equal(t, FriendRow("fay"), flash.Row, "the flash card goes to the flash friend")

	// a tick later the judgment is not written again
	dealWith(w, fay)
	assert.Len(t, w.notesOf(NNoTier), 1, "one judgment per card, written once")
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))

	// the command it names sets the tier: the next tick deals it to the flash friend and closes the judgment
	w.must(Brief(w.s, BriefReq{ID: "s1-1", Tier: cardhdr.RouteFlash}))
	require.False(t, TierUnset(w.s.Primary("s1-1")))
	dealWith(w, fay)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc, "dealt once its tier is set")
	assert.Equal(t, FriendRow("fay"), wc.Row)
	for _, o := range w.s.Open {
		assert.NotEqual(t, NNoTier, o.Note.Type, "the judgment closes once the card is dealt")
	}
	assert.Empty(t, Check(w.s, nil))
}

// The friends' deal alone (FriendDeal) never places a card whose tier is unset, whatever
// its WHO line: a hard pin to a flash friend with no tier waits ready.
func TestTheFriendDealSkipsACardWithNoTier(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: pinned, no tier\nREPO: mas-bandwidth/nova-tools\nWHO: only friend fay\n\nThe task.")
	p := FriendDeal(w.s, w.s.Work.Column(Ready), []FriendSeat{{Name: "fay", Width: 4, Status: Up, Class: cardhdr.RouteFlash}})
	assert.Empty(t, p.Units, "a card whose tier is unset is dealt to no friend")
}

// A flash friend is never dealt a pro card by the deal or the level: with only pro cards
// ready and a flash friend up with room, her row stays empty.
func TestAFlashFriendIsNeverDealtAProCard(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: a tier: pro\n\nThe task.", "c: b tier: pro\nREPO: mas-bandwidth/nova-tools\nWHO: friend fay\n\nThe task.")
	fay := FriendSeat{Name: "fay", Width: 24, Status: Up, Class: cardhdr.RouteFlash}
	dealWith(w, fay)
	dealWith(w, fay)
	assert.Zero(t, w.s.Fleet.Count(FriendRow("fay"), Working)+w.s.Fleet.Count(FriendRow("fay"), Ready), "no pro card on a flash friend's row")
}
