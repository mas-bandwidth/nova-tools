package main

// The red tests for `nova-pulse hygiene --lane-dirs <root>`, written before the
// machine.
//
// The mistake they remove, measured on the Studio on 2026-09-18: 92 `lane-*`
// and `dogfood-*` clone directories under ~/rowan-working/tmp, one per lane
// worker of the day, and nothing that ever takes one away. A lane clone is not
// scratch -- it holds a branch somebody may still be pushing -- so the verb may
// not sweep by age or by name. It removes a clone only when the work in it is
// FINISHED AND ELSEWHERE: the checkout is clean, every local branch is on the
// remote, and the forge says the pull request whose head is that branch is
// merged or closed. Anything else is kept, by name, with the reason.
//
// Every read of the world is a seam: the git reads (branch, status, local
// commits not on a remote) and the forge read (the PR whose head is a branch)
// are interfaces with a fake here, so no test in this file reads a real
// repository or the network. The one exception is TestOSLaneGitReadsARealCheckout
// at the foot, which drives a real `git init` in its own temporary directory --
// local only, no network -- because a seam nobody has ever run against the real
// thing is a seam that proves nothing.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// ---------------------------------------------------------------------------
// The two fakes, keyed by the CHECKOUT directory (which is the candidate itself
// for a `<root>/lane-foo/.git` clone and `<root>/lane-foo/repo` for the nested
// shape today's lane workers actually write).

type laneFakeGit struct {
	branch   map[string]string
	dirty    map[string]bool
	unpushed map[string]bool
	fail     map[string]bool
}

func (f *laneFakeGit) Branch(dir string) (string, error) {
	if f.fail[dir] {
		return "", os.ErrNotExist
	}
	return f.branch[dir], nil
}

func (f *laneFakeGit) Dirty(dir string) (bool, error) {
	if f.fail[dir] {
		return false, os.ErrNotExist
	}
	return f.dirty[dir], nil
}

func (f *laneFakeGit) Unpushed(dir string) (bool, error) {
	if f.fail[dir] {
		return false, os.ErrNotExist
	}
	return f.unpushed[dir], nil
}

type laneFakePRs struct {
	byBranch map[string]pulse.LanePR
	asked    []string
}

func (f *laneFakePRs) ForBranch(dir, branch string) (pulse.LanePR, error) {
	f.asked = append(f.asked, branch)
	return f.byBranch[branch], nil
}

// swapHygieneLanes puts the two fakes behind the verb's doors and gives back the
// restore, the way swapHygieneEnv does for /proc and df.
func swapHygieneLanes(git pulse.LaneGit, prs pulse.LanePRSource) func() {
	oldGit, oldPRs := hygieneGit, hygienePRs
	hygieneGit, hygienePRs = git, prs
	return func() { hygieneGit, hygienePRs = oldGit, oldPRs }
}

// ---------------------------------------------------------------------------
// The fixture: a root of candidate directories in either shape.

// laneFlat writes <root>/<name>/.git and answers the candidate, which is also
// the checkout.
func laneFlat(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	hygieneMkdir(t, filepath.Join(dir, ".git"))
	hygieneWrite(t, filepath.Join(dir, "README.md"), "lane\n", time.Time{})
	return dir
}

// laneNested writes <root>/<name>/repo/.git and answers the candidate and the
// checkout, which are not the same directory.
func laneNested(t *testing.T, root, name string) (candidate, checkout string) {
	t.Helper()
	candidate = filepath.Join(root, name)
	checkout = filepath.Join(candidate, "repo")
	hygieneMkdir(t, filepath.Join(checkout, ".git"))
	hygieneWrite(t, filepath.Join(checkout, "README.md"), "lane\n", time.Time{})
	return candidate, checkout
}

// laneAge stamps the candidate directory, which is what --older-than reads. It
// is called LAST, because writing anything inside a directory moves its mtime.
func laneAge(t *testing.T, dir string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
}

