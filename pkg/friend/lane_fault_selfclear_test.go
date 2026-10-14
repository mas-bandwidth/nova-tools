package friend

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These two tests use only the daemon's surface from before the change and literal bounds (the
// hold is fifteen minutes), so they run on the code before it and fail there: the lane fault
// stood until a restart.

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

// A lane fault stands for the hold and no longer: until then the lane takes no card and the
// fault file stands, so a real refusal is never retried each minute (tla/LaneFault.tla).
func TestALaneFaultStandsUntilTheHoldIsOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h, stateDir := laneFaultedByTheDetector(t)
		r.run(t, 14*60)
		turns, _, _ := h.got()
		assert.Empty(t, turns, "a faulted lane takes no card inside the hold")
		assert.NotEmpty(t, ReadLaneFault(stateDir), "the fault file stands inside the hold")
		assert.NotContains(t, strings.Join(r.records, "\n"), "lane fault cleared", "nothing cleared it yet")
	})
}

// A lane fault clears itself after the hold, not only by a restart: the watch is cleared, the
// fault file removed, the clearing on the record, and the lane takes its card again (emma's
// lanes sat locked on a refusal read as "{" until a person restarted her daemon).
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
