package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// add holds a coding brief to the brief checks at its BASE tip in the lander's clone
// (docs/SPEC-SPRINT.md section 11, card lint; swarm.LintBrief): with no clone it refuses
// naming what is missing; a brief whose PATHS misses a file its START names is refused
// with the token, its remedy and the corrected PATHS line, the one-brief and the
// many-brief form alike, nothing written; the corrected brief is admitted; a BASE origin
// no longer holds is refused as no live base.
func TestAddLintRunsTheBriefChecksAtTheBaseInTheLandersClone(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.commit("a.go", "package a\n", "a")
	r.commit("a_test.go", "package a\n", "a test")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/sprint/s")
	brief := func(base, paths string) string {
		return passingBrief("card: fix a, tier: pro\nREPO: " + r.remote + "\nBASE: " + base + "\nSTART: a.go, README (read)\nPATHS: " + paths + "\nTEST: . TestNewA\n\nFix a.")
	}
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
		return p
	}
	before := r.applies()

	// no clone of the repository under the lander's root: MISSING, never a pass
	code, _, errs := r.do("add --stream s --count 1 --one --brief-file " + write("one.md", brief("sprint/s", "a.go,a_test.go")))
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "LINT DRIFT brief brief: paths-at-base: ")
	assert.Contains(t, errs, "MISSING: the lander keeps no clone of "+r.remote)

	root, err := r.a.landRoot()
	require.NoError(t, err)
	r.git("", "clone", "-q", "--no-tags", "--single-branch", r.remote, filepath.Join(root, repoDirName(r.remote)))

	// START names a.go to change, PATHS covers only its test: refused with the corrected line
	require.NoError(t, os.Mkdir(filepath.Join(dir, "many"), 0o755))
	for _, line := range []string{
		"add --stream s --count 1 --one --brief-file " + write("narrow.md", brief("sprint/s", "a_test.go")),
		"add --stream s --brief-dir " + filepath.Dir(write("many/s-1.md", brief("sprint/s", "a_test.go"))),
	} {
		code, _, errs = r.do(line)
		assert.Equal(t, 2, code, "%s: %s", line, errs)
		assert.Contains(t, errs, `paths-cover-named: 5: the brief names "a.go" to change, and PATHS does not cover it`, line)
		assert.Contains(t, errs, "remedy=every repository path the brief names", line)
		assert.Contains(t, errs, "fix=PATHS: a_test.go,a.go", line)
		assert.Contains(t, errs, "fails the brief checks at its BASE", line)
	}
	assert.Equal(t, before, r.applies(), "a refused add writes nothing")

	// a BASE origin no longer holds is no live base
	code, _, errs = r.do("add --stream s --count 1 --one --brief-file " + write("gone.md", brief("sprint/gone", "a.go,a_test.go")))
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "base-is-live: 3: BASE sprint/gone is not a branch origin holds")
	assert.Equal(t, before, r.applies(), "a refused add writes nothing")

	// the corrected brief is admitted
	r.ok("add --stream s --count 1 --one --brief-file " + write("fixed.md", brief("sprint/s", "a_test.go,a.go")))
}

// briefRepo is a repository a test's coding briefs can name: a bare repository holding
// files (each "package x") on every branch, and the lander's clone of it under a fresh
// land root, which it gives the app, so add's brief checks read its tree at BASE
// (holdBriefChecks) and never a clone outside the test. It returns the repository's path,
// for a brief's REPO: line.
func briefRepo(t *testing.T, a *app, files []string, branches ...string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(in string, args ...string) {
		t.Helper()
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: in, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: in != ""}, args...)
		require.NoError(t, err, "git %v: %s", args, res.Stderr)
	}
	remote, work, root := filepath.Join(dir, "remote.git"), filepath.Join(dir, "work"), filepath.Join(dir, "land")
	git("", "init", "-q", "--bare", "-b", "main", remote)
	git("", "init", "-q", "-b", "main", work)
	for _, f := range append([]string{"README"}, files...) {
		p := filepath.Join(work, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("package x\n"), 0o600))
	}
	git(work, "add", ".")
	git(work, "commit", "-q", "-m", "base")
	for _, b := range append([]string{"main"}, branches...) {
		git(work, "push", "-q", remote, "HEAD:refs/heads/"+b)
	}
	git("", "clone", "-q", "--no-tags", "--single-branch", remote, filepath.Join(root, repoDirName(remote)))
	a.landRoot = func() (string, error) { return root, nil }
	return remote
}
