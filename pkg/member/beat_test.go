package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBeatAlwaysCarriesItsLoad pins the protocol the member owes a nova-sprint that
// cannot measure the worker's machine: every fleet beat names --load, even when the
// meter has no sample at all, the value the last sample it had else 0, and a
// --load-unmeasured flag only when the installed CLI lists it (docs/SPEC-SWARM.md,
// the member).
func TestBeatAlwaysCarriesItsLoad(t *testing.T) {
	t.Parallel()

	t.Run("no sample ever names load 0", func(t *testing.T) {
		t.Parallel()
		meter, _ := secondsOfLoad()
		g := newRig(Config{As: "m", Width: 1, Meter: meter,
			LoadUnmeasured: func() bool { return false }})
		require.NoError(t, g.m.Beat())
		require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, g.s.lines("beat"),
			"a meter with no sample still names a load")
	})

	t.Run("no sample ever carries the flag when the CLI lists it", func(t *testing.T) {
		t.Parallel()
		meter, _ := secondsOfLoad()
		g := newRig(Config{As: "m", Width: 1, Meter: meter,
			LoadUnmeasured: func() bool { return true }})
		require.NoError(t, g.m.Beat())
		require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0 --load-unmeasured"},
			g.s.lines("beat"), "an unmeasured 0 says so when the flag is known")
	})

	t.Run("a sample rides, and the last sample rides again with none new", func(t *testing.T) {
		t.Parallel()
		meter, _ := secondsOfLoad(42)
		g := newRig(Config{As: "m", Width: 1, Meter: meter,
			LoadUnmeasured: func() bool { return true }})
		require.NoError(t, g.m.Beat())
		require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 42.0"}, g.s.lines("beat"))
		g.s.reset()
		require.NoError(t, g.m.Beat())
		require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 42.0"}, g.s.lines("beat"),
			"no sample since names the last one it had, never a bare beat")
	})

	t.Run("the flag is probed once from fleet beat -h", func(t *testing.T) {
		t.Parallel()
		meter, _ := secondsOfLoad()
		g := newRig(Config{As: "m", Width: 1, Meter: meter})
		g.s.set("beat", 0, "usage: fleet beat <member> [--load <pct>] [--load-unmeasured]")
		require.NoError(t, g.m.Beat())
		require.Equal(t, []string{
			"fleet beat -h",
			"fleet beat m --stop-returns 0 --load 0.0 --load-unmeasured",
		}, g.s.lines("beat"), "the probe names the flag, once")
		g.s.reset()
		require.NoError(t, g.m.Beat())
		require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0 --load-unmeasured"},
			g.s.lines("beat"), "the probe is not repeated")
	})
}
