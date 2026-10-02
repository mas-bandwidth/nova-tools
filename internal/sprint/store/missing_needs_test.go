package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddRefusesDependentsOfAnUnadmittedID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		bad string
	}{
		{bad: "bad.id"},
		{bad: "ctl-absent"},
	}
	for _, tc := range cases {
		t.Run(tc.bad, func(t *testing.T) {
			h := newHarness(t)
			h.setup(0)
			r := h.run(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{tc.bad, "waiter"}, Needs: []string{tc.bad}}))
			require.Len(t, r.Refused, 2, "dependent admitted: %+v", r)
			assert.Nil(t, h.workCard("waiter"), "dependent admitted: %+v", r)
			assert.Nil(t, h.workCard(tc.bad), "dependent admitted: %+v", r)
			h.clean("refused missing dependency")
		})
	}
	// All or nothing: an add naming several writes none of them when any is
	// refused, naming every one; an existing id is refused as well.
	h := newHarness(t)
	h.setup(1)
	r := h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"bad.id", "good"}}))
	require.Len(t, r.Refused, 2, "partial acceptance: %+v", r)
	assert.Nil(t, h.workCard("good"), "partial acceptance: %+v", r)
	assert.Contains(t, fmt.Sprint(r.Refused), "all or none", "partial acceptance: %+v", r)

	r = h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s1-1", "waiter"}, Needs: []string{"s1-1"}}))
	require.Len(t, r.Refused, 2, "an existing id with a new one: %+v", r)
	require.Nil(t, h.workCard("waiter"), "an existing id with a new one: %+v", r)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
	require.Equal(t, sprint.Waiting, h.state("waiter"), "waiter: %s", h.state("waiter"))
	h.clean("unrelated refusals")
}

func TestStoredMissingNeedsHaveOneActionableJudgment(t *testing.T) {
	t.Parallel()
	const missing = "a primary is blocked on something missing"
	cases := []struct {
		sentinel bool
		live     bool
	}{
		{sentinel: false, live: false},
		{sentinel: false, live: true},
		{sentinel: true, live: false},
		{sentinel: true, live: true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("sentinel=%v/live=%v", tc.sentinel, tc.live), func(t *testing.T) {
			h := newHarness(t)
			h.setup(1)
			h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}, Sentinel: tc.sentinel}))
			needs := "bad.id"
			if tc.live {
				needs += ",s1-1"
			}
			h.seedMissingNeeds("waiter", needs)
			h.startMachine()
			for i := 0; i < 3; i++ {
				if i == 2 {
					h.tick(time.Minute + time.Second)
				}
				h.machine()
			}
			notes := h.openJudgments(missing, "waiter")
			require.Len(t, notes, 1, "missing dependency is silent or repeated: %+v", notes)
			require.Len(t, h.allJudgments(missing), 1, "missing dependency is silent or repeated: %+v", notes)
			n := notes[0].Note
			assert.Equal(t, "bad.id", strings.Join(n.Needs, ","), "not actionable: %+v", n)
			assert.Equal(t, "drop,ack", strings.Join(n.Decisions, ","), "not actionable: %+v", n)
			assert.Contains(t, n.What, "bad.id", "not actionable: %+v", n)
			h.must(AckStep(sprint.AckReq{Notes: []string{n.ID}, Reason: "dependency not required", Who: "tester"}))
			c := h.workCard("waiter")
			assert.Equal(t, "bad.id", c.F("waived"), "waiver not recorded: %+v", c)
			assert.Equal(t, "tester", c.F("waived_by"), "waiver not recorded: %+v", c)
			assert.NotEmpty(t, c.F("waived_at"), "waiver not recorded: %+v", c)
			switch {
			case tc.live:
				require.Equal(t, sprint.Waiting, c.Col, "live dependency bypassed: %+v", c)
				require.Empty(t, c.F("reached"), "live dependency bypassed: %+v", c)
			case tc.sentinel:
				require.Equal(t, sprint.Waiting, c.Col, "sentinel not reached: %+v", c)
				require.NotEmpty(t, c.F("reached"), "sentinel not reached: %+v", c)
			default:
				require.Equal(t, sprint.Ready, c.Col, "primary not ready: %+v", c)
			}
			h.machine()
			require.Empty(t, h.openJudgments(missing, "waiter"), "acknowledged missing need repeated")
			require.Len(t, h.allJudgments(missing), 1, "acknowledged missing need repeated")
			h.clean("missing need acknowledged")
		})
	}
}

