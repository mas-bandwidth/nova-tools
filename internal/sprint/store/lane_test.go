package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lanes on the in-memory store at the harness's clock (docs/SPEC-SPRINT.md section
// 18): the width is the sprint's go_lanes setting, one by default; the record is kept
// outside the tables; a take waits behind the queue, a give grants its head, a hold
// not renewed is released after sprint.LaneHoldFor; the rows show it all.
func TestTheLanesAreKeptOnTheStoreWithTheWidthTheSprintSets(t *testing.T) {
	t.Parallel()
	t.Run("one lane by default, queued fairly, given back, timed out", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		take := func(who string) sprint.LaneAnswer {
			ans, err := h.st.LaneStep(h.ctx, sprint.LaneGo, "m1", who, false)
			require.NoError(t, err)
			return ans
		}
		assert.True(t, take("a").Granted, "the first take is granted")
		b := take("b")
		assert.Equal(t, sprint.LaneAnswer{Place: 1, Held: 1, Width: 1}, b, "the second waits at the head of the queue")
		h.tick(time.Second)
		ans, err := h.st.LaneStep(h.ctx, sprint.LaneGo, "m1", "a", true)
		require.NoError(t, err)
		assert.True(t, ans.Gave, "a gives its lane back")
		assert.True(t, take("b").Granted, "the give granted the head")
		rows, err := h.st.LaneRows(h.ctx)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, []string{"b"}, rows[0].Held)
		h.tick(sprint.LaneHoldFor + time.Second)
		assert.True(t, take("c").Granted, "a hold not renewed was released")
	})
	t.Run("the width is the sprint's setting", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.must(SetStep(sprint.SetReq{GoLanes: "2", Who: h.st.Actor}))
		w, err := h.st.LaneWidth(h.ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, w)
		for _, who := range []string{"a", "b"} {
			ans, err := h.st.LaneStep(h.ctx, sprint.LaneGo, "m1", who, false)
			require.NoError(t, err)
			assert.True(t, ans.Granted, who)
		}
		ans, err := h.st.LaneStep(h.ctx, sprint.LaneGo, "m1", "c", false)
		require.NoError(t, err)
		assert.False(t, ans.Granted, "the third waits at width 2")
		h.must(SetStep(sprint.SetReq{GoLanes: "default", Who: h.st.Actor}))
		w, err = h.st.LaneWidth(h.ctx)
		require.NoError(t, err)
		assert.Equal(t, sprint.LaneWidthDefault, w, "default is one a machine")
	})
	t.Run("a bad kind, machine or worker is refused whole, nothing written", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.st.LaneStep(h.ctx, "rust", "m 1", "a,b", false)
		require.Error(t, err)
		assert.ErrorContains(t, err, "a lane kind is one of go")
		assert.ErrorContains(t, err, "a machine name")
		assert.ErrorContains(t, err, "a worker name")
		_, ok, err := h.m.GetKey(h.ctx, laneKey("rust"))
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("the record is kept outside the tables, one record a kind", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		_, err := h.st.LaneStep(h.ctx, sprint.LaneGo, "m2", "a", false)
		require.NoError(t, err)
		raw, ok, err := h.m.GetKey(h.ctx, laneKey(sprint.LaneGo))
		require.NoError(t, err)
		require.True(t, ok)
		var ls sprint.Lanes
		require.NoError(t, json.Unmarshal([]byte(raw), &ls))
		assert.Equal(t, "a", ls["m2"].Holders[0].Who)
	})
}
