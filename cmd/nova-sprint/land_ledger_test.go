package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
)

func TestLandGitPreservesNULStatus(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.files("tracked statuses", map[string]string{"a.txt": "base\n", "b.txt": "base\n", "c.txt": "base\n"})
	for _, name := range []string{"a.txt", "b.txt", "c.txt", " new.txt "} {
		require.NoError(t, os.WriteFile(filepath.Join(r.worker, name), []byte("changed\n"), 0o600))
	}
	r.git(r.worker, "add", "--", "b.txt", "c.txt")
	require.NoError(t, os.WriteFile(filepath.Join(r.worker, "c.txt"), []byte("changed again\n"), 0o600))
	l := &lander{a: r.a}
	status, err := l.git(context.Background(), r.worker, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	require.NoError(t, err)
	assert.Equal(t, " M a.txt\x00M  b.txt\x00MM c.txt\x00??  new.txt \x00", status)
	assert.Equal(t, []string{"a.txt", "c.txt", " new.txt "}, updateWrote(status))
	paths, err := l.git(context.Background(), r.worker, "ls-files", "--others", "--exclude-standard", "-z")
	require.NoError(t, err)
	assert.Equal(t, " new.txt \x00", paths)
	head, err := l.git(context.Background(), r.worker, "rev-parse", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, r.git(r.worker, "rev-parse", "HEAD"), head)
}

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
	fake := landLedgers[0]
	fake.tests, fake.run = "TestFakeLedger", []string{"sh", "-c", run}
	r.a.ledgers = []landLedger{fake}
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

// moveLedgerBase puts a third debt row and its ledger on origin's main after the cards
// were cut, so each card's ledger change meets a tip that changed the same lines: the
// merges stop on the generated ledger alone.
func moveLedgerBase(r *landRig) {
	r.t.Helper()
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("debt c", map[string]string{"debt/c": "c\n", fakeLedger: "# ceiling: 3\na\nb\nc\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
}

// A card's change to a generated ledger is no change land merges: two cards that each
// delete a debt row and move the shared ceiling line land, each head's ledger change
// taken off its merge (or, on a tip that moved the same lines, the merge's conflict in it
// taken from the tip), and the ledger regenerated once for the batch at its merged tree
// by its tests' update run, amended into the batch's last merge, which names the cards
// and the ledger; each card's timeline says so. Anything else is refused as before: a
// conflict outside the generated ledgers (a prose file, beside a ledger or alone, a Go
// file in a ledger's directory) is the card's whose merge stops, and an update run that
// fails, writes a tracked file or a new one outside the ledgers, or never settles (cut at
// its fourth run, each prefix probed) blames the first card whose ledgers it was to
// make, nothing after it landed, the clone clean.
func TestLandRegeneratesTheGeneratedLedgersOnceABatch(t *testing.T) {
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
	const dropped, conflicted = "its head changed were taken off its merge and regenerated", "conflicted and were regenerated"
	for _, tc := range []struct {
		name          string
		run           string // the update run; the rows that write more are given theirs below
		moved         bool   // the tip moved the ledger's lines after the cards were cut
		first, second map[string]string
		blamed        string // the card refused; "" lands both
		why           string
		runs          int // the update runs counted, when not 0
	}{
		{"a card's own ledger change lands", fakeLedgerRun, false, debtA, debtB, "", "", 0},
		{"a ledger-only conflict lands", fakeLedgerRun, true, debtA, debtB, "", "", 0},
		{"the update run reads the module read-only", fakeLedgerRun, false, debtA, debtB, "", "", 0},
		{"a mixed conflict is refused", fakeLedgerRun, false, with(debtA, "notes.tsv", "first\n"), with(debtB, "notes.tsv", "second\n"), "s1-2", "does not merge", 0},
		{"a prose conflict is refused", fakeLedgerRun, false, map[string]string{"notes.tsv": "first\n"}, map[string]string{"notes.tsv": "second\n"}, "s1-2", "does not merge", 0},
		{"a Go file in a ledger directory is refused", fakeLedgerRun, false,
			map[string]string{"internal/ci/testdata/generality/x.go": "package x // first\n"},
			map[string]string{"internal/ci/testdata/generality/x.go": "package x // second\n"}, "s1-2", "does not merge", 0},
		{"a failed update run is refused", "echo 'rows.txt: a listed row grew'; exit 1", false, debtA, debtB, "s1-1",
			"changes the generated ledgers " + fakeLedger + ", which land regenerates and could not: its generated ledgers were taken from the tip and TestFakeLedger did not regenerate them", 0},
		{"a failed update run on a conflict is refused", "echo 'rows.txt: a listed row grew'; exit 1", true, debtA, debtB, "s1-1",
			"does not merge: git merge: exit status 1: ", 0},
		{"an update that writes a tracked file is refused", fakeLedgerRun + "\n", false, debtA, debtB, "s1-1", "the update run changed notes.tsv, which is no ledger of TestFakeLedger", 0},
		{"an update that writes a new file is refused", fakeLedgerRun + "\n", false, debtA, debtB, "s1-1", "the update run changed stray.txt, which is no ledger of TestFakeLedger", 0},
		{"an update that never settles is refused at the fourth run", "", false, debtA, debtB, "s1-1", "TestFakeLedger still rewrote them after 4 update runs", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			count := filepath.Join(t.TempDir(), "runs")
			run := tc.run
			switch {
			case strings.Contains(tc.name, "read-only"):
				run = "[ \"$GOFLAGS\" = -mod=readonly ] || { echo \"GOFLAGS=$GOFLAGS\"; exit 1; }\n" + fakeLedgerRun
			case strings.Contains(tc.name, "tracked file"):
				run = "echo more >> notes.tsv\n" + fakeLedgerRun
			case strings.Contains(tc.name, "new file"):
				run = "touch stray.txt\n" + fakeLedgerRun
			case tc.runs > 0:
				run = "echo run >> " + count + "\necho x >> " + fakeLedger + "\necho 'updated, rerun'\nexit 1"
			}
			r := ledgerRig(t, run)
			if strings.Contains(tc.name, "read-only") {
				r.a.gitEnv = append(r.a.gitEnv, "GOFLAGS=-mod=mod") // the caller's, replaced by the run's
			}
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", tc.first), "s1-2": r.card("s1-2", tc.second)}
			log := []string{"the debt", "base"}
			if tc.moved {
				moveLedgerBase(r)
				log = append([]string{"debt c"}, log...)
			}
			r.queued(heads, "s1-1", "s1-2")
			before := r.git(r.remote, "rev-parse", "main")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.runs > 0 {
				runs, err := os.ReadFile(count)
				require.NoError(t, err)
				assert.Equal(t, tc.runs, strings.Count(string(runs), "run"), "the update runs: four for the batch, four for its probe")
			}
			if tc.blamed != "" {
				assert.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "LAND REFUSED stream=s1 cards=1 base=- tip=- ids="+tc.blamed+" fact=conflict reason=the head "+heads[tc.blamed]+" of "+tc.blamed+" ")
				assert.Contains(t, errs, tc.why)
				places := map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}
				if tc.blamed == "s1-1" {
					places = map[string]string{"s1-1": "merging/stuck", "s1-2": "merging/queued"}
					assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "nothing landed")
				} else {
					log = append([]string{"land s1-1 (sprint stream s1)"}, log...)
				}
				assert.Equal(t, log, r.mainLog())
				assert.Equal(t, places, r.places("s1-1", "s1-2"))
				assert.Empty(t, r.git(r.clone, "status", "--porcelain", "--untracked-files=all"), "the refused merge is aborted and what the update wrote is gone")
				r.clean()
				return
			}
			assert.Equal(t, 0, code, out+errs)
			assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
			assert.NotEqual(t, before, r.git(r.remote, "rev-parse", "main"))
			assert.Equal(t, append([]string{"land s1-2 (sprint stream s1)", "land s1-1 (sprint stream s1)"}, log...), r.mainLog())
			how, want := dropped, "# ceiling: 0"
			if tc.moved {
				how, want = conflicted, "# ceiling: 1\nc"
			}
			assert.Equal(t, want, r.git(r.remote, "show", "main:"+fakeLedger), "the ledger is the merged tree's, regenerated")
			assert.Equal(t, "one", r.git(r.remote, "show", "main:notes.tsv"))
			body := r.git(r.remote, "log", "-1", "--format=%b", "main")
			assert.Contains(t, body, "The generated ledgers "+fakeLedger+" were taken from the tip at the merge of s1-1, s1-2 and TestFakeLedger regenerated them at this merged tree (NOVA_CI_UPDATE=1), once for the batch.")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
			assert.Contains(t, r.ok("card s1-2"), "merged into s1 and landed by coordinator in a batch of 2 (with s1-1), ci green; the generated ledgers "+fakeLedger+" "+how+" at the merge by TestFakeLedger (NOVA_CI_UPDATE=1)")
			assert.Contains(t, r.ok("card s1-1"), "the generated ledgers "+fakeLedger+" "+how+" once for the batch, at its merge of s1-2, by TestFakeLedger (NOVA_CI_UPDATE=1)")
			r.clean()
		})
	}
}

