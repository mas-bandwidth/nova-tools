package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A friend is asleep after FriendAsleepAfter (15 s) without a beat, and a beat
// wakes her at once; a hold is held whatever she beats. The fleet's rule is
// longer, and its word is down: at 16 s a machine with the same beat is up.
func TestAFriendIsAsleepAfterFifteenSecondsWithoutABeat(t *testing.T) {
	t.Parallel()
	b := Beat{At: p0}
	cases := []struct {
		at   time.Duration
		held bool
		want string
	}{
		{0, false, Up},
		{14 * time.Second, false, Up},
		{FriendAsleepAfter, false, Asleep},
		{16 * time.Second, false, Asleep},
		{time.Hour, false, Asleep},
		{14 * time.Second, true, Held},
		{16 * time.Second, true, Held},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, FriendStatus(c.held, b, p0.Add(c.at)), "beat at t, status at t+%v held=%v", c.at, c.held)
	}
	assert.Equal(t, Up, PresenceStatus(false, b, p0.Add(16*time.Second)), "a machine's rule is separate and longer")

	again := p0.Add(16 * time.Second)
	assert.Equal(t, Up, FriendStatus(false, Beat{At: again}, again), "a beat at t+16 s is up at once")
	assert.Equal(t, Asleep, FriendStatus(false, Beat{}, p0), "never beaten is asleep")
}
