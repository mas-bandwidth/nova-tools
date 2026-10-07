package friend

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The follow holds the lanes without killing one: while the follow holds, a lane whose
// turn runs finishes its card, no new card is taken and no session opened, the daemon
// reports idle only once no lane holds a card, and every beat names exactly the jobs the
// lanes hold (the finished job leaves the list).
func TestAFollowHoldsNewLanesAndNeverKillsARunningOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true, "c3": true}, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 2)
		var mu sync.Mutex
		hold := false
		var idles []bool
		var reports []BeatReport
		r.d.Follow = func(_ context.Context, _ time.Time, idle bool) bool {
			mu.Lock()
			defer mu.Unlock()
			idles = append(idles, idle)
			return hold
		}
		report := func() { reports = append(reports, r.d.Report()) }
		// step 3: two lanes run c1 and c2, blocked in their turns; the follow begins
		r.at[3] = func() {
			mu.Lock()
			hold = true
			mu.Unlock()
			report()
		}
		// step 6: the turns end; c3 waits in the queue and no lane may take it
		r.at[6] = func() { report(); close(h.block) }
		r.at[12] = func() { report() }
		r.run(t, 14)

		turns, _, seeds := h.got()
		assert.Len(t, seeds, 2, "both lanes opened before the hold")
		assert.ElementsMatch(t, []string{"ses_1: c1", "ses_2: c2"}, turns, "the two cards in hand finished; c3 was never taken while the follow held")
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, reports, 3)
		assert.ElementsMatch(t, []string{"c1~15", "c2~15"}, reports[0].Running, "the beat names the jobs the lanes hold")
		assert.Equal(t, 2, reports[0].Working)
		assert.Equal(t, 2, reports[0].Width)
		assert.ElementsMatch(t, []string{"c1~15", "c2~15"}, reports[1].Running, "still held, still named, while the turns run")
		assert.Empty(t, reports[2].Running, "finished jobs leave the list")
		assert.Equal(t, 0, reports[2].Working)
		assert.Contains(t, idles, false, "not idle while a card is in a lane's hand")
		assert.True(t, idles[len(idles)-1], "idle once every card in hand is finished and none is taken")
		assert.Equal(t, "1:ses_1:- 2:ses_2:-", r.last().Lanes, "no lane was killed or retired: both stand with no card")
		for _, id := range []string{"c1", "c2"} {
			assert.True(t, exists(filepath.Join(dir, "outbox", id+"~15", "RESULT.md")), id+" finished")
		}
		assert.False(t, exists(filepath.Join(dir, "outbox", "c3~15", "RESULT.md")), "c3 waits for the daemon that comes up")
	})
}
