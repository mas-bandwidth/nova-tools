package sprint_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Merge health in the coordinator's pass (docs/SPEC-SPRINT.md section 8, "The coordinator's
// pass", merge health; tla/CoordinatorPass.tla): the base red at its tip, the promotion PR's
// state and the branches not on the base, as a fake forge reports them, are one judgment,
// raised again with a push every ten minutes of running time while any line holds, rewritten
// with the latest lines, and closed when none does. The twin store, a fake clock.

// fakeForge is the merge facts the store's hook reads, set by the test.
type fakeForge struct {
	mu    sync.Mutex
	facts *sprint.MergeFacts
}

func (f *fakeForge) set(m *sprint.MergeFacts) { f.mu.Lock(); f.facts = m; f.mu.Unlock() }

func (f *fakeForge) read(context.Context) (*sprint.MergeFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.facts == nil {
		return nil, nil
	}
	c := *f.facts
	return &c, nil
}

func TestTheCoordinatorPassCarriesMergeHealthEveryTenMinutes(t *testing.T) {
	t.Parallel()
	r := newPassRig(t)
	forge := &fakeForge{}
	r.st.Merge = forge.read
	subject := sprint.StreamSubject("")
	fresh := func() { r.pongs["amy"], r.pongs["bob"] = r.clock(), r.clock() }

	// the forge has read nothing, then reads all well: no judgment
	fresh()
	r.tick(time.Minute)
	assert.Nil(t, r.open(sprint.NMergeHealth, subject), "no merge facts read")
	forge.set(&sprint.MergeFacts{Base: "sprint/mechanical-2026-10-02", BaseSha: "a36a443db"})
	fresh()
	r.tick(time.Minute)
	assert.Nil(t, r.open(sprint.NMergeHealth, subject), "the base green, no PR open, no branch off it")

	// a failing promotion PR and a branch an open card names, off the base: one judgment
	forge.set(&sprint.MergeFacts{Base: "sprint/mechanical-2026-10-02", BaseSha: "a36a443db",
		Promotion: &sprint.PromotionPR{Number: 5120, State: sprint.PRFailing},
		Branches:  []sprint.BranchLag{{Branch: "sprint/x.w1.g3.e15", Named: "card x.w1", Ahead: 3, Behind: 7}, {Branch: "sprint/y.w1.g3.e15", Named: "card y.w1", Ahead: 0, Behind: 2}}})
	fresh()
	r.tick(time.Minute)
	j := r.open(sprint.NMergeHealth, subject)
	require.NotNil(t, j, "merge health is a judgment while a line holds")
	assert.Contains(t, j.What, "promotion PR #5120 is failing")
	assert.Contains(t, j.What, "branch sprint/x.w1.g3.e15 (card x.w1) is not on sprint/mechanical-2026-10-02: 3 ahead, 7 behind")
	assert.NotContains(t, j.What, "sprint/y.w1", "a branch with nothing ahead is on the base")
	assert.NotContains(t, j.What, "red", "the base is green")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NMergeHealth, ""), "one judgment")
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NMergeHealth))
	written := r.clock()

	// nine minutes on: held, not raised again
	fresh()
	r.tick(9 * time.Minute)
	assert.Equal(t, 0, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NMergeHealth), "not before ten minutes")
	ends := r.tickEnds()

	// ten minutes on: raised again, a push to the coordinator, the tick ends with a wake
	fresh()
	r.tick(time.Minute)
	require.Equal(t, 10*time.Minute, r.clock().Sub(written))
	assert.Equal(t, 1, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NMergeHealth), "raised again at ten minutes")
	j = r.open(sprint.NMergeHealth, subject)
	require.NotNil(t, j)
	assert.Equal(t, 1, j.Before)
	assert.Greater(t, r.tickEnds(), ends, "the push wakes inbox --wait")

	// the lines change: the base goes red, the PR conflicts; the same judgment, the latest lines
	forge.set(&sprint.MergeFacts{Base: "sprint/mechanical-2026-10-02", BaseSha: "b47c0de11", BaseRed: "go test ./internal/sprint/ failed",
		Promotion: &sprint.PromotionPR{Number: 5120, State: sprint.PRConflicted},
		Branches:  []sprint.BranchLag{{Branch: "sprint/x.w1.g3.e15", Named: "card x.w1", Ahead: 3, Behind: 9}}})
	fresh()
	r.tick(time.Minute)
	j = r.open(sprint.NMergeHealth, subject)
	require.NotNil(t, j)
	assert.Contains(t, j.What, "base sprint/mechanical-2026-10-02 is red at b47c0de11: go test ./internal/sprint/ failed")
	assert.Contains(t, j.What, "promotion PR #5120 is conflicted")
	assert.Contains(t, j.What, "3 ahead, 9 behind")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NMergeHealth, ""), "rewritten in place, never a second judgment")
	fresh()
	r.tick(9 * time.Minute)
	assert.Equal(t, 2, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NMergeHealth), "raised again at twenty minutes")
	fresh()
	r.tick(10 * time.Minute)
	assert.Equal(t, 3, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NMergeHealth), "and every ten minutes while it holds")

	// the base green, the PR merged, the branch landed: closed, and no more pushes
	forge.set(&sprint.MergeFacts{Base: "sprint/mechanical-2026-10-02", BaseSha: "c0ffee123"})
	fresh()
	r.tick(time.Minute)
	assert.Nil(t, r.open(sprint.NMergeHealth, subject), "closed when no line holds")
	fresh()
	r.tick(30 * time.Minute)
	assert.Equal(t, 3, r.count(sprint.Happened, sprint.NRaisedAgain, sprint.NMergeHealth), "a closed judgment is not pushed")
	assert.Equal(t, 1, r.count(sprint.Judgment, sprint.NMergeHealth, ""))

	// a queued PR is a line too, a new episode
	forge.set(&sprint.MergeFacts{Base: "sprint/mechanical-2026-10-02", BaseSha: "c0ffee123", Promotion: &sprint.PromotionPR{Number: 5121, State: sprint.PRQueued}})
	fresh()
	r.tick(time.Minute)
	j = r.open(sprint.NMergeHealth, subject)
	require.NotNil(t, j)
	assert.Contains(t, j.What, "promotion PR #5121 is queued")
	assert.Equal(t, 2, r.count(sprint.Judgment, sprint.NMergeHealth, ""))
}
