package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The where view's machine line (docs/SPEC-SPRINT.md section 1; WhereMachineLine): a
// RUNNING machine whose last tick is older than MachineSilence reads running with the tick
// late by n seconds, never STOPPED; STOPPED is a stop only. The clock is the caller's.
func TestWhereMachineLineReadsALateTickAsRunning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 14, 30, 0, 0, time.UTC)
	running := Machine{State: Running, Since: now.Add(-time.Hour)}
	tick := func(ago time.Duration) Heartbeat { return Heartbeat{At: now.Add(-ago)} }

	assert.Equal(t, "machine: running", WhereMachineLine(now, running, tick(3*time.Second)), "a recent tick")
	assert.Equal(t, "machine: running", WhereMachineLine(now, running, tick(MachineSilence)), "at the silence, not past it")
	assert.Equal(t, "machine: running (tick late 16s)", WhereMachineLine(now, running, tick(16*time.Second+900*time.Millisecond)), "whole seconds")
	assert.Equal(t, "machine: running (tick late 300s)", WhereMachineLine(now, running, tick(5*time.Minute)))
	assert.Equal(t, "machine: STOPPED", MachineLine(now, running, tick(16*time.Second)), "MachineLine, the inbox's and the driver's, is unchanged")

	started := Machine{State: Running, Since: now.Add(-2 * time.Second)}
	assert.Equal(t, "machine: running", WhereMachineLine(now, started, tick(time.Hour)), "a start is as good as a tick")

	stopped := Machine{State: Stopped, Since: now.Add(-time.Minute)}
	assert.Equal(t, "machine: STOPPED", WhereMachineLine(now, stopped, tick(time.Hour)), "a stop")
	assert.Equal(t, "machine: STOPPED", WhereMachineLine(now, stopped, tick(time.Second)), "a stop with a fresh look")
	funds := Machine{State: Stopped, Since: now, Cause: sprint.FundsCause}
	assert.Equal(t, "machine: STOPPED ("+sprint.FundsCause+")", WhereMachineLine(now, funds, tick(time.Hour)))
	done := Machine{State: Stopped, Since: now, Cause: sprint.DoneCause}
	assert.Equal(t, "machine: "+DoneState, WhereMachineLine(now, done, tick(time.Hour)))
}
