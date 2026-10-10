package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The disk guard (diskguard.go): one run caps every Go build cache, cleans a module cache
// over its cap when no go runs, rotates the loop logs, sweeps a pool whose loop stopped,
// removes land clones unused for a day, and refuses anything with work or a live process
// in it. Every test builds its own fixture under t.TempDir() and drives one run with a
// fixed clock: the times on disk are set from it, and nothing here reads the wall clock.

// dgNow is every test's clock.
var dgNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// dgFile writes size bytes at path, its parents made, its time at.
func dgFile(t *testing.T, path string, size int, at time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o644))
	require.NoError(t, os.Chtimes(path, at, at))
}

// dgText writes text at path, its parents made.
func dgText(t *testing.T, path, text string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
}

// dgAge sets the time of everything under root, root included, to at.
func dgAge(t *testing.T, root string, at time.Time) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, at, at)
	}))
}

// dgGuard is a guard with nothing to look at and limits nothing reaches: a test sets what
// it holds. Its process list and its open paths are empty, its volume has 100 GiB free.
func dgGuard(t *testing.T) (*guard, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return &guard{
		cacheMax: 1 << 40, modMax: 1 << 40, logMax: 1 << 40, logKeep: 3, floor: 10 * gib,
		poolIdle: 30 * time.Minute, cloneAge: 24 * time.Hour, now: dgNow, home: t.TempDir(),
		procs:    func() ([]string, error) { return nil, nil },
		held:     func() ([]string, error) { return nil, nil },
		free:     func(string) (uint64, error) { return 100 * gib, nil },
		cleanMod: func(string) error { return errors.New("no module cache is cleaned in this test") },
		dirty:    func(string) (bool, error) { return false, nil },
		out:      out,
	}, out
}

// A Go build cache over its cap loses its entries used longest ago until it is under the
// cap less a fifth; an entry used in the last two hours, a file that is no cache entry, and
// a cache under its cap are never touched.
func TestDiskGuardHoldsAGoBuildCacheUnderItsCap(t *testing.T) {
	t.Parallel()
	over, under := t.TempDir(), t.TempDir()
	var old []string
	for i := range 6 {
		p := filepath.Join(over, fmt.Sprintf("%02x", i), fmt.Sprintf("%064x-d", i))
		dgFile(t, p, 300, dgNow.Add(-72*time.Hour+time.Duration(i)*time.Hour))
		old = append(old, p)
	}
	recent := filepath.Join(over, "07", fmt.Sprintf("%064x-a", 7))
	dgFile(t, recent, 300, dgNow.Add(-30*time.Minute))
	stray := filepath.Join(over, "08", "README")
	dgFile(t, stray, 5000, dgNow.Add(-72*time.Hour))
	small := filepath.Join(under, "00", fmt.Sprintf("%064x-d", 1))
	dgFile(t, small, 300, dgNow.Add(-72*time.Hour))

	g, out := dgGuard(t)
	g.caches, g.cacheMax = []string{over, under}, 1000
	g.buildCaches()

	// 2100 bytes against a cap of 1000: the five oldest go (1500), which leaves 600, under 800
	for _, p := range old[:5] {
		assert.NoFileExists(t, p)
	}
	assert.FileExists(t, old[5], "the trim stops once the cache is under its cap less a fifth")
	assert.FileExists(t, recent, "an entry used in the last two hours is never removed")
	assert.FileExists(t, stray, "a file that is no cache entry is never removed")
	assert.FileExists(t, small, "a cache under its cap is never trimmed")
	assert.Contains(t, out.String(), "TRIMMED go-build "+over+" freed=1500 size=600 cap=1000\n")
	assert.NotContains(t, out.String(), under)
	assert.EqualValues(t, 1500, g.freed)
}

// A loop log over its size is copied to .1 and emptied in place (its supervisor appends to
// the same file), the older copies shift up and the one past --log-keep goes; a log under
// its size and a link are never touched.
func TestDiskGuardRotatesALoopLogOverItsSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	big := filepath.Join(dir, "member-a.log")
	dgFile(t, big, 150, dgNow)
	dgText(t, big+".1", "one\n")
	dgText(t, big+".2", "two\n")
	small := filepath.Join(dir, "reader-a.log")
	dgFile(t, small, 50, dgNow)
	target := filepath.Join(t.TempDir(), "elsewhere.log")
	dgFile(t, target, 500, dgNow)
	link := filepath.Join(dir, "link.log")
	require.NoError(t, os.Symlink(target, link))

	g, out := dgGuard(t)
	g.logDir, g.logMax, g.logKeep = dir, 100, 2
	g.logs()

	fi, err := os.Stat(big)
	require.NoError(t, err)
	assert.Zero(t, fi.Size(), "the log is emptied in place, so the supervisor's open file goes on")
	b, err := os.ReadFile(big + ".1")
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("x", 150), string(b))
	b, err = os.ReadFile(big + ".2")
	require.NoError(t, err)
	assert.Equal(t, "one\n", string(b))
	assert.NoFileExists(t, big+".3")
	fi, err = os.Stat(small)
	require.NoError(t, err)
	assert.EqualValues(t, 50, fi.Size())
	fi, err = os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "a link is never rotated")
	fi, err = os.Stat(target)
	require.NoError(t, err)
	assert.EqualValues(t, 500, fi.Size())
	assert.Equal(t, "ROTATED log "+big+" freed=4 size=150 keep=2\n", out.String())
	assert.EqualValues(t, 4, g.freed)
}

