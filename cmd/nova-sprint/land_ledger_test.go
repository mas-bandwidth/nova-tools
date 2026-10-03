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

// fakeLedger is the generated ledger of these tests, a generality ledger by its path.
const fakeLedger = "internal/ci/testdata/generality/rows.txt"

// fakeLedgerRun is a generated, shrink-only ledger's update run as the owning tests' is
// (internal/ci, NOVA_CI_UPDATE=1): the ledger is the files under debt/ under a ceiling
// line; a run that writes says "updated, rerun" and fails, one with nothing to write
// passes.
const fakeLedgerRun = `L=` + fakeLedger + `
n=$(ls debt 2>/dev/null | wc -l | tr -d ' ')
new=$(printf '# ceiling: %s\n' "$n"; ls debt 2>/dev/null)
[ "$new" = "$(cat $L 2>/dev/null)" ] && exit 0
printf '%s\n' "$new" > $L
echo 'updated, rerun'
exit 1`

// ledgerRig is the land rig with the fake ledger's update run as the generality family's,
// and a base holding debt/a, debt/b and their ledger.
func ledgerRig(t *testing.T, run string) *landRig {
	t.Helper()
	r := newLandRig(t)
	r.a.ledgers = []landLedger{{owns: generalityLedger, tests: "TestFakeLedger", run: []string{"sh", "-c", run}}}
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the debt", map[string]string{"debt/a": "a\n", "debt/b": "b\n", fakeLedger: "# ceiling: 2\na\nb\n", "notes.tsv": "one\n",
		"internal/ci/testdata/generality/x.go": "package x\n"})
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
// ledger, and lands both, the resolution on the card's timeline. Anything else is
// refused as a conflict as before, the merge aborted and the card stuck: a conflict
// outside the generated ledgers (a prose file, beside a ledger or alone, a Go file in a
// ledger's directory), an update run that fails, one that writes a tracked file or a new
// one outside the ledgers, and one that never settles, cut at its fourth run.
func TestLandResolvesAConflictOnlyInGeneratedLedgers(t *testing.T) {
	t.Parallel()
	debtA := map[string]string{"debt/a": "", fakeLedger: "# ceiling: 1\nb\n"}
	debtB := map[string]string{"debt/b": "", fakeLedger: "# ceiling: 1\na\n"}
	with := func(m map[string]string, k, v string) map[string]string {
		out := map[string]string{k: v}
		for a, b := range m {
			out[a] = b
		}
		return out
	}
	for _, tc := range []struct {
		name          string
		run           string // the update run; the three rows that write more are given theirs below
		first, second map[string]string
		why           string // "" lands both
		runs          int    // the update runs counted, when not 0
	}{
		{"a ledger-only conflict lands", fakeLedgerRun, debtA, debtB, "", 0},
		{"a mixed conflict is refused", fakeLedgerRun, with(debtA, "notes.tsv", "first\n"), with(debtB, "notes.tsv", "second\n"), "does not merge", 0},
		{"a prose conflict is refused", fakeLedgerRun, map[string]string{"notes.tsv": "first\n"}, map[string]string{"notes.tsv": "second\n"}, "does not merge", 0},
		{"a Go file in a ledger directory is refused", fakeLedgerRun,
			map[string]string{"internal/ci/testdata/generality/x.go": "package x // first\n"},
			map[string]string{"internal/ci/testdata/generality/x.go": "package x // second\n"}, "does not merge", 0},
		{"a failed update run is refused", "echo 'rows.txt: a listed row grew'; exit 1", debtA, debtB, "its generated ledgers conflict and TestFakeLedger did not regenerate them", 0},
		{"an update that writes a tracked file is refused", fakeLedgerRun + "\n", debtA, debtB, "the update run changed notes.tsv, which is no ledger of TestFakeLedger", 0},
		{"an update that writes a new file is refused", fakeLedgerRun + "\n", debtA, debtB, "the update run changed stray.txt, which is no ledger of TestFakeLedger", 0},
		{"an update that never settles is refused at the fourth run", "", debtA, debtB, "TestFakeLedger still rewrote them after 4 update runs", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			count := filepath.Join(t.TempDir(), "runs")
			run := tc.run
			switch {
			case strings.Contains(tc.name, "tracked file"):
				run = "echo more >> notes.tsv\n" + fakeLedgerRun
			case strings.Contains(tc.name, "new file"):
				run = "touch stray.txt\n" + fakeLedgerRun
			case tc.runs > 0:
				run = "echo run >> " + count + "\necho x >> " + fakeLedger + "\necho 'updated, rerun'\nexit 1"
			}
			r := ledgerRig(t, run)
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", tc.first), "s1-2": r.card("s1-2", tc.second)}
			r.queued(heads, "s1-1", "s1-2")
			before := r.git(r.remote, "rev-parse", "main")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.runs > 0 {
				runs, err := os.ReadFile(count)
				require.NoError(t, err)
				assert.Equal(t, tc.runs, strings.Count(string(runs), "run"), "the update runs")
			}
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 does not merge")
				assert.Contains(t, errs, tc.why)
				assert.Equal(t, []string{"land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the refused merge is aborted and what the update wrote is gone")
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.NotEqual(t, before, r.git(r.remote, "rev-parse", "main"))
			assert.Equal(t, []string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)", "the debt", "base"}, r.mainLog())
			assert.Equal(t, "# ceiling: 0", r.git(r.remote, "show", "main:"+fakeLedger), "the ledger is the merged tree's, regenerated")
			assert.Equal(t, "one", r.git(r.remote, "show", "main:notes.tsv"))
			body := r.git(r.remote, "log", "-1", "--format=%b", "main")
			assert.Contains(t, body, "The generated ledgers "+fakeLedger+" conflicted. The tip's side was taken and TestFakeLedger regenerated them at the merged tree (NOVA_CI_UPDATE=1).")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			story := r.ok("card s1-2")
			assert.Contains(t, story, "merged into s1 and landed by coordinator in a batch of 2 (with s1-1), ci green; the generated ledgers "+fakeLedger+" conflicted and were regenerated at the merge by TestFakeLedger (NOVA_CI_UPDATE=1)")
			assert.NotContains(t, r.ok("card s1-1"), "regenerated", "a card that merged plainly says nothing more")
			r.clean()
		})
	}
}

