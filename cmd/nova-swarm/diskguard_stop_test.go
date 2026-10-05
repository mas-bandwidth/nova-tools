package main

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dgStopGuard is dgGuard with a stop floor of 100 GiB, the volume's free disk given, and
// loop units a test lists and records the stops of.
func dgStopGuard(t *testing.T, freeGiB uint64, units ...string) (*guard, *[]string, func() string) {
	t.Helper()
	g, out := dgGuard(t)
	g.floor, g.stopFloor = 200*gib, 100*gib
	g.free = func(string) (uint64, error) { return freeGiB * gib, nil }
	var stopped []string
	g.loopUnits = func() ([]string, error) { return units, nil }
	g.stopUnit = func(u string) error { stopped = append(stopped, u); return nil }
	return g, &stopped, out.String
}

func TestDiskGuardStopsEveryLoopButItsOwnUnderTheStopFloor(t *testing.T) {
	t.Parallel()
	g, stopped, out := dgStopGuard(t, 90, "com.nova.loop.land", "com.nova.loop.disk-guard-m1", "com.nova.loop.mirror-refresh-m1")
	assert.Equal(t, 0, g.run())
	assert.Equal(t, []string{"com.nova.loop.land", "com.nova.loop.mirror-refresh-m1"}, *stopped, "the guard's own loop runs on")
	assert.Contains(t, out(), "DISK-GUARD STOPPED unit=com.nova.loop.land free="+strconv.FormatInt(90*gib, 10))
	assert.Contains(t, out(), "DISK-GUARD WARN", "the stop floor is under the warning floor, which warns too")
}

func TestDiskGuardStopsNothingAboveTheStopFloorOrWithNone(t *testing.T) {
	t.Parallel()
	g, stopped, _ := dgStopGuard(t, 150, "nova-loop-land.service")
	assert.Equal(t, 0, g.run())
	assert.Empty(t, *stopped, "150 GiB is above the stop floor")

	g, stopped, _ = dgStopGuard(t, 5, "nova-loop-land.service")
	g.stopFloor = 0
	assert.Equal(t, 0, g.run())
	assert.Empty(t, *stopped, "no --stop-floor stops nothing")
}

func TestDiskGuardDryRunSaysWhatItWouldStop(t *testing.T) {
	t.Parallel()
	g, stopped, out := dgStopGuard(t, 90, "nova-loop-land.service", "nova-loop-land.timer")
	g.dry = true
	assert.Equal(t, 0, g.run())
	assert.Empty(t, *stopped)
	assert.Equal(t, 2, strings.Count(out(), "DISK-GUARD WOULD-STOP unit=nova-loop-land."))
}

func TestDiskGuardSaysAStopItCouldNotMake(t *testing.T) {
	t.Parallel()
	g, _, out := dgStopGuard(t, 90, "nova-loop-a.service")
	g.stopUnit = func(string) error { return errors.New("permission denied") }
	assert.Equal(t, 1, g.run(), "a loop left running under the stop floor is an incomplete run")
	assert.Contains(t, out(), "nova-loop-a.service could not be stopped (permission denied)")

	g, _, out = dgStopGuard(t, 90)
	g.loopUnits = func() ([]string, error) { return nil, errors.New("no systemd user instance") }
	assert.Equal(t, 1, g.run())
	assert.Contains(t, out(), "the loop units could not be listed (no systemd user instance); stop them by hand")
}

func TestDiskGuardReadsTheLoopUnitsOfEachSupervisor(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"com.nova.loop.land", "com.nova.loop.disk-guard"},
		launchctlLoops("PID\tStatus\tLabel\n412\t0\tcom.nova.loop.land\n-\t0\tcom.nova.redis\n-\t0\tcom.nova.loop.disk-guard\n"))
	assert.Equal(t, []string{"nova-loop-land.service", "nova-loop-land.timer"},
		systemctlLoops("nova-loop-land.service loaded active running nova loop land\nnova-loop-land.timer loaded active waiting nova loop land\n"))
}

func TestDiskGuardRefusesAStopFloorAtOrAboveTheWarning(t *testing.T) {
	t.Parallel()
	var out, errs bytes.Buffer
	require.Equal(t, 2, swarmRun([]string{"disk-guard", "--disk-floor", "100", "--stop-floor", "100", "--dry-run"}, &out, &errs))
	assert.Contains(t, errs.String(), "--stop-floor is 0 (off) or more GiB below --disk-floor 100, got 100")
}
