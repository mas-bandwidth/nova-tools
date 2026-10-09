package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// view coordinator counts the delivery milestones apart (docs/SPEC-SPRINT.md section 7,
// delivery milestones): two cards landed are staged; promoted --cards verifies the one it
// carried; installed puts it on a target; a failed promotion is the alarm "promotion failed"
// until a promotion merges after it. On the twin with the fake clock.
func TestViewCoordinatorCountsStagedDevAndInstalledApart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.readersReadPro()
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"", ""}, []string{"", ""}, []string{"", ""})

	v := ta.coordView("")
	assert.Equal(t, 2, v.N.Staged, "landed is staged")
	assert.Equal(t, 0, v.N.Dev)
	assert.Contains(t, v.Sum, "| staged 2 dev 0 installed 0")

	ta.ok("promoted --failed 'merge-group run red' --branch sprint/main --tip 89abcde --evidence pr=7")
	v = ta.coordView("")
	it, ok := item(v, "a:promotion")
	require.True(t, ok, "the failed promotion is an alarm: %+v", v.Items)
	assert.Equal(t, "promotion failed", it.W)
	assert.Contains(t, it.S, "sprint/main@89abcde failed: merge-group run red (pr=7)")
	assert.Equal(t, 2, it.B, "the staged cards not in dev are behind it")

	ta.a.sleep(time.Minute) // the promotion after the failure, by the clock
	out := ta.ok("promoted --sha 0123abc --branch sprint/main --tip 89abcde --cards s1-1 --evidence pr=8")
	assert.Contains(t, out, "1 verified in dev")
	v = ta.coordView("")
	_, ok = item(v, "a:promotion")
	assert.False(t, ok, "a promotion that merged after it closes the failure")
	assert.Equal(t, 1, v.N.Dev)

	code, _, errs := ta.do("installed target-a --sha 0123abc")
	assert.NotEqual(t, 0, code, "an install wants its receipt")
	assert.Contains(t, errs, "--receipt")
	ta.ok("installed target-a --sha 0123abc --receipt 'nova-tools 1.2.3, sha256 ok'")
	v = ta.coordView("")
	assert.Equal(t, [3]int{2, 1, 1}, [3]int{v.N.Staged, v.N.Dev, v.N.Installed})
	assert.Contains(t, v.Sum, "| staged 2 dev 1 installed 1")
	ta.clean()
}

// A two-card batch records staged_parent as the base tip the batch was merged onto.
// The pushed tip's first parent is the first card's merge, inside the batch, and is
// not that base (docs/SPEC-SPRINT.md section 7, delivery milestones).
func TestATwoCardBatchRecordsTheBatchBaseAsStagedParent(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	base := r.git(r.remote, "rev-parse", "main")
	heads := map[string]string{}
	for _, id := range []string{"s1-1", "s1-2"} {
		heads[id] = r.head(id, "main", id+".txt", id+"\n")
	}
	r.queued(heads, "s1-1", "s1-2")
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	tip := r.git(r.remote, "rev-parse", "main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main tip="+tip)
	firstParent := r.git(r.remote, "rev-parse", tip+"^")
	assert.NotEqual(t, base, firstParent, "tip^ is inside the batch, the first card's merge")
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	require.NoError(t, err)
	for _, id := range []string{"s1-1", "s1-2"} {
		c := s.Work.Card(id)
		require.NotNil(t, c, id)
		assert.Equal(t, tip, c.F(sprint.FieldStagedCommit), id)
		assert.Equal(t, base, c.F(sprint.FieldStagedParent), id+" names the batch base, not tip^")
	}
	r.clean()
}
