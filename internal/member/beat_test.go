package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestABeatWithNoSampleStillCarriesLoad: a member whose meter has no sample still sends
// `--load <pct>` on every beat. The value is the last sample it had, else 0. A beat is
// never sent without --load (docs/SPEC-SPRINT.md, the fleet): v1.2.11's CLI refuses a beat
// without it, and on darwin/amd64 the meter never had a sample.
func TestABeatWithNoSampleStillCarriesLoad(t *testing.T) {
	t.Parallel()
	meter, _ := secondsOfLoad()
	g := newRig(Config{As: "m", Width: 1, Meter: meter})
	g.s.set("queue", 0, queueJSON(t, 7))
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0"}, g.s.lines("beat"),
		"no sample: the beat names 0, never a beat without --load")
}

// TestABeatNamesLoadUnmeasuredWhenTheCLIListsIt: when the installed nova-sprint's
// `fleet beat -h` lists --load-unmeasured (probed once), a beat with no sample carries it,
// so the server reads a load of 0 as "no sample yet" and not an idle machine.
func TestABeatNamesLoadUnmeasuredWhenTheCLIListsIt(t *testing.T) {
	t.Parallel()
	meter, _ := secondsOfLoad()
	g := newRig(Config{As: "m", Width: 1, Meter: meter})
	g.s.set("queue", 0, queueJSON(t, 7))
	g.s.set("beathelp", 0, "usage: fleet beat <member> --stop-returns <n> --load <percent> --load-unmeasured")
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0 --load-unmeasured"}, g.s.lines("beat"),
		"the CLI lists the flag: the no-sample beat says so")
	require.Equal(t, []string{"fleet beat -h"}, g.s.lines("beathelp"), "the probe runs once at start")
}
