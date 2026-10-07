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
	assert.Equal(t, "MIRROR OK a\nMIRROR OK b\n", out.String())
	require.Len(t, g.calls, 4)
	assert.Equal(t, "|init --bare --quiet -- "+filepath.Join(dir, "a.git"), g.calls[0])
	assert.Equal(t, filepath.Join(dir, "a.git")+"|fetch --quiet --no-tags --prune -- https://example.test/org/a.git +refs/heads/*:refs/heads/* +refs/pull/*/head:refs/pull/*/head", g.calls[1])

	// a second pass finds both mirrors and only fetches
	g.calls = nil
	out.Reset()
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "a,b", "--base", "https://example.test/org"}, &out, &errb, g.run, nil))
	require.Len(t, g.calls, 2)
	assert.Contains(t, g.calls[0], "|fetch ")
}

func TestMirrorOneFailureDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	g := &recordGit{failOn: "/a.git"}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", t.TempDir(), "--repos", "a,b", "--base", "https://example.test/o"}, &out, &errb, g.run, nil)
	assert.Equal(t, 1, code)
	assert.Equal(t, "MIRROR FAILED a: fatal: unable to access\nMIRROR OK b\n", out.String())
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
