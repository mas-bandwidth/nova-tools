package sprint

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// windowClock is a fake clock: sleep moves it, so a window test never waits.
type windowClock struct{ t time.Time }

func (c *windowClock) now() time.Time { return c.t }
func (c *windowClock) sleep(d time.Duration) {
	c.t = c.t.Add(d)
}

// windowTable is a fake process table that answers the same list every sweep.
func windowTable(ps ...AdoptWindowProcess) func() ([]AdoptWindowProcess, error) {
	return func() ([]AdoptWindowProcess, error) { return ps, nil }
}

// TestAdoptWindowRefusesAStoppedAgentStillRunning: an agent the adopt stopped
// that is still running at the bound refuses, and the refusal names it by pid
// and label and nothing else.
func TestAdoptWindowRefusesAStoppedAgentStillRunning(t *testing.T) {
	t.Parallel()
	clock := &windowClock{t: time.Date(2026, 10, 7, 19, 42, 0, 0, time.UTC)}
	agent := AdoptWindowAgent{PID: 21128, Label: "com.nova.loop.sprint-server"}
	res, err := WaitAdoptWindow(AdoptWindowOptions{
		Stopped: []AdoptWindowAgent{agent},
		Bound:   DefaultAdoptWindow,
		Now:     clock.now,
		Sleep:   clock.sleep,
		Sweep: windowTable(
			AdoptWindowProcess{PID: 21128, Args: "/opt/nova/bin/nova-sprint run", Mine: true},
			AdoptWindowProcess{PID: 1, Args: "/sbin/launchd"},
		),
	})
	require.NoError(t, err)
	assert.False(t, res.OK(), "a stopped agent still runs at the bound")
	assert.Equal(t, []AdoptWindowAgent{agent}, res.Running)
	assert.Equal(t, DefaultAdoptWindow, res.Waited, "the bound is the wait")
	assert.Equal(t, "these stopped agents still run after 1m0s, so nothing was migrated: 21128 com.nova.loop.sprint-server", res.Refusal())
}

// TestAdoptWindowListsAVerbProcessAsOther: a process of the binary the adopt
// did not stop is listed as OTHER and never refuses, even with every stopped
// agent gone.
func TestAdoptWindowListsAVerbProcessAsOther(t *testing.T) {
	t.Parallel()
	clock := &windowClock{t: time.Date(2026, 10, 7, 19, 42, 0, 0, time.UTC)}
	res, err := WaitAdoptWindow(AdoptWindowOptions{
		Stopped: []AdoptWindowAgent{{PID: 21100, Label: "com.nova.loop.server"}},
		Now:     clock.now,
		Sleep:   clock.sleep,
		Sweep: windowTable(
			AdoptWindowProcess{PID: 21128, Args: "/opt/nova/bin/nova-sprint where --json", Mine: true},
			AdoptWindowProcess{PID: 21129, Args: "/opt/nova/bin/nova-sprint inbox --wait", Mine: true},
		),
	})
	require.NoError(t, err)
	assert.True(t, res.OK(), "a verb process of the binary is never a reason to refuse")
	assert.Empty(t, res.Running)
	assert.Equal(t, []AdoptWindowProcess{
		{PID: 21128, Args: "/opt/nova/bin/nova-sprint where --json", Mine: true},
		{PID: 21129, Args: "/opt/nova/bin/nova-sprint inbox --wait", Mine: true},
	}, res.Others)
	assert.Equal(t, []string{
		"OTHER 21128 /opt/nova/bin/nova-sprint where --json",
		"OTHER 21129 /opt/nova/bin/nova-sprint inbox --wait",
	}, res.OtherLines())
}

// TestAdoptWindowOKWhenEveryStoppedAgentIsGone: every stopped agent gone is
// WINDOW OK with the counts, waited the time it took, others none.
func TestAdoptWindowOKWhenEveryStoppedAgentIsGone(t *testing.T) {
	t.Parallel()
	clock := &windowClock{t: time.Date(2026, 10, 7, 19, 42, 0, 0, time.UTC)}
	res, err := WaitAdoptWindow(AdoptWindowOptions{
		Stopped: []AdoptWindowAgent{
			{PID: 21100, Label: "com.nova.loop.server"},
			{PID: 21101, Label: "com.nova.loop.member"},
		},
		Now:   clock.now,
		Sleep: clock.sleep,
		Sweep: windowTable(AdoptWindowProcess{PID: 1, Args: "/sbin/launchd"}),
	})
	require.NoError(t, err)
	assert.True(t, res.OK())
	assert.Equal(t, 2, res.Stopped)
	assert.Equal(t, time.Duration(0), res.Waited)
	assert.Equal(t, "WINDOW OK stopped=2 waited=0s others=0", res.Line())
}

