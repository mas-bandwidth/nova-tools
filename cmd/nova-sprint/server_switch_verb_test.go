package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerGroupAndSwitchHelp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	// Bare group refuses naming its verbs
	code, _, errs := ta.do("server")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "server wants one of its verbs")
	assert.Contains(t, errs, "server switch")

	// Help for group
	code, out, _ := ta.do("help server")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "server switch")

	// Verb -h
	code, out, _ = ta.do("server switch -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-sprint server switch")
	assert.Contains(t, out, "--rollback")
	assert.Contains(t, out, "--window")
	assert.Contains(t, out, "--repo")
	assert.Contains(t, out, "--base")
}

// verbTwin is a bare origin holding the sprint base and a side branch cut from it, and a
// clone of it for server switch --repo; base and side are each branch's tip.
func verbTwin(t *testing.T) (clone, base, side string) {
	t.Helper()
	dir := t.TempDir()
	g := func(in string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", in, "-c", "user.name=twin", "-c", "user.email=twin@example.com", "-c", "init.defaultBranch=main"}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	origin, work, clone := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work"), filepath.Join(dir, "clone")
	g(dir, "init", "-q", "--bare", origin)
	g(dir, "init", "-q", "-b", "sprint/base", work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "f"), []byte("base"), 0o644))
	g(work, "add", "f")
	g(work, "commit", "-q", "-m", "on the base")
	base = g(work, "rev-parse", "HEAD")
	g(work, "checkout", "-q", "-b", "side")
	g(work, "commit", "-q", "--allow-empty", "-m", "off the base")
	side = g(work, "rev-parse", "HEAD")
	g(work, "remote", "add", "origin", origin)
	g(work, "push", "-q", "origin", "sprint/base", "side")
	g(dir, "clone", "-q", origin, clone)
	return clone, base, side
}

// candidateScript is a candidate built from commit: its version verb prints the stamp, and
// its shadow tick passes the canary (shadow.go).
func candidateScript(commit string) string {
	return "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'nova-sprint 20261005120000-" + commit[:12] + " linux/amd64 go1.24.0'; exit 0; fi\necho '" +
		`{"shadow":{"epoch":0,"state":"STOPPED","parts":[],"size":0,"took_ns":1}}` + "'\n"
}

// TestServerSwitchVerbRefusesABinaryOffTheSprintBase: server switch refuses a candidate whose
// build commit is not an ancestor of origin's sprint base, naming the commit, the base and the
// remedy, before its shadow tick and with nothing on disk changed; it refuses with no base
// named, a check that cannot be made; and it switches a candidate built from the base (docs/SPEC-SPRINT.md section 14,
// "server-from-base-only.w1").
func TestServerSwitchVerbRefusesABinaryOffTheSprintBase(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	clone, base, side := verbTwin(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "nova-sprint")
	require.NoError(t, os.WriteFile(target, []byte("the running server"), 0o755))
	offBase, onBase := filepath.Join(dir, "off"), filepath.Join(dir, "on")
	require.NoError(t, os.WriteFile(offBase, []byte(candidateScript(side)), 0o755))
	require.NoError(t, os.WriteFile(onBase, []byte(candidateScript(base)), 0o755))

	code, _, errs := ta.do("server switch " + onBase + " --target " + target)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "server switch REFUSED: the base check of "+onBase+" could not be made")
	assert.Contains(t, errs, "remedy: name the sprint base with --base <branch> and a clone whose origin holds it with --repo <clone>")

	code, out, errs := ta.do("server switch " + offBase + " --target " + target + " --repo " + clone + " --base sprint/base --rollback")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "server switch REFUSED: "+offBase+" was built from commit "+side[:12])
	assert.Contains(t, errs, "not an ancestor of origin/sprint/base")
	assert.Contains(t, errs, "remedy: build nova-sprint from origin/sprint/base at its tip, then run: nova-sprint server switch <that binary>")
	assert.Contains(t, errs, "the old server keeps running")
	assert.NotContains(t, out, "SHADOW TICK OK", "refused before its shadow tick")
	kept, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "the running server", string(kept))
	for _, side := range []string{".prev", ".switch.json", ".shadow.json"} {
		_, err := os.Stat(target + side)
		assert.True(t, os.IsNotExist(err), "nothing written beside the target (%s)", side)
	}

	code, out, errs = ta.do("server switch " + onBase + " --target " + target + " --repo " + clone + " --base sprint/base")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "BASE OK binary="+onBase+" commit="+base+" base=origin/sprint/base")
	assert.Contains(t, out, "SERVER SWITCH OK")
}

func TestServerSwitchVerbSwitchesAndRollsBack(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "nova-sprint")
	candidate := filepath.Join(dir, "nova-sprint-candidate")

	require.NoError(t, os.WriteFile(target, []byte("version-1"), 0o755))
	// version 2 is built from the sprint base and passes the canary: its shadow tick
	// prints a plan (shadow.go)
	clone, base, _ := verbTwin(t)
	version2 := candidateScript(base)
	require.NoError(t, os.WriteFile(candidate, []byte(version2), 0o755))

	// Refuses with no args and no --rollback
	code, _, errs := ta.do("server switch")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants <binary> [--rollback] or --rollback alone")

	// Switch to candidate with rollback enabled
	code, out, _ := ta.do("server switch " + candidate + " --target " + target + " --repo " + clone + " --base sprint/base --rollback")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "SERVER SWITCH OK")

	// Verify target is now version-2 and target.prev is version-1
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, version2, string(content))

	prevContent, err := os.ReadFile(target + ".prev")
	require.NoError(t, err)
	assert.Equal(t, "version-1", string(prevContent))

	// Explicit rollback command
	code, out, _ = ta.do("server switch --target " + target + " --rollback")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "SERVER SWITCH ROLLED BACK")

	restored, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "version-1", string(restored))
}
