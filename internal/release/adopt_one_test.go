package release

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One machine has one adoption in flight: a second Start while it runs, or of
// the episode it ran, starts nothing; an adoption that cannot run says why.
func TestOneMachineAdoptsOneAtATime(t *testing.T) {
	t.Parallel()
	o := &OneMachine{Version: "not a version", Flags: []string{"--no-certify"}, Dir: t.TempDir()}
	_, ok := o.Run("m1", "e1")
	assert.False(t, ok, "none started")
	require.True(t, o.Start("m1", o.Version, "e1"))
	assert.False(t, o.Start("m1", o.Version, "e1"), "in flight, or ran: never twice")
	var run OneRun
	require.Eventually(t, func() bool { run, _ = o.Run("m1", "e1"); return !run.Running }, 10*time.Second, 10*time.Millisecond)
	assert.Contains(t, run.Err, "no release to adopt")
	assert.False(t, o.Start("m1", o.Version, "e1"), "the episode ran")
	assert.True(t, o.Start("m1", o.Version, "e2"), "a new episode starts")
}

// A machine name adopt would refuse is refused before any list is written.
func TestOneMachineRefusesANameAdoptRefuses(t *testing.T) {
	t.Parallel()
	o := &OneMachine{Version: "v1.2.0", Dir: t.TempDir()}
	_, _, err := o.adopt("-V", "v1.2.0")
	assert.ErrorContains(t, err, "not a machine name")
}

// The installed version is read off a dry run's line for the machine.
func TestWouldLineReadsTheInstalledVersion(t *testing.T) {
	t.Parallel()
	m := wouldLine.FindStringSubmatch("RELEASE WOULD ADOPT machine=hulk version=v1.2.0 installed=v1.1.0 dest=yes action=install bin=~/bin")
	require.Len(t, m, 3)
	assert.Equal(t, []string{"hulk", "v1.1.0"}, m[1:])
}
