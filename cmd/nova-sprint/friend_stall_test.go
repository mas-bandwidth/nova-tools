package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friend stall ladder on the live path (docs/SPEC-SPRINT.md section
// friend-stall-ladder-r.w1; the model is tla/StallLadder.tla): the tick verb's stall
// part plans a wake at rungs 1 and 2 (sprint.Plan.Wakes), and once its step commits
// the binding sends each with wakeFriendStall, a bus message to her, once.

// stallWakes is the stall wakes sent to the friend so far, by subject.
func (ta *testApp) stallWakes(friend string) []string {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	var out []string
	for _, m := range ta.sent {
		if strings.HasPrefix(m.Subject, "stall wake: friend "+friend+" ") {
			out = append(out, m.Subject)
		}
	}
	return out
}

// later moves the test's clock on.
func (ta *testApp) later(d time.Duration) {
	ta.mu.Lock()
	ta.now = ta.now.Add(d)
	ta.mu.Unlock()
}

func TestATickWakesAStalledFriendOnceAtEachWakeRung(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	require.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row, "the card is dealt to amy")
	assert.Empty(t, ta.stallWakes("amy"), "a friend dealt a card now is not stalled")

	// quiet past the stall bound: rung 1, one wake
	ta.later(sprint.FriendStallAfterDefault + time.Minute)
	ta.ok("tick")
	require.Len(t, ta.stallWakes("amy"), 1, "rung 1 wakes her: %v", ta.stallWakes("amy"))
	assert.Contains(t, ta.stallWakes("amy")[0], "turn 1")

	// the same rung again: the rung is written, and no second wake goes
	ta.ok("tick")
	assert.Len(t, ta.stallWakes("amy"), 1, "a rung wakes her once, however many ticks see it")

	// a step on: rung 2, the second wake
	ta.later(sprint.FriendStallStepDefault)
	ta.ok("tick")
	wakes := ta.stallWakes("amy")
	require.Len(t, wakes, 2, "rung 2 wakes her again: %v", wakes)
	assert.Contains(t, wakes[1], "turn 2")

	// a step on: rung 3 is the coordinator's judgment, and no wake
	ta.later(sprint.FriendStallStepDefault)
	ta.ok("tick")
	assert.Len(t, ta.stallWakes("amy"), 2, "rungs past 2 wake no one")
	assert.Contains(t, ta.ok("inbox"), "friend amy stalled 31m0s: coordinator note", "rung 3 tells the coordinator")
}
