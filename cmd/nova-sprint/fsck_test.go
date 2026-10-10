package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// TestFsckGitRunnerRunsInTheRepositoryDirectory pins fsck's fix: gitRunner takes the
// repository directory and sets it as the git child's working directory, so git merge-base
// --is-ancestor runs in the clone fsck keeps under the sprint root and never in the
// directory nova-sprint happens to run in.
func TestFsckGitRunnerRunsInTheRepositoryDirectory(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	dir := t.TempDir()
	_, err := gitrun.Run(context.Background(), gitrun.Options{Dir: dir, OwnRepo: true}, "init", "-q", dir)
	require.NoError(t, err)
	run := ta.a.gitRunner(context.Background(), dir)
	code, out, errs := run("rev-parse", "--show-toplevel")
	require.Equal(t, 0, code, "rev-parse in %s exited %d: %s", dir, code, errs)
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(strings.TrimSpace(out))
	require.NoError(t, err)
	assert.Equal(t, want, got, "gitRunner ran the command outside the repository directory")
}
