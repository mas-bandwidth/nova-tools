package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordGit is a mirrorGit that records each call and makes the bare directory `init` is
// asked for, failing the calls whose arguments contain failOn.
type recordGit struct {
	calls  []string
	failOn string
}

func (r *recordGit) run(_ context.Context, dir string, args ...string) error {
	r.calls = append(r.calls, dir+"|"+strings.Join(args, " "))
	if r.failOn != "" && strings.Contains(strings.Join(args, " "), r.failOn) {
		return errors.New("fatal: unable to access")
	}
	if args[0] == "init" {
		return os.MkdirAll(args[len(args)-1], 0o755)
	}
	return nil
}

func TestMirrorClonesWhatIsAbsentAndFetchesHeadsAndPullRequests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &recordGit{}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--repos", "a, b", "--base", "https://example.test/org/"}, &out, &errb, g.run, nil)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "MIRROR OK a\n")
	assert.Contains(t, out.String(), "MIRROR OK b\n")
	require.Len(t, g.calls, 4*2)
	assert.Equal(t, "|init --bare --quiet -- "+filepath.Join(dir, "a.git"), g.calls[0])
	assert.Equal(t, filepath.Join(dir, "a.git")+"|fetch --quiet --no-tags --prune -- https://example.test/org/a.git +refs/heads/*:refs/heads/* +refs/pull/*/head:refs/pull/*/head", g.calls[1])

	// a second pass finds every mirror and only fetches
	g.calls = nil
	out.Reset()
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "a,b", "--base", "https://example.test/org"}, &out, &errb, g.run, nil))
	require.Len(t, g.calls, 4)
	assert.Contains(t, g.calls[0], "|fetch ")
}

// The repositories every bench carries are the stream repos --repos names unioned
// with the fixed set: a bench that names only `a` still gets nova-tools and
// nova-sprint (swarm.MirrorSetRepos, docs/SPEC-SWARM.md, warm-clones-and-caches.w2).
func TestMirrorAddsTheFixedRepositoriesToTheStreamList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &recordGit{}
	var out, errb bytes.Buffer
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, g.run, nil), errb.String())
	for _, want := range []string{"a", "nova-tools", "nova-sprint"} {
		assert.Contains(t, out.String(), "MIRROR OK "+want+"\n")
	}
}

func TestMirrorOneFailureDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	g := &recordGit{failOn: "/a.git"}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", t.TempDir(), "--repos", "a,b", "--base", "https://example.test/o"}, &out, &errb, g.run, nil)
	assert.Equal(t, 1, code)
	assert.Contains(t, out.String(), "MIRROR FAILED a: fatal: unable to access\n")
	assert.Contains(t, out.String(), "MIRROR OK b\n")
}

func TestMirrorRefusesWhatIsNotARepositoryName(t *testing.T) {
	t.Parallel()
	for args, want := range map[string]string{
		"--dir x": "--repos is required",
		"--base https://example.test/o --repos ../evil":       "no repository name",
		"--base https://example.test/o --repos a/b":           "no repository name",
		"--base https://example.test/o --repos a --every -1s": "--every is 0 or a positive duration",
	} {
		g := &recordGit{}
		var out, errb bytes.Buffer
		code := runMirror(append([]string{"--dir", t.TempDir()}, strings.Fields(args)...), &out, &errb, g.run, nil)
		assert.Equal(t, 2, code, args)
		assert.Contains(t, errb.String(), want, args)
		assert.Empty(t, g.calls, "a refused invocation runs no git: %s", args)
	}
}

// A --repos of separators alone parses to no repository; the verb refuses it before
// --dir is made, so a refused list leaves no empty mirror directory behind.
func TestMirrorRefusesAnEmptyRepositoryListBeforeMakingTheDirectory(t *testing.T) {
	t.Parallel()
	for _, list := range []string{",", " , ", ",,,"} {
		dir := filepath.Join(t.TempDir(), "mirror")
		g := &recordGit{}
		var out, errb bytes.Buffer
		code := runMirror([]string{"--dir", dir, "--repos", list, "--base", "https://example.test/o"}, &out, &errb, g.run, nil)
		assert.Equal(t, 2, code, list)
		assert.Contains(t, errb.String(), "names no repository", list)
		assert.Empty(t, g.calls, "a refused list runs no git: %q", list)
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "the refused list made the mirror directory: %q", list)
	}
}