// shaA and shaB are two commits' names.
const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// dgLaunch stages an ended launch in slots: its directory with a 1000-byte file in its job,
// its native log beside it, every time at. A work launch (gen) records staged and, when
// head is not empty, a checkout whose branch is at head.
func dgLaunch(t *testing.T, slots, name string, at time.Time, staged, head string) string {
	t.Helper()
	card := name[:strings.Index(name, ".")]
	dir := filepath.Join(slots, name)
	dgFile(t, filepath.Join(dir, "jobs", card, "data.bin"), 1000, at)
	if staged != "" {
		dgText(t, filepath.Join(dir, "staged"), staged+"\n")
	}
	if head != "" {
		git := filepath.Join(dir, "jobs", card, "repo", ".git")
		dgText(t, filepath.Join(git, "HEAD"), "ref: refs/heads/main\n")
		dgText(t, filepath.Join(git, "refs", "heads", "main"), head+"\n")
	}
	dgAge(t, dir, at)
	dgFile(t, filepath.Join(slots, name+".native.log"), 10, at)
	return dir
}

// A pool no process names and with no activity for --pool-idle is swept as the member
// sweeps its own at start: ended launches beyond the newest five go, a live pid's and a
// name that is no launch's stay, and a work launch whose checkout moved past its staged
// commit is kept and said, since the guard cannot prove that work was pushed.
func TestDiskGuardSweepsAPoolWhoseLoopStopped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	slots := filepath.Join(root, "slots")
	day := dgNow.Add(-24 * time.Hour)
	var kept []string
	for i := 2; i <= 6; i++ {
		kept = append(kept, dgLaunch(t, slots, "c"+strconv.Itoa(i)+".a1.e3", day.Add(-time.Duration(6-i)*time.Hour), "", ""))
	}
	same := dgLaunch(t, slots, "same.g1.e3", day.Add(-4*time.Hour-30*time.Minute), shaA, shaA)
	c1 := dgLaunch(t, slots, "c1.a1.e3", day.Add(-5*time.Hour), "", "")
	work := dgLaunch(t, slots, "w.g1.e3", day.Add(-6*time.Hour), shaA, shaB)
	live := dgLaunch(t, slots, "live.a1.e3", day.Add(-7*time.Hour), "", "")
	pid := filepath.Join(slots, "live.a1.e3.pid")
	dgText(t, pid, strconv.Itoa(os.Getpid())+"\n")
	require.NoError(t, os.Chtimes(pid, day, day))
	other := filepath.Join(slots, "notalaunch")
	dgFile(t, filepath.Join(other, "f"), 1000, day)
	require.NoError(t, os.Chtimes(other, day, day))
	require.NoError(t, os.Chtimes(slots, day, day))

	g, out := dgGuard(t)
	g.roots = []string{root}
	g.pools()

	assert.NoDirExists(t, same)
	assert.NoDirExists(t, c1)
	for _, d := range append(kept, work, live, other) {
		assert.DirExists(t, d)
	}
	assert.FileExists(t, c1+".native.log", "the small files beside a launch are not the guard's")
	text := out.String()
	assert.Contains(t, text, "REMOVED slot "+same+" freed=")
	assert.Contains(t, text, "REMOVED slot "+c1+" freed=1000\n")
	assert.Contains(t, text, "KEPT slot "+work+": its checkout holds commits past the staged one")
	assert.Equal(t, 3, strings.Count(text, "\n"), text)
}

