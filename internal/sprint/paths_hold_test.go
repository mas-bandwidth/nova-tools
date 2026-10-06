package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A PATHS hold widens the twin by rule, once (docs/SPEC-SPRINT.md section 8, the rules
// table's row paths; tla/Lifecycle.tla, PathsProposed and TwinnedOnceByRule). On 2026-10-06
// fourteen cards came back with one defect, a file outside PATHS the work needed, and each
// cost a coordinator twin by hand (the owner: "twinning = inefficiency"). A HOLD with
// PATHS-PROPOSED inside the repository is answered by the tick with no judgment: the twin
// replaces the card, PATHS widened, the head and branch carried, the tier kept, dependents
// inherited, the rule recorded on both cards. A second proposal on the same card, and a
// protected path, are the judgment they are today. On the twin store (store.Mem), no
// sockets, no real time.
func TestAPathsHoldIsAnsweredByATwinWithTheProposedPaths(t *testing.T) {
	t.Parallel()

	// twinned is the rig with p-a held on PATHS-PROPOSED: internal/a/b.go and the tick run.
	twinned := func(t *testing.T) *conflictRig {
		r, _ := newPathsRig(t)
		r.hold("p-a", "Verdict: HOLD\nHead: "+pathsHead+"\nThe fix needs internal/a/b.go, outside PATHS.\nPATHS-PROPOSED: internal/a/b.go")
		r.tick()
		return r
	}

	t.Run("a HOLD with PATHS-PROPOSED inside the repo twins by rule", func(t *testing.T) {
		t.Parallel()
		r, _ := newPathsRig(t)
		r.hold("p-a", "Verdict: HOLD\nHead: "+pathsHead+"\nThe fix needs internal/a/b.go, outside PATHS.\nPATHS-PROPOSED: internal/a/b.go")
		held := r.snap().Work.Card("p-a")
		tierWas, workWas := held.F(sprint.FieldTierNow), r.snap().Fleet.Card(held.F("work"))
		require.NotNil(t, workWas)
		r.tick()

		s := r.snap()
		old, twin := s.Work.Card("p-a"), s.Work.Card("p-a-t")
		require.NotNil(t, old)
		require.NotNil(t, twin, "the twin keeps the id with a -t suffix")
		assert.False(t, old.Placed(), "the held card is dropped")
		assert.True(t, twin.Placed())
		assert.Equal(t, "p-a", twin.F(sprint.FieldReplaces), "--replaces the card")
		assert.Contains(t, twin.F("brief"), "PATHS: internal/a/a.go,internal/a/b.go", "PATHS widened by the proposal")
		assert.Contains(t, twin.F("brief"), "CARRY: p-a attempt 1 head="+pathsHead, "the held head carried")
		fix := twin.F("fix")
		assert.Contains(t, fix, "carry "+pathsHead, "THE TASK names the carried head")
		if b := workWas.F("branch"); b != "" {
			assert.Contains(t, fix, "branch "+b, "and the branch")
		}
		assert.Contains(t, fix, "the only change is PATHS")
		assert.Equal(t, tierWas, twin.F(sprint.FieldTierNow), "the tier kept")
		assert.Equal(t, "p-a-t", s.Work.Card("dep").F("needs"), "dependents inherited")

		assert.Equal(t, "p-a-t", old.F(sprint.FieldPathsTwin), "the rule recorded on the card")
		assert.True(t, strings.HasPrefix(old.F(sprint.FieldRuleAnswer), sprint.RulePaths+": "), old.F(sprint.FieldRuleAnswer))
		assert.Equal(t, "p-a", twin.F(sprint.FieldPathsTwinned), "the rule recorded on the twin")
		assert.True(t, strings.HasPrefix(twin.F(sprint.FieldRuleAnswer), sprint.RulePaths+": "), twin.F(sprint.FieldRuleAnswer))

		assert.Empty(t, judgmentsOn(s, "p-a"), "no judgment to the coordinator")
		assert.Empty(t, judgmentsOn(s, "p-a-t"))
		assert.Empty(t, r.open(sprint.NBlocked))
	})

	t.Run("a second proposal on the same card is a judgment", func(t *testing.T) {
		t.Parallel()
		r := twinned(t)
		require.NotNil(t, r.snap().Work.Card("p-a-t"))
		r.hold("p-a-t", "Verdict: HOLD\nHead: "+pathsHead+"\nStill short: internal/a/c.go.\nPATHS-PROPOSED: internal/a/c.go")
		for range 3 {
			r.tick()
		}
		s := r.snap()
		tw := s.Work.Card("p-a-t")
		assert.Equal(t, sprint.Review, tw.Col, "held for a mind, never dealt the same brief again")
		assert.Equal(t, 1, tw.Int("attempt"))
		for _, id := range []string{"p-a-t2", "p-a-t-t"} {
			assert.Nil(t, s.Work.Card(id), "a card is twinned at most once by the rule: no %s", id)
		}
		js := judgmentsOn(s, "p-a-t")
		require.Len(t, js, 1, "the one judgment it is today")
		why := ruleWhy(s, js[0])
		assert.Contains(t, why, "paths proposed twice")
		assert.Contains(t, why, "p-a")
	})

	t.Run("a protected path is a judgment", func(t *testing.T) {
		t.Parallel()
		for _, g := range []string{".github/workflows/ci.yml", "internal/secrets/vault.go", "go.mod", "internal/a/deploy.pem"} {
			t.Run(g, func(t *testing.T) {
				t.Parallel()
				r, _ := newPathsRig(t)
				r.hold("p-a", "Verdict: HOLD\nHead: "+pathsHead+"\nThe fix needs "+g+".\nPATHS-PROPOSED: "+g)
				for range 2 {
					r.tick()
				}
				s := r.snap()
				assert.Nil(t, s.Work.Card("p-a-t"), "no twin by rule")
				assert.Equal(t, 1, s.Work.Card("p-a").Int("attempt"), "never dealt the same brief again")
				js := judgmentsOn(s, "p-a")
				require.Len(t, js, 1)
				assert.Contains(t, ruleWhy(s, js[0]), "paths proposed, protected: "+g)
			})
		}
	})

	t.Run("what is protected", func(t *testing.T) {
		t.Parallel()
		for g, protected := range map[string]bool{
			"internal/a/b.go": false, "docs/SPEC-SPRINT.md": false, "tla/Lifecycle.tla": false, "internal/tokens/x.go": false,
			".github/workflows/ci.yml": true, ".github": true, "internal/secrets/x.go": true, "internal/seatcred/*.go": true,
			"internal/*/x.go": true, "go.sum": true, "cmd/x/CODEOWNERS": true, "internal/a/.env": true, "internal/a/client_secret.json": true,
			"*.go": true, "**": true,
		} {
			assert.Equal(t, protected, len(sprint.ProtectedProposed([]string{g})) > 0, g)
		}
	})
}

// ruleWhy is what the rules say of the open judgment.
func ruleWhy(s *sprint.Snapshot, o sprint.Open) string {
	for _, a := range sprint.RuleAnswers(s, sprint.TickReq{AnswerRules: true}) {
		if a.Judgment == o.Note.ID {
			return a.Act + ": " + a.Why
		}
	}
	return ""
}
