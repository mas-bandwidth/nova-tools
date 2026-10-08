package sprint_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// holdAt finishes the primary's attempt held, on branch when one is named.
func (r *conflictRig) holdAt(id, report, branch string) {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	if wc.Col != sprint.Working {
		r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
		wc = r.snap().Fleet.Card(wc.ID)
	}
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")},
		Failed: true, Head: pathsHead, Branch: branch, Report: report}))
	require.Equal(r.t, sprint.Review, r.snap().Work.Card(id).Col, id)
}

// TestAPathsHoldIsAnsweredByATwinWithTheProposedPaths is the paths hold the tick
// answers: a proposal inside the repository becomes a twin whose PATHS are the
// proposal, a second proposal stays a judgment, and a protected path stays one.
func TestAPathsHoldIsAnsweredByATwinWithTheProposedPaths(t *testing.T) {
	t.Parallel()
	const branch = "sprint/p-a.w1"

	t.Run("inside the repository", func(t *testing.T) {
		t.Parallel()
		r, _ := newPathsRig(t)
		r.holdAt("p-a", "HOLD: the fix needs internal/a/b.go, outside PATHS\nPATHS-PROPOSED: internal/a/b.go", branch)
		r.tick()

		s := r.snap()
		old := s.Work.Card("p-a")
		require.NotNil(t, old)
		assert.False(t, old.Placed(), "the held card is off the table")
		twin := s.Work.Card("p-a-t")
		require.NotNil(t, twin)
		require.True(t, twin.Placed())
		assert.Equal(t, old.F(sprint.FieldTier), twin.F(sprint.FieldTier), "the twin keeps the tier")
		assert.Contains(t, twin.F("brief"), "tier: flash")
		assert.Contains(t, twin.F("brief"), "PATHS: internal/a/a.go,internal/a/b.go")
		assert.Contains(t, twin.F("brief"), "CARRY: p-a attempt 1 head="+pathsHead)
		assert.Contains(t, twin.F("brief"), "THE TASK. carry "+pathsHead+"; the only change is PATHS")
		assert.Contains(t, twin.F("brief"), "The branch is "+branch)
		assert.Equal(t, "p-a-t", s.Work.Card("dep").F("needs"))
		assert.Contains(t, old.F(sprint.FieldRuleAnswer), sprint.RulePaths)
		assert.Contains(t, twin.F(sprint.FieldRuleAnswer), sprint.RulePaths)
		assert.Empty(t, judgmentsOn(s, "p-a"))
	})

	t.Run("a second proposal is a judgment", func(t *testing.T) {
		t.Parallel()
		r, _ := newPathsRig(t)
		r.holdAt("p-a", "HOLD: the fix needs internal/a/b.go\nPATHS-PROPOSED: internal/a/b.go", branch)
		r.tick()
		for range 4 {
			if r.snap().Work.Card("p-a-t").Col != sprint.Waiting {
				break
			}
			r.tick()
		}
		r.holdAt("p-a-t", "HOLD: the fix needs internal/a/c.go\nPATHS-PROPOSED: internal/a/c.go", branch)
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("p-a-t2"))
		assert.Nil(t, s.Work.Card("p-a-t-t"))
		require.NotNil(t, s.Work.Card("p-a-t"))
		assert.True(t, s.Work.Card("p-a-t").Placed())
		assert.NotEmpty(t, judgmentsOn(s, "p-a-t"))
	})

	t.Run("a protected path is a judgment", func(t *testing.T) {
		t.Parallel()
		r, _ := newPathsRig(t)
		r.holdAt("p-a", "HOLD: the fix needs a key\nPATHS-PROPOSED: secrets/keys.go", branch)
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("p-a-t"))
		require.True(t, s.Work.Card("p-a").Placed())
		assert.NotEmpty(t, judgmentsOn(s, "p-a"))
	})

	t.Run("a path outside the repository is a judgment", func(t *testing.T) {
		t.Parallel()
		r, _ := newPathsRig(t)
		r.holdAt("p-a", "HOLD: the fix climbs out\nPATHS-PROPOSED: ../outside.go", branch)
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("p-a-t"))
		require.True(t, s.Work.Card("p-a").Placed())
		assert.NotEmpty(t, judgmentsOn(s, "p-a"))
	})
}