// --every runs another pass each time the wait returns true and stops when it returns
// false; the test's wait is a fake, so nothing sleeps.
func TestMirrorEveryRunsAgainUntilTheWaitEnds(t *testing.T) {
	t.Parallel()
	g := &recordGit{}
	var out, errb bytes.Buffer
	waits := 0
	var waited []time.Duration
	wait := func(_ context.Context, d time.Duration) bool {
		waits++
		waited = append(waited, d)
		return waits <= 2
	}
	code := runMirror([]string{"--dir", t.TempDir(), "--repos", "a", "--base", "https://example.test/o", "--every", "60s"}, &out, &errb, g.run, wait)
	assert.Equal(t, 0, code, errb.String())
	assert.Equal(t, 3, strings.Count(out.String(), "MIRROR OK a"), "one pass, then one after each of two waits")
	assert.Equal(t, []time.Duration{time.Minute, time.Minute, time.Minute}, waited)
}

// The mirror set is a converged property of the fleet: every bench (a fleet row) carries
// every repository a card can name. `nova-swarm mirror --check` is the converge-check: it
// names a bench missing a mirror as a fault and writes nothing. A refresh pass creates the
// missing mirrors. docs/FLEET.md, the mirrors' refresh row; docs/SPEC-SWARM.md,
// warm-clones-and-caches.w2.
func TestMirrorSetCoversEveryBench(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &recordGit{}
	base := "https://example.test/org"

	// The fleet inventory: two benches, and the one repository each already carries so it
	// is missing exactly the other; neither carries a fixed repository yet.
	for _, keep := range []string{"b1/alpha.git", "b2/beta.git"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, keep), 0o755), keep)
	}
	repos := []string{"alpha", "beta"}
	required := swarm.MirrorSetRepos(repos)

	// converge-check names every mirror a bench lacks and runs no git.
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--benches", "b1,b2", "--repos", "alpha,beta", "--base", base, "--check"}, &out, &errb, g.run, nil)
	assert.Equal(t, 1, code, errb.String())
	var missing []string
	for _, b := range []string{"b1", "b2"} {
		for _, r := range required {
			if (b == "b1" && r == "alpha") || (b == "b2" && r == "beta") {
				continue
			}
			missing = append(missing, "MIRROR MISSING "+b+" "+r)
		}
	}
	for _, want := range missing {
		assert.Contains(t, out.String(), want+"\n")
	}
	assert.Empty(t, g.calls, "a converge-check runs no git")

	// A refresh pass creates every missing mirror on every bench.
	out.Reset()
	code = runMirror([]string{"--dir", dir, "--benches", "b1,b2", "--repos", "alpha,beta", "--base", base}, &out, &errb, g.run, nil)
	require.Equal(t, 0, code, errb.String())
	for _, b := range []string{"b1", "b2"} {
		for _, r := range required {
			_, err := os.Stat(filepath.Join(dir, b, r+".git"))
			require.NoError(t, err, "the refresh pass did not create %s/%s.git", b, r)
		}
	}
}

func TestDiskGuardStopsUnderItsStopFloor(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	g.stopFloor = 200 * gib
	g.free = func(string) (uint64, error) { return 150 * gib, nil }
	assert.Equal(t, 3, g.run())
	assert.Contains(t, out.String(), "DISK-GUARD STOP ")
	assert.NotContains(t, out.String(), "DISK-GUARD OK")

	g, out = dgGuard(t)
	g.stopFloor = 50 * gib
	assert.Equal(t, 0, g.run(), "a volume over the stop floor ends OK")
	assert.Contains(t, out.String(), "DISK-GUARD OK")
}

// emptyObject writes one empty loose object into the bare repository at bare: what a write
// cut short leaves (2026-10-24, 14-23 of them on two benches' mirrors).
func emptyObject(t *testing.T, bare string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(bare, "objects", "ab"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bare, "objects", "ab", strings.Repeat("c", 38)), nil, 0o644))
}

