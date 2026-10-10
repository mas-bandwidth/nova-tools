package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLandDevSyncIsOffUnlessAskedAndThenUsesTheTreeGate is --dev-sync
// (docs/SPEC-SPRINT.md, "Dev sync every cycle"): off unless the flag is set,
// and when it is set the round's tree gate gates the merge before the push.
func TestLandDevSyncIsOffUnlessAskedAndThenUsesTheTreeGate(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := newApp(func(string) string { return "" }).run([]string{"land", "-h"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	help := out.String()
	assert.Contains(t, help, "--dev-sync")
	assert.Contains(t, help, "off by default")
	assert.Contains(t, help, "tree gate")

	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	r.commit("feature.txt", "feature\n", "feature on dev")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/dev")
	land := "land --repo-dir '" + r.clone + "' --base main"

	code, _, errs := r.do(land)
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, r.git(r.remote, "ls-tree", "-r", "--name-only", "main"), "feature.txt", "without --dev-sync, land does not sync")

	code, _, errs = r.do(land + " --dev-sync --dry-run")
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, r.git(r.remote, "ls-tree", "-r", "--name-only", "main"), "feature.txt", "--dry-run never syncs")

	code, _, errs = r.do(land + " --dev-sync")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, r.git(r.remote, "ls-tree", "-r", "--name-only", "main"), "feature.txt")
	assert.Contains(t, r.git(r.remote, "log", "-1", "--format=%s", "main"), "land dev sync")

	red := newLandRig(t)
	red.commit("go.mod", "module example.com/landsync\n\ngo 1.26.6\n", "module")
	red.commit("bad.go", "package landsync\n\nfunc Broken( {\n", "syntax")
	red.git(red.worker, "push", "-q", "origin", "HEAD:refs/heads/dev")
	code, _, errs = red.do("land --repo-dir '" + red.clone + "' --base main --dev-sync")
	require.NotEqual(t, 0, code, errs)
	assert.Contains(t, errs, "tree gate")
	assert.NotContains(t, red.git(red.remote, "ls-tree", "-r", "--name-only", "main"), "bad.go", "a red gate pushes nothing")
}
