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

// printingLanes is a lanes harness whose card turn prints when the test says so: it keeps
// the turn's output watch (WithOutputSeen) for the test to call.
type printingLanes struct {
	*lanesHarness
	mu   sync.Mutex
	seen func()
}

func (h *printingLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	h.mu.Lock()
	h.seen, _ = ctx.Value(outputKey{}).(func())
	h.mu.Unlock()
	return h.lanesHarness.DeliverTo(ctx, session, text)
}

func (h *printingLanes) print() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen != nil {
		h.seen()
	}
}

// The daemon stamps progress on a lane's card while its turn prints, at most every
// ProgressEvery, and a turn silent since stamps nothing more (docs/SPEC-SPRINT.md section 8,
// the rules table's row late; tla/SprintRules.tla, Stamp). The clock is the rig's: a second
// a step.
func TestTheDaemonStampsProgressWhileALaneTurnPrints(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &printingLanes{lanesHarness: &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}, block: make(chan struct{})}}
		r, _ := laneRig(t, h.lanesHarness, 1)
		r.d.Deliver = h
		var mu sync.Mutex
		var stamps []time.Time
		var cards [][]Card
		r.d.Progress = func(_ context.Context, cs []Card) error {
			mu.Lock()
			defer mu.Unlock()
			r.mu.Lock()
			at := r.now // the step's clock, read without moving it
			r.mu.Unlock()
			stamps, cards = append(stamps, at), append(cards, cs)
			return nil
		}
		r.at[30] = h.print // the turn's first output: stamped
		r.at[60] = h.print // again inside ProgressEvery: stamped once ProgressEvery has passed
		// then fifteen minutes silent, the turn still running: nothing more is stamped
		r.at[60+900] = func() { close(h.block) }
		r.run(t, 60+920)
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, stamps, 2, "the first output, then the output since it once ProgressEvery passed; nothing while silent: %v", stamps)
		assert.Equal(t, ProgressEvery, stamps[1].Sub(stamps[0]))
		for _, cs := range cards {
			require.Len(t, cs, 1)
			assert.Equal(t, "c1", cs[0].ID)
			assert.Equal(t, "15", cs[0].Epoch())
		}
	})
}

// A stamp is one server verb for the cards of each epoch, the friend named as the holder.
func TestProgressArgvIsOneVerbAnEpoch(t *testing.T) {
	t.Parallel()
	card := func(id, epoch string) Card { return Card{ID: id, Outbox: filepath.Join("x", "outbox", id+"~"+epoch)} }
	got := ProgressArgv("bob", []Card{card("c2", "15"), card("c1", "15"), card("c3", "16")})
	assert.Equal(t, [][]string{
		{"progress", "--as", "friend.bob", "c1", "c2", "--epoch", "15"},
		{"progress", "--as", "friend.bob", "c3", "--epoch", "16"},
	}, got)
}