// A mirror holding an empty object file is re-cloned: a new mirror is made beside it, fetched
// and checked, and only then takes its place; the old one is gone and nothing is left beside.
func TestMirrorReclonesAMirrorWithAnEmptyObjectFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bare := filepath.Join(dir, "a.git")
	emptyObject(t, bare)
	require.NoError(t, os.WriteFile(filepath.Join(bare, "objects", "ab", "keep"), []byte("x"), 0o644))
	g := &recordGit{}
	var out, errb bytes.Buffer
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, g.run, nil), errb.String())
	assert.Contains(t, out.String(), "MIRROR RECLONED a: 1 empty object file(s)\n")
	assert.Contains(t, out.String(), "MIRROR OK a\n")
	fresh := bare + ".reclone"
	assert.Equal(t, []string{
		"|init --bare --quiet -- " + fresh,
		fresh + "|fetch --quiet --no-tags --prune -- https://example.test/o/a.git +refs/heads/*:refs/heads/* +refs/pull/*/head:refs/pull/*/head",
		fresh + "|fsck --connectivity-only --no-progress",
	}, g.calls[:3])
	assert.NoDirExists(t, filepath.Join(bare, "objects"), "the damaged mirror was not replaced")
	assert.DirExists(t, bare)
	assert.NoDirExists(t, fresh)
	assert.NoDirExists(t, bare+".bad")
	assert.Zero(t, mirrorEmptyObjects(bare))
}

// A re-clone that fails keeps the old mirror where it was: the bench is no worse than it was,
// and the pass says the re-clone failed (staging's one fallback to origin covers the cards).
func TestMirrorKeepsTheOldMirrorWhenTheRecloneFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bare := filepath.Join(dir, "a.git")
	emptyObject(t, bare)
	g := &recordGit{failOn: "fsck"}
	var out, errb bytes.Buffer
	assert.Equal(t, 1, runMirror([]string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, g.run, nil))
	assert.Contains(t, out.String(), "MIRROR FAILED a: the re-clone after 1 empty object file(s) failed, the old mirror kept: ")
	assert.Equal(t, 1, mirrorEmptyObjects(bare), "the old mirror is not where it was")
	assert.NoDirExists(t, bare+".reclone")
}

// A fetch that fails into a mirror that fails git fsck --connectivity-only re-clones it; a
// fetch that fails into a sound mirror is the network's failure and re-clones nothing.
func TestMirrorReclonesWhenTheFetchFailsAndFsckFails(t *testing.T) {
	t.Parallel()
	for _, sound := range []bool{false, true} {
		dir := t.TempDir()
		bare := filepath.Join(dir, "a.git")
		require.NoError(t, os.MkdirAll(filepath.Join(bare, "objects"), 0o755))
		var calls []string
		git := func(_ context.Context, d string, args ...string) error {
			calls = append(calls, d+"|"+strings.Join(args, " "))
			switch {
			case args[0] == "init":
				return os.MkdirAll(args[len(args)-1], 0o755)
			case d == bare && args[0] == "fetch":
				return errors.New("fatal: bad object")
			case d == bare && args[0] == "fsck" && !sound:
				return errors.New("broken link")
			}
			return nil
		}
		var out, errb bytes.Buffer
		code := runMirror([]string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o"}, &out, &errb, git, nil)
		if sound {
			assert.Equal(t, 1, code)
			assert.Contains(t, out.String(), "MIRROR FAILED a: fatal: bad object\n")
			assert.NotContains(t, strings.Join(calls, "\n"), ".reclone")
			continue
		}
		assert.Equal(t, 0, code, out.String())
		assert.Contains(t, out.String(), "MIRROR RECLONED a: the fetch failed (fatal: bad object) and git fsck --connectivity-only failed (broken link)\n")
		assert.NoDirExists(t, bare+".reclone")
	}
}

// Only object files count: an empty .keep or tmp pack of a fetch in flight is no damage.
func TestMirrorEmptyObjectsCountsObjectsOnly(t *testing.T) {
	t.Parallel()
	bare := t.TempDir()
	pack := filepath.Join(bare, "objects", "pack")
	require.NoError(t, os.MkdirAll(pack, 0o755))
	for _, f := range []string{"pack-1.keep", "tmp_pack_x", "pack-1.promisor"} {
		require.NoError(t, os.WriteFile(filepath.Join(pack, f), nil, 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(bare, "objects", "info"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bare, "objects", "info", "packs"), nil, 0o644))
	assert.Zero(t, mirrorEmptyObjects(bare))
	require.NoError(t, os.WriteFile(filepath.Join(pack, "pack-2.idx"), nil, 0o644))
	emptyObject(t, bare)
	assert.Equal(t, 2, mirrorEmptyObjects(bare))
	assert.Zero(t, mirrorEmptyObjects(filepath.Join(bare, "absent")))
}