// resumedRig is two cards queued, s1-2 recording its branch, the stream stopped on a
// conflict on s1-2 and resumed after the coordinator worked on s1-2's branch (resolve:
// on the worker, with origin's main moved by one commit, ending on the branch): the
// rig, the heads and the tip pushed.
func resumedRig(t *testing.T, resolve func(r *landRig) string) (*landRig, map[string]string, string) {
	t.Helper()
	r := ledgerRig(t, fakeLedgerRun)
	r.branch = func(id string) string { return "sprint/" + id }
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"one.txt": "one\n"}), "s1-2": r.card("s1-2", map[string]string{"notes.tsv": "second\n"})}
	r.queued(heads, "s1-1", "s1-2")
	r.ok("merge --stream s1 --conflict s1-2")
	r.moveBase("main", "moved.txt")
	tip := resolve(r)
	r.git(r.worker, "push", "-q", "origin", ":refs/heads/sprint/s1-2")
	r.git(r.worker, "push", "-q", "origin", "sprint/s1-2")
	r.ok("resume --stream s1 --did 'merged the base into the branch of s1-2'")
	return r, heads, tip
}

// baseMerged merges origin's main into s1-2's branch on the worker: its sha.
func baseMerged(r *landRig) string {
	r.git(r.worker, "switch", "-q", "sprint/s1-2")
	r.git(r.worker, "merge", "-q", "--no-edit", "origin/main")
	return r.git(r.worker, "rev-parse", "HEAD")
}

// A card a resume put back after a conflict lands its branch's tip only when the tip is
// exactly the base merged into the head its readers read: then the tip lands, recorded
// beside the head, and its timeline says so. A tip carrying one more line, or one that
// does not descend from the head, is refused in one line naming both commits, and the
// stream stops again: no change a reader has not read lands.
func TestLandResumedAfterAConflictLandsOnlyTheBaseMerge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		resolve func(r *landRig) string
		why     string // "" lands the tip
	}{
		{"the exact base merge lands", baseMerged, ""},
		{"a tip with one more line is refused", func(r *landRig) string {
			baseMerged(r)
			return r.files("and one more", map[string]string{"notes.tsv": "second\nunread\n"})
		}, "carries changes beyond the base merge and the ledgers of its head"},
		{"a base merge with one more line in it is refused", func(r *landRig) string {
			r.git(r.worker, "switch", "-q", "sprint/s1-2")
			r.git(r.worker, "merge", "-q", "--no-commit", "--no-ff", "origin/main")
			return r.files("the base merged, and a line", map[string]string{"notes.tsv": "second\nunread\n"})
		}, "carries changes beyond the base merge and the ledgers of its head"},
		{"a tip that does not descend is refused", func(r *landRig) string {
			r.git(r.worker, "switch", "-q", "--no-track", "-C", "sprint/s1-2", "origin/main")
			return r.files("again", map[string]string{"notes.tsv": "second\n"})
		}, "does not descend from its recorded head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, heads, tip := resumedRig(t, tc.resolve)
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.why != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids=s1-2 fact=conflict reason=the tip "+tip+" of the branch sprint/s1-2 of s1-2 ")
				assert.Contains(t, errs, tc.why+" "+heads["s1-2"])
				assert.Equal(t, "stopped conflict", r.streamState("s1"))
				assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
				assert.Equal(t, 1, strings.Count(r.git(r.clone, "worktree", "list"), "\n")+1, "the scratch worktree is removed")
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.Equal(t, "second", r.git(r.remote, "show", "main:notes.tsv"))
			assert.Equal(t, tip, r.git(r.remote, "rev-parse", "main^2"), "the tip is what merged")
			var v cardView
			r.json("card s1-2", &v)
			assert.Equal(t, "landed", v.Primary.Col)
			assert.Equal(t, tip, v.Primary.F("landed_head"), "the tip it landed is recorded")
			assert.Equal(t, heads["s1-2"], v.Primary.F("head"), "its head stays the one its readers read")
			assert.Contains(t, r.ok("card s1-2"), "; resumed after a conflict: landed the tip "+tip+" of the branch sprint/s1-2, the base merged into the head "+heads["s1-2"]+" it stopped on and nothing else")
			assert.Equal(t, 1, strings.Count(r.git(r.clone, "worktree", "list"), "\n")+1, "the scratch worktree is removed")
			r.clean()
		})
	}
}