// The ledgers are regenerated once a batch, not once a head: three cards that each move
// the generated ledger land with one regeneration, the update run made twice (once to
// write, once to find nothing left to write), where a regeneration at each merge made it
// at least once a head.
func TestLandRegeneratesTheLedgersOnceForThreeCards(t *testing.T) {
	t.Parallel()
	count := filepath.Join(t.TempDir(), "runs")
	r := ledgerRig(t, "echo run >> "+count+"\n"+fakeLedgerRun)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("more debt", map[string]string{"debt/c": "c\n", fakeLedger: "# ceiling: 3\na\nb\nc\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{
		"s1-1": r.card("s1-1", map[string]string{"debt/a": "", fakeLedger: "# ceiling: 2\nb\nc\n"}),
		"s1-2": r.card("s1-2", map[string]string{"debt/b": "", fakeLedger: "# ceiling: 2\na\nc\n"}),
		"s1-3": r.card("s1-3", map[string]string{"debt/c": "", fakeLedger: "# ceiling: 2\na\nb\n"}),
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	out := r.ok("land --repo-dir " + r.clone + " --base main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=3 base=main")
	runs, err := os.ReadFile(count)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(runs), "run"), "one regeneration for the batch: a run that writes, a run that finds it done")
	assert.Equal(t, "# ceiling: 0", r.git(r.remote, "show", "main:"+fakeLedger))
	r.clean()
}