// A pool a process names (its loop, or a native child of it) or whose slots moved within
// --pool-idle is its loop's to clean, and the guard leaves it; a process naming another
// pool whose path begins with this one's does not keep this one.
func TestDiskGuardLeavesAPoolItsLoopStillRuns(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	named, beat, near := filepath.Join(base, "pool-2"), filepath.Join(base, "beat"), filepath.Join(base, "pool")
	old := dgNow.Add(-48 * time.Hour)
	for _, root := range []string{named, beat, near} {
		slots := filepath.Join(root, "slots")
		for i := 1; i <= 7; i++ {
			dgLaunch(t, slots, "c"+strconv.Itoa(i)+".a1.e3", old.Add(time.Duration(i)*time.Minute), "", "")
		}
		require.NoError(t, os.Chtimes(slots, old, old))
	}
	recent := dgNow.Add(-5 * time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(beat, "slots"), recent, recent))

	g, out := dgGuard(t)
	g.roots = []string{named, beat, near}
	g.procs = func() ([]string, error) {
		return []string{"/home/u/.local/bin/nova-swarm member --as a --root " + named + " --width 2"}, nil
	}
	g.pools()

	for _, root := range []string{named, beat} {
		for i := 1; i <= 7; i++ {
			assert.DirExists(t, filepath.Join(root, "slots", "c"+strconv.Itoa(i)+".a1.e3"))
		}
	}
	assert.NoDirExists(t, filepath.Join(near, "slots", "c1.a1.e3"), "a process naming pool-2 does not keep pool")
	assert.NotContains(t, out.String(), named)
	assert.NotContains(t, out.String(), beat)
}

// A land clone unused for --clone-age goes; a newer one, one with uncommitted work, one a
// process names and a name that is no clone's stay.
func TestDiskGuardRemovesLandClonesUnusedForADay(t *testing.T) {
	t.Parallel()
	land := t.TempDir()
	clone := func(name string, at time.Time) string {
		dir := filepath.Join(land, name)
		dgText(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/land/s1\n")
		dgFile(t, filepath.Join(dir, "main.go"), 100, at)
		dgAge(t, dir, at)
		return dir
	}
	old := clone("github.com-o-r-0123456789abcdef", dgNow.Add(-48*time.Hour))
	fresh := clone("github.com-o-r-1111111111111111", dgNow.Add(-time.Hour))
	dirty := clone("github.com-o-r-2222222222222222", dgNow.Add(-48*time.Hour))
	busy := clone("github.com-o-r-3333333333333333", dgNow.Add(-48*time.Hour))
	other := clone("scratch", dgNow.Add(-48*time.Hour))

	g, out := dgGuard(t)
	g.landDir = land
	g.dirty = func(dir string) (bool, error) { return dir == dirty, nil }
	g.procs = func() ([]string, error) { return []string{"git -C " + busy + " fetch -q origin"}, nil }
	g.landClones()

	assert.NoDirExists(t, old)
	for _, d := range []string{fresh, dirty, busy, other} {
		assert.DirExists(t, d)
	}
	text := out.String()
	assert.Contains(t, text, "REMOVED land clone "+old+" freed=")
	assert.Contains(t, text, "KEPT land clone "+dirty+": it has uncommitted work")
	assert.Contains(t, text, "KEPT land clone "+busy+": a live process names it")
	assert.Equal(t, 3, strings.Count(text, "\n"), text)
}

// A module cache over its cap is cleaned whole by go clean -modcache, and only when no go
// command runs on the machine; one under its cap is never touched.
func TestDiskGuardCleansAModuleCacheOverItsCapOnlyWithNoGoRunning(t *testing.T) {
	t.Parallel()
	over, under := t.TempDir(), t.TempDir()
	dgFile(t, filepath.Join(over, "a@v1", "a.go"), 1000, dgNow)
	dgFile(t, filepath.Join(over, "b@v1", "b.go"), 1000, dgNow)
	dgFile(t, filepath.Join(under, "c@v1", "c.go"), 100, dgNow)
	var cleaned []string
	busy, out := dgGuard(t)
	busy.modCaches, busy.modMax = []string{over, under}, 1500
	busy.cleanMod = func(dir string) error { cleaned = append(cleaned, dir); return nil }
	busy.procs = func() ([]string, error) { return []string{"/usr/local/go/bin/go build ./..."}, nil }
	busy.modules()
	assert.Empty(t, cleaned)
	assert.Equal(t, "KEPT go-mod "+over+": a go command is running (/usr/local/go/bin/go build ./...)\n", out.String())

	idle, out := dgGuard(t)
	idle.modCaches, idle.modMax, idle.cleanMod = []string{over, under}, 1500, busy.cleanMod
	idle.procs = func() ([]string, error) { return []string{"/bin/zsh", "/opt/bin/gopls serve"}, nil }
	idle.modules()
	assert.Equal(t, []string{over}, cleaned)
	assert.Equal(t, "CLEANED go-mod "+over+" freed=2000 cap=1500\n", out.String())
	assert.EqualValues(t, 2000, idle.freed)
}

// Under the floor the run says so on its own line, and every run ends on one line with
// what it freed and the free disk.
func TestDiskGuardWarnsUnderTheFloorAndEndsOnOneLine(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	g.free = func(string) (uint64, error) { return 5 * gib, nil }
	assert.Equal(t, 0, g.run())
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 2, out.String())
	assert.True(t, strings.HasPrefix(lines[0], "DISK-GUARD WARN free=5368709120 floor=10737418240 on the volume of "+g.home+": "), lines[0])
	assert.Equal(t, "DISK-GUARD OK freed=0 free=5368709120", lines[1])
}

