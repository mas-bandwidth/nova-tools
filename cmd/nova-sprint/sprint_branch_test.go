package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
	"github.com/nova-tools/internal/sprint/store"
)

// writeBaseBrief writes a passing brief whose header block names its repository and,
// unless base is "", its BASE: line, as a card the coordinator cuts does, and returns
// its path.
func writeBaseBrief(t *testing.T, dir, repo, id, base string) string {
	t.Helper()
	lead := "RESULT: " + id + " sha=000000000000 tier: flash\nKIND: fix\nREPO: " + repo + "\n"
	if base != "" {
		lead += "BASE: " + base + "\n"
	}
	lead += "PATHS: internal/" + id + ".go\nTEST: none a fixture of the sprint branch rule\n\nFix " + id + "."
	path := filepath.Join(dir, id+".md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief(lead)), 0o600))
	return path
}

// Every stream lands on the sprint branch, and promotion alone reaches dev
// (docs/SPEC-SPRINT.md section 7, the sprint branch). Found 2026-10-04: cards cut with
// BASE dev were merged by the lander straight onto dev, and the merge queue's promotion
// run restarted every time. add refuses a card whose BASE is dev in a stream that is not
// the promotion stream, the one-brief and the many-brief form alike, nothing written,
// with the sprint branch as the remedy; a card cut on the sprint branch, or naming no
// BASE (the lander's --base), is admitted, and quack, which cuts its own briefs, is held
// to the same rule. The lander refuses, before any git, land and its dry run alike, a
// batch whose effective base is dev in a stream that is not the promotion stream,
// nothing pushed or recorded, naming the sprint branch and the mark
// (sprint.ProtectedLandWhy); the same batch lands on the sprint branch, and dev does not
// move. Only the mark `stream set --land-protected` writes opens dev to a stream
// (TestLandRefusesAProtectedBaseUntilTheStreamIsMarked).
func TestEveryStreamLandsOnTheSprintBranchAndOnlyPromotionReachesDev(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	// add reads each brief at its base (the brief checks): a twin of the repository holds
	// every card's file on dev and on the sprint branch, and the pin is its commit
	files := map[string]string{}
	for _, id := range []string{"d1", "d2", "k1", "n1", "m1", "m2"} {
		files["internal/"+id+".go"] = "package internal\n"
	}
	repo, sha := twinRemote(t, ta, files, "dev", "sprint/mechanical-2026-10-02")
	onDev := writeBaseBrief(t, dir, repo, "d1", "dev")
	pinned := writeBaseBrief(t, dir, repo, "d2", "dev@"+sha)
	onSprint := writeBaseBrief(t, dir, repo, "k1", "sprint/mechanical-2026-10-02")
	noBase := writeBaseBrief(t, dir, repo, "n1", "")

	for _, c := range []struct{ name, line, card string }{
		{"one brief", "add --stream s1 d1 --one --brief-file " + onDev, "d1"},
		{"a pinned dev base", "add --stream s1 d2 --one --brief-file " + pinned, "d2"},
		{"a count of cards", "add --stream s1 --count 2 --brief-file " + onDev, "s1-1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := ta.applies()
			code, out, errs := ta.do(c.line)
			assert.NotEqual(t, 0, code, "%s: %s%s", c.line, out, errs)
			assert.Contains(t, out+errs, "card "+c.card+" is cut on dev, and stream s1 is not the promotion stream: every stream lands on the sprint branch, and promotion alone reaches dev")
			assert.Contains(t, out+errs, "re-cut the card with BASE: <the sprint branch> (sprint/<name>, the branch its stream lands on)")
			assert.NotContains(t, out, "MOVED", "nothing written")
			assert.False(t, ta.placed(c.card), "nothing written")
			assert.Equal(t, before, ta.applies(), "no store write")
		})
	}

	many := t.TempDir()
	writeBaseBrief(t, many, repo, "m1", "sprint/mechanical-2026-10-02")
	writeBaseBrief(t, many, repo, "m2", "dev")
	code, out, errs := ta.do("add --stream s2 --brief-dir " + many)
	assert.NotEqual(t, 0, code, "many-brief add with a dev card: %s%s", out, errs)
	assert.Contains(t, out+errs, "card m2 is cut on dev, and stream s2 is not the promotion stream")
	assert.False(t, ta.placed("m1") || ta.placed("m2"), "nothing written, all or none")

	assert.Contains(t, ta.ok("add --stream s1 k1 --one --brief-file "+onSprint), "MOVED k1 -> ready", "a card cut on the sprint branch is admitted")
	assert.Contains(t, ta.ok("add --stream s1 n1 --one --brief-file "+noBase), "MOVED n1 -> ready", "a card naming no BASE lands on the lander's --base")

	code, out, errs = ta.do("quack --streams q --count 1 --repo https://example.com/quack.git --base dev")
	assert.NotEqual(t, 0, code, "quack on dev: %s%s", out, errs)
	assert.Contains(t, out+errs, "is cut on dev, and stream q is not the promotion stream", "quack is held to the rule as add is")
	assert.Contains(t, ta.ok("quack --streams q --count 1 --repo https://example.com/quack.git"), "MOVED", "quack's default base is the sprint branch")

	t.Run("the lander", func(t *testing.T) {
		t.Parallel()
		r := newLandRig(t)
		for _, b := range []string{"dev", "sprint/s1"} {
			r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/"+b)
		}
		r.git(r.worker, "fetch", "-q", "origin") // r.head starts the card from refs/remotes/origin/sprint/s1
		r.ok("add --stream s1 --count 1 --one")
		r.queued(map[string]string{"s1-1": r.head("s1-1", "sprint/s1", "a.txt", "a\n")}, "s1-1")
		r.ok("stream set s1 --land-protected default") // queued marks every stream; s1 is an ordinary stream again
		dev := r.git(r.remote, "rev-parse", "dev")
		for _, land := range []string{"land --repo-dir " + r.clone + " --base dev --dry-run", "land --repo-dir " + r.clone + " --base dev"} {
			code, out, errs := r.do(land)
			assert.Equal(t, 1, code, land)
			assert.Contains(t, out+errs, "LAND REFUSED stream=s1 cards=1 base=dev tip=- ids=s1-1 ", land)
			assert.Contains(t, out+errs, "card s1-1 lands on dev, a protected branch of its repository (it names no REPO: line), and stream s1 is not marked to land on it", land)
			assert.Contains(t, out+errs, "every stream lands on the sprint branch, and promotion alone reaches dev; re-cut the card with BASE: <the sprint branch>", land)
			assert.Contains(t, out+errs, "; run: nova-sprint stream set s1 --land-protected any; or mark it the promotion stream, for every repository: nova-sprint stream set s1 --promotion\n", land)
		}
		assert.Equal(t, dev, r.git(r.remote, "rev-parse", "dev"), "nothing was pushed to dev")
		assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"), "nothing was recorded")

		assert.Contains(t, r.ok("land --repo-dir "+r.clone+" --base sprint/s1"), "LAND OK stream=s1 cards=1 base=sprint/s1", "the sprint branch is where a stream lands")
		assert.Contains(t, r.git(r.remote, "log", "--first-parent", "--format=%s", "sprint/s1"), "land s1-1 (sprint stream s1)")
		assert.Equal(t, dev, r.git(r.remote, "rev-parse", "dev"), "dev moved only by promotion")
	})
}

// placed says the work table holds a primary of this id.
func (ta *testApp) placed(id string) bool {
	ta.t.Helper()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	require.NoError(ta.t, err)
	return s.Work.Placed(id) != nil
}
