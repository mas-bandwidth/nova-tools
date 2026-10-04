package friend

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend/keepalive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merge keeps the dedicated proof machine and the renamed ordinary bus
// independent: an asleep daemon answers its coordinator without a model turn.
func TestTheKeepaliveAndSleepHoldUnderTheRename(t *testing.T) {
	t.Parallel()
	seat := keepalive.Seat{Holder: "ada", Epoch: 1, Generation: 1}
	a, err := keepalive.New("ada", "bob", "coordinator", "a1", seat)
	require.NoError(t, err)
	b, err := keepalive.New("bob", "ada", "friend", "b1", seat)
	require.NoError(t, err)
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		af, due, err := a.Next(at, false)
		require.NoError(t, err)
		require.True(t, due)
		bf, due, err := b.Next(at, true)
		require.NoError(t, err)
		require.True(t, due)
		proved, err := a.Observe(at, bf)
		require.NoError(t, err)
		assert.Equal(t, i == 1, proved)
		proved, err = b.Observe(at, af)
		require.NoError(t, err)
		assert.Equal(t, i == 1, proved)
	}
	assert.True(t, a.Status(now.Add(time.Second)).Up)
	assert.True(t, a.Status(now.Add(time.Second)).Asleep)
	assert.True(t, a.Status(now.Add(11*time.Second-time.Nanosecond)).Up)
	assert.False(t, a.Status(now.Add(11*time.Second)).Up, "proof expires at ten seconds")

	synctest.Test(t, func(t *testing.T) {
		r := asleepRig(t, "")
		r.send(t, "ada", "PING rename-nonce", PingText("ada", t0, "rename-nonce"))
		r.run(t, 5)
		assert.Equal(t, []string{"daemon-pong: daemon-pong rename-nonce asleep=true"}, r.adaGot(t))
		assert.Empty(t, r.delivered, "a daemon-answered PING never costs a model turn")
		state, err := ReadSessionState(r.d.StateDir)
		require.NoError(t, err)
		assert.True(t, state.Asleep, "keepalive does not wake a sleeping session")
	})
}
