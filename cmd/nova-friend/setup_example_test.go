package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupExampleWorld makes the documented working directory in memory; the
// pasted /tmp paths never refer to the host's files or native programs.
func setupExampleWorld(t *testing.T) (world, *friend.MemFS) {
	t.Helper()
	r := newRig(t, "worker")
	w := r.world()
	fs := friend.NewMemFS()
	require.NoError(t, fs.MkdirAll("/tmp/worker", 0700))
	w.settings = fs
	w.setupRunner = &friend.SetupRunner{FS: fs, Exec: func(context.Context, string, string, []string, string) (string, int, error) {
		require.FailNow(t, "setup example must not execute native commands")
		return "", 0, nil
	}, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) {
		require.FailNow(t, "setup example must not lock or write")
		return func() error { return nil }, nil
	}, Nonce: func() string { return "run" }}
	return w, fs
}

func TestSetupFirstRunExampleMatchesWholeTranscript(t *testing.T) {
	t.Parallel()
	command := "nova-friend setup --as worker --harness opencode --dir /tmp/worker --config-dir /tmp/worker-config --home /tmp/worker-home --profile isolated --program /usr/local/bin/opencode --deny-self /tmp/coordinator --profile-only --dry-run"
	w, fs := setupExampleWorld(t)
	argv, err := onboarding.SplitShell(command)
	require.NoError(t, err)
	got := cliOf(w).Do(t, argv[1:]...).Exit(0)
	raw := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md"))
	lines, err := onboarding.FirstRun(raw, "nova-friend")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-friend", lines)
	require.NoError(t, err)
	var selected []onboarding.Step
	for _, step := range steps {
		if step.Line == "$ "+command {
			selected = append(selected, step)
		}
	}
	require.Len(t, selected, 1, "the exact setup example belongs in the first-run sitting")
	for _, p := range onboarding.CompareTranscript(selected, []onboarding.Result{{Code: got.Code, Stdout: got.Stdout, Stderr: got.Stderr}}, nil) {
		assert.Failf(t, "documented setup transcript differs", "docs/TESTS.md: %s", p)
	}
	for _, path := range []string{"/tmp/worker-home", "/tmp/worker-config", "/tmp/worker/opencode.json"} {
		entry, err := fs.Lstat(path)
		require.NoError(t, err)
		assert.Equal(t, friend.KindMissing, entry.Kind, "dry-run must preserve missing %s", path)
	}
	var out, errb strings.Builder
	require.Equal(t, 0, run([]string{"help"}, strings.NewReader(""), &out, &errb, w))
	examples, err := onboarding.ExampleLines(out.String(), "nova-friend")
	require.NoError(t, err)
	assert.Contains(t, examples, command)
}