func TestMissingNeedWaiverDoesNotIncludeLaterMissingNeed(t *testing.T) {
	t.Parallel()
	const missing = "a primary is blocked on something missing"
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
	h.seedMissingNeeds("waiter", "bad.id")
	h.startMachine()
	h.machine()
	notes := h.openJudgments(missing, "waiter")
	require.Len(t, notes, 1, "missing judgment: %+v", notes)
	h.seedMissingNeeds("waiter", "bad.id,later.bad")
	h.must(AckStep(sprint.AckReq{Notes: []string{notes[0].Note.ID}, Reason: "only the first", Who: "tester"}))
	c := h.workCard("waiter")
	require.Equal(t, "bad.id", c.F("waived"), "waived unreviewed need: %+v", c)
	require.Equal(t, sprint.Waiting, c.Col, "waived unreviewed need: %+v", c)
	h.machine()
	notes = h.openJudgments(missing, "waiter")
	require.Len(t, notes, 1, "new missing need not raised: %+v", notes)
	require.Equal(t, "later.bad", strings.Join(notes[0].Note.Needs, ","), "new missing need not raised: %+v", notes)
	h.clean("later missing need remains judged")
}

func TestRestoredMissingNeedIsNotWaivedAndItsJudgmentCloses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action string
	}{
		{action: "ack"},
		{action: "resolve"},
		{action: "dropped"},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			h := newHarness(t)
			h.setup(1)
			h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
			h.seedMissingNeeds("waiter", "later")
			h.must(ResolveStep(sprint.ResolveReq{}))
			notes := h.openJudgments(sprint.NMissingNeed, "waiter")
			require.Len(t, notes, 1, "missing judgment: %+v", notes)
			h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"later"}}))
			switch tc.action {
			case "ack":
				h.must(AckStep(sprint.AckReq{Notes: []string{notes[0].Note.ID}, Reason: "it exists now"}))
			case "resolve":
				h.must(ResolveStep(sprint.ResolveReq{}))
			case "dropped":
				h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"later"}}, Reason: "not needed"}))
				h.must(ResolveStep(sprint.ResolveReq{}))
				require.Len(t, h.openJudgments(sprint.NBlocked, "waiter"), 1, "dropped need was hidden by former missing judgment")
			}
			c := h.workCard("waiter")
			assert.Equal(t, sprint.Waiting, c.Col, "wrong recovery: card=%+v notes=%+v", c, h.openJudgments(sprint.NMissingNeed, "waiter"))
			assert.Empty(t, c.F("waived"), "wrong recovery: card=%+v notes=%+v", c, h.openJudgments(sprint.NMissingNeed, "waiter"))
			assert.Empty(t, h.openJudgments(sprint.NMissingNeed, "waiter"), "wrong recovery: card=%+v notes=%+v", c, h.openJudgments(sprint.NMissingNeed, "waiter"))
			h.clean("dependency exists again")
		})
	}
}

// Every missing-need judgment is written in the one tick that finds it: a
// tick has no bound on its notes but the step's (the owner's rule, never a
// row at a time), and none is written twice.
func TestMissingNeedJudgmentsAllInOneTick(t *testing.T) {
	t.Parallel()
	const n = 51
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: n, Needs: []string{"s1-1"}}))
	for i := 1; i <= n; i++ {
		h.seedMissingNeeds(fmt.Sprintf("s2-%d", i), "bad.id")
	}
	h.startMachine()
	h.machine()
	got := len(h.openJudgments(sprint.NMissingNeed, ""))
	require.Equal(t, n, got, "first tick: %d", got)
	h.machine()
	h.machine()
	got = len(h.allJudgments(sprint.NMissingNeed))
	require.Equal(t, n, got, "not written once each: %d", got)
	h.clean("missing judgments drained")
}