// A lint ledger (staticcheck's and errcheck's package shards) a child committed is no
// change outside its PATHS: the lander takes it off the merge and its update run makes it
// at the batch's tip, so the card lands and the stream does not stop (2026-10-04: five
// lint cards stopped the lint stream, thirty queued behind each, for E12 on their shards).
func TestLandTakesALintLedgerOffTheMergeAndRegeneratesIt(t *testing.T) {
	t.Parallel()
	const shard = "internal/ci/testdata/errcheck/cmd/nova-memory.txt"
	r := newLandRig(t)
	lint := landLedgers[1]
	lint.tests = "TestFakeLint"
	lint.run = []string{"sh", "-c", `n=$(ls work 2>/dev/null | wc -l | tr -d ' ')
new=$(printf 'cmd/nova-memory:unchecked %s\n' "$n")
[ "$new" = "$(cat ` + shard + `)" ] && exit 0
printf '%s\n' "$new" > ` + shard + `
echo 'updated, rerun'
exit 1`}
	r.a.ledgers = []landLedger{landLedgers[0], lint}
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the lint ledger", map[string]string{"work/a": "a\n", "work/b": "b\n", shard: "cmd/nova-memory:unchecked 2\n"})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	dir := t.TempDir()
	r.ok("add --stream s1 --brief-file " + writeNeedsBrief(t, dir, "c1", "Fix c1.\nPATHS: work/a", "") + " --brief-file " + writeNeedsBrief(t, dir, "c2", "Fix c2.\nPATHS: work/b", ""))
	heads := map[string]string{
		"c1": r.card("c1", map[string]string{"work/a": "", shard: "cmd/nova-memory:unchecked 1\n"}),
		"c2": r.card("c2", map[string]string{"work/b": "", shard: "cmd/nova-memory:unchecked 1\n", "internal/ci/testdata/staticcheck/cmd/nova-redis.txt": "x 1\n"}),
	}
	r.queued(heads, "c1", "c2")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
	assert.NotContains(t, errs, "E12")
	assert.Equal(t, "cmd/nova-memory:unchecked 0", r.git(r.remote, "show", "main:"+shard), "the shard is the merged tree's, made by the update run")
	assert.Empty(t, r.git(r.remote, "ls-tree", "main", "internal/ci/testdata/staticcheck/cmd/nova-redis.txt"), "a shard the child added and the update does not make is not landed")
	assert.Equal(t, map[string]string{"c1": "landed/merged", "c2": "landed/merged"}, r.places("c1", "c2"))
	assert.Contains(t, r.ok("card c1"), "the generated ledgers "+shard+" its head changed were taken off its merge and regenerated once for the batch, at its merge of c2, by TestFakeLint")
	r.clean()
}

