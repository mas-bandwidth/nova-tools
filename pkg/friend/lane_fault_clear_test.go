package friend

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// laneFaultedByTheDetector is a rig whose limit detector hands the daemon one lane fault at the
// start (lanes failing alike), the fault file in a state directory, and a card c1 queued that the
// lane would finish if it took it.
func laneFaultedByTheDetector(t *testing.T) (*rig, *lanesHarness, string) {
	t.Helper()
	dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
	h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
	r, _ := laneRig(t, h, 1)
	stateDir := t.TempDir()
	r.d.Fault = func() string { return ReadLaneFault(stateDir) }
	r.d.FaultWrite = func(report string) error { return WriteLaneFault(stateDir, report) }
	said := false
	r.d.FaultExternal = func() string {
		if said {
			return ""
		}
		said = true
		return `lane-fault=3 refusal="dsh: unknown session id"`
	}
	return r, h, stateDir
}

// A lane fault stands for LaneFaultHold and no longer: until then the lane takes no card and the
// fault file stands, so a real refusal is never retried each minute (tla/LaneFault.tla FaultBounded).
func TestALaneFaultStandsUntilTheHoldIsOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h, stateDir := laneFaultedByTheDetector(t)
		r.run(t, int((LaneFaultHold-time.Minute)/time.Second))
		turns, _, _ := h.got()
		assert.Empty(t, turns, "a faulted lane takes no card inside the hold")
		assert.NotEmpty(t, ReadLaneFault(stateDir), "the fault file stands inside the hold")
		assert.NotContains(t, strings.Join(r.records, "\n"), "lane fault cleared", "nothing cleared it yet")
	})
}

// A lane fault clears itself after LaneFaultHold, not only by a restart: the watch is cleared, the
// fault file removed, the clearing on the record, and the lane takes its card again (emma's
// lanes sat locked on a refusal read as "{" until a person restarted her daemon). This test uses
// only the daemon's old surface and a literal bound, so it fails on the code before the change.
func TestALaneFaultClearsItselfAfterTheHold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h, stateDir := laneFaultedByTheDetector(t)
		r.run(t, 15*60+120)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "lane fault cleared after 15m0s", records)
		assert.Empty(t, ReadLaneFault(stateDir), "the fault file is removed with the fault")
		turns, _, _ := h.got()
		require.NotEmpty(t, turns, "the lane takes cards again once the fault cleared itself: %s", records)
		assert.Contains(t, turns[0], "c1")
	})
}

// A watch that was cleared counts a fresh run of refusals anew: a fault that clears itself and
// then meets the same refusal twice faults again, one judgment per episode.
func TestAClearedLaneFaultCountsAnew(t *testing.T) {
	t.Parallel()
	var w LaneFaultWatch
	_, yes := w.Observe("content_filter_error: no")
	require.False(t, yes)
	_, yes = w.Observe("content_filter_error: no")
	require.True(t, yes)
	require.True(t, w.Faulted())
	w.Clear()
	require.False(t, w.Faulted())
	_, yes = w.Observe("content_filter_error: no")
	assert.False(t, yes, "the first refusal after a clear is the first of a new run")
	_, yes = w.Observe("content_filter_error: no")
	assert.True(t, yes, "the second faults again")
}

// A refusal that is not a real refusal, an empty line or a bare brace, is no lane fault however
// often it is seen: it is a read fault, and counts toward nothing.
func TestABareLineIsNeverALaneFault(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"", " ", "{", "}", "[{", "{}", "[]", `{"`, ",", `"":`} {
		assert.True(t, BareLine(line), "%q has no word", line)
		var w LaneFaultWatch
		for i := 0; i < 2*LaneFaultAlike; i++ {
			_, yes := w.Observe(line)
			assert.False(t, yes, "%q", line)
		}
		assert.False(t, w.Faulted(), "%q", line)
		assert.Empty(t, w.Report(), "%q", line)
	}
	for _, line := range []string{`"error": "boom"`, "a", "1", "error: no", `{"error":"x"}`} {
		assert.False(t, BareLine(line), "%q has a word", line)
	}
}
