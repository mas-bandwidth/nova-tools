package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLedgerRun is a generated, shrink-only ledger's update run as the owning tests' is
// (internal/ci, NOVA_CI_UPDATE=1): ledger/rows.tsv is the files under debt/ under a
// ceiling line; a run that writes says "updated, rerun" and fails, one that has nothing
// to write passes.
const fakeLedgerRun = `n=$(ls debt 2>/dev/null | wc -l | tr -d ' ')
new=$(printf '# ceiling: %s\n' "$n"; ls debt 2>/dev/null)
[ "$new" = "$(cat ledger/rows.tsv 2>/dev/null)" ] && exit 0
printf '%s\n' "$new" > ledger/rows.tsv
echo 'updated, rerun'
exit 1`

// ledgerRig is the land rig with the fake ledger as the generated ledgers land knows,
// and a base holding debt/a, debt/b and their ledger.
func ledgerRig(t *testing.T, run string) *landRig {
	t.Helper()
	r := newLandRig(t)
	r.a.ledgers = []landLedger{{globs: []string{"ledger/**"}, tests: "TestFakeLedger", run: []string{"sh", "-c", run}}}
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the debt", map[string]string{"debt/a": "a\n", "debt/b": "b\n", "ledger/rows.tsv": "# ceiling: 2\na\nb\n", "notes.tsv": "one\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	return r
}

// files writes (or, for "", deletes) files in the worker's clone and commits them.
func (r *landRig) files(msg string, files map[string]string) string {
	r.t.Helper()
	for f, text := range files {
		p := filepath.Join(r.worker, f)
		if text == "" {
			r.git(r.worker, "rm", "-q", "--", f)
			continue
		}
		require.NoError(r.t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(r.t, os.WriteFile(p, []byte(text), 0o600))
		r.git(r.worker, "add", "--", f)
	}
	r.git(r.worker, "commit", "-q", "-m", msg)
	return r.git(r.worker, "rev-parse", "HEAD")
}

// card is a card's work on its own branch from origin's main: its sha.
func (r *landRig) card(id string, files map[string]string) string {
	r.t.Helper()
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/"+id, "refs/remotes/origin/main")
	return r.files("work of "+id, files)
}

// Two cards that each delete a debt row and move the shared ceiling line conflict line
// by line in the generated ledger; land takes the tip's side, regenerates the ledger at
// the merged tree with its tests' update run, commits the merge naming the card and the
// ledger, and lands both, the resolution on the card's timeline. Any conflict outside
// the generated ledgers (a prose file, beside a ledger or alone), or an update run that
// fails, is refused as a conflict as before: the merge aborted, the card stuck.
func TestLandResolvesAConflictOnlyInGeneratedLedgers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		run    string
		first  map[string]string
		second map[string]string
		why    string // "" lands both
	}{
		{"a ledger-only conflict lands", fakeLedgerRun,
			map[string]string{"debt/a": "", "ledger/rows.tsv": "# ceiling: 1\nb\n"},
			map[string]string{"debt/b": "", "ledger/rows.tsv": "# ceiling: 1\na\n"}, ""},
		{"a mixed conflict is refused", fakeLedgerRun,
			map[string]string{"debt/a": "", "ledger/rows.tsv": "# ceiling: 1\nb\n", "notes.tsv": "first\n"},
			map[string]string{"debt/b": "", "ledger/rows.tsv": "# ceiling: 1\na\n", "notes.tsv": "second\n"}, "does not merge"},
		{"a prose conflict is refused", fakeLedgerRun,
			map[string]string{"notes.tsv": "first\n"},
			map[string]string{"notes.tsv": "second\n"}, "does not merge"},
		{"a failed update run is refused", "echo 'rows.tsv: a listed row grew'; exit 1",
			map[string]string{"debt/a": "", "ledger/rows.tsv": "# ceiling: 1\nb\n"},
			map[string]string{"debt/b": "", "ledger/rows.tsv": "# ceiling: 1\na\n"}, "its generated ledgers conflict and TestFakeLedger did not regenerate them"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := ledgerRig(t, tc.run)
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", tc.first), "s1-2": r.card("s1-2", tc.second)}
			r.queued(heads, "s1-1", "s1-2")
			before := r.git(r.remote, "rev-parse", "main")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 does not merge")
				assert.Contains(t, errs, tc.why)
				assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=no"), "the refused merge is aborted")
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.NotEqual(t, before, r.git(r.remote, "rev-parse", "main"))
			assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
			assert.Equal(t, "# ceiling: 0", r.git(r.remote, "show", "main:ledger/rows.tsv"), "the ledger is the merged tree's, regenerated")
			assert.Equal(t, "one", r.git(r.remote, "show", "main:notes.tsv"))
			body := r.git(r.remote, "log", "-1", "--format=%b", "main")
			assert.Contains(t, body, "The generated ledgers ledger/rows.tsv conflicted. The tip's side was taken and TestFakeLedger regenerated them at the merged tree (NOVA_CI_UPDATE=1).")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			story := r.ok("card s1-2")
			assert.Contains(t, story, "merged into s1 and landed by coordinator in a batch of 2 (with s1-1), ci green; the generated ledgers ledger/rows.tsv conflicted and were regenerated at the merge by TestFakeLedger (NOVA_CI_UPDATE=1)")
			assert.NotContains(t, r.ok("card s1-1"), "regenerated", "a card that merged plainly says nothing more")
			r.clean()
		})
	}
}