// A run that cannot read the process list removes nothing that needs it, says so, and ends
// INCOMPLETE, exit 1.
func TestDiskGuardWithoutTheProcessListRemovesNothingThatNeedsIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	slots := filepath.Join(root, "slots")
	old := dgNow.Add(-48 * time.Hour)
	for i := 1; i <= 7; i++ {
		dgLaunch(t, slots, "c"+strconv.Itoa(i)+".a1.e3", old, "", "")
	}
	require.NoError(t, os.Chtimes(slots, old, old))
	g, out := dgGuard(t)
	g.roots = []string{root}
	g.procs = func() ([]string, error) { return nil, errors.New("ps: not found") }
	assert.Equal(t, 1, g.run())
	for i := 1; i <= 7; i++ {
		assert.DirExists(t, filepath.Join(slots, "c"+strconv.Itoa(i)+".a1.e3"))
	}
	assert.Contains(t, out.String(), "NOTE the process list could not be read (ps: not found)")
	assert.Contains(t, out.String(), "DISK-GUARD INCOMPLETE freed=0 free=107374182400 failed=1\n")
}

// A limit that is no limit is refused before anything is read, naming the flag.
func TestDiskGuardRefusesLimitsThatAreNoLimits(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--cache-max-gb", "0"}, {"--modcache-max-gb", "-1"}, {"--log-max-mb", "0"}, {"--log-keep", "0"},
		{"--disk-floor", "-1"}, {"--clone-age", "0s"}, {"--pool-idle", "1m"},
	} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 2, swarmRun(append([]string{"disk-guard"}, args...), &stdout, &stderr), "%v", args)
		assert.Contains(t, stderr.String(), args[0], "%v", args)
		assert.Empty(t, stdout.String(), "%v", args)
	}
}

// disk-guard -h prints its usage, its flags and its exit codes.
func TestDiskGuardHelp(t *testing.T) {
	t.Parallel()
	help := swarmHelp(t, "disk-guard", "-h")
	for _, flag := range []string{"--root", "--scan", "--cache", "--cache-max-gb", "--modcache-max-gb", "--logs", "--log-max-mb", "--log-keep", "--pool-idle", "--land", "--clone-age", "--mirrors", "--disk-floor", "--dry-run"} {
		assert.Contains(t, help, "\n  "+flag+" ", "disk-guard -h does not list %s", flag)
	}
	assert.Contains(t, help, "DISK-GUARD OK")
	assert.Contains(t, help, "\neffect: local write: ")
	assert.Equal(t, help, swarmHelp(t, "help", "disk-guard"))
}