// The tip a resumed card lands is pinned the first time land verifies it: a land whose
// push was not reported, run again, lands that tip and pushes nothing new, though the
// branch moved on in between (tla/Land.tla, Recovers: its merges and push are no-ops).
func TestLandPinsAResumedTipAcrossAnUnreportedPush(t *testing.T) {
	t.Parallel()
	r, _, tip := resumedRig(t, baseMerged)
	r.a.beforePush = func(int) {
		r.ok("return s1-1 --reason 'taken back under the push'")
		r.files("after the pin", map[string]string{"notes.tsv": "second\nlater\n"})
		r.git(r.worker, "push", "-q", "origin", "sprint/s1-2")
	}
	code, _, errs := r.do("land --repo-dir " + r.clone + " --base main")
	require.Equal(t, 2, code, errs)
	pushed := r.git(r.remote, "rev-parse", "main")
	r.a.beforePush = nil
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main tip="+pushed+" ids=s1-2")
	assert.Equal(t, pushed, r.git(r.remote, "rev-parse", "main"), "the recovery pushed nothing new")
	assert.Equal(t, "second", r.git(r.remote, "show", "main:notes.tsv"), "the line pushed after the pin never lands")
	var v cardView
	r.json("card s1-2", &v)
	assert.Equal(t, tip, v.Primary.F("landed_head"))
	r.clean()
}

// The decisions behind a resolution, apart from git: which files are generality ledgers,
// which ledgers own the conflicted paths, the paths git ls-files --unmerged names and the
// tip's side among them, a symlink an update could write through, what an update wrote,
// and when an update run is done.
func TestLandLedgerDecisions(t *testing.T) {
	t.Parallel()
	t.Run("generality ledgers", func(t *testing.T) {
		t.Parallel()
		for p, want := range map[string]bool{
			"internal/ci/testdata/generality/cmd/nova-bus.txt":                true,
			"internal/ci/testdata/generality-text/docs.txt":                   true,
			"internal/ci/testdata/generality-text/cmd/x/testdata/y.txt":       true,
			"internal/ci/testdata/generality_text_fixtures_allowlist.txt":     true,
			"internal/ci/testdata/generality":                                 false,
			"internal/ci/testdata/generality/x.go":                            false,
			"internal/ci/testdata/generality-text/sub/y_test.go":              false,
			"internal/ci/testdata/testify/internal/ci.txt":                    false,
			"internal/ci/testdata/net-allowlist.txt":                          false,
			"internal/ci/testdata/generalityX/a.txt":                          false,
			"cmd/internal/ci/testdata/generality/a.txt":                       false,
			"internal/ci/testdata/generality_text_fixtures_allowlist.txt.bak": false,
		} {
			assert.Equal(t, want, generalityLedger(p), p)
		}
	})
	ledgers := []landLedger{{owns: generalityLedger, tests: "A"}, {owns: func(p string) bool { return strings.HasPrefix(p, "other/") }, tests: "B"}}
	t.Run("owners", func(t *testing.T) {
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
	})
	t.Run("unmerged", func(t *testing.T) {
		t.Parallel()
		paths, ours := unmergedPaths("100644 aaa 1\tl.txt\n100644 bbb 2\tl.txt\n100644 ccc 3\tl.txt\n100644 ddd 1\tgone.txt\n100644 eee 3\tgone.txt")
		assert.Equal(t, []string{"l.txt", "gone.txt"}, paths)
		assert.Equal(t, map[string]bool{"l.txt": true}, ours)
	})
	t.Run("links", func(t *testing.T) {
		t.Parallel()
		files := "100644 a 0\tother/plain.txt\n120000 b 0\tother/link.txt\n120000 c 0\tdir\n120000 d 0\telsewhere"
		assert.Equal(t, "other/link.txt", linkedLedger(files, nil, ledgers), "a ledger that is a link")
		assert.Equal(t, "dir", linkedLedger("120000 c 0\tdir", []string{"dir/x.txt"}, ledgers), "a directory a conflicted path lies under")
		assert.Empty(t, linkedLedger("120000 d 0\telsewhere\n100644 a 0\tother/plain.txt", []string{"other/plain.txt"}, ledgers))
	})
	t.Run("wrote", func(t *testing.T) {
		t.Parallel()
		status := "M  staged-by-the-merge.go\x00 M worktree.txt\x00MM both.txt\x00?? new.txt\x00R  to.txt\x00from.txt\x00"
		assert.Equal(t, []string{"worktree.txt", "both.txt", "new.txt"}, updateWrote(status))
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
