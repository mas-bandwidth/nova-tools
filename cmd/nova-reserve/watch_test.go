package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeGuard has the parent gone after parentLives checks and the group gone
// after groupLives checks, and records the kill and the sleeps.
func fakeGuard(parentLives, groupLives int) (guard, *int, *[]time.Duration) {
	kills, slept := 0, []time.Duration(nil)
	pc, gc := 0, 0
	return guard{
		parentGone: func() bool { pc++; return pc > parentLives },
		groupGone:  func() bool { gc++; return gc > groupLives },
		killGroup:  func() { kills++ },
		sleep: func(d time.Duration) {
			slept = append(slept, d)
		},
	}, &kills, &slept
}

func TestWatchKillsTheGroupWhenTheParentIsGone(t *testing.T) {
	t.Parallel()
	g, kills, sleeps := fakeGuard(2, 100)
	assert.True(t, g.watch())
	assert.Equal(t, 1, *kills)
	assert.Equal(t, []time.Duration{poll, poll}, *sleeps)
}

func TestWatchLeavesAGroupWhenTheParentOutlivesIt(t *testing.T) {
	t.Parallel()
	g, kills, _ := fakeGuard(100, 3)
	assert.False(t, g.watch())
	assert.Zero(t, *kills)
}

func TestWatchKillsNothingWhenTheGroupIsAlreadyGone(t *testing.T) {
	t.Parallel()
	g, kills, sleeps := fakeGuard(0, 0)
	assert.False(t, g.watch())
	assert.Zero(t, *kills)
	assert.Empty(t, *sleeps)
}
