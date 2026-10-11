package friend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockedLanes is the server-lane harness with one card's turn held until its release:
// a lane still working while another lane's turn meets the rate limit, so the cap falls
// while the blocked card is still in hand.
type blockedLanes struct {
	*lanesHarness
	card    string
	release chan struct{}
}

func (b *blockedLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	if m := cardOfText.FindStringSubmatch(text); m != nil && m[1] == b.card {
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return b.lanesHarness.DeliverTo(ctx, session, text)
}

// A lane the server says holds a card, beyond the live cap a rate limit lowered,
// keeps it: the server is the one truth for what a lane holds (lane_take.go), so a
// local hand-back drops only the daemon's copy while the card stays working on her
// row, and the lane stops checking the server's word on it. A card held between
// turns waits on its lane until the cap lets it run; the turn it then runs is that
// card's next turn, counted, never a fresh CardTurns.
// docs/SPEC-FRIEND.md, one-shot lanes; pkg/friend/lane_take.go header.
func TestALaneBeyondTheCapKeepsTheCardTheServerGaveIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{},
			reject: map[string]string{"c2": "Permission to read /elsewhere was auto-rejected"},
			refuse: map[string]error{"ses_1: c1": RateLimited{Session: "ses_1", Reason: "input token limit exceeded"}}}
		r, state := laneRig(t, h, 2)
		state.Sessions = map[int]string{1: "ses_1", 2: "ses_2"} // both lanes ready: lane 1 takes c1, lane 2 c2
		bl := &blockedLanes{lanesHarness: h, card: "c2", release: make(chan struct{})}
		r.d.Deliver = bl
		srv := newLaneServer(true, map[string]string{"c1~15": "ready", "c2~15": "ready"})
		srv.wire(r)
		var heldWhileCapped map[string]string
		c2Lane := -1
		r.at[10] = func() { close(bl.release) } // lane 1's rate limit landed; let the held turn end
		r.at[60] = func() {
			srv.mu.Lock()
			heldWhileCapped, c2Lane = map[string]string{}, srv.lane["c2~15"]
			for k, v := range srv.col {
				heldWhileCapped[k] = v
			}
			srv.mu.Unlock()
		}
		r.run(t, 1400)

		records := strings.Join(r.records, "\n")
		turns, _, _ := h.got()
		c2turns := 0
		for _, c := range ranCards(turns) {
			if c == "c2" {
				c2turns++
			}
		}
		require.NotNil(t, heldWhileCapped, "the c2 lane was not between turns while capped: %s", records)
		assert.Contains(t, records, "rate limit: lanes paused", "lane 1's turn answers RateLimited: %s", records)
		// (a) no record says a hand-back while the fake server still holds c2 working on lane 2
		assert.NotContains(t, records, "handed back for another lane", "the server still holds c2: %s", records)
		assert.Equal(t, "working", heldWhileCapped["c2~15"], "the server holds c2: %v", heldWhileCapped)
		assert.Equal(t, 2, c2Lane, "the server holds c2 on lane 2")
		// (b) after the cap climbs back c2 runs one more turn on lane 2 and is set aside: 2 turns, never 3
		assert.Equal(t, 2, c2turns, "c2 runs exactly two turns, on its own lane: %v\n%s", turns, records)
		assert.Equal(t, 1, strings.Count(records, "card=again"), "one 'again', then a set-aside: %s", records)
		assert.Contains(t, records, "card=set_aside", records)
	})
}

// A machine STOP while a lane is beyond the cap and holds a card between turns sends
// the stop-return for it: the lane keeps what the server says it holds (lane_take.go),
// so the card, not dropped, is released by its lane mark and named with its lane.
// docs/SPEC-FRIEND.md, one-shot lanes; pkg/friend/stop.go.
func TestAStopWhileALaneIsBeyondTheCapReturnsItsCard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{},
			reject: map[string]string{"c2": "Permission to read /elsewhere was auto-rejected"},
			refuse: map[string]error{"ses_1: c1": RateLimited{Session: "ses_1", Reason: "input token limit exceeded"}}}
		r, state := laneRig(t, h, 2)
		state.Sessions = map[int]string{1: "ses_1", 2: "ses_2"} // both lanes ready: lane 1 takes c1, lane 2 c2
		bl := &blockedLanes{lanesHarness: h, card: "c2", release: make(chan struct{})}
		r.d.Deliver = bl
		srv := newLaneServer(true, map[string]string{"c1~15": "ready", "c2~15": "ready"})
		srv.wire(r)
		var mu sync.Mutex
		var stops [][]string
		r.d.StopReturn = func(_ context.Context, argv []string) error {
			mu.Lock()
			stops = append(stops, append([]string(nil), argv...))
			mu.Unlock()
			return nil
		}
		stopped := false
		r.d.MachineStopped = func() bool { return stopped }
		r.at[10] = func() { close(bl.release) }
		r.at[60] = func() { stopped = true }
		r.run(t, 90)

		mu.Lock()
		got := append([][]string(nil), stops...)
		mu.Unlock()
		require.Len(t, got, 1, "one stop-return for the one card a lane still holds: %v\n%s", got, strings.Join(r.records, "\n"))
		assert.Equal(t, []string{"stop-return", "--as", "friend.bob", "c2@1", "--epoch", "15", "--reason", StopReturnReason, "--lane", "2"}, got[0])
	})
}
