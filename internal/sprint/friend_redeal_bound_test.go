package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card taken back from one friend and dealt to the next is measured from the next
// friend's own deal (TickDeadlines, steps_tick.go). The take-back stamps untaken_since at the
// take and unsets dealt (FriendTake, withdrawUnit); the deal to the next friend stamps dealt and
// keeps untaken_since (friendRedealUnit, nextGen), so the time the card waited withdrawn, with no
// friend to take it, counted against the next friend. On 2026-10-08 the work card
// one-lane-engine-per-friend-installed-by-the-adopt-bbb.w1, taken back at 02:54:31Z and withdrawn
// for ten hours, was dealt to a friend at 12:37:46Z and taken back by rule friend-take at 12:37:48Z
// ("over the dealt bound 6h"); dealt to a second friend at 13:04:28Z after 27 minutes withdrawn, it
// was taken back at 13:07:57Z ("ready over 30m while friend stella has a lane free"): seven
// generations in thirty minutes, started by no one.

// takenBackAndWithdrawnFor is s1-1 on amy's row past its bound, taken back by rule friend-take and
// left withdrawn for the gap (no friend with room), then dealt to bob: ready on his row from now.
func takenBackAndWithdrawnFor(t *testing.T, gap time.Duration) (*world, FriendSeat, FriendSeat) {
	t.Helper()
	w, bob := friendDealt(t)
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
	rules(w, on(amy, bob))
	require.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col, "taken back by the rule")
	require.Empty(t, openOf(w, NWorkLate, "s1-1"), "answered")
	w.tick(gap)
	dealWith(w, amy, bob)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, FriendRow("bob"), wc.Row, "dealt again, to the other friend")
	require.Equal(t, Ready, wc.Col, "ready on his row until he starts it")
	return w, amy, bob
}

func TestACardTakenBackFromOneFriendGetsTheNextFriendsOwnBound(t *testing.T) {
	t.Parallel()

	t.Run("the lane-free bound runs from his deal, not from the take-back", func(t *testing.T) {
		t.Parallel()
		w, amy, bob := takenBackAndWithdrawnFor(t, 25*time.Minute)
		deadlines(w, on(amy, bob))
		assert.Empty(t, openOf(w, NWorkLate, "s1-1"), "dealt to bob this second: not late on his row")
		w.tick(6 * time.Minute) // 31 minutes since the take-back, 6 since his deal
		deadlines(w, on(amy, bob))
		assert.Empty(t, openOf(w, NWorkLate, "s1-1"), "six minutes on his row: not late, whatever the take-back's clock says")
		w.tick(25 * time.Minute) // 31 minutes since his deal
		deadlines(w, on(amy, bob))
		open := openOf(w, NWorkLate, "s1-1")
		require.Len(t, open, 1, "past his own lane-free bound with no start: late on his row")
		assert.Contains(t, open[0].Note.What, "friend bob has a lane free")
		w.clean("redealt to bob")
	})

	t.Run("the dealt bound runs from his deal: ten hours withdrawn is not his", func(t *testing.T) {
		t.Parallel()
		w, amy, bob := takenBackAndWithdrawnFor(t, 10*time.Hour)
		deadlines(w, on(amy, bob))
		assert.Empty(t, openOf(w, NWorkLate, "s1-1"), "dealt to bob this second: not late on his row")
		for _, a := range RuleAnswers(w.s, on(amy, bob)) {
			assert.False(t, a.Type == NWorkLate && a.Subject == "s1-1" && a.Answers(), "nothing for friend-take to take back: %s %s", a.Act, a.Why)
		}
		w.tick(w.s.FriendStartMax() + FriendReadyMax + time.Minute)
		deadlines(w, on(amy, bob))
		require.Len(t, openOf(w, NWorkLate, "s1-1"), 1, "late on his row by his own bound")
		w.clean("redealt to bob after ten hours withdrawn")
	})
}
