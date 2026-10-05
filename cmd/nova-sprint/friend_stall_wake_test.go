package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A stalled friend is woken by the tick itself: nova-sprint tick deals her
// card, and the next tick, past the stall bound, sends one stall-wake bus
// message. The peek that asks whether the part has work sends nothing.
func TestAStalledFriendIsWokenByTheTick(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.ok("friend beat amy")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-1.md"), []byte(passingBrief("s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	require.Empty(t, stallWakes(ta), "a friend just dealt is not stalled")

	ta.mu.Lock()
	ta.now = ta.now.Add(sprint.FriendStallAfterDefault + time.Minute)
	ta.sent = nil
	ta.mu.Unlock()
	ta.ok("tick")

	woke := stallWakes(ta)
	require.Len(t, woke, 1)
	require.Equal(t, []string{"amy"}, woke[0].To)
	require.Contains(t, woke[0].Subject, "stall wake")
	require.Contains(t, woke[0].Subject, "turn 1")
}

func stallWakes(ta *testApp) []bus.Message {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	var out []bus.Message
	for _, m := range ta.sent {
		if strings.Contains(m.Subject, "stall wake") {
			out = append(out, m)
		}
	}
	return out
}
