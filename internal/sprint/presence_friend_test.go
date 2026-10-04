package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A friend is down after FriendDownAfter (15 s) without a beat, and a beat
// wakes her at once; a hold is held whatever she beats. The fleet's rule is
// longer, and its word is down: at 16 s a machine with the same beat is up.
func TestAFriendIsDownAfterFifteenSecondsWithoutABeat(t *testing.T) {
	t.Parallel()
	b := Beat{At: p0}
	cases := []struct {
		at   time.Duration
		held bool
		want string
	}{
		{0, false, Up},
		{14 * time.Second, false, Up},
		{FriendDownAfter, false, Down},
		{16 * time.Second, false, Down},
		{time.Hour, false, Down},
		{14 * time.Second, true, Held},
		{16 * time.Second, true, Held},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, FriendStatus(c.held, b, p0.Add(c.at)), "beat at t, status at t+%v held=%v", c.at, c.held)
	}
	assert.Equal(t, Up, PresenceStatus(false, b, p0.Add(16*time.Second)), "a machine's rule is separate and longer")

	again := p0.Add(16 * time.Second)
	assert.Equal(t, Up, FriendStatus(false, Beat{At: again}, again), "a beat at t+16 s is up at once")
	assert.Equal(t, Down, FriendStatus(false, Beat{}, p0), "never beaten is down")
}

func TestAsleepFriendStillExpiresAndKeepsMachinePresenceSeparate(t *testing.T) {
	t.Parallel()
	b := Beat{At: p0, Asleep: true}
	for _, c := range []struct {
		name string
		at time.Duration
		held bool
		want string
	}{
		{"fresh", 0, false, "asleep"},
		{"before expiry", 14*time.Second, false, "asleep"},
		{"expired", FriendDownAfter, false, Down},
		{"held fresh", 0, true, Held},
		{"held expired", FriendDownAfter, true, Held},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, FriendStatus(c.held, b, p0.Add(c.at)))
		})
	}
	assert.Equal(t, Up, PresenceStatus(false, b, p0), "sleep is a friend state, not machine presence")
	assert.Equal(t, Down, FriendStatus(false, Beat{Asleep: true}, p0), "sleep without a beat is down")
}
