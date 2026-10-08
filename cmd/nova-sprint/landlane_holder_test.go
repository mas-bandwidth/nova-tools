package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Two streams with the same first bench must ask as different lane holders. The
// lane record and clock stay in this test; no fleet, process, or socket runs.
func TestEachStreamHoldsItsOwnGateLane(t *testing.T) {
	t.Parallel()
	hosts := []string{"bench-a", "bench-b"}
	first := "stream-0"
	second := ""
	for i := 1; i < 10; i++ {
		candidate := fmt.Sprintf("stream-%d", i)
		if benchRing(candidate, hosts)[0] == benchRing(first, hosts)[0] {
			second = candidate
			break
		}
	}
	require.NotEmpty(t, second)
	ring := benchRing(first, hosts)
	require.Equal(t, ring, benchRing(second, hosts))
	one, two := landLaneWho(first), landLaneWho(second)
	assert.Equal(t, "lander/"+first, one)
	assert.Equal(t, "lander/"+second, two)
	assert.NotEqual(t, one, two)

	now := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)
	lanes := sprint.Lanes{}
	var ans sprint.LaneAnswer
	lanes, ans = lanes.Take(ring[0], one, 1, now)
	require.True(t, ans.Granted)
	var visits []string
	asked := map[string]bool{}
	host := askGateRing(ring, two, asked, func(machine, holder string) (sprint.LaneAnswer, error) {
		visits = append(visits, machine)
		var answer sprint.LaneAnswer
		lanes, answer = lanes.Take(machine, holder, 1, now)
		return answer, nil
	})
	assert.Equal(t, ring, visits, "the sibling is refused on the held slot and steps to the next")
	assert.Equal(t, ring[1], host)
	assert.Equal(t, map[string]bool{ring[0]: true, ring[1]: true}, asked)
	require.Len(t, lanes[ring[0]].Queue, 1)
	assert.Equal(t, two, lanes[ring[0]].Queue[0].Who, "the first slot did not renew the first fork's hold")
	assert.Equal(t, two, lanes[ring[1]].Holders[0].Who)

	lanes, ans = lanes.Give(ring[0], one, 1, now)
	assert.True(t, ans.Gave)
	require.Len(t, lanes[ring[0]].Holders, 1)
	assert.Equal(t, two, lanes[ring[0]].Holders[0].Who, "the first fork's give must preserve the sibling's queued place, now granted")
	assert.Equal(t, two, lanes[ring[1]].Holders[0].Who, "the first fork's give must not remove the sibling's other hold")
}

func TestTheBaseRecheckHasItsOwnGateLaneName(t *testing.T) {
	t.Parallel()
	base := landLaneWho("base")
	assert.Equal(t, "lander/base", base)
	assert.NotEqual(t, landLaneWho("stream-0"), base)
	now := time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)
	lanes, ans := (sprint.Lanes{}).Take("bench-a", base, 1, now)
	require.True(t, ans.Granted)
	lanes, ans = lanes.Take("bench-a", landLaneWho("stream-0"), 1, now)
	assert.False(t, ans.Granted)
	assert.Equal(t, base, lanes["bench-a"].Holders[0].Who)
}
