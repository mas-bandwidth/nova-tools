package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cappedHarness is a lane harness whose card turn prints lines and then works on, printing
// nothing more, until its turn is ended: a lane that runs past its cap.
type cappedHarness struct {
	mu    sync.Mutex
	turns int
	lines int
}

func (h *cappedHarness) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *cappedHarness) OpenSession(context.Context, string) (string, error) { return "ses_1", nil }

func (h *cappedHarness) DeliverTo(ctx context.Context, _, _ string) (LaneTurn, error) {
	h.mu.Lock()
	h.turns++
	n := h.lines
	h.mu.Unlock()
	for i := range n {
		printed(ctx, fmt.Appendf(nil, "line %d of the lane\n", i))
	}
	<-ctx.Done()
	return LaneTurn{Exit: -1}, errors.New("the delivery was stopped with its process group")
}

func (h *cappedHarness) got() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.turns
}

// A lane's wall time is capped by its card's tier (a-lane-is-capped-by-its-tier.w1): a
// flash card's lane that runs past the flash cap is ended by the daemon, the card's
// REPORT.md is a HOLD naming `capped at <cap>` with the last 40 lines of the lane's
// output, the failed finish carries the cap, and the card is not handed again. (The
// sprint's half, the finish read and the card dealt once more at the next tier up, is
// internal/sprint's lane_cap_friend_test.go.)
func TestALaneIsCappedByItsTier(t *testing.T) {
	t.Parallel()

	var report string
	var sent []string
	synctest.Test(t, func(t *testing.T) {
		dir := laneEndFixture(t, "c1~15")
		out := filepath.Join(dir, "outbox", "c1~15")
		h := &cappedHarness{lines: 50}
		r, state := laneRig(t, &lanesHarness{dir: dir, active: map[string]int{}}, 1)
		r.d.Deliver = h
		r.d.heldCards = []HeldCard{{Card: "c1", Job: "c1~15", Col: "working", Kind: "work", Tier: "flash", Epoch: 15}}
		r.d.LaneCaps = func() map[string]time.Duration { return map[string]time.Duration{"flash": 2 * time.Minute} }
		f := &finishes{}
		r.d.Finish = f.finish
		r.run(t, 400)

		raw, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
		require.NoError(t, err, strings.Join(r.records, "\n"))
		report = string(raw)
		assert.True(t, strings.HasPrefix(report, "Verdict: HOLD\n\nnova-friend lane 1 of bob finished card c1: capped at 2m0s (tier flash, overrun "), report)
		assert.Contains(t, report, "the card's wall reached its tier's cap and the daemon ended the lane")
		assert.Contains(t, report, "no pushed head found.")
		assert.Contains(t, report, "The last 40 lines of the lane's output:\n\n    line 10 of the lane\n")
		assert.Contains(t, report, "    line 49 of the lane\n")
		assert.NotContains(t, report, "line 9 of the lane", "only the last 40 lines")
		assert.Equal(t, 1, h.got(), "the capped card is ended, not handed again")
		assert.Empty(t, state.Started)
		assert.Equal(t, []string{"c1~15"}, state.GivenUp)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "lane 1: card c1 capped at 2m0s (tier flash): its wall since ")
		assert.Contains(t, records, "card=capped turn=1/2")
		assert.Contains(t, records, "finish=failed sent=server")
		got := f.got()
		require.Len(t, got, 1, "one finish")
		sent = got[0]
	})

	require.Equal(t, []string{"finish", "--as", "friend.bob", "c1@1", "--epoch", "15", "--failed", "--branch", "sprint/c1.g1.e15", "--report"}, sent[:10])
	assert.True(t, strings.HasPrefix(sent[10], "friend bob HOLD: nova-friend lane 1 of bob finished card c1: capped at 2m0s (tier flash, overrun "), sent[10])
}

// The cap of a card's lane is its tier's: the row's when it names one, else the defaults,
// and the longest default for a tier not read; the row's caps ride on the beat's answer.
func TestALanesCapIsItsTiersFromTheRowOrTheDefaults(t *testing.T) {
	t.Parallel()
	row := map[string]time.Duration{"flash": 5 * time.Minute}
	for tier, want := range map[string]time.Duration{"flash": 5 * time.Minute, "pro": 45 * time.Minute, "heavy": 90 * time.Minute, "frontier": 150 * time.Minute, "": 150 * time.Minute, "odd": 150 * time.Minute} {
		assert.Equal(t, want, LaneCap(tier, row), "tier %q", tier)
	}
	assert.Equal(t, 15*time.Minute, LaneCap("flash", nil))

	caps, ok := ParseLaneCaps("BEAT OK row_mode=one-shot row_width=2 row_lane_caps=flash:10m,pro:1h")
	require.True(t, ok)
	assert.Equal(t, map[string]time.Duration{"flash": 10 * time.Minute, "pro": time.Hour}, caps)
	for _, bad := range []string{"BEAT OK row_width=2", "row_lane_caps=flash", "row_lane_caps=flash:0s", "row_lane_caps=:10m", "row_lane_caps=pro:soon"} {
		_, ok := ParseLaneCaps(bad)
		assert.False(t, ok, bad)
	}

	words := CappedWords(15*time.Minute, "flash", 2400*time.Millisecond)
	assert.Equal(t, "capped at 15m0s (tier flash, overrun 2s)", words)
	assert.Equal(t, "capped at 2h30m0s (tier -, overrun 0s)", CappedWords(150*time.Minute, "", -time.Second))
	assert.Equal(t, "line 2\nline 3", LastLines("line 1\nline 2\nline 3\n", 2))
}

// printed is p, printed by the command a delivery runs, said to ctx's watch and tail: what
// RealExec does with each write, for a test harness that runs no command through it.
func printed(ctx context.Context, p []byte) {
	if len(p) == 0 {
		return
	}
	if seen, _ := ctx.Value(outputKey{}).(func()); seen != nil {
		seen()
	}
	if tail, _ := ctx.Value(tailKey{}).(func([]byte)); tail != nil {
		tail(p)
	}
}
