package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

func TestAddRefusesACardWhoseWorkIsAlreadyOnTheBase(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	remote, _ := twinRemote(t, ta, map[string]string{
		"cmd/example/example.go":      "package example\n",
		"cmd/example/example_test.go": "package example\n\nimport \"testing\"\n\nfunc TestTask(t *testing.T) {}\n",
		"cmd/example/new.go":          "package example\n",
	}, "sprint/base")
	git := func(dir string, args ...string) string {
		t.Helper()
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: dir != ""}, args...)
		require.NoError(t, err, "git %v: %s", args, res.Stderr)
		return strings.TrimSpace(string(res.Stdout))
	}
	work := filepath.Join(t.TempDir(), "work")
	git("", "clone", "-q", remote, work)
	git(work, "config", "user.name", "Fixture")
	git(work, "config", "user.email", "fixture@example.invalid")
	git(work, "commit", "-q", "--allow-empty", "-m", "commit-done: already landed")
	branch := "sprint/branch-done.w1.g1.e15"
	git(work, "switch", "-q", "-c", branch)
	git(work, "commit", "-q", "--allow-empty", "-m", "unrelated: branch work")
	git(work, "push", "-q", remote, "HEAD:refs/heads/"+branch)
	git(work, "switch", "-q", "sprint/base")
	git(work, "merge", "-q", "--ff-only", branch)
	git(work, "push", "-q", remote, "HEAD:refs/heads/sprint/base")
	makeBrief := func(id, commit, created, testName string) string {
		head := "RESULT: " + id + " sha=0123456789ab tier: pro\nREPO: " + remote + "\nBASE: sprint/base\nPATHS: cmd/example/*.go\n"
		if created != "" {
			head += "NEW: " + created + "\n"
		}
		tree := "\nTHE TASK. Complete the example task.\n\nSTEP 1. Implement the example.\n  PATHS: cmd/example/*.go\n  COMMIT: " + commit + "\n  VERDICT: ok when the change is present\n"
		brief := passingBrief(head + "TEST: ./cmd/example " + testName + "\n" + tree)
		return brief
	}
	add := func(id, brief string, allow bool) (int, string, string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(path, []byte(brief), 0o600))
		line := "add --stream s1 --one --actor lead --brief-file " + path
		if allow {
			line += " --allow-done"
		}
		return ta.do(line)
	}

	for _, tc := range []struct {
		name, id, commit, created, testName string
		want                                string
	}{
		{"commit subject", "commit-done", "commit-done: implement", "", "TestNew", "commit-done: already landed"},
		{"pushed branch ancestor", "branch-done", "branch-work: implement", "", "TestNew", "branch"},
		{"created files and named test", "files-done", "new-work: implement", "cmd/example/new.go", "TestTask", "new.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ta.applies()
			code, out, stderr := add(tc.id, makeBrief(tc.id, tc.commit, tc.created, tc.testName), false)
			assert.Equal(t, 2, code, "%s%s", out, stderr)
			assert.Contains(t, stderr, "already done", "%s", stderr)
			assert.Contains(t, stderr, tc.want, "%s", stderr)
			assert.False(t, ta.placed(tc.id), "refused work was written")
			assert.Equal(t, before, ta.applies(), "refusal wrote to the store")
		})
	}

	code, out, stderr := add("fresh-card", makeBrief("fresh-card", "fresh-card: implement", "", "TestNew"), false)
	require.Equal(t, 0, code, "%s%s", out, stderr)
	assert.True(t, ta.placed("fresh-card"), "fresh work was not added")
	code, out, stderr = add("commit-done", makeBrief("commit-done", "commit-done: implement", "", "TestNew"), true)
	require.Equal(t, 0, code, "--allow-done did not override the refusal: %s%s", out, stderr)
	assert.True(t, ta.placed("commit-done"), "--allow-done did not add the completed card")
}
