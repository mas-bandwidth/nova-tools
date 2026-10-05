package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Go lane is a per-machine semaphore the machine grants (docs/SPEC-SPRINT.md section
// 18): a take is granted while the machine's holders are under its width and nobody
// waits ahead, else queued behind the waiters in the order they asked; a give, a hold
// not renewed within LaneHoldFor, a grant not claimed within LaneWaitFor and a waiter
// that stops asking for LaneWaitFor each free the place, and the head of the queue is
// granted. The clock is a value and the store is the Lanes value the steps move: no
// real time and no socket.
func TestGoLaneIsAPerMachineSemaphoreGrantedByTheMachine(t *testing.T) {
	t.Parallel()
	type op struct {
		at      time.Duration // from p0
		verb    string        // take or give
		machine string
		who     string
		width   int
		granted bool // take: the answer
		place   int  // take: the place in the queue when not granted
	}
	cases := []struct {
		name string
		ops  []op
		// the holders and waiters of machine m1 after the last op, in order
		held, waiting []string
	}{
		{name: "one lane: the first take is granted and the second waits", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
		}, held: []string{"a"}, waiting: []string{"b"}},
		{name: "a take again by the holder renews and is granted", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Minute, "take", "m1", "a", 1, true, 0},
		}, held: []string{"a"}},
		{name: "the lanes are per machine", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{0, "take", "m2", "b", 1, true, 0},
		}, held: []string{"a"}},
		{name: "the width is the machine's lanes", ops: []op{
			{0, "take", "m1", "a", 2, true, 0},
			{0, "take", "m1", "b", 2, true, 0},
			{0, "take", "m1", "c", 2, false, 1},
		}, held: []string{"a", "b"}, waiting: []string{"c"}},
		{name: "the queue is fair: a give grants the head, never a later asker", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{2 * time.Second, "take", "m1", "c", 1, false, 2},
			{3 * time.Second, "give", "m1", "a", 1, false, 0},
			{4 * time.Second, "take", "m1", "c", 1, false, 1},
			{5 * time.Second, "take", "m1", "b", 1, true, 0},
		}, held: []string{"b"}, waiting: []string{"c"}},
		{name: "a newcomer waits behind the queue even with room made", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{2 * time.Second, "give", "m1", "a", 1, false, 0},
			{3 * time.Second, "take", "m1", "d", 1, false, 1},
		}, held: []string{"b"}, waiting: []string{"d"}},
		{name: "a wider width grants the head of the queue, never the newcomer", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{2 * time.Second, "take", "m1", "d", 2, false, 1},
		}, held: []string{"a", "b"}, waiting: []string{"d"}},
		{name: "a hold not renewed is released after LaneHoldFor and the head is granted", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{LaneHoldFor - time.Second, "take", "m1", "b", 1, false, 1},
			{LaneHoldFor + time.Second, "take", "m1", "b", 1, true, 0},
		}, held: []string{"b"}},
		{name: "a grant not claimed within LaneWaitFor goes to the next", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{2 * time.Second, "take", "m1", "c", 1, false, 2},
			{3 * time.Second, "give", "m1", "a", 1, false, 0},
			{30 * time.Second, "take", "m1", "c", 1, false, 1},
			{3*time.Second + LaneWaitFor + time.Second, "take", "m1", "c", 1, true, 0},
		}, held: []string{"c"}},
		{name: "a waiter that stops asking leaves the queue", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{2 * time.Second, "take", "m1", "c", 1, false, 2},
			{2*time.Second + LaneWaitFor, "take", "m1", "a", 1, true, 0},
			{2*time.Second + LaneWaitFor, "take", "m1", "c", 1, false, 1},
		}, held: []string{"a"}, waiting: []string{"c"}},
		{name: "a give of a waiter takes it off the queue", ops: []op{
			{0, "take", "m1", "a", 1, true, 0},
			{time.Second, "take", "m1", "b", 1, false, 1},
			{2 * time.Second, "give", "m1", "b", 1, false, 0},
		}, held: []string{"a"}},
		{name: "a narrower width takes no lane back and grants none past it", ops: []op{
			{0, "take", "m1", "a", 2, true, 0},
			{0, "take", "m1", "b", 2, true, 0},
			{time.Second, "take", "m1", "c", 1, false, 1},
			{2 * time.Second, "give", "m1", "a", 1, false, 0},
			{3 * time.Second, "take", "m1", "c", 1, false, 1},
		}, held: []string{"b"}, waiting: []string{"c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lanes := Lanes{} // the store: the record the steps read and write
			for i, o := range tc.ops {
				now := p0.Add(o.at)
				switch o.verb {
				case "take":
					var ans LaneAnswer
					lanes, ans = lanes.Take(o.machine, o.who, o.width, now)
					assert.Equal(t, o.granted, ans.Granted, "op %d: %s takes on %s", i, o.who, o.machine)
					if !o.granted {
						assert.Equal(t, o.place, ans.Place, "op %d: %s's place in the queue", i, o.who)
					}
				case "give":
					lanes, _ = lanes.Give(o.machine, o.who, o.width, now)
				default:
					require.FailNow(t, "unknown verb "+o.verb)
				}
			}
			l := lanes["m1"]
			assert.Equal(t, tc.held, l.holders(), "the holders of m1")
			assert.Equal(t, tc.waiting, l.waiters(), "the waiters of m1")
		})
	}
}
