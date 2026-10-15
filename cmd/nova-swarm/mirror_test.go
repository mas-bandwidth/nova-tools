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
	code := runMirror([]string{"--dir", dir, "--repos", "a, b", "--base", "https://example.test/org/", "--no-links"}, &out, &errb, g.run, nil)
	require.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "MIRROR OK a\n")
	assert.Contains(t, out.String(), "MIRROR OK b\n")
	require.Len(t, g.calls, 4*2)
	assert.Equal(t, "|init --bare --quiet -- "+filepath.Join(dir, "a.git"), g.calls[0])
	assert.Equal(t, filepath.Join(dir, "a.git")+"|fetch --quiet --no-tags --prune -- https://example.test/org/a.git +refs/heads/*:refs/heads/* +refs/pull/*/head:refs/pull/*/head", g.calls[1])

	// a second pass finds every mirror and only fetches
	g.calls = nil
	out.Reset()
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "a,b", "--base", "https://example.test/org", "--no-links"}, &out, &errb, g.run, nil))
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
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o", "--no-links"}, &out, &errb, g.run, nil), errb.String())
	for _, want := range []string{"a", "nova-tools", "nova-sprint"} {
		assert.Contains(t, out.String(), "MIRROR OK "+want+"\n")
	}
}

func TestMirrorOneFailureDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	g := &recordGit{failOn: "/a.git"}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", t.TempDir(), "--repos", "a,b", "--base", "https://example.test/o", "--no-links"}, &out, &errb, g.run, nil)
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
		code := runMirror(append([]string{"--dir", t.TempDir(), "--no-links"}, strings.Fields(args)...), &out, &errb, g.run, nil)
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
	code := runMirror([]string{"--dir", t.TempDir(), "--repos", "a", "--base", "https://example.test/o", "--every", "60s", "--no-links"}, &out, &errb, g.run, wait)
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

// A refresh of this bench's own flat mirror points <linkDir>/<repo>-mirror.git at the bare
// mirror, the path swarm.FindBenchMirror reads after the bench-home candidates (stage.go).
// runMirrorIn names the link directory, so this test uses t.TempDir() and never /tmp. A
// second pass replaces the link with the same target, --no-links writes none, and --check
// writes none.
func TestMirrorLinksItsOwnFlatMirrorForFindBenchMirror(t *testing.T) {
	t.Parallel()
	dir, linkDir := t.TempDir(), t.TempDir()
	link := filepath.Join(linkDir, "a-mirror.git")
	g := &recordGit{}
	var out, errb bytes.Buffer
	args := []string{"--dir", dir, "--repos", "a", "--base", "https://example.test/o"}

	require.Equal(t, 0, runMirrorIn(args, &out, &errb, g.run, nil, linkDir), errb.String())
	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "a.git"), target)

	// a second pass replaces the link, and it still names the bare mirror
	require.Equal(t, 0, runMirrorIn(args, &out, &errb, g.run, nil, linkDir), errb.String())
	target, err = os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "a.git"), target)

	// --no-links is the pass that writes nothing outside --dir
	noLinkDir := t.TempDir()
	require.Equal(t, 0, runMirrorIn(append(args, "--no-links"), &out, &errb, g.run, nil, noLinkDir), errb.String())
	assert.NoFileExists(t, filepath.Join(noLinkDir, "a-mirror.git"))

	// --check names the missing mirrors (a fresh --dir holds none) and writes no link
	checkDir, checkMirrorDir := t.TempDir(), t.TempDir()
	code := runMirrorIn([]string{"--dir", checkMirrorDir, "--repos", "a", "--base", "https://example.test/o", "--check"}, &out, &errb, g.run, nil, checkDir)
	assert.Equal(t, 1, code, "the fresh mirror directory holds none of the repositories")
	assert.NoFileExists(t, filepath.Join(checkDir, "a-mirror.git"))
}

// linkMirror validates the repository name through pkg/safepath before it joins a path, so
// a name that is no path element is refused rather than followed out of the link directory.
func TestMirrorLinkRefusesANameThatIsNoPathElement(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../evil", "a/b", "", ".", "-a"} {
		err := linkMirror(t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "x.git"), name)
		assert.Error(t, err, name)
		assert.Contains(t, err.Error(), "no repository name", name)
	}
}