// A mirror's leftover temporary packs (tmp_pack_*, .tmp-*: an aborted fetch's) older than
// an hour go when nothing may be fetching into it: no process names the mirror and no git
// fetch or index-pack runs naming no path at all (one in the mirror's own directory). A
// real pack, a newer temporary one and a directory that is no repository are never
// touched, and nothing here runs git prune.
func TestDiskGuardRemovesAMirrorsLeftoverPacksWhenNoFetchRuns(t *testing.T) {
	t.Parallel()
	mirrors := t.TempDir()
	repo := func(name string) string {
		dir := filepath.Join(mirrors, name)
		dgText(t, filepath.Join(dir, "HEAD"), "ref: refs/heads/dev\n")
		return filepath.Join(dir, "objects", "pack")
	}
	pack := repo("nova-tools.git")
	old1, old2 := filepath.Join(pack, "tmp_pack_AbC123"), filepath.Join(pack, ".tmp-4242-pack-abc.pack")
	dgFile(t, old1, 500, dgNow.Add(-2*time.Hour))
	dgFile(t, old2, 300, dgNow.Add(-2*time.Hour))
	fresh := filepath.Join(pack, "tmp_pack_new")
	dgFile(t, fresh, 100, dgNow.Add(-10*time.Minute))
	real := filepath.Join(pack, "pack-abc.pack")
	dgFile(t, real, 1000, dgNow.Add(-48*time.Hour))
	otherPack := repo("other.git")
	busyTmp := filepath.Join(otherPack, "tmp_pack_Busy01")
	dgFile(t, busyTmp, 700, dgNow.Add(-2*time.Hour))
	loose := filepath.Join(mirrors, "loose", "objects", "pack", "tmp_pack_Loose1")
	dgFile(t, loose, 900, dgNow.Add(-2*time.Hour))
	other := filepath.Dir(filepath.Dir(otherPack))

	g, out := dgGuard(t)
	g.mirrorDir = mirrors
	g.procs = func() ([]string, error) { return []string{"git -C " + other + " fetch -q origin"}, nil }
	g.mirrors()
	assert.NoFileExists(t, old1)
	assert.NoFileExists(t, old2)
	for _, p := range []string{fresh, real, busyTmp, loose} {
		assert.FileExists(t, p)
	}
	assert.Equal(t, "REMOVED mirror temp packs "+filepath.Dir(filepath.Dir(pack))+" files=2 freed=800\n"+
		"KEPT mirror "+other+": a fetch or clone of it may be running (git -C "+other+" fetch -q origin)\n", out.String())

	// a fetch whose line names no path may be running in either mirror's directory
	dgFile(t, old1, 500, dgNow.Add(-2*time.Hour))
	g, out = dgGuard(t)
	g.mirrorDir = mirrors
	g.procs = func() ([]string, error) { return []string{"git index-pack --stdin --fix-thin"}, nil }
	g.mirrors()
	assert.FileExists(t, old1)
	assert.FileExists(t, busyTmp)
	assert.Equal(t, 2, strings.Count(out.String(), "KEPT mirror "), out.String())
}

// --dry-run judges every rule the same and removes or rotates nothing, each action said
// with WOULD-.
func TestDiskGuardDryRunRemovesNothing(t *testing.T) {
	t.Parallel()
	logs, land, cache := t.TempDir(), t.TempDir(), t.TempDir()
	log := filepath.Join(logs, "member-a.log")
	dgFile(t, log, 150, dgNow)
	clone := filepath.Join(land, "github.com-o-r-0123456789abcdef")
	dgFile(t, filepath.Join(clone, "main.go"), 100, dgNow.Add(-48*time.Hour))
	dgAge(t, clone, dgNow.Add(-48*time.Hour))
	entry := filepath.Join(cache, "00", fmt.Sprintf("%064x-d", 0))
	dgFile(t, entry, 3000, dgNow.Add(-72*time.Hour))
	g, out := dgGuard(t)
	g.dry, g.logDir, g.logMax, g.landDir, g.caches, g.cacheMax = true, logs, 100, land, []string{cache}, 1000
	assert.Equal(t, 0, g.run())
	fi, err := os.Stat(log)
	require.NoError(t, err)
	assert.EqualValues(t, 150, fi.Size())
	assert.NoFileExists(t, log+".1")
	assert.DirExists(t, clone)
	assert.FileExists(t, entry)
	text := out.String()
	assert.Contains(t, text, "WOULD-ROTATE log "+log+" freed=0 size=150 keep=3\n")
	assert.Contains(t, text, "WOULD-REMOVE land clone "+clone+" freed=100\n")
	assert.Contains(t, text, "WOULD-TRIM go-build "+cache+" size=3000 cap=1000\n")
	assert.NotContains(t, text, "REMOVED")
}

// The process list leaves out the guard's own line, which names every root it was given.
func TestDiskGuardProcessListLeavesOutItsOwnLine(t *testing.T) {
	t.Parallel()
	ps := "    1 /sbin/launchd\n  42 /home/u/.local/bin/nova-swarm disk-guard --root /home/u/pool\n 77 /home/u/.local/bin/nova-swarm member --root /home/u/pool\n\n"
	assert.Equal(t, []string{"/sbin/launchd", "/home/u/.local/bin/nova-swarm member --root /home/u/pool"}, psLines([]byte(ps), 42))
}

