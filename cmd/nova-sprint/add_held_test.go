package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// add --held (nova-tools#5096 item 15, the wave-2 card builder: "a sentinel
// at the head of an empty stream is 'reached' at once and raises a
// judgment"): a sentinel admitted held at the head of an empty stream is not
// reached, writes no judgment, and holds the wave loaded behind it through a
// tick, with check clean (the no-stall rule reads the hold as the
// coordinator's); release lands it as it lands a reached one. A card admitted
// held stays waiting with nothing before it until release lets it go.
func TestAddHeldLoadsAWaveBehindAQuietSentinel(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	out := ta.ok("add --stream w --sentinel gate --held --actor lead")
	require.Contains(t, out, "MOVED sentinel gate -> waiting stream=w score=1; held until release", "add --held")
	require.NotContains(t, out, "reached", "a held sentinel is never reached")
	ta.ok("add --stream w a b --actor lead")
	ta.ok("add --stream v c --held --actor lead")
	ta.ok("start --actor lead")
	ta.ok("tick")
	for _, g := range ta.inboxGroups() {
		require.NotEqual(t, sprint.NSentinelReached, g.Type, "a held sentinel raised a judgment: %+v", g)
		require.NotEqual(t, sprint.NStalled, g.Type, "a held card is a stall: %+v", g)
	}
	for _, id := range []string{"gate", "a", "b", "c"} {
		require.Equal(t, string(sprint.Waiting), ta.primary(id).Col, "%s left waiting while held", id)
	}
	ta.clean()
	out = ta.ok("release gate c --reason 'the wave is loaded' --actor lead")
	require.Contains(t, out, "sentinel gate waiting -> landed (released by lead); 2 cards are now ready", "release of a held sentinel")
	require.Contains(t, out, "c waiting -> ready (released by lead)", "release of a held card")
	ta.ok("tick") // a RUNNING machine applies the coordinator's verbs at its tick
	for _, id := range []string{"a", "b", "c"} {
		require.NotEqual(t, string(sprint.Waiting), ta.primary(id).Col, "%s still waiting after the release", id)
	}
	ta.clean()
}
