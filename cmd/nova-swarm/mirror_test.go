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

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMirrorGit records each git a mirror run asks for and makes the directory a clone
// names, failing the repositories named in fail.
type fakeMirrorGit struct {
	calls []string
	fail  map[string]bool
}

func (g *fakeMirrorGit) run(_ context.Context, dir string, args ...string) error {
	g.calls = append(g.calls, filepath.Base(dir)+": "+strings.Join(args, " "))
	for repo := range g.fail {
		if strings.Contains(dir+" "+strings.Join(args, " "), repo) {
			return errors.New("fatal: repository not found")
		}
	}
	if args[0] == "clone" {
		return os.MkdirAll(args[len(args)-1], 0o755)
	}
	return nil
}

func onePass(context.Context, time.Duration) bool { return false }

func TestMirrorClonesWhatIsMissingAndFetchesWhatIsThere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "old.git"), 0o755))
	g := &fakeMirrorGit{}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--repos", "old,new", "--url", "git@github-{repo}:org/{repo}.git"}, &out, &errb, g.run, onePass)
	require.Equal(t, 0, code, "stderr: %s", errb.String())
	require.Len(t, g.calls, 2)
	assert.Equal(t, "old.git: fetch --prune --quiet origin", g.calls[0])
	assert.Contains(t, g.calls[1], "clone --mirror --quiet git@github-new:org/new.git "+filepath.Join(dir, "new.git.tmp-"))
	assert.DirExists(t, filepath.Join(dir, "new.git"), "the clone is renamed into place")
	assert.Contains(t, out.String(), "MIRROR FETCHED repo=old")
	assert.Contains(t, out.String(), "MIRROR CLONED repo=new")
	assert.Contains(t, out.String(), "MIRROR OK repos=2 failed=0")
}

func TestMirrorSaysEachFailureAndEndsTheFailedPassAtOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &fakeMirrorGit{fail: map[string]bool{"gone": true}}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--repos", "gone,here", "--url", "https://example.invalid/{repo}.git"}, &out, &errb, g.run, onePass)
	assert.Equal(t, 1, code)
	assert.Contains(t, out.String(), "MIRROR FAILED repo=gone: the clone failed: fatal: repository not found")
	assert.Contains(t, out.String(), "MIRROR CLONED repo=here", "one failure does not stop the pass")
	assert.Contains(t, out.String(), "MIRROR FAILED repos=2 failed=1")
	assert.NoDirExists(t, filepath.Join(dir, "gone.git"), "a failed clone leaves no mirror")
}

func TestMirrorDryRunRunsNoGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a.git"), 0o755))
	g := &fakeMirrorGit{}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--repos", "a,b", "--url", "u/{repo}", "--dry-run"}, &out, &errb, g.run, onePass)
	require.Equal(t, 0, code, "stderr: %s", errb.String())
	assert.Empty(t, g.calls)
	assert.Contains(t, out.String(), "MIRROR WOULD-FETCH repo=a")
	assert.Contains(t, out.String(), "MIRROR WOULD-CLONE repo=b")
	assert.NoDirExists(t, filepath.Join(dir, "b.git"))
}

func TestMirrorEveryRunsAPassEachTimeUntilTheWaitEnds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := &fakeMirrorGit{}
	waits := 0
	wait := func(_ context.Context, d time.Duration) bool {
		assert.Equal(t, time.Minute, d)
		waits++
		return waits < 3
	}
	var out, errb bytes.Buffer
	code := runMirror([]string{"--dir", dir, "--repos", "a", "--url", "u/{repo}", "--every", "1m"}, &out, &errb, g.run, wait)
	require.Equal(t, 0, code, "stderr: %s", errb.String())
	assert.Equal(t, 3, strings.Count(out.String(), "MIRROR OK"), "three passes, the wait ending the third")
	require.Len(t, g.calls, 3)
	assert.Contains(t, g.calls[0], "clone --mirror")
	assert.Equal(t, []string{"a.git: fetch --prune --quiet origin", "a.git: fetch --prune --quiet origin"}, g.calls[1:])
}

func TestMirrorRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	for want, args := range map[string][]string{
		"--repos":                 {"--url", "u/{repo}"},
		"--url":                   {"--repos", "a"},
		"not a repository's name": {"--repos", "../x", "--url", "u/{repo}"},
		"names no {repo}":         {"--repos", "a", "--url", "https://example.invalid/a.git"},
		"--every is 0 (one pass)": {"--repos", "a", "--url", "u/{repo}", "--every", "10ms"},
	} {
		g := &fakeMirrorGit{}
		var out, errb bytes.Buffer
		code := runMirror(append([]string{"--dir", t.TempDir()}, args...), &out, &errb, g.run, onePass)
		assert.Equal(t, 2, code, "%v", args)
		assert.Contains(t, errb.String(), want, "%v", args)
		assert.Empty(t, g.calls, "%v: a refused run runs no git", args)
	}
}

// The real git: a mirror of a local repository is cloned, then fetched after a new
// commit and a pull request's ref, both of which reach the mirror.
func TestMirrorKeepsARealMirrorFresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "proj.git")
	work := t.TempDir()
	git := func(dir string, args ...string) string {
		out, err := gitrun.Output(ctx, gitrun.Options{C: dir, OwnRepo: true}, args...)
		require.NoError(t, err, "git %v", args)
		return out
	}
	git(work, "init", "--quiet", "--initial-branch", "main")
	git(work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "one")
	git(work, "clone", "--quiet", "--bare", work, src)
	dir := t.TempDir()
	url := filepath.Join(filepath.Dir(src), "{repo}.git")
	var out, errb bytes.Buffer
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "proj", "--url", url}, &out, &errb, gitMirror, onePass), "stderr: %s; out: %s", errb.String(), out.String())
	mirror := filepath.Join(dir, "proj.git")
	assert.Equal(t, git(work, "rev-parse", "HEAD"), git(mirror, "rev-parse", "main"))

	git(work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "two")
	git(work, "push", "--quiet", src, "main", "HEAD:refs/pull/7/head")
	out.Reset()
	require.Equal(t, 0, runMirror([]string{"--dir", dir, "--repos", "proj", "--url", url}, &out, &errb, gitMirror, onePass), "stderr: %s; out: %s", errb.String(), out.String())
	assert.Contains(t, out.String(), "MIRROR FETCHED repo=proj")
	assert.Equal(t, git(work, "rev-parse", "HEAD"), git(mirror, "rev-parse", "main"))
	assert.Equal(t, git(work, "rev-parse", "HEAD"), git(mirror, "rev-parse", "refs/pull/7/head"), "a pull request's ref reaches the mirror")
}