// A live process whose argument line names none of a rule's paths but whose working
// directory or an open file lies under one (a `git push` or a `make` run inside a land
// clone, a fetch in a mirror, a test in a stopped pool's launch, a gopls reading the module
// cache) holds it: no rule removes or empties it (Zhi's HOLD on #5136, 2026-10-02). The
// open path is the one the kernel reports, links resolved, so a rule's path reached
// through a link (t.TempDir() on darwin is under /var, a link to /private/var) still
// matches.
func TestDiskGuardKeepsWhatALiveProcessHoldsOpen(t *testing.T) {
	t.Parallel()
	real := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		require.NoError(t, err)
		return r
	}
	old := dgNow.Add(-48 * time.Hour)

	// a land clone a `git push` runs in, its cwd the clone, its argv naming no path
	land := t.TempDir()
	clone := filepath.Join(land, "github.com-o-r-0123456789abcdef")
	dgText(t, filepath.Join(clone, ".git", "HEAD"), "ref: refs/heads/land/s1\n")
	dgFile(t, filepath.Join(clone, "main.go"), 100, old)
	dgAge(t, clone, old)
	g, out := dgGuard(t)
	g.landDir = land
	g.procs = func() ([]string, error) { return []string{"git push -q origin HEAD:land/s1"}, nil }
	g.held = func() ([]string, error) { return []string{"/", real(clone)}, nil }
	g.landClones()
	assert.DirExists(t, clone, "a process working in the clone keeps it")
	assert.Equal(t, "KEPT land clone "+clone+": a live process holds "+real(clone)+"\n", out.String())

	// a stopped pool one of whose launches a process holds a file open in
	root := t.TempDir()
	slots := filepath.Join(root, "slots")
	for i := 1; i <= 7; i++ {
		dgLaunch(t, slots, "c"+strconv.Itoa(i)+".a1.e3", old.Add(time.Duration(i)*time.Minute), "", "")
	}
	require.NoError(t, os.Chtimes(slots, old, old))
	g, _ = dgGuard(t)
	g.roots = []string{root}
	g.held = func() ([]string, error) {
		return []string{filepath.Join(real(slots), "c1.a1.e3", "out.log")}, nil
	}
	g.pools()
	for i := 1; i <= 7; i++ {
		assert.DirExists(t, filepath.Join(slots, "c"+strconv.Itoa(i)+".a1.e3"), "a held pool is not swept")
	}

	// a mirror a fetch runs in by its cwd: the fetch's line names an absolute path that is
	// not the mirror, so the line alone would let the packs go
	mirrors := t.TempDir()
	repo := filepath.Join(mirrors, "nova-tools.git")
	dgText(t, filepath.Join(repo, "HEAD"), "ref: refs/heads/dev\n")
	tmp := filepath.Join(repo, "objects", "pack", "tmp_pack_AbC123")
	dgFile(t, tmp, 500, dgNow.Add(-2*time.Hour))
	g, out = dgGuard(t)
	g.mirrorDir = mirrors
	g.procs = func() ([]string, error) { return []string{"git fetch -q /srv/upstream.git"}, nil }
	g.held = func() ([]string, error) { return []string{real(repo)}, nil }
	g.mirrors()
	assert.FileExists(t, tmp, "a held mirror keeps its packs")
	assert.Equal(t, "KEPT mirror "+repo+": a live process holds "+real(repo)+"\n", out.String())

	// a module cache a tool that is not go reads
	mod := t.TempDir()
	dgFile(t, filepath.Join(mod, "a@v1", "a.go"), 2000, dgNow)
	var cleaned []string
	g, out = dgGuard(t)
	g.modCaches, g.modMax = []string{mod}, 1000
	g.cleanMod = func(dir string) error { cleaned = append(cleaned, dir); return nil }
	g.procs = func() ([]string, error) { return []string{"/opt/bin/gopls serve"}, nil }
	g.held = func() ([]string, error) { return []string{filepath.Join(real(mod), "a@v1", "a.go")}, nil }
	g.modules()
	assert.Empty(t, cleaned, "a held module cache is not emptied")
	assert.Equal(t, "KEPT go-mod "+mod+": a live process holds "+filepath.Join(real(mod), "a@v1", "a.go")+"\n", out.String())

	// a path that only shares a prefix with the clone does not hold it
	g, out = dgGuard(t)
	g.landDir = land
	g.held = func() ([]string, error) { return []string{real(clone) + "-other"}, nil }
	g.landClones()
	assert.NoDirExists(t, clone)
	assert.Contains(t, out.String(), "REMOVED land clone "+clone+" freed=")
}

// A run that cannot read the open paths removes nothing that needs them, says so, and ends
// INCOMPLETE, exit 1, as without the process list.
func TestDiskGuardWithoutTheOpenPathsRemovesNothingThatNeedsThem(t *testing.T) {
	t.Parallel()
	land := t.TempDir()
	clone := filepath.Join(land, "github.com-o-r-0123456789abcdef")
	dgFile(t, filepath.Join(clone, "main.go"), 100, dgNow.Add(-48*time.Hour))
	dgAge(t, clone, dgNow.Add(-48*time.Hour))
	g, out := dgGuard(t)
	g.landDir = land
	g.held = func() ([]string, error) { return nil, errors.New("lsof: not found") }
	assert.Equal(t, 1, g.run())
	assert.DirExists(t, clone)
	assert.Contains(t, out.String(), "NOTE the open files of live processes could not be read (lsof: not found)")
	assert.Contains(t, out.String(), "DISK-GUARD INCOMPLETE freed=0 free=107374182400 failed=1\n")
}

