//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{"the update run reads the module read-only", fakeLedgerRun, debtA, debtB, "", 0},
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

// An update rewrites its whole family in place, so before any update run every path it
// writes, and every directory on the way, is held to be no symlink, in the tree and on
// disk, whichever paths conflicted: a family directory linked outside the clone, with
// the conflict only in another, is refused, and the update never writes through it. Each
// row is held by one of the two checks alone: a tracked link checked out as a plain file
// (core.symlinks off) only the tree shows, an untracked link only the disk.
func TestLandRefusesAResolutionThroughASymlink(t *testing.T) {
	t.Parallel()
	const linked = "internal/ci/testdata/generality-text"
	for _, tc := range []struct {
		name            string
		tracked, asFile bool
	}{
		{"a tracked link to a family directory no conflict lies under", true, false},
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
			assert.Contains(t, errs, "ids=s1-2 fact=conflict reason=the head "+heads["s1-2"]+" of s1-2 does not merge")
			assert.Contains(t, errs, "its generated ledgers conflict and "+linked+" is a symlink, which an update would write through")
			assert.NoFileExists(t, filepath.Join(outside, "leak.txt"), "no update ran through the link")
			assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "merging/stuck"}, r.places("s1-1", "s1-2"))
			r.clean()
		})
	}
}