// An update rewrites its whole family in place, so before any update run every path it
// writes, and every directory on the way, is held to be no symlink, in the tree and on
// disk, whichever paths the cards changed: a family directory linked outside the clone,
// with the cards' changes only in another, is refused, and the update never writes
// through it. Each row is held by one of the two checks alone: a tracked link checked out
// as a plain file (core.symlinks off) only the tree shows, an untracked link only the disk.
func TestLandRefusesAResolutionThroughASymlink(t *testing.T) {
	t.Parallel()
	const linked = "internal/ci/testdata/generality-text"
	for _, tc := range []struct {
		name            string
		tracked, asFile bool
	}{
		{"a tracked link to a family directory no change lies under", true, false},
		{"a tracked link the checkout writes as a plain file", true, true},
		{"an untracked link on disk", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			outside := t.TempDir()
			r := ledgerRig(t, "[ -d "+linked+" ] && echo leak > "+linked+"/leak.txt\n"+fakeLedgerRun)
			if tc.tracked {
				require.NoError(t, os.Symlink(outside, filepath.Join(r.worker, linked)))
				r.git(r.worker, "add", "--", linked)
				r.git(r.worker, "commit", "-q", "-m", "a linked ledger directory")
				r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
				r.git(r.worker, "fetch", "-q", "origin")
			}
			if tc.asFile {
				r.git(r.clone, "config", "core.symlinks", "false")
			}
			r.ok("add --stream s1 --count 2")
			heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"debt/a": "", fakeLedger: "# ceiling: 1\nb\n"}),
				"s1-2": r.card("s1-2", map[string]string{"debt/b": "", fakeLedger: "# ceiling: 1\na\n"})}
			r.queued(heads, "s1-1", "s1-2")
			if !tc.tracked {
				require.NoError(t, os.MkdirAll(filepath.Join(r.clone, filepath.Dir(linked)), 0o755))
				require.NoError(t, os.Symlink(outside, filepath.Join(r.clone, linked)))
			}
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			assert.Equal(t, 1, code, out+errs)
			assert.Contains(t, errs, "ids=s1-1 fact=conflict reason=the head "+heads["s1-1"]+" of s1-1 changes the generated ledgers "+fakeLedger)
			assert.Contains(t, errs, "its generated ledgers are regenerated here and "+linked+" is a symlink, which an update would write through")
			assert.NoFileExists(t, filepath.Join(outside, "leak.txt"), "no update ran through the link")
			assert.Equal(t, map[string]string{"s1-1": "merging/stuck", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"))
			r.clean()
		})
	}
}

// The decisions behind a resolution, apart from git: which ledgers own the conflicted paths, the paths git ls-files --unmerged names and the
// tip's side among them, a tracked symlink an update could write through and the paths
// the disk check walks, what an update wrote,
// and when an update run is done.
func TestLandLedgerDecisions(t *testing.T) {
	t.Parallel()
	ledgers := []landLedger{{owns: diffcheck.GeneralityLedger, tests: "A"}, {owns: func(p string) bool { return strings.HasPrefix(p, "other/") }, tests: "B"}}
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
	assert.True(t, strings.HasPrefix(ledgerNote([]string{"a", "b"}, "A", "", true), "the generated ledgers a, b conflicted and were regenerated at the merge by A"))
	assert.Contains(t, ledgerNote([]string{"a"}, "A", "c9", false), "the generated ledgers a its head changed were taken off its merge and regenerated once for the batch, at its merge of c9, by A")
}