func laneNow() time.Time { return time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC) }

// ---------------------------------------------------------------------------
// hygiene-lane-dirs-removes-a-merged-or-closed-clone

func TestHygieneLaneDirsRemovesMergedAndClosedClones(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	merged := laneFlat(t, root, "lane-merged")
	closed, closedCheckout := laneNested(t, root, "lane-closed")

	git := &laneFakeGit{branch: map[string]string{
		merged:         "rowan/merged-work",
		closedCheckout: "rowan/closed-work",
	}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{
		"rowan/merged-work": {Number: 1379, State: "MERGED"},
		"rowan/closed-work": {Number: 1380, State: "CLOSED"},
	}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root)
	if code != 0 {
		t.Fatalf("hygiene --lane-dirs exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(merged) {
		t.Errorf("a clone whose PR is MERGED survived: %s\n%s", merged, out)
	}
	if hygieneExists(closed) {
		t.Errorf("a clone whose PR is CLOSED survived: %s\n%s", closed, out)
	}
	for _, want := range []string{
		"HYGIENE LANE dir=" + merged + " pr=1379 state=MERGED removed=yes",
		"HYGIENE LANE dir=" + closed + " pr=1380 state=CLOSED removed=yes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "candidates=2 remove=2 removed=2 kept=0") {
		t.Errorf("the summary line does not count two removals:\n%s", out)
	}
}

// hygiene-lane-dirs-keeps-open-and-unknown-branches

func TestHygieneLaneDirsKeepsOpenAndUnknownBranches(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	open := laneFlat(t, root, "lane-open")
	none := laneFlat(t, root, "lane-nopr")

	git := &laneFakeGit{branch: map[string]string{
		open: "rowan/open-work",
		none: "rowan/never-pushed-a-pr",
	}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{
		"rowan/open-work": {Number: 1381, State: "OPEN"},
	}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root)
	if code != 0 {
		t.Fatalf("hygiene --lane-dirs exit = %d, stderr=%s", code, errb)
	}
	if !hygieneExists(open) || !hygieneExists(none) {
		t.Fatalf("a kept clone was removed:\n%s", out)
	}
	for _, want := range []string{
		"HYGIENE KEEP dir=" + open + " reason=open",
		"HYGIENE KEEP dir=" + none + " reason=no-pr",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "candidates=2 remove=0 removed=0 kept=2") {
		t.Errorf("the summary line does not count two kept:\n%s", out)
	}
}

// hygiene-lane-dirs-keeps-dirty-and-unpushed-before-it-asks-the-forge
//
// The two git refusals come BEFORE the forge read, so a merged PR can never
// talk the verb past an uncommitted file or a commit nobody else has.

func TestHygieneLaneDirsKeepsDirtyAndUnpushed(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	dirty := laneFlat(t, root, "lane-dirty")
	unpushed := laneFlat(t, root, "lane-unpushed")

	git := &laneFakeGit{
		branch:   map[string]string{dirty: "rowan/dirty-work", unpushed: "rowan/unpushed-work"},
		dirty:    map[string]bool{dirty: true},
		unpushed: map[string]bool{unpushed: true},
	}
	// Both branches are MERGED on the forge. Neither directory may go.
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{
		"rowan/dirty-work":    {Number: 1382, State: "MERGED"},
		"rowan/unpushed-work": {Number: 1383, State: "MERGED"},
	}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root)
	if code != 0 {
		t.Fatalf("hygiene --lane-dirs exit = %d, stderr=%s", code, errb)
	}
	if !hygieneExists(dirty) {
		t.Errorf("a clone with uncommitted work was removed: %s\n%s", dirty, out)
	}
	if !hygieneExists(unpushed) {
		t.Errorf("a clone holding a commit no remote has was removed: %s\n%s", unpushed, out)
	}
	for _, want := range []string{
		"HYGIENE KEEP dir=" + dirty + " reason=dirty",
		"HYGIENE KEEP dir=" + unpushed + " reason=unpushed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not carry %q:\n%s", want, out)
		}
	}
	if len(prs.asked) != 0 {
		t.Errorf("the forge was asked about a directory git had already refused: %v", prs.asked)
	}
}

