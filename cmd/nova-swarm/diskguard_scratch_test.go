package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A scratch entry nothing changed in for a day goes, a directory or a file; one with a file
// changed inside it in the last day, one under a kept name, one a process names, one a
// process holds a path in, one whose repository holds work, and a link all stay; each kept
// one that a rule judged is said on a KEPT line.
func TestDiskGuardSweepsScratchNothingUsesForADay(t *testing.T) {
	t.Parallel()
	jobs := t.TempDir()
	old := dgNow.Add(-48 * time.Hour)
	mk := func(name string) string {
		dir := filepath.Join(jobs, name)
		dgFile(t, filepath.Join(dir, "repo", "main.go"), 100, old)
		dgAge(t, dir, old)
		return dir
	}
	gone := mk("done-job")
	logFile := filepath.Join(jobs, "done-job.log")
	dgFile(t, logFile, 7, old)
	young := mk("young-job")
	dgFile(t, filepath.Join(young, "repo", "deep", "new.txt"), 1, dgNow.Add(-time.Hour))
	friend := mk("stella-working")
	named := mk("named-job")
	held := mk("held-job")
	work := mk("work-job")
	require.NoError(t, os.MkdirAll(filepath.Join(work, "repo", ".git"), 0o755))
	dgAge(t, work, old)
	link := filepath.Join(jobs, "link-job")
	require.NoError(t, os.Symlink(gone, link))

	g, out := dgGuard(t)
	g.scratchPaths = []string{gone, logFile, young, friend, named, held, work, link}
	g.procs = func() ([]string, error) { return []string{"make -C " + named}, nil }
	g.held = func() ([]string, error) { return []string{filepath.Join(held, "repo", "main.go")}, nil }
	g.gitWork = func(repo string) string {
		if repo == filepath.Join(work, "repo") {
			return repo + " has commits no remote has"
		}
		return ""
	}
	g.scratch()

	assert.NoDirExists(t, gone)
	assert.NoFileExists(t, logFile)
	for _, p := range []string{young, friend, named, held, work} {
		assert.DirExists(t, p)
	}
	_, err := os.Lstat(link)
	assert.NoError(t, err, "a link is never swept")
	assert.Equal(t, "REMOVED scratch "+gone+" freed=100\n"+
		"REMOVED scratch "+logFile+" freed=7\n"+
		"KEPT scratch "+named+": a live process names it\n"+
		"KEPT scratch "+held+": a live process holds "+filepath.Join(held, "repo", "main.go")+"\n"+
		"KEPT scratch "+work+": "+filepath.Join(work, "repo")+" has commits no remote has\n", out.String())
	assert.EqualValues(t, 107, g.freed)
}

// Reversed witness of the age rule: the same entry a day younger stays, and under
// --dry-run an old one is said WOULD-REMOVE and stays.
func TestDiskGuardScratchAgeAndDryRun(t *testing.T) {
	t.Parallel()
	jobs := t.TempDir()
	dir := filepath.Join(jobs, "job")
	dgFile(t, filepath.Join(dir, "f"), 10, dgNow.Add(-23*time.Hour))
	dgAge(t, dir, dgNow.Add(-23*time.Hour))
	g, out := dgGuard(t)
	g.scratchPaths = []string{dir}
	g.scratch()
	assert.DirExists(t, dir)
	assert.Empty(t, out.String())

	dgAge(t, dir, dgNow.Add(-25*time.Hour))
	g, out = dgGuard(t)
	g.scratchPaths, g.dry = []string{dir}, true
	g.scratch()
	assert.DirExists(t, dir)
	assert.Equal(t, "WOULD-REMOVE scratch "+dir+" freed=10\n", out.String())
}

// An entry another user owns is never swept: with the guard's uid not the owner's, the old
// entry stays and nothing is said.
func TestDiskGuardScratchLeavesAnotherUsersEntry(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "job")
	dgFile(t, filepath.Join(dir, "f"), 10, dgNow.Add(-48*time.Hour))
	dgAge(t, dir, dgNow.Add(-48*time.Hour))
	g, out := dgGuard(t)
	g.scratchPaths, g.uid = []string{dir}, os.Getuid()+1
	g.scratch()
	assert.DirExists(t, dir)
	assert.Empty(t, out.String())
}

// The repositories of a scratch directory are found three levels in, a worktree's .git
// file too, and never deeper.
func TestScratchReposFindsRepositoriesThreeLevelsIn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b", ".git"), 0o755))
	dgText(t, filepath.Join(dir, "a", "wt", ".git"), "gitdir: elsewhere\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b", "c", "d", ".git"), 0o755))
	assert.ElementsMatch(t, []string{dir, filepath.Join(dir, "a", "b"), filepath.Join(dir, "a", "wt")}, scratchRepos(dir, 3))
}

// A --scratch glob matching the home, a parent of it, or anything under ~/nova-bench is
// refused; the others match once each.
func TestScratchMatchesRefusesTheHomeAndTheBench(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	bench := filepath.Join(home, "nova-bench")
	require.NoError(t, os.MkdirAll(filepath.Join(bench, "release"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "jobs", "a"), 0o755))
	tilde := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return filepath.Join(home, p[2:])
		}
		return p
	}
	got, err := scratchMatches([]string{"~/jobs/*", "~/jobs/*"}, tilde, home, []string{bench})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(home, "jobs", "a")}, got)
	for _, pat := range []string{"~/nova-bench/*", filepath.Dir(home), home} {
		_, err := scratchMatches([]string{pat}, tilde, home, []string{bench})
		assert.Error(t, err, pat)
	}
}

// Under --trim-recent-under-gb free a build cache is held under a quarter of its cap with
// its recently used entries included; above it (reversed witness) a recent entry stays.
func TestDiskGuardTrimsRecentEntriesUnderTheRecentFloor(t *testing.T) {
	t.Parallel()
	mk := func() (string, string) {
		dir := t.TempDir()
		for i := range 4 {
			dgFile(t, filepath.Join(dir, fmt.Sprintf("%02x", i), fmt.Sprintf("%064x-d", i)), 100, dgNow.Add(-72*time.Hour))
		}
		recent := filepath.Join(dir, "07", fmt.Sprintf("%064x-a", 7))
		dgFile(t, recent, 600, dgNow.Add(-10*time.Minute))
		return dir, recent
	}

	above, aboveRecent := mk()
	g, _ := dgGuard(t)
	g.caches, g.cacheMax, g.recentUnder = []string{above}, 1000, 30*gib
	g.free = func(string) (uint64, error) { return 40 * gib, nil }
	g.buildCaches()
	assert.FileExists(t, aboveRecent, "above the recent floor an entry used in the last two hours stays")

	under, underRecent := mk()
	g, out := dgGuard(t)
	g.caches, g.cacheMax, g.recentUnder = []string{under}, 1000, 30*gib
	g.free = func(string) (uint64, error) { return 25 * gib, nil }
	g.buildCaches()
	assert.NoFileExists(t, underRecent, "under the recent floor a recent entry goes too")
	assert.Contains(t, out.String(), fmt.Sprintf("PRESSURE free=%d under=%d: every go build cache is held under cap=250 this run, entries used in the last two hours included\n", 25*gib, 30*gib))
}
