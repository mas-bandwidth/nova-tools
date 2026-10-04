package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The card's flow nova-sprint help walks through (realSteps) runs as written:
// a twin file and a bare repository in a directory of the test's own, the
// worker finishing at its pushed commit and land merging, pushing and
// reporting it, so the card lands and the base holds it. The shell's lines run
// as a shell would (each && part one git), the tool's in-process with the
// environment the help says to export; the one substitution is the shell's own,
// "$(git -C work rev-parse HEAD)". Paths the help gives relative to the shell's
// directory are given under the test's directory.
func TestTheHelpsWalkthroughLandsACardForReal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := append(os.Environ(), "HOME="+dir, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=walker", "GIT_AUTHOR_EMAIL=walker@example.invalid", "GIT_COMMITTER_NAME=walker", "GIT_COMMITTER_EMAIL=walker@example.invalid")
	git := func(args ...string) string {
		res, err := gitrun.Run(context.Background(), gitrun.Options{Dir: dir, Env: env}, args...)
		require.NoError(t, err, "git %v: %s", args, res.Stderr)
		return strings.TrimSpace(string(res.Stdout))
	}
	sprintEnv := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + filepath.Join(dir, "sprint.twin"), "NOVA_SPRINT_ACTOR": "boss"}
	var last string
	for _, line := range realSteps {
		if strings.HasPrefix(line, "git ") {
			for _, part := range strings.Split(line, " && ") {
				args, err := onboarding.SplitShell(part)
				require.NoError(t, err, part)
				git(args[1:]...)
			}
			continue
		}
		require.True(t, strings.HasPrefix(line, prog+" "), "a line of the walkthrough is git or nova-sprint: %q", line)
		line = strings.ReplaceAll(line, `"$(git -C work rev-parse HEAD)"`, git("-C", "work", "rev-parse", "HEAD"))
		args, err := onboarding.SplitShell(strings.TrimPrefix(line, prog+" "))
		require.NoError(t, err, line)
		for i := range args {
			if i > 0 && args[i-1] == "--repo-dir" {
				args[i] = filepath.Join(dir, args[i])
			}
		}
		a := newApp(func(k string) string { return sprintEnv[k] })
		a.gitEnv = env
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		a.close()
		require.Equal(t, 0, code, "%s\n%s%s", line, out.String(), errb.String())
		last = out.String()
	}
	assert.Contains(t, last, "\nDONE\n", "the walkthrough ends with the card landed")
	assert.Contains(t, git("-C", "origin.git", "log", "--format=%s", "main"), "land s1-1 (sprint stream s1)", "the base holds the landing")
	// the twin's own flow (no git) differs in the finish and the land alone,
	// and the help shows its two lines beside the walkthrough
	for _, l := range twinSteps {
		assert.Contains(t, banner(), "  "+l+"\n")
	}
}
