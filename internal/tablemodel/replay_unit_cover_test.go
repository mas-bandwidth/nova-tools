package tablemodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// replayUnitCoverCaptureCancelled returns *Failure with "ran out of time".
func TestTablemodelReplayCoverCaptureCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	err := guard(func() { _ = capture(r) })
	var f *Failure
	require.ErrorAs(t, err, &f, "want Failure, got %v", err)
	tassert.Contains(t, err.Error(), "ran out of time")
}

// replayUnitCoverReplayReceiptsCancelled returns *Failure with "ran out of time".
func TestTablemodelReplayCoverReplayReceiptsCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	trace := Trace{Initial: State{}}
	err := guard(func() { replayReceipts(r, trace) })
	var f *Failure
	require.ErrorAs(t, err, &f, "want Failure, got %v", err)
	tassert.Contains(t, err.Error(), "ran out of time")
}

// replayUnitCoverRunReplayWithNonExistentTmpDir returns *CannotRun, calls OnStep zero times, writes no trace.json.
func TestTablemodelReplayCoverRunReplayWithNonExistentTmpDir(t *testing.T) {
	t.Parallel()
	source := filepath.Join("testdata", "table-pinned.lua")
	dir := t.TempDir()
	nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")
	o := ReplayOptions{
		Source: source,
		Models: filepath.Join("..", "..", "tla"),
		Dir:    dir,
		Budget: 1 * time.Minute,
		Server: ServerOptions{TmpDir: nonExistentDir},
		OnStep: func(ReplayEvent) {},
	}
	var c *CannotRun
	err := RunReplay(o)
	require.ErrorAs(t, err, &c, "want CannotRun")
	tracePath := filepath.Join(dir, "trace.json")
	_, err = os.Stat(tracePath)
	tassert.True(t, os.IsNotExist(err))
}

// replayUnitCoverRunHarnessDirDoesNotExist refuses and never calls executor.
func TestTablemodelReplayCoverRunHarnessDirDoesNotExist(t *testing.T) {
	t.Parallel()
	nonExistentDir := filepath.Join(t.TempDir(), "does-not-exist")
	var called bool
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		called = true
		return tlc.ExitPass
	}
	_, err := runHarness(context.Background(), ReplayOptions{Exec: exec}, nonExistentDir, "execution", false, Trace{})
	require.Error(t, err)
	tassert.False(t, called, "executor should not be called")
}

// replayUnitCoverRunHarnessModuleFileMissing refuses and never calls executor.
func TestTablemodelReplayCoverRunHarnessModuleFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	modelsDir := t.TempDir()
	var called bool
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		called = true
		return tlc.ExitPass
	}
	_, err := runHarness(context.Background(), ReplayOptions{Models: modelsDir, Exec: exec}, dir, "execution", false, Trace{})
	require.Error(t, err)
	tassert.False(t, called, "executor should not be called")
}

// replayUnitCoverRunHarnessCfgMissing refuses and never calls executor.
func TestTablemodelReplayCoverRunHarnessCfgMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmpDir := t.TempDir()
	for _, m := range modelModules {
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, m+".tla"), []byte("test"), 0o644))
	}
	var called bool
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		called = true
		return tlc.ExitPass
	}
	_, err := runHarness(context.Background(), ReplayOptions{Models: tmpDir, Exec: exec}, dir, "execution", false, Trace{})
	require.Error(t, err)
	tassert.False(t, called, "executor should not be called")
}

// replayUnitCoverCopyFilesMatchIsDirectory refuses.
func TestTablemodelReplayCoverCopyFilesMatchIsDirectory(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(src, "test.cfg"), 0o755))
	err := copyFiles(src, dst, "*.cfg")
	require.Error(t, err)
}

// replayUnitCoverCopyFilesDstDoesNotExist refuses.
func TestTablemodelReplayCoverCopyFilesDstDoesNotExist(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	nonExistentDst := filepath.Join(t.TempDir(), "does-not-exist")
	require.NoError(t, os.WriteFile(filepath.Join(src, "test.cfg"), []byte("test"), 0o644))
	err := copyFiles(src, nonExistentDst, "*.cfg")
	require.Error(t, err)
}
