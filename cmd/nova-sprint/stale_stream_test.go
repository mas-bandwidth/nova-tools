package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// staleGroup is the inbox's stale-stream group of the stream, if it shows one.
func (ta *testApp) staleGroup(stream string) (sprint.Group, bool) {
	for _, g := range ta.inboxGroups() {
		if g.Type == sprint.NStreamStale && g.Stream == stream {
			return g, true
		}
	}
	return sprint.Group{}, false
}

// after steps the running clock on by d, a tick every ten minutes.
func (ta *testApp) after(d time.Duration) {
	for ; d > 0; d -= 10 * time.Minute {
		ta.mu.Lock()
		ta.now = ta.now.Add(min(d, 10*time.Minute))
		ta.mu.Unlock()
		ta.ok("tick")
	}
}

// The first real sprint, at epoch 15: a docs stream whose only cards wait behind an
// unreleased round-2 sentinel, by design, while other work moves. It is held, not stale:
// its cards wait on the stop, as the table shows (docs/SPEC-SPRINT.md section 8).
func TestAStreamWhoseCardsAllWaitIsNotStale(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("clear --confirm sprint --actor lead")
	ta.ok("add --stream s1 --count 1 --actor lead")
	ta.ok("add --stream docs --sentinel docs-round2 --actor lead")
	ta.ok("add --stream docs --count 2 --actor lead")
	ta.ok("start --actor lead")
	ta.after(90 * time.Minute)
	g, stale := ta.staleGroup("docs")
	assert.False(t, stale, "a stream whose cards all wait behind its sentinel is stale: %+v", g)
}

// A stale-stream judgment carries the sprint's epoch like every other note, and wait
// quiets it for its period: at epoch 1 its id names epoch 1, wait takes it, and the inbox
// does not show it again until the review time has passed.
func TestAStaleStreamCarriesTheEpochAndWaitQuietsIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	ta.ok("clear --confirm sprint --actor lead")
	ta.ok("add --stream s1 --count 1 --actor lead")
	ta.ok("start --actor lead")
	ta.mu.Lock()
	ta.live = nil // no member beats: the ready card does not move
	ta.mu.Unlock()
	ta.after(100 * time.Minute)
	g, stale := ta.staleGroup("s1")
	require.True(t, stale, "s1 has not moved in an hour: %+v", ta.inboxGroups())
	assert.Equal(t, uint64(1), sprint.IDEpoch(g.ID), "the stale judgment names the sprint's epoch: %s", g.ID)
	out := ta.ok("wait " + g.ID + " --for 2h --actor lead")
	assert.True(t, strings.HasPrefix(out, "WAIT OK"), out)
	ta.after(30 * time.Minute)
	_, stale = ta.staleGroup("s1")
	assert.False(t, stale, "quiet for the period wait set")
	ta.after(2 * time.Hour)
	_, stale = ta.staleGroup("s1")
	assert.True(t, stale, "stale again once the period has passed")
}
