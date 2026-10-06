package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parityRig is a lane rig whose row carries lane settings (the parity is on), its
// sprint verbs recorded, and a session usage that grows by used per read after the
// first.
func parityRig(t *testing.T, h *lanesHarness, row LaneRow, used int64) (*rig, *[]string) {
	r, _ := laneRig(t, h, 1)
	var mu sync.Mutex
	var verbs []string
	row.Set = true
	r.d.LaneRow = func() LaneRow { return row }
	r.d.Verb = func(_ context.Context, argv []string) error {
		mu.Lock()
		defer mu.Unlock()
		verbs = append(verbs, strings.Join(argv, " "))
		return nil
	}
	reads := int64(0)
	r.d.Usage = func(_ context.Context, session string) (TokenUsage, error) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		return TokenUsage{Input: used * (reads - 1), Sessions: 1}, nil
	}
	return r, &verbs
}

// A finished card's cost is on its REPORT.md and RESULT.md, taken from the session's
// usage since the card began and priced by the row; the coordinator gets one note.
func TestALaneFinishPublishesTheCardsCostAndTellsTheCoordinator(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		row := LaneRow{Model: "p/m", Route: RouteRow{Name: "r1", Input: "1", ReasoningAsOutput: true}}
		r, _ := parityRig(t, h, row, 2_000_000)
		r.run(t, 20)
		res, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "RESULT.md"))
		require.NoError(t, err)
		assert.Contains(t, string(res), "cost: $2.00 (opencode: $0.00)")
		got := strings.Join(r.adaGot(t), "\n")
		assert.Contains(t, got, "bob one-shot lane finished c1~15")
		assert.Contains(t, got, "cost $")
	})
}

// A card over the row's token cap gets a HOLD REPORT.md naming the cap and its turn
// is stopped; the lane's end finishes the card with that report.
func TestALaneOverTheTokenCapIsStoppedWithAHold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, block: make(chan struct{})}
		r, _ := parityRig(t, h, LaneRow{TokenCap: 1000}, 5000)
		r.run(t, 90)
		raw, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(raw), "Verdict: HOLD\nHead: none\n"), string(raw))
		assert.Contains(t, string(raw), "per-card cap of 1000 tokens")
		assert.Contains(t, strings.Join(r.records, "\n"), "TOKEN CAP c1")
	})
}

// A dealt card the row's filter takes back and that was not started is taken back for
// the dealer once; a card the filter leaves is not handed to a lane.
func TestALaneTakesBackACardOutsideTheRowsFilterAndHandsNoneOfThem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}}
		r, verbs := parityRig(t, h, LaneRow{Tiers: []string{"flash"}}, 0)
		r.d.heldCards = []HeldCard{{Card: "c1", Job: "c1~15", Col: "working", Tier: "pro"}, {Card: "c2", Job: "c2~15", Col: "working", Tier: "flash"}}
		r.run(t, 20)
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: c2"}, turns)
		assert.Equal(t, []string{"friend take bob c1 --reason tier pro: this friend works only flash cards; the rest go back to the dealer"}, *verbs)
	})
}

// A provider failure that holds the lanes holds the friend down with the exact message,
// and the row's stop-on-provider turns a rate limit into the same hold.
func TestAProviderFailureHoldsEveryLaneAndTheFriendDown(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{"out of funds": OutOfFunds{Session: "s", Reason: "Error: 402 payment required"}, "rate limit with the row's stop": RateLimited{Session: "s", Reason: "Error: 402 payment required"}} {
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			h := &failingHarness{lanesHarness: &lanesHarness{dir: dir, active: map[string]int{}}, err: err}
			r, verbs := parityRig(t, h.lanesHarness, LaneRow{StopOnProvider: true, Model: "p/m"}, 0)
			r.d.Deliver = h
			r.run(t, 20)
			assert.Equal(t, []string{"friend down bob --reason provider failure (p/m): Error: 402 payment required"}, *verbs, name)
			assert.Equal(t, 1, h.calls, name+": the hold starts no second turn")
		})
	}
}

type failingHarness struct {
	*lanesHarness
	err   error
	calls int
}

func (f *failingHarness) DeliverTo(context.Context, string, string) (LaneTurn, error) {
	f.calls++
	return LaneTurn{Exit: 1}, errors.Join(f.err)
}