// hygiene-lane-dirs-refuses-a-path-that-leaves-the-root
//
// A symlink child is the one shape that can name a directory outside <root>.
// It is kept, named, and its target is untouched -- the safepath rule, before
// any git or forge read.

func TestHygieneLaneDirsRefusesAPathOutsideTheRoot(t *testing.T) {
	now := laneNow()
	root := t.TempDir()
	outside := t.TempDir()

	target := laneFlat(t, outside, "somebody-elses-clone")
	link := filepath.Join(root, "lane-escape")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	git := &laneFakeGit{branch: map[string]string{link: "rowan/escape", target: "rowan/escape"}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{"rowan/escape": {Number: 1384, State: "MERGED"}}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root)
	if code != 0 {
		t.Fatalf("hygiene --lane-dirs exit = %d, stderr=%s", code, errb)
	}
	if !hygieneExists(target) {
		t.Fatalf("a directory OUTSIDE the root was removed through a symlink: %s\n%s", target, out)
	}
	if !hygieneExists(link) {
		t.Errorf("the symlink itself was removed: %s\n%s", link, out)
	}
	if !strings.Contains(out, "HYGIENE KEEP dir="+link+" reason=unsafe") {
		t.Errorf("the output does not name the refused path:\n%s", out)
	}
	if len(prs.asked) != 0 {
		t.Errorf("the forge was asked about a path safepath had refused: %v", prs.asked)
	}
}

// hygiene-lane-dirs-dry-run-prints-the-same-lines-and-removes-nothing

func TestHygieneLaneDirsDryRunRemovesNothing(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	merged := laneFlat(t, root, "lane-merged")
	open := laneFlat(t, root, "lane-open")

	git := &laneFakeGit{branch: map[string]string{merged: "rowan/merged-work", open: "rowan/open-work"}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{
		"rowan/merged-work": {Number: 1379, State: "MERGED"},
		"rowan/open-work":   {Number: 1381, State: "OPEN"},
	}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root, "--dry-run")
	if code != 0 {
		t.Fatalf("hygiene --lane-dirs --dry-run exit = %d, stderr=%s", code, errb)
	}
	if !hygieneExists(merged) || !hygieneExists(open) {
		t.Fatalf("--dry-run removed something:\n%s", out)
	}
	if !strings.Contains(out, "HYGIENE LANE dir="+merged+" pr=1379 state=MERGED removed=no") {
		t.Errorf("the dry run does not print the same line with removed=no:\n%s", out)
	}
	if !strings.Contains(out, "HYGIENE KEEP dir="+open+" reason=open") {
		t.Errorf("the dry run does not print the keep line:\n%s", out)
	}
	if !strings.Contains(out, "candidates=2 remove=1 removed=0 kept=1 dry-run=yes") {
		t.Errorf("the dry run's summary does not separate what it would take from what it took:\n%s", out)
	}
}

// hygiene-lane-dirs-older-than-skips-a-fresh-clone

