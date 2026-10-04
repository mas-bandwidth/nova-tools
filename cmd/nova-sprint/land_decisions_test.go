package main

import (
	"context"
	"errors"
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
)

// Every decision the lander makes is a function of its inputs, apart from the git and the
// go runs that feed it (docs/SPEC-SPRINT.md section 7), and each is pinned here, in memory,
// on every change; the landings themselves, on real git and a real go toolchain, run in the
// functional tier (//go:build functional).
func TestLandDecisionsArePinnedInMemory(t *testing.T) {
	t.Parallel()
	// the unit tier makes no repository and runs no go: those tests carry the tag
	t.Run("the unit tier", func(t *testing.T) {
		t.Parallel()
		files, err := filepath.Glob("*_test.go")
		require.NoError(t, err)
		calls := []string{"newLandRig" + "(", "gitrun.Run" + "(", "cleanGit" + "("}
		for _, f := range files {
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			if first, _, _ := strings.Cut(string(b), "\n"); constraint.IsGoBuild(first) {
				expr, err := constraint.Parse(first)
				require.NoError(t, err, f)
				if !expr.Eval(func(tag string) bool { return tag != "functional" && tag != "slow" && tag != "perf" }) {
					continue
				}
			}
			for _, c := range calls {
				assert.NotContainsf(t, string(b), c, "%s builds in the unit tier and calls %s: a test on real git or go carries //go:build functional", f, c)
			}
		}
	})
	ledgers := []landLedger{{owns: diffcheck.GeneralityLedger, tests: "A"}, {owns: func(p string) bool { return strings.HasPrefix(p, "other/") }, tests: "B"}}
	t.Run("conflict plan", func(t *testing.T) {
		t.Parallel()
		const shrink, shard = "internal/ci/testdata/sharedtemp_allowlist.txt", "internal/ci/testdata/generality/cmd/x.txt"
		for _, tc := range []struct {
			name                  string
			paths                 []string
			union, regen, outside []string
			owners                string
		}{
			{"a shrink-only ledger is a union", []string{shrink}, []string{shrink}, nil, nil, ""},
			{"a generated ledger is regenerated", []string{shard}, nil, []string{shard}, nil, "A"},
			{"both at once", []string{shard, shrink}, []string{shrink}, []string{shard}, nil, "A"},
			{"a prose file is a conflict", []string{"README.md"}, nil, []string{"README.md"}, []string{"README.md"}, ""},
			{"a union beside a prose file is a conflict", []string{shrink, "README.md"}, []string{shrink}, []string{"README.md"}, []string{"README.md"}, ""},
		} {
			union, rest, owners, outside := conflictPlan(tc.paths, ledgers)
			assert.Equal(t, tc.union, union, tc.name)
			assert.Equal(t, tc.regen, rest, tc.name)
			assert.Equal(t, tc.outside, outside, tc.name)
			assert.Equal(t, tc.owners, testsOf(owners), tc.name)
		}
	})
	t.Run("gate result mapping", func(t *testing.T) {
		t.Parallel()
		unset := decide.GateBars{Flaky: decide.Unset, PreExisting: decide.Unset}
		assert.Equal(t, "recorded; the flaky bar is unset, so nothing is rerun", gateRouted(unset, decide.Caused))
		assert.Equal(t, "caused, not rerun", gateRouted(decide.GateBars{Flaky: 0.9, PreExisting: decide.Unset}, decide.Caused))
		red := "--- FAIL: TestA (1.02s)\n    a_test.go:9: timed out after 1s\nFAIL\nFAIL\tm/p\t1.1s\n"
		l := &lander{gateNote: "the key is absent"}
		assert.Equal(t, "the check failed", l.gateRerun(context.Background(), "", "s1", "main", "abcdef0", nil, "the check failed", "no go test output"),
			"no go test failure: red as it was")
		assert.Equal(t, "the check failed (no gate decision: the key is absent)", l.gateRerun(context.Background(), "", "s1", "main", "abcdef0", nil, "the check failed", red),
			"no gate: red, with why no decision was made")
	})
	t.Run("base gate cache", func(t *testing.T) {
		t.Parallel()
		l := &lander{baseGateCache: map[string]string{"base-1": "", "base-2": "go build ./...: exit status 1"}}
		dir := t.TempDir() // no go.mod: a miss is green without a run
		assert.Empty(t, l.treeGateBase(context.Background(), dir, "base-1"))
		assert.Equal(t, "go build ./...: exit status 1", l.treeGateBase(context.Background(), dir, "base-2"), "a red base stays red at its commit")
		assert.Empty(t, l.treeGateBase(context.Background(), dir, "base-3"))
		assert.Contains(t, l.baseGateCache, "base-3", "a miss is cached")
	})
	t.Run("ledger owners", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			paths   []string
			owners  string
			outside []string
		}{
			{"a shard", []string{"internal/ci/testdata/generality/cmd/x.txt"}, "A", nil},
			{"two ledgers", []string{"other/y.txt", "internal/ci/testdata/generality_text_fixtures_allowlist.txt"}, "A, B", nil},
			{"a prose file beside", []string{"other/y.txt", "README.md"}, "B", []string{"README.md"}},
			{"a Go file in a ledger directory", []string{"internal/ci/testdata/generality/x.go"}, "", []string{"internal/ci/testdata/generality/x.go"}},
		} {
			owners, outside := ledgerOwners(tc.paths, ledgers)
			assert.Equal(t, tc.owners, testsOf(owners), tc.name)
			assert.Equal(t, tc.outside, outside, tc.name)
		}
		assert.True(t, strings.HasPrefix(ledgerNote([]string{"a", "b"}, "A"), "the generated ledgers a, b conflicted"))
	})
	t.Run("unmerged paths", func(t *testing.T) {
		t.Parallel()
		paths, ours := unmergedPaths("100644 aaa 1\tl.txt\n100644 bbb 2\tl.txt\n100644 ccc 3\tl.txt\n100644 ddd 1\tgone.txt\n100644 eee 3\tgone.txt")
		assert.Equal(t, []string{"l.txt", "gone.txt"}, paths)
		assert.Equal(t, map[string]bool{"l.txt": true}, ours)
	})
	t.Run("family links", func(t *testing.T) {
		t.Parallel()
		family := []landLedger{{owns: func(p string) bool { return strings.HasPrefix(p, "led/") && strings.HasSuffix(p, ".txt") }, roots: []string{"led/a", "led/b"}}}
		for _, tc := range []struct{ name, files, want string }{
			{"a ledger that is a link", "120000 x 0\tled/a/r.txt", "led/a/r.txt"},
			{"a root that is a link, nothing under it conflicted", "100644 x 0\tled/a/r.txt\n120000 y 0\tled/b", "led/b"},
			{"a directory above a root", "120000 y 0\tled", "led"},
			{"a link under a root", "120000 y 0\tled/a/sub", "led/a/sub"},
			{"a link elsewhere", "120000 y 0\tother\n100644 x 0\tled/a/r.txt", ""},
		} {
			assert.Equal(t, tc.want, familyLink(tc.files, family), tc.name)
		}
		assert.Equal(t, []string{"led/a", "led/b", "led/a/r.txt"}, familyPaths("100644 x 0\tled/a/r.txt\n100644 x 0\tnotes.md", family))
	})
	t.Run("update wrote", func(t *testing.T) {
		t.Parallel()
		status := "M  staged-by-the-merge.go\x00 M worktree.txt\x00MM both.txt\x00?? new.txt\x00R  to.txt\x00from.txt\x00"
		assert.Equal(t, []string{"worktree.txt", "both.txt", "new.txt"}, updateWrote(status))
	})
	t.Run("regeneration done", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name        string
			err         error
			out         string
			done, again bool
		}{
			{"passed", nil, "ok", true, false},
			{"wrote", errors.New("exit status 1"), "x: updated, rerun (1 package shards changed)", false, true},
			{"failed", errors.New("exit status 1"), "a row grew", false, false},
		} {
			done, again := regenDone(tc.err, tc.out)
			assert.Equal(t, []bool{tc.done, tc.again}, []bool{done, again}, tc.name)
		}
	})
	t.Run("place", func(t *testing.T) {
		t.Parallel()
		l := &lander{}
		why, also := l.placeWhy("s1", []landCard{{id: "s1-1", head: "s1-1"}, {id: "s1-2", head: "abcdef0"}})
		assert.Equal(t, "card s1-1 names no BASE: line and no --base was given, and the card names no REPO: line and no --repo-dir was given; run: nova-sprint land --stream s1 --base <branch> --repo-dir <clone>", why,
			"every problem of the batch at once")
		require.Len(t, also, 1, "with every head that is not a commit")
		assert.Contains(t, also[0], "the head s1-1 of s1-1 is not a commit id")
		why, also = l.placeWhy("s1", []landCard{{id: "s1-1", head: "s1-1", base: "-x"}})
		assert.Equal(t, "card s1-1 names the base -x, which is not a branch name; run: nova-sprint card s1-1", why, "an option for a base is refused alone")
		assert.Empty(t, also)
		why, _ = l.placeWhy("s1", []landCard{{id: "s1-1", head: "abcdef0", base: "main", repo: "https://example.invalid/r.git"}})
		assert.Empty(t, why, "a batch with its base and repository is placed")
		assert.Equal(t, []string{"-", "a", "a..c"}, []string{idSpan(nil), idSpan([]string{"a"}), idSpan([]string{"a", "b", "c"})}, "a batch is named by its first and last ids")
	})
	t.Run("head not a commit", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, headNotCommit("s1", landCard{id: "s1-1", head: "abcdef0"}))
		why := headNotCommit("s1", landCard{id: "s1-1", head: "s1-1"})
		assert.Contains(t, why, "the head s1-1 of s1-1 is not a commit id")
		assert.True(t, strings.HasSuffix(why, "nova-sprint resume --stream s1 --did 'returned s1-1 for rework'"), why)
	})
	t.Run("moved exactly", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name       string
			moved, ids []string
			want       bool
		}{
			{"the batch in another order", []string{"b merging -> landed", "a merging -> landed"}, []string{"a", "b"}, true},
			{"one short", []string{"a merging -> landed"}, []string{"a", "b"}, false},
			{"another card", []string{"a merging -> landed", "c merging -> landed"}, []string{"a", "b"}, false},
			{"a card named twice", []string{"a merging -> landed", "a merging -> landed"}, []string{"a", "a"}, false},
			{"not a landing", []string{"a merging -> stuck", "b merging -> landed"}, []string{"a", "b"}, false},
		} {
			assert.Equal(t, tc.want, movedExactly(tc.moved, tc.ids), tc.name)
		}
	})
	t.Run("prune", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ branch, base, want string }{
			{"", "main", "it records no branch"},
			{"main", "main", "is the base"},
			{"-x", "main", "is not a branch name"},
			{"sprint/a:refs/heads/main", "main", "not one the sprint names"},
			{"sprint/a..b", "main", "not one the sprint names"},
			{"sprint/a.lock", "main", "not one the sprint names"},
			{"sprint/a+b", "main", "not one the sprint names"},
		} {
			assert.Contains(t, pruneWhy(tc.branch, tc.base), tc.want, tc.branch)
		}
		assert.Empty(t, pruneWhy("sprint/s1-1.w1.g1.e13", "main"))
	})
}