// A launch agent's PATH leaves out /usr/sbin, where macOS keeps lsof: the guard looks for
// lsof on PATH, then at its standard paths, and says once at its start which it took, or
// that it has none and what that costs the run.
func TestDiskGuardFindsLsofOffPathAndSaysSoOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing, there := filepath.Join(dir, "sbin", "lsof"), filepath.Join(dir, "usr", "sbin", "lsof")
	dgFile(t, there, 10, dgNow)
	require.NoError(t, os.Chmod(there, 0o755))
	notOnPath := func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }

	path, note := findLsof(func(string) (string, error) { return "/opt/bin/lsof", nil }, []string{there}, isExecutable)
	assert.Equal(t, "/opt/bin/lsof", path, "PATH's lsof is taken first")
	assert.Empty(t, note, "nothing to say when PATH has it")

	path, note = findLsof(notOnPath, []string{missing, there}, isExecutable)
	assert.Equal(t, there, path, "off PATH, the first standard path that holds an executable lsof")
	assert.Equal(t, "NOTE lsof is not on PATH; the open files of live processes are read with "+there, note)

	plain := filepath.Join(dir, "plain", "lsof")
	dgFile(t, plain, 10, dgNow)
	require.NoError(t, os.Chmod(plain, 0o644))
	path, note = findLsof(notOnPath, []string{missing, plain}, isExecutable)
	assert.Empty(t, path, "a file that cannot be run is no lsof")
	assert.Contains(t, note, "NOTE lsof is not on PATH nor at "+missing+", "+plain+": the open files of live processes cannot be read")

	held, err := lsofHeld("")
	assert.Nil(t, held)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lsof is not on PATH nor at /usr/sbin/lsof")

	g, out := dgGuard(t)
	g.start = []string{note}
	g.held = func() ([]string, error) { return lsofHeld("") }
	land := t.TempDir()
	clone := filepath.Join(land, "github.com-o-r-0123456789abcdef")
	dgFile(t, filepath.Join(clone, "main.go"), 100, dgNow.Add(-48*time.Hour))
	dgAge(t, clone, dgNow.Add(-48*time.Hour))
	g.landDir = land
	assert.Equal(t, 1, g.run())
	assert.DirExists(t, clone, "without lsof nothing a live process may hold is removed")
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.NotEmpty(t, lines)
	assert.Equal(t, note, lines[0], "the run says what it lacks before any rule")
	assert.Equal(t, 1, strings.Count(out.String(), "NOTE lsof is not on PATH"), "and says it once")
}

// lsofPaths reads lsof's -F pn listing: every absolute name, the guard's own pid's left out.
func TestDiskGuardLsofPathsLeavesOutItsOwnFiles(t *testing.T) {
	t.Parallel()
	in := "p1\nfcwd\nn/\nftxt\nn/sbin/launchd\np42\nfcwd\nn/home/u/pool\np77\nfcwd\nn/home/u/land/c1\nf3\nnlocalhost:6379\nf4\nn/home/u/land/c1/.git/index.lock\n"
	assert.Equal(t, []string{"/", "/sbin/launchd", "/home/u/land/c1", "/home/u/land/c1/.git/index.lock"}, lsofPaths([]byte(in), 42))
}

// procPaths reads a /proc tree: each process's cwd and fd links, each path once, the
// guard's own pid and every name that is not a path (a socket) left out.
func TestDiskGuardProcPathsReadsCwdAndOpenFiles(t *testing.T) {
	t.Parallel()
	proc := t.TempDir()
	link := func(target, at string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(at), 0o755))
		require.NoError(t, os.Symlink(target, at))
	}
	link("/home/u/land/c1", filepath.Join(proc, "77", "cwd"))
	link("/home/u/land/c1/.git/index.lock", filepath.Join(proc, "77", "fd", "4"))
	link("socket:[123]", filepath.Join(proc, "77", "fd", "5"))
	link("/home/u/land/c1", filepath.Join(proc, "78", "cwd"))
	link("/home/u/pool", filepath.Join(proc, "42", "cwd"))
	require.NoError(t, os.MkdirAll(filepath.Join(proc, "self-not-a-pid"), 0o755))
	got, err := procPaths(proc, 42)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"/home/u/land/c1", "/home/u/land/c1/.git/index.lock"}, got)
	_, err = procPaths(filepath.Join(proc, "missing"), 42)
	assert.Error(t, err, "an unreadable /proc is no empty list")
}

