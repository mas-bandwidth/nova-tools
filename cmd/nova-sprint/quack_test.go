package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

var quackID = regexp.MustCompile(`quack-[0-9a-f]{6}-[A-Za-z0-9_-]+?-[0-9]{3}`)

// quackCards runs one quack line and returns the ids it added, in order, with
// each card's brief as the work table holds it.
func (ta *testApp) quackCards(line string) ([]string, map[string]string) {
	ta.t.Helper()
	out := ta.ok(line)
	var ids []string
	briefs := map[string]string{}
	for _, id := range quackID.FindAllString(out, -1) {
		if _, seen := briefs[id]; seen {
			continue
		}
		_, _, _, f, ok := ta.m.Record(sprint.Work, id)
		require.True(ta.t, ok, "%s is on the work table", id)
		ids = append(ids, id)
		briefs[id] = f["brief"]
	}
	return ids, briefs
}

// quack cuts n cards per stream into a running store: ids that carry the run's
// stamp and so never repeat across passes, the tiers taken in turn down each
// stream, and a brief that names the card's own file, the repository and the
// base, and passes the card lint of the repository's own rules file.
func TestQuackCutsStampedCardsThatAlternateTiersAndPassTheLint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	rules := filepath.Join("..", "..", "fleet", "child-rules.txt")
	ta.ok("init --readers reader-a,reader-b --members m1 --rules " + rules)
	const repo = "https://example.com/quack.git"
	first, briefs := ta.quackCards("quack --streams a,b --count 3 --repo " + repo)
	require.Len(t, first, 6)
	rs, err := swarm.ReadChildRules(rules)
	require.NoError(t, err)
	stamp := first[0][len("quack-") : len("quack-")+6]
	for _, s := range []string{"a", "b"} {
		for i, tier := range []string{"flash", "pro", "flash"} {
			id := "quack-" + stamp + "-" + s + "-00" + string(rune('1'+i))
			brief, ok := briefs[id]
			require.True(t, ok, "%s was cut: %v", id, first)
			m, why := cardhdr.ReadModel(brief)
			assert.Empty(t, why, id)
			assert.Equal(t, tier, m.Tier, "%s takes the tiers in turn", id)
			for _, want := range []string{"quacks/" + id + ".txt", "REPO: " + repo, "BASE: dev", "`quack: " + id + "`"} {
				assert.Contains(t, brief, want, id)
			}
			assert.Empty(t, swarm.LintCardChildWith([]byte(brief), rs), "%s passes the repository's card lint", id)
		}
	}
	second, _ := ta.quackCards("quack --streams a --count 2 --tiers pro --base main --repo " + repo)
	require.Len(t, second, 2)
	for _, id := range second {
		assert.False(t, slices.Contains(first, id), "%s is new across passes", id)
	}
	assert.NotEqual(t, stamp, second[0][len("quack-"):len("quack-")+6], "each pass has its own stamp")
}

// One run names every problem: no streams, no count, no repository, a tier
// that is no tier, an id given; and nothing is written.
func TestQuackRefusesEveryMissingInputAtOnce(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	code, _, errs := ta.do("quack x1 --tiers flash,turbo")
	assert.Equal(t, 2, code)
	for _, want := range []string{"takes no ids", "--streams", "--count", "--repo", `"turbo" is not a tier`, "run: nova-sprint"} {
		assert.Contains(t, errs, want)
	}
	assert.Equal(t, before, ta.applies(), "a refusal writes nothing")
	assert.True(t, strings.HasPrefix(errs, "nova-sprint quack: "), errs)
}
