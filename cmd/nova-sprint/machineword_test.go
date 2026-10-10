package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A worker's verb carries the machine's word, so a member cancels its lanes on STOPPED
// (pkg/member machineStop) and a friend's daemon too (pkg/friend/stop.go): the
// queue's JSON has "machine", the friend's beat answer "machine=", each RUNNING or STOPPED
// from the machine's first record (init writes one: a new sprint is STOPPED), and nothing
// on a store without the records. The beat also takes the stop-returns a friend's
// lanes still owe (--stop-returns), which start waits on (docs/SPEC-SPRINT.md section 14).
func TestWorkerVerbsCarryTheMachinesWord(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	ta.pong("amy")
	var q struct {
		Machine string `json:"machine"`
	}
	ta.json("queue --as friend.amy", &q)
	assert.Equal(t, "STOPPED", q.Machine, "a new sprint is STOPPED (section 14): the word says so from the first record")
	out := ta.ok("friend beat amy --working 1")
	assert.Contains(t, out, " machine=STOPPED")

	ta.ok("start")
	ta.json("queue --as friend.amy", &q)
	assert.Equal(t, "RUNNING", q.Machine)
	out = ta.ok("friend beat amy --working 1")
	assert.Contains(t, out, " machine=RUNNING")

	ta.ok("stop --reason 'the bench is rebooting' --until 30m")
	ta.json("queue --as friend.amy", &q)
	assert.Equal(t, "STOPPED", q.Machine)
	out = ta.ok("friend beat amy --working 0 --stop-returns 2")
	assert.Contains(t, out, " stop_returns=2")
	assert.Contains(t, out, " machine=STOPPED")
	var b struct {
		StopReturns int    `json:"stop_returns"`
		Machine     string `json:"machine"`
	}
	ta.json("friend beat amy --working 0 --stop-returns 2", &b)
	require.Equal(t, 2, b.StopReturns)
	assert.Equal(t, "STOPPED", b.Machine)
}
