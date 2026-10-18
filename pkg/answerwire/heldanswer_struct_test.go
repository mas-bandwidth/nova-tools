package answerwire_test

// The friend cards answer's JSON text keeps the keys of the Go struct it
// replaces (pkg/friend HeldAnswer). An external test: pkg/friend imports
// pkg/bus, which reads the bus send-token record through this package, so the
// package's own tests cannot import pkg/friend.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// heldOf is the table read of the JSON text the struct writes.
func heldOf(t *testing.T, v friend.HeldAnswer) *answerwire.HeldAnswer {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var b answerwire.HeldAnswerBuilder
	require.True(t, b.Init())
	var r answerwire.TableReport
	require.True(t, answerwire.HeldAnswerFromJson(&b, raw, &r), string(raw))
	require.NoError(t, r.Err(), string(raw))
	require.True(t, b.Lock())
	return b.AsConst()
}

func TestHeldAnswerReadsTheStructKeys(t *testing.T) {
	t.Parallel()
	got := heldOf(t, friend.HeldAnswer{Friend: "rowan", Cards: []friend.HeldCard{{Card: "a", Job: "a.w1~21", Col: "working", Epoch: 21, Brief: "/b", Lane: 2}}})
	assert.Equal(t, "rowan", string(got.FriendName[:got.FriendNameLength]))
	require.Equal(t, int32(1), got.Cards.Count)
	c := got.Cards.At(0)
	assert.Equal(t, "a.w1~21", string(c.Job[:c.JobLength]))
	assert.Equal(t, uint64(21), c.Epoch)
	assert.Equal(t, uint32(2), c.Lane)

	// the struct writes a 600 byte brief and a 200 byte job: both read back whole
	long := friend.HeldCard{Card: "a", Job: strings.Repeat("j", 200), Brief: strings.Repeat("b", 600)}
	got = heldOf(t, friend.HeldAnswer{Friend: "rowan", Cards: []friend.HeldCard{long}})
	c = got.Cards.At(0)
	assert.Equal(t, long.Job, string(c.Job[:c.JobLength]))
}
