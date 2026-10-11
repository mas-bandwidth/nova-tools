/*
Tests for the one box mutation, fuse.MutateBox.

tla/FuseBox.tla models the box these tests drive: two writers over one box, and
the LostUpdate action -- a soft writer publishing the whole snapshot it read
before a lockdown was blown, so the lockdown is gone and both writers reported
success. The invariant these tests are named for is LockdownMonotone (once blown,
a lockdown stays until a lift), and the witness that reaches it is
MCFuseBoxBrokenLostUpdate.cfg.

An interleaving here is an order the test chooses, not a race it hopes to win: a
soft writer's change holds its window inside the lock open on a channel, the
other writer runs while that window is open, and the channels say who went first.
No sleep, no timer and no clock: the two writers meet at the box's lock.
*/
package fuse

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
)

// blow is the lockdown half of a two-writer interleaving: the change a lockdown
// makes, driven through MutateBox like cmdLockdown drives it.
func blow(b Box, readErr error) (Box, bool, error) {
	if readErr != nil {
		return b, false, readErr
	}
	b.Lockdown = &Fuse{At: "2026-10-02T00:00:01Z", Reason: "suspected compromise"}
	return b, true, nil
}

// TestLockdownMonotoneHoldsAgainstAConcurrentQuarantine: a quarantine reads a
// clear box and holds that read while a lockdown is blown and published; the
// lockdown is still standing afterwards, and so is the quarantine. This is
// FuseBox.tla's LostUpdate refused -- before the box's lock, the quarantine
// published its older snapshot over the verified lockdown and both runs answered
// success. The probe in the middle is the other half of the proof: while a
// mutation is inside its window, a second writer cannot take the box's lock.
func TestLockdownMonotoneHoldsAgainstAConcurrentQuarantine(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	require.NoError(t, CreateBox(path))

	atWindow := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	var qErr error
	wg.Go(func() {
		qErr = MutateBox(path, true, func(b Box, readErr error) (Box, bool, error) {
			close(atWindow) // the soft writer has read the box, inside the lock
			<-release       // and holds that read while the lockdown runs
			if readErr != nil {
				return b, false, readErr
			}
			b.Quarantine["discord"] = Fuse{At: "2026-10-02T00:00:00Z", Reason: "abuse"}
			return b, true, nil
		})
	})
	<-atWindow
	_, held := filelock.TryLock(BoxLockPath(path), "a second writer")
	assert.ErrorIs(t, held, filelock.ErrHeld, "a mutation must hold the box's lock across its read and its write")

	var lErr error
	wg.Go(func() { lErr = MutateBox(path, true, blow) })
	close(release)
	wg.Wait()

	require.NoError(t, qErr, "quarantine: %v", qErr)
	require.NoError(t, lErr, "lockdown: %v", lErr)
	b, err := ReadBox(path)
	require.NoError(t, err)
	assert.NotNil(t, b.Lockdown, "LockdownMonotone: a lockdown blown while a quarantine held its read was lost: %+v", b)
	assert.Contains(t, b.Quarantine, "discord", "the quarantine that ran first was lost: %+v", b)
}

// TestLockdownMonotoneHoldsAgainstAConcurrentLift: the same interleaving with the
// other soft power. A lift is the one step that means to make a surface clear
// (FuseBox.tla, OnlyALiftInitOrHandClears), and it is still not a lift of the
// lockdown: the lockdown stands and the surface is lifted.
func TestLockdownMonotoneHoldsAgainstAConcurrentLift(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	write(t, path, `{"lockdown":null,"quarantine":{"discord":{"at":"2026-10-02T00:00:00Z","reason":"abuse"}}}`)

	atWindow := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	var liftErr error
	wg.Go(func() {
		liftErr = MutateBox(path, true, func(b Box, readErr error) (Box, bool, error) {
			close(atWindow)
			<-release
			if readErr != nil {
				return b, false, readErr
			}
			removed := b.LiftQuarantine("discord")
			if len(removed) == 0 {
				return b, false, nil
			}
			return b, true, nil
		})
	})
	<-atWindow
	var lErr error
	wg.Go(func() { lErr = MutateBox(path, true, blow) })
	close(release)
	wg.Wait()

	require.NoError(t, liftErr, "lift: %v", liftErr)
	require.NoError(t, lErr, "lockdown: %v", lErr)
	b, err := ReadBox(path)
	require.NoError(t, err)
	assert.NotNil(t, b.Lockdown, "LockdownMonotone: a lockdown blown while a lift held its read was lost: %+v", b)
	assert.Empty(t, b.Quarantine, "the lift that ran first was lost: %+v", b)
}

// TestNoLostUpdateBetweenTwoQuarantines: two soft writers, one box. A whole-box
// replacement let the second publish erase the first entry, so a surface a run
// had quarantined and verified read as clear again; under the box's lock the
// second writer reads what the first published and both entries stand.
func TestNoLostUpdateBetweenTwoQuarantines(t *testing.T) {
	t.Parallel()

	path := boxIn(t)
	require.NoError(t, CreateBox(path))

	atWindow := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	quarantine := func(surface string) func(Box, error) (Box, bool, error) {
		return func(b Box, readErr error) (Box, bool, error) {
			if readErr != nil {
				return b, false, readErr
			}
			b.Quarantine[surface] = Fuse{At: "2026-10-02T00:00:00Z", Reason: "abuse"}
			return b, true, nil
		}
	}
	var firstErr error
	wg.Go(func() {
		firstErr = MutateBox(path, true, func(b Box, readErr error) (Box, bool, error) {
			next, publish, err := quarantine("discord")(b, readErr)
			close(atWindow)
			<-release
			return next, publish, err
		})
	})
	<-atWindow
	var secondErr error
	wg.Go(func() { secondErr = MutateBox(path, true, quarantine("zulip")) })
	close(release)
	wg.Wait()

	require.NoError(t, firstErr, "the first quarantine: %v", firstErr)
	require.NoError(t, secondErr, "the second quarantine: %v", secondErr)
	b, err := ReadBox(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"discord", "zulip"}, b.Surfaces(), "one of the two quarantines was lost: %+v", b)
}