// A card a resume put back after a conflict lands its branch's tip when the coordinator
// merged the base into the branch and pushed it (the tip descends from the head it
// stopped on), the tip recorded on the card beside its head and the landing on its
// timeline; a tip
// that does not descend is refused with one line naming both commits, and the stream
// stops again.
func TestLandResumedAfterAConflictLandsTheBranchTip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		descend bool
	}{
		{"a descending tip lands", true},
		{"a tip that does not descend is refused", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := ledgerRig(t, fakeLedgerRun)
			r.branch = func(id string) string { return "sprint/" + id }
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"notes.tsv": "first\n"}), "s1-2": r.card("s1-2", map[string]string{"notes.tsv": "second\n"})}
			r.queued(heads, "s1-1", "s1-2")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			require.Equal(t, 1, code, out+errs)
			require.Equal(t, "stopped conflict", r.streamState("s1"))
			// the coordinator resolves on the card's branch and pushes it
			r.git(r.worker, "fetch", "-q", "origin")
			if tc.descend {
				r.git(r.worker, "switch", "-q", "sprint/s1-2")
				_, err := r.tryGit(r.worker, "merge", "-q", "--no-edit", "origin/main")
				require.Error(t, err, "the base conflicts with the branch")
			} else {
				r.git(r.worker, "switch", "-q", "--no-track", "-C", "sprint/s1-2", "origin/main")
			}
			tip := r.files("resolved s1-2", map[string]string{"notes.tsv": "first\nsecond\n"})
			r.git(r.worker, "push", "-q", "origin", ":refs/heads/sprint/s1-2")
			r.git(r.worker, "push", "-q", "origin", "sprint/s1-2")
			r.ok("resume --stream s1 --did 'merged the base into the branch of s1-2'")
			code, out, errs = r.do("land --repo-dir " + r.clone + " --base main")
			if !tc.descend {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the branch sprint/s1-2 of s1-2 is at "+tip+", which does not descend from its recorded head "+heads["s1-2"])
				assert.Equal(t, "stopped conflict", r.streamState("s1"))
				assert.Equal(t, map[string]string{"s1-2": "merging/stuck"}, r.places("s1-2"))
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
			assert.Equal(t, "first\nsecond", r.git(r.remote, "show", "main:notes.tsv"))
			assert.Equal(t, tip, r.git(r.remote, "rev-parse", "main^2"), "the tip is what merged")
			var v cardView
			r.json("card s1-2", &v)
			assert.Equal(t, "landed", v.Primary.Col)
			assert.Equal(t, tip, v.Primary.F("landed_head"), "the tip it landed is recorded")
			assert.Equal(t, heads["s1-2"], v.Primary.F("head"), "its head stays the one its readers read")
			assert.Contains(t, r.ok("card s1-2"), "; resumed after a conflict: landed the branch sprint/s1-2 at "+tip+", which descends from the head "+heads["s1-2"]+" it stopped on")
			r.clean()
		})
	}
}

// tryGit runs git in dir and returns its error, for a git the test expects to fail.
func (r *landRig) tryGit(dir string, args ...string) (string, error) {
	r.t.Helper()
	l := &lander{a: r.a}
	return l.git(r.t.Context(), dir, args...)
}

// The decisions behind a resolution, apart from git: which ledgers own the conflicted
// paths, the paths git ls-files --unmerged names and the tip's side among them, and when
// an update run is done.
func TestLandLedgerDecisions(t *testing.T) {
	t.Parallel()
	ledgers := []landLedger{{globs: []string{"internal/ci/testdata/generality/**", "list.txt"}, tests: "A"}, {globs: []string{"other/*.txt"}, tests: "B"}}
	t.Run("owners", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			paths   []string
			owners  string
			outside []string
		}{
			{"a shard", []string{"internal/ci/testdata/generality/cmd/x.txt"}, "A", nil},
			{"two ledgers", []string{"other/y.txt", "list.txt"}, "A, B", nil},
			{"a prose file beside", []string{"list.txt", "README.md"}, "A", []string{"README.md"}},
			{"a nested file no glob names", []string{"other/deep/z.txt"}, "", []string{"other/deep/z.txt"}},
		} {
			owners, outside := ledgerOwners(tc.paths, ledgers)
			assert.Equal(t, tc.owners, testsOf(owners), tc.name)
			assert.Equal(t, tc.outside, outside, tc.name)
		}
	})
	t.Run("unmerged", func(t *testing.T) {
		t.Parallel()
		paths, ours := unmergedPaths("100644 aaa 1\tl.txt\n100644 bbb 2\tl.txt\n100644 ccc 3\tl.txt\n100644 ddd 1\tgone.txt\n100644 eee 3\tgone.txt")
		assert.Equal(t, []string{"l.txt", "gone.txt"}, paths)
		assert.Equal(t, map[string]bool{"l.txt": true}, ours)
	})
	t.Run("regen", func(t *testing.T) {
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
	assert.True(t, strings.HasPrefix(ledgerNote([]string{"a", "b"}, "A"), "the generated ledgers a, b conflicted"))
}