// The stop floor reads the data volume's free. A volume whose free could not be read is
// no reading of zero: it must not fake a stop. The failure is said on its NOTE line and
// the closing line is DISK-GUARD INCOMPLETE, never DISK-GUARD STOP (the reader's finding,
// diskguard.go's free stays zero when the home volume could not be read).
func TestDiskGuardDoesNotStopOnAnUnreadVolume(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	g.home = filepath.Join(t.TempDir(), "data")
	g.stopFloor = 200 * gib
	g.free = func(p string) (uint64, error) {
		if p == g.home {
			return 0, errors.New("permission denied")
		}
		return 300 * gib, nil
	}
	assert.Equal(t, 1, g.run(), "an unread volume is reported, not read as zero")
	assert.Contains(t, out.String(), "DISK-GUARD INCOMPLETE")
	assert.NotContains(t, out.String(), "DISK-GUARD STOP")
}

// The stop floor reads every volume this pass reads, the home volume and each --root, not
// the home volume alone: a root under the stop floor stops the loops whatever the home
// volume reads (the reader's finding, diskguard.go stored the free of index 0, the home
// volume, so a root under the floor never stopped).
func TestDiskGuardStopsOnARootUnderTheStopFloor(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	root := t.TempDir()
	g.roots = []string{root}
	g.stopFloor = 200 * gib
	g.free = func(p string) (uint64, error) {
		if p == root {
			return 150 * gib, nil
		}
		return 300 * gib, nil
	}
	assert.Equal(t, 3, g.run(), "a root under the stop floor stops the loops")
	assert.Contains(t, out.String(), "DISK-GUARD STOP ")
	assert.Contains(t, out.String(), "free=161061273600")
	assert.NotContains(t, out.String(), "DISK-GUARD OK")
}

// A volume that could not be read reaches the closing line: the run never stops on the
// readings it has while one volume is unknown, even when another volume is under the stop
// floor (the reader's finding, diskguard.go returned STOP before the INCOMPLETE line, so
// an unread root lost to a low home reading).
func TestDiskGuardDoesNotStopWhenAVolumeCouldNotBeRead(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	root := filepath.Join(t.TempDir(), "data")
	g.roots = []string{root}
	g.stopFloor = 200 * gib
	g.free = func(p string) (uint64, error) {
		if p == root {
			return 0, errors.New("permission denied")
		}
		return 10 * gib, nil // the home volume is under the stop floor
	}
	assert.Equal(t, 1, g.run(), "an unread root is reported, not read as a low home volume")
	assert.Contains(t, out.String(), "could not be read (permission denied)")
	assert.Contains(t, out.String(), "DISK-GUARD INCOMPLETE")
	assert.NotContains(t, out.String(), "DISK-GUARD STOP")
}

// The bench sweep looks only at the run directories, <root>/runs/*: an old run with no
// live process goes, a young run and a run a live process names stay, and no other
// directory under the bench root (the shared build cache, a friend's copy) is ever a
// candidate, so the sweep can never delete an arbitrary two-level directory.
func TestDiskGuardSweepsOnlyBenchRunDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	oldRun := filepath.Join(root, "runs", "gate-old")
	youngRun := filepath.Join(root, "runs", "gate-young")
	liveRun := filepath.Join(root, "runs", "read-live")
	cacheDir := filepath.Join(root, "cache", "go-build")
	friendDir := filepath.Join(root, "friends", "alice")
	for _, d := range []string{oldRun, youngRun, liveRun, cacheDir, friendDir} {
		dgFile(t, filepath.Join(d, "f"), 10, dgNow)
	}
	dgAge(t, oldRun, dgNow.Add(-3*time.Hour))
	dgAge(t, liveRun, dgNow.Add(-3*time.Hour))
	dgAge(t, cacheDir, dgNow.Add(-72*time.Hour))
	dgAge(t, friendDir, dgNow.Add(-72*time.Hour))
	// youngRun keeps dgNow: it is inside bench.RunAgeLimit.

	g, out := dgGuard(t)
	g.roots = []string{root}
	g.procs = func() ([]string, error) { return []string{"go build -o " + liveRun + "/f"}, nil }
	g.bench()

	assert.NoDirExists(t, oldRun, "an old run with no live process is swept")
	assert.DirExists(t, youngRun, "a run younger than the bound is kept")
	assert.DirExists(t, liveRun, "a run a live process names is kept")
	assert.DirExists(t, cacheDir, "the build cache below the root is never a candidate")
	assert.DirExists(t, friendDir, "a two-level directory below the root is never a candidate")
	assert.Contains(t, out.String(), "REMOVED run "+oldRun)
	assert.Contains(t, out.String(), "KEPT run "+liveRun)
	assert.NotContains(t, out.String(), cacheDir)
	assert.NotContains(t, out.String(), friendDir)
}
