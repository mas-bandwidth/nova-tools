package friend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestKeepaliveCountsOnlyAFreshNonceOfThatFriend(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	cases := []struct {
		name   string
		from   string
		nonce  string
		sentAt int // when the nonce went to bob
		readAt int
		counts bool
	}{
		{"bob's nonce, fresh", "bob", "n1", 0, 1, true},
		{"bob's nonce, at the bound", "bob", "n1", 0, 10, false},
		{"a nonce never sent", "bob", "zz", 0, 1, false},
		{"bob's nonce from another friend", "cy", "n1", 0, 1, false},
		{"from no friend row", "eve", "n1", 0, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			k := NewKeepalive()
			k.Friends(at(0), []string{"bob", "cy"})
			k.Sent("bob", "n1", at(c.sentAt))
			assert.Equal(t, c.counts, k.Pong(c.from, c.nonce, at(c.readAt)))
		})
	}
}

func TestKeepaliveSaysEachChangeOnce(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	k := NewKeepalive()
	k.Friends(at(0), []string{"bob", "cy"})
	k.Sent("bob", "n1", at(0))
	assert.True(t, k.Pong("bob", "n1", at(1)))
	assert.False(t, k.Pong("bob", "n1", at(2)), "a replayed nonce answers nothing")
	assert.Equal(t, []Change{{Friend: "bob", State: PeerUp, LastPong: at(1)}}, k.Step(at(1)))
	assert.Empty(t, k.Step(at(9)), "cy is undecided before the bound, bob still up")
	k.Failed("cy", "cy is no known name")
	assert.Equal(t, []Change{{Friend: "cy", State: PeerDown, Reason: "no pong for 10s; the ping could not be sent: cy is no known name"}}, k.Step(at(10)))
	assert.Equal(t, []Change{{Friend: "bob", State: PeerDown, LastPong: at(1), Reason: "no pong for 10s"}}, k.Step(at(11)))
	assert.Empty(t, k.Step(at(30)), "down is said once")
	k.Sent("bob", "n2", at(30))
	assert.True(t, k.Pong("bob", "n2", at(31)))
	assert.Equal(t, []Change{{Friend: "bob", State: PeerUp, LastPong: at(31)}}, k.Step(at(31)), "a pong brings a down friend back")
	k.Friends(at(32), []string{"bob", "dee"})
	assert.Equal(t, []string{"bob", "dee"}, k.Names(), "a removed row is forgotten, an added one pinged")
	assert.Equal(t, []Change{
		{Friend: "bob", State: PeerDown, LastPong: at(31), Reason: "no pong for 10s"},
		{Friend: "dee", State: PeerDown, Reason: "no pong for 10s"},
	}, k.Step(at(42)), "a new row's bound runs from when it was read")
}

func TestPongNonceReadsBothPongs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		body  string
		nonce string
		ok    bool
	}{
		{"daemon-pong n1\n", "n1", true},
		{"pong n1 queue=1 working=0 width=4\n", "n1", true},
		{"PING n1", "", false},
		{"hello", "", false},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			t.Parallel()
			n, ok := PongNonce(c.body)
			assert.Equal(t, c.ok, ok)
			assert.Equal(t, c.nonce, n)
		})
	}
}
