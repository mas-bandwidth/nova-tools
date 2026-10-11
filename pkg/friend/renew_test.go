package friend

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lane's session full (a provider's context-limit refusal) or missing (the
// harness no longer has it) renews itself (fix
// a-friend-session-full-or-missing-starts-fresh): the lane starts a fresh
// session, seeded from the friend's own files, its card continues in it, and
// the seat is told once per renewal. No hand session, no restart.

// A provider's context-limit refusal on a lane's session makes the daemon
// open a new session itself and continue the lane: the card is handed again in
// the fresh session, where it finishes; the seat is told once.
func TestAContextLimitRefusalOnTheSessionRenewsItAndContinuesTheLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, finish: map[string]bool{"c1": true},
			refuse: map[string]error{"ses_1: c1": ProviderRefused{Session: "ses_1",
				Reason: "invalid_request_error: prompt is too long: 183462 tokens > 200000 maximum"}}}
		r, state := laneRig(t, h, 1)
		r.run(t, 8)
		turns, _, seeds := h.got()
		assert.Equal(t, []string{"ses_1: c1", "ses_1: c1"}, turns, "the card is handed again, in the fresh session")
		require.Len(t, seeds, 2, "the lane opened a fresh session itself")
		assert.Contains(t, seeds[1], filepath.Join(dir, "bob", "AGENTS.md"), "the fresh session carries the friend's standing brief")
		assert.Equal(t, map[int]string{1: "ses_1"}, state.Sessions, "the lane keeps the renewed session")
		assert.Empty(t, state.GivenUp, "the card was never set aside")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "session_renewed=full", records)
		assert.Contains(t, records, "card=done", "the card finishes in the fresh session")
		got := r.adaGot(t)
		require.Len(t, got, 1, "one note to the seat, no other word")
		assert.True(t, strings.HasPrefix(got[0], "friend bob: session ses_1 full, renewed"), got[0])
		assert.Contains(t, got[0], "prompt is too long", "the note carries the provider's reason")
	})
}

// A session the harness no longer has renews the same way: the turn's answer
// says the session is gone, a fresh one is opened and the card continues in
// it, counted toward nothing, never given up.
func TestAMissingSessionRenewsItselfAndContinuesTheLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, finish: map[string]bool{"c1": true},
			missing: map[string]bool{"ses_1: c1": true}}
		r, state := laneRig(t, h, 1)
		*state = LaneState{Sessions: map[int]string{1: "ses_1"}} // the harness has dropped the session the lane keeps
		r.run(t, 8)
		turns, _, seeds := h.got()
		assert.Equal(t, []string{"ses_1: c1", "ses_1: c1"}, turns, "the card is handed again, in the fresh session")
		require.Len(t, seeds, 1, "the lane opened a fresh session itself (the dropped one was kept, none was opened before)")
		assert.Equal(t, map[int]string{1: "ses_1"}, state.Sessions)
		assert.Empty(t, state.GivenUp, "the card was never given up for want of a session")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "session_renewed=missing", records)
		assert.Contains(t, records, "card=done", "the card finishes in the fresh session")
		got := r.adaGot(t)
		require.Len(t, got, 1)
		assert.True(t, strings.HasPrefix(got[0], "friend bob: session ses_1 missing, renewed"), got[0])
		assert.Contains(t, got[0], "no such session", "the note carries the harness's answer")
	})
}

// The renewal is once per card: a context-limit refusal the fresh session
// also answers is the card's business again, handed and set aside as any
// turn's failure, and one renewal only is told.
func TestARenewalIsOncePerCardAndTheNextRefusalIsTheCardsOwn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		full := ProviderRefused{Session: "ses_1", Reason: "invalid_request_error: prompt is too long: 183462 tokens > 200000 maximum"}
		h := &lanesHarness{dir: dir, active: map[string]int{},
			refuse: map[string]error{"ses_1: c1": full}}
		r, state := laneRig(t, h, 1)
		refuse := func() { h.mu.Lock(); h.refuse["ses_1: c1"] = full; h.mu.Unlock() }
		for i := 2; i <= 9; i++ {
			r.at[i] = refuse // the fresh session refuses the card's turns too, until the card is set aside
		}
		r.run(t, 10)
		_, _, seeds := h.got()
		require.Len(t, seeds, 2, "one renewal, no second")
		assert.Equal(t, []string{"c1~15"}, state.GivenUp, "the card is set aside as any turn's failure")
		got := r.adaGot(t)
		renewed := 0
		for _, g := range got {
			if strings.HasPrefix(g, "friend bob: session ses_1 full, renewed") {
				renewed++
			}
		}
		assert.Equal(t, 1, renewed, "one note per renewal: %v", got)
	})
}
