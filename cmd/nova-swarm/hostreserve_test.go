package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAMemberOnTheServerHostRunsItsCardsInTheBackground pins the server host
// reserve (docs/SPEC-SWARM.md, the server host reserve): the pure policy, the
// GOMAXPROCS it puts on a card's environment, the background hook the launch
// calls once for the leader, the one MEMBER RESERVE line, and the zero policy a
// member falls back to when it cannot tell it is on the server host.
func TestAMemberOnTheServerHostRunsItsCardsInTheBackground(t *testing.T) {
	t.Parallel()

	t.Run("hostReserve", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name       string
			serverHost bool
			goos       string
			cores      int
			want       reservePolicy
		}{
			{"server host darwin 32", true, "darwin", 32, reservePolicy{Background: true, GoMaxProcs: 4}},
			{"server host darwin 8", true, "darwin", 8, reservePolicy{Background: true, GoMaxProcs: 2}},
			{"not server host darwin", false, "darwin", 32, reservePolicy{}},
			{"server host linux", true, "linux", 32, reservePolicy{}},
		}
		for _, c := range cases {
			c := c
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, c.want, hostReserve(c.serverHost, c.goos, c.cores))
			})
		}
	})

	t.Run("card environment", func(t *testing.T) {
		t.Parallel()
		// the member's launch bounds the card's threads on the server host
		on := &nativeRunner{env: []string{"PATH=/bin"}, reserve: reservePolicy{GoMaxProcs: 4}}
		assert.Contains(t, on.childEnvFor("fake/model"), "GOMAXPROCS=4")
		// elsewhere the policy bounds nothing, so no GOMAXPROCS is added
		assert.NotContains(t, reserveEnv([]string{"PATH=/bin"}, reservePolicy{}), "GOMAXPROCS=4")
		// a card env that already sets GOMAXPROCS keeps its value
		own := &nativeRunner{env: []string{"PATH=/bin", "GOMAXPROCS=7"}, reserve: reservePolicy{GoMaxProcs: 4}}
		got := own.childEnvFor("fake/model")
		assert.Contains(t, got, "GOMAXPROCS=7")
		assert.NotContains(t, got, "GOMAXPROCS=4")
	})

	t.Run("background hook", func(t *testing.T) {
		t.Parallel()
		var pids []int
		hook := func(pid int) error { pids = append(pids, pid); return nil }
		require.NoError(t, applyBackground(reservePolicy{Background: true}, 4242, hook))
		require.NoError(t, applyBackground(reservePolicy{}, 99, hook))
		assert.Equal(t, []int{4242}, pids, "the background hook is called exactly once for the leader, never elsewhere")
	})

	t.Run("reserve line printed once", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		printReserve(&buf, true, reservePolicy{Background: true, GoMaxProcs: 4}, 32)
		assert.Equal(t, "MEMBER RESERVE server-host=true background=true gomaxprocs=4 cores=32\n", buf.String())
	})

	t.Run("missing answer means not the server", func(t *testing.T) {
		t.Parallel()
		fail := func() (string, bool) { return "", false }
		onServer := serverHostOf(false, fail, func() (string, error) { return "bench-1", nil })
		assert.False(t, onServer)
		assert.Equal(t, reservePolicy{}, hostReserve(onServer, "darwin", 32))
	})
}