// TestAdoptWindowWaitsForAStoppedAgentToExit: the window reads the table until
// every stopped agent is gone, and says how long it waited.
func TestAdoptWindowWaitsForAStoppedAgentToExit(t *testing.T) {
	t.Parallel()
	clock := &windowClock{t: time.Date(2026, 10, 7, 19, 42, 0, 0, time.UTC)}
	gone := AdoptWindowAgent{PID: 21100, Label: "com.nova.loop.member"}
	left := AdoptWindowAgent{PID: 21101, Label: "com.nova.loop.server"}
	calls := 0
	sweep := func() ([]AdoptWindowProcess, error) {
		calls++
		if calls < 3 {
			return []AdoptWindowProcess{{PID: left.PID, Args: "nova-sprint run", Mine: true}, {PID: gone.PID, Args: "nova-swarm member"}}, nil
		}
		return nil, nil
	}
	res, err := WaitAdoptWindow(AdoptWindowOptions{Stopped: []AdoptWindowAgent{gone, left}, Now: clock.now, Sleep: clock.sleep, Sweep: sweep})
	require.NoError(t, err)
	assert.True(t, res.OK())
	assert.Equal(t, 2*time.Second, res.Waited, "two one-second sweeps before both were gone")
	assert.Equal(t, "WINDOW OK stopped=2 waited=2s others=0", res.Line())
}

// TestAdoptWindowRefusesOnlyTheAgentStillRunning: a stopped agent that exits
// while another still runs at the bound refuses naming only the one running.
func TestAdoptWindowRefusesOnlyTheAgentStillRunning(t *testing.T) {
	t.Parallel()
	clock := &windowClock{t: time.Date(2026, 10, 7, 19, 42, 0, 0, time.UTC)}
	gone := AdoptWindowAgent{PID: 21100, Label: "com.nova.loop.member"}
	left := AdoptWindowAgent{PID: 21101, Label: "com.nova.loop.server"}
	calls := 0
	sweep := func() ([]AdoptWindowProcess, error) {
		calls++
		if calls == 1 {
			return []AdoptWindowProcess{{PID: gone.PID, Args: "nova-swarm member"}, {PID: left.PID, Args: "nova-sprint run", Mine: true}}, nil
		}
		return []AdoptWindowProcess{{PID: left.PID, Args: "nova-sprint run", Mine: true}}, nil
	}
	res, err := WaitAdoptWindow(AdoptWindowOptions{Stopped: []AdoptWindowAgent{gone, left}, Now: clock.now, Sleep: clock.sleep, Sweep: sweep})
	require.NoError(t, err)
	assert.False(t, res.OK())
	assert.Equal(t, []AdoptWindowAgent{left}, res.Running)
	assert.Contains(t, res.Refusal(), "21101 com.nova.loop.server")
	assert.NotContains(t, res.Refusal(), "21100")
}

// TestAdoptWindowSweepErrorIsReturned: a process table that does not read is
// an error, never a silent pass.
func TestAdoptWindowSweepErrorIsReturned(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("ps did not read")
	clock := &windowClock{t: time.Unix(0, 0)}
	_, err := WaitAdoptWindow(AdoptWindowOptions{
		Stopped: []AdoptWindowAgent{{PID: 1, Label: "com.nova.loop.server"}},
		Now:     clock.now,
		Sleep:   func(time.Duration) {},
		Sweep:   func() ([]AdoptWindowProcess, error) { return nil, sentinel },
	})
	require.ErrorIs(t, err, sentinel)
}

// TestAdoptWindowRefusalNamesNoArguments: the refusal line names each running
// stopped agent's pid and label, never its arguments.
func TestAdoptWindowRefusalNamesNoArguments(t *testing.T) {
	t.Parallel()
	res := AdoptWindowResult{
		Waited:  DefaultAdoptWindow,
		Running: []AdoptWindowAgent{{PID: 7, Label: "com.nova.loop.guard"}},
	}
	line := res.Refusal()
	assert.Contains(t, line, "7 com.nova.loop.guard")
	assert.NotContains(t, line, "--")
	assert.NotContains(t, line, "OTHER")
}