func TestHygieneLaneDirsOlderThanSkipsAFreshClone(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	stale := laneFlat(t, root, "lane-stale")
	fresh := laneFlat(t, root, "lane-fresh")
	laneAge(t, stale, now.Add(-72*time.Hour))
	laneAge(t, fresh, now.Add(-1*time.Hour))

	git := &laneFakeGit{branch: map[string]string{stale: "rowan/stale-work", fresh: "rowan/fresh-work"}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{
		"rowan/stale-work": {Number: 1385, State: "MERGED"},
		"rowan/fresh-work": {Number: 1386, State: "MERGED"},
	}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root, "--older-than", "2d")
	if code != 0 {
		t.Fatalf("hygiene --lane-dirs --older-than exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(stale) {
		t.Errorf("a merged clone three days old survived --older-than 2d: %s\n%s", stale, out)
	}
	if !hygieneExists(fresh) {
		t.Fatalf("a clone touched an hour ago was removed under --older-than 2d: %s\n%s", fresh, out)
	}
	if !strings.Contains(out, "HYGIENE KEEP dir="+fresh+" reason=fresh") {
		t.Errorf("the output does not name the fresh clone it skipped:\n%s", out)
	}
	if len(prs.asked) != 1 || prs.asked[0] != "rowan/stale-work" {
		t.Errorf("the forge was asked about a clone the age filter had already skipped: %v", prs.asked)
	}
}

// hygiene-lane-dirs-flags-refuse-nonsense: every refusal names the flag and the
// one thing to do about it, and nothing is ever removed on a guess.

func TestHygieneLaneDirsFlagsRefuseNonsense(t *testing.T) {
	now := laneNow()
	root := t.TempDir()
	dir := laneFlat(t, root, "lane-merged")

	git := &laneFakeGit{branch: map[string]string{dir: "rowan/merged-work"}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{"rowan/merged-work": {Number: 1379, State: "MERGED"}}}
	defer swapHygieneLanes(git, prs)()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"hours are not days", []string{"--lane-dirs", root, "--older-than", "36h"}, "--older-than"},
		{"a bare number is not a day", []string{"--lane-dirs", root, "--older-than", "2"}, "--older-than"},
		{"zero days is not a window", []string{"--lane-dirs", root, "--older-than", "0d"}, "--older-than"},
		{"no value at all", []string{"--lane-dirs", root, "--older-than"}, "--older-than"},
		{"the root is not a directory", []string{"--lane-dirs", filepath.Join(root, "nowhere")}, "--lane-dirs"},
		{"the root is relative", []string{"--lane-dirs", "tmp"}, "--lane-dirs"},
		{"a subcommand as well", []string{"--lane-dirs", root, "run"}, "--lane-dirs"},
		{"--older-than without --lane-dirs", []string{"run", "--older-than", "2d", "--home", root}, "--older-than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errb := hygieneRun(t, now, c.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stdout=%s stderr=%s", code, out, errb)
			}
			if !strings.Contains(errb, c.want) {
				t.Errorf("the refusal does not name %s:\n%s", c.want, errb)
			}
			if !hygieneExists(dir) {
				t.Fatalf("a refusal removed a directory: %s", dir)
			}
		})
	}

	// And the shape that was legal before this flag existed still is: a bare
	// hygiene with no subcommand prints the usage and exits 2.
	code, _, errb := hygieneRun(t, now)
	if code != 2 || !strings.Contains(errb, "nova-pulse hygiene run") {
		t.Errorf("a bare `nova-pulse hygiene` no longer prints the usage, exit=%d:\n%s", code, errb)
	}
}

// hygiene-lane-dirs-flag-takes-both-spellings, the way its neighbours do.

func TestHygieneLaneDirsTakesBothFlagSpellings(t *testing.T) {
	now := laneNow()
	root := t.TempDir()
	dir := laneFlat(t, root, "lane-merged")
	laneAge(t, dir, now.Add(-72*time.Hour))

	git := &laneFakeGit{branch: map[string]string{dir: "rowan/merged-work"}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{"rowan/merged-work": {Number: 1379, State: "MERGED"}}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs="+root, "--older-than=2d", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "state=MERGED removed=no") {
		t.Errorf("--flag=value was not read the way --flag value is:\n%s", out)
	}
}

// hygiene-lane-dirs-output-is-bounded: a root of 92 clones is what the Studio
// actually holds, and a verb that printed 92 lines every time would be the
// listing this repo's bounded package exists to end.

