package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The lanes' verbs on the in-memory store at the test's clock (docs/SPEC-SPRINT.md
// section 18): a take granted or queued with its place and its next command, a give
// that grants the head, the width the sprint sets, a --wait that asks again on the
// clock until a holder that never gave back is released, lane list and where --json --cards.
func TestTheLaneVerbsTakeGiveAndListTheMachinesGoLanes(t *testing.T) {
	t.Parallel()
	t.Run("take, queue, give, list", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1,m2")
		assert.Contains(t, ta.ok("lane take go --machine m1 --as a"), "LANE-TAKE OK go machine=m1 as=a held=1/1")
		code, _, errs := ta.do("lane take go --machine m1 --as b")
		assert.Equal(t, 1, code, "a take the lane says no to exits 1")
		assert.Contains(t, errs, "REFUSED: the go lanes of m1 are held (1/1); b is 1st in the queue")
		assert.Contains(t, errs, "run: nova-sprint lane take go --machine m1 --as b --wait 30m")
		assert.Contains(t, ta.ok("lane list"), "LANE go machine=m1 held=a width=1 waiting=b since=")
		assert.Contains(t, ta.ok("lane give go --machine m1 --as a"), "LANE-GIVE OK go machine=m1 as=a gave=yes held=1/1")
		assert.Contains(t, ta.ok("lane take go --machine m1 --as b"), "held=1/1", "the give granted the head")
		assert.Contains(t, ta.ok("lane take go --machine m2 --as c"), "LANE-TAKE OK go machine=m2", "each machine has its own lanes")

		var view struct {
			Lanes []sprint.LaneRow `json:"lanes"`
		}
		require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json --cards")), &view))
		require.Len(t, view.Lanes, 2, "where --json --cards carries the lanes")
		assert.Equal(t, []string{"b"}, view.Lanes[0].Held)
		assert.Equal(t, "m2", view.Lanes[1].Machine)
	})
	t.Run("the width is set --go-lanes", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1,m2")
		ta.ok("set --go-lanes 2")
		ta.ok("lane take go --machine m1 --as a")
		assert.Contains(t, ta.ok("lane take go --machine m1 --as b"), "held=2/2")
		code, _, _ := ta.do("set --go-lanes 0")
		assert.Equal(t, 1, code, "a width under 1 is refused")
	})
	t.Run("--wait asks again on the clock until the holder that never gave back is released", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1,m2")
		ta.ok("lane take go --machine m1 --as a")
		began := ta.a.now()
		assert.Contains(t, ta.ok("lane take go --machine m1 --as b --wait 30m"), "LANE-TAKE OK go machine=m1 as=b")
		waited := ta.a.now().Sub(began)
		assert.Greater(t, waited, sprint.LaneHoldFor, "granted only once the holder's hold ran out")
		assert.Less(t, waited, sprint.LaneHoldFor+sprint.LaneAskEvery+time.Second, "and at the first ask after") // wall-ok: the test app's clock, which the wait's sleep advances
	})
	t.Run("a wait that runs out is the queue's refusal", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1,m2")
		ta.ok("lane take go --machine m1 --as a")
		code, _, errs := ta.do("lane take go --machine m1 --as b --wait 12s")
		assert.Equal(t, 1, code)
		assert.Contains(t, errs, "1st in the queue")
	})
	t.Run("every problem at once, nothing written", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1,m2")
		code, _, errs := ta.do("lane take rust --machine 'm 1'")
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "wants one lane kind, one of go")
		assert.Contains(t, errs, "--machine wants")
		assert.Contains(t, errs, "--as wants")
		assert.Contains(t, ta.ok("lane list"), "LANE-LIST OK machines=0")
	})
}

// The server runs a lane's take and give for a worker, exactly as `lane <verb> <kind>
// --machine <m> --as <worker>`, and never a wait (serve.go, workerVerb).
func TestTheServerRunsALanesTakeAndGiveAndNoWait(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		argv []string
		as   string
		why  string
	}{
		{"a take", []string{"lane", "take", "go", "--machine", "m1", "--as", "w1"}, "w1", ""},
		{"a give", []string{"lane", "give", "go", "--machine", "m1", "--as", "w1"}, "w1", ""},
		{"a wait", []string{"lane", "take", "go", "--machine", "m1", "--as", "w1", "--wait", "30m"}, "", "and nothing more"},
		{"a kind there is not", []string{"lane", "take", "rust", "--machine", "m1", "--as", "w1"}, "", "the kind one of go"},
		{"a list", []string{"lane", "list"}, "", "the server runs the workers' verbs only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			as, words, why := workerVerb(tc.argv)
			assert.Equal(t, tc.as, as)
			if tc.why == "" {
				assert.Empty(t, why)
				assert.Equal(t, 2, words)
				return
			}
			assert.Contains(t, why, tc.why)
		})
	}
}
