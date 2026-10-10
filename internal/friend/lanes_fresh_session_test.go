package friend

import (
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend paid per token spends tokens that land no work when one lane opens
// a session seeded with its lane number and width (so no lane's seed matches
// another's, and a provider's prompt cache shares nothing) and then delivers
// every later card into that session, paying for every card before it. The
// seed is now byte-identical for every lane (the lane number and width ride in
// each card's text), and a FreshSessionPerCard option drops a lane's session
// when its card finishes, so the next card opens a fresh session with the same
// seed.
func TestFriendDeliveryIsLeanCachedAndTargeted(t *testing.T) {
	t.Parallel()

	// Two lanes of one friend open two sessions from one byte-identical seed,
	// and each card's turn names its lane.
	t.Run("one seed for every lane", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
			h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
			r, _ := laneRig(t, h, 2)
			r.run(t, 15)
			_, texts, seeds := h.got()
			require.Len(t, seeds, 2, "one session per lane")
			assert.Equal(t, seeds[0], seeds[1], "the seed is byte-identical for every lane")
			for _, text := range texts {
				assert.Regexp(t, `nova-friend: lane [12] of 2:`, text, "each card's text names its lane")
			}
		})
	})

	// With FreshSessionPerCard a lane drops its session when its card finishes:
	// two cards on one lane open two sessions, each seeded with the same text.
	t.Run("a fresh session per card", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
			h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
			r, _ := laneRig(t, h, 1)
			r.d.FreshSessionPerCard = true
			r.run(t, 15)
			turns, texts, seeds := h.got()
			assert.Equal(t, []string{"ses_1: c1", "ses_2: c2"}, turns, "two cards on one lane open two sessions")
			assert.Equal(t, seeds[0], seeds[1], "each card's session is seeded with the same seed")
			assert.Contains(t, texts[0], "nova-friend: lane 1 of 1: one card this turn, c1.")
			assert.Contains(t, texts[1], "nova-friend: lane 1 of 1: one card this turn, c2.")
		})
	})

	// Off (the default), one session serves every card.
	t.Run("one session serves every card", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
			h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
			r, _ := laneRig(t, h, 1)
			r.run(t, 15)
			turns, _, seeds := h.got()
			require.Len(t, seeds, 1, "one session serves both cards")
			assert.Equal(t, []string{"ses_1: c1", "ses_1: c2"}, turns)
		})
	})
}
