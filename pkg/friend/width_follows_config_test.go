package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widthHarness opens a session per lane (ses_<lane>) and runs each card's turn until the
// test releases it, then writes the card's RESULT.md: the test holds exactly the lanes it
// means to keep running. A turn that ends without its release was killed.
type widthHarness struct {
	mu      sync.Mutex
	dir     string
	running map[string]chan struct{} // card: its release
	killed  []string
}

func (h *widthHarness) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *widthHarness) OpenSession(_ context.Context, seed string) (string, error) {
	return "ses_" + laneOfSeed.FindStringSubmatch(seed)[1], nil
}

func (h *widthHarness) DeliverTo(ctx context.Context, _, text string) (LaneTurn, error) {
	id := cardOfText.FindStringSubmatch(text)[1]
	release := make(chan struct{})
	h.mu.Lock()
	h.running[id] = release
	h.mu.Unlock()
	select {
	case <-release:
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.running, id)
		h.killed = append(h.killed, id)
		h.mu.Unlock()
		return LaneTurn{Exit: -1}, ctx.Err()
	}
	out := filepath.Join(h.dir, "outbox", id+"~15")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return LaneTurn{}, err
	}
	if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: "+id+"\n"), 0o644); err != nil {
		return LaneTurn{}, err
	}
	return LaneTurn{Exit: 0}, nil
}

// live is how many lanes run a card's turn now.
func (h *widthHarness) live() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.running)
}

// release ends n running turns, the lowest card ids first.
func (h *widthHarness) release(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]string, 0, len(h.running))
	for id := range h.running {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids[:min(n, len(ids))] {
		close(h.running[id])
		delete(h.running, id)
	}
}

func (h *widthHarness) kills() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.killed)
}

// TestFriendLanesFollowTheConfigWidthUpAndDown pins the rule (the owner, 2026-10-14:
// "nova-config is *THE* one singular definition of width!"; "machines and friends must
// poll and auto update to follow the config width. no exceptions!"): a one-shot daemon runs
// exactly the width its beat answers (row_width, which the server reads from nova-config),
// followed on the next step with no restart. Raised, new lanes open; lowered, running lanes
// finish their cards and are not refilled until the running count is under the width; no
// answer is no lane, said once, never a floor of 1 (tla/FriendLane.tla RowWidth,
// LanesStartNeverPastWidth, NoLaneKilledOnLower, LanesConverge).
func TestFriendLanesFollowTheConfigWidthUpAndDown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var ids []string
		var tasks [][2]string
		for i := 1; i <= 12; i++ {
			id := fmt.Sprintf("c%02d", i)
			ids = append(ids, id)
			tasks = append(tasks, [2]string{id, "queued"})
		}
		dir := cardDirFixture(t, tasks, ids, nil)
		h := &widthHarness{dir: dir, running: map[string]chan struct{}{}}
		r, _ := laneRig(t, &lanesHarness{dir: dir, active: map[string]int{}}, 0)
		r.d.Deliver = h
		var mu sync.Mutex
		width := 3 // the beat answer's row_width; 0 is an answer that names none
		set := func(w int) { mu.Lock(); width = w; mu.Unlock() }
		r.d.Row = func() (string, int) { mu.Lock(); defer mu.Unlock(); return ModeOneShot, width }
		got := map[int]int{}
		look := func(at int, then func()) {
			r.at[at] = func() {
				got[at] = h.live()
				if then != nil {
					then()
				}
			}
		}
		look(10, func() { set(1) })               // three run; the row lowers to one
		look(15, func() { h.release(1) })         // none killed; one finishes
		look(20, func() { h.release(1) })         // two left, none refilled; one more finishes
		look(25, func() { h.release(1) })         // one left at width one; it finishes
		look(30, func() { set(4) })               // the freed lane refilled to one; the row raises to four
		look(36, func() { set(0); h.release(4) }) // four run; the row names none, every turn finishes
		look(42, nil)
		r.run(t, 44)

		assert.Equal(t, 3, got[10], "width 3: three lanes run")
		assert.Equal(t, 3, got[15], "lowered to 1: no running lane is killed")
		assert.Equal(t, 2, got[20], "a lane that finished under a lowered width is not refilled")
		assert.Equal(t, 1, got[25], "the lanes fall to the width as they end")
		assert.Equal(t, 1, got[30], "at width 1 the one lane is refilled, and no more")
		assert.Equal(t, 4, got[36], "raised to 4: new lanes open on the next step")
		assert.Equal(t, 0, got[42], "no width answered: no lane takes a card (never a floor of 1)")
		assert.Empty(t, h.kills(), "no lane was killed for a width change")
		records := strings.Join(r.records, "\n")
		require.Equal(t, 1, strings.Count(records, "NOTE width 0: the beat answer names no width for this friend"), "said once:\n%s", records)
		assert.Contains(t, records, "nova-config friend set bob --width <n>", "the remedy is nova-config's")
		assert.Equal(t, 0, r.last().Width, "the status says the answered width, 0")
	})
}