func TestHygieneLaneDirsOutputIsBounded(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	git := &laneFakeGit{branch: map[string]string{}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{}}
	for i := 0; i < 30; i++ {
		dir := laneFlat(t, root, "lane-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		branch := "rowan/work-" + filepath.Base(dir)
		git.branch[dir] = branch
		prs.byBranch[branch] = pulse.LanePR{Number: 1000 + i, State: "OPEN"}
	}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
	if n := strings.Count(out, "HYGIENE KEEP "); n != 20 {
		t.Errorf("30 candidates printed %d item lines, want the default ceiling of 20:\n%s", n, out)
	}
	if !strings.Contains(out, "HYGIENE MORE kind=lane shown=20 total=30") {
		t.Errorf("the elided candidates are not stood for by one MORE line:\n%s", out)
	}
	if !strings.Contains(out, "candidates=30 remove=0 removed=0 kept=30") {
		t.Errorf("the summary counts the state, not the output:\n%s", out)
	}

	// And --max 0 lifts the ceiling, the way every --*-max in this repo does.
	code, all, errb := hygieneRun(t, now, "--lane-dirs", root, "--max", "0")
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
	if n := strings.Count(all, "HYGIENE KEEP "); n != 30 {
		t.Errorf("--max 0 printed %d item lines, want all 30", n)
	}
	if strings.Contains(all, "HYGIENE MORE") {
		t.Errorf("--max 0 printed a MORE line over nothing:\n%s", all)
	}
}

// hygiene-lane-dirs-ignores-what-is-not-a-checkout: a plain directory under the
// root is not a candidate, is not counted, and is never removed.

func TestHygieneLaneDirsIgnoresWhatIsNotACheckout(t *testing.T) {
	now := laneNow()
	root := t.TempDir()

	notes := filepath.Join(root, "notes")
	hygieneMkdir(t, notes)
	hygieneWrite(t, filepath.Join(notes, "a.md"), "not a clone\n", time.Time{})
	hygieneWrite(t, filepath.Join(root, "loose.txt"), "not a directory\n", time.Time{})
	merged := laneFlat(t, root, "lane-merged")

	git := &laneFakeGit{branch: map[string]string{merged: "rowan/merged-work"}}
	prs := &laneFakePRs{byBranch: map[string]pulse.LanePR{"rowan/merged-work": {Number: 1379, State: "MERGED"}}}
	defer swapHygieneLanes(git, prs)()

	code, out, errb := hygieneRun(t, now, "--lane-dirs", root)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
	if !hygieneExists(notes) {
		t.Fatalf("a directory that is not a checkout was removed: %s\n%s", notes, out)
	}
	if !strings.Contains(out, "candidates=1 remove=1 removed=1 kept=0") {
		t.Errorf("a directory that is not a checkout was counted as a candidate:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// The one test that drives the real seam, against a real repository in its own
// temporary directory. Local only: `git init`, a commit, a second clone as the
// "remote". Nothing here reaches the network.

func TestOSLaneGitReadsARealCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("no git on PATH: %v", err)
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.invalid",
			"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "rowan/real-work")
	hygieneWrite(t, filepath.Join(dir, "a.txt"), "one\n", time.Time{})
	run("add", "a.txt")
	run("commit", "-m", "one")

	g := pulse.OSLaneGit{}

	branch, err := g.Branch(dir)
	if err != nil || branch != "rowan/real-work" {
		t.Fatalf("Branch = %q, %v; want the checked-out branch", branch, err)
	}
	if dirty, err := g.Dirty(dir); err != nil || dirty {
		t.Errorf("Dirty = %v, %v; a checkout with nothing changed is clean", dirty, err)
	}
	if unpushed, err := g.Unpushed(dir); err != nil || !unpushed {
		t.Errorf("Unpushed = %v, %v; a commit no remote has is unpushed", unpushed, err)
	}

	hygieneWrite(t, filepath.Join(dir, "b.txt"), "two\n", time.Time{})
	if dirty, err := g.Dirty(dir); err != nil || !dirty {
		t.Errorf("Dirty = %v, %v; an untracked file is a dirty checkout", dirty, err)
	}
}
