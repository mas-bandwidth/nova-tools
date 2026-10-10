package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// TestNovaSprintSnapshotCoverTakeOK pins snapshot --dir with a twin store:
// the file and its .sha256 exist, the line says SNAPSHOT OK with verified=checksum+twin,
// and --keep 2 leaves exactly two files after three takes.
func TestNovaSprintSnapshotCoverTakeOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	snapsDir := filepath.Join(dir, "snaps")
	_ = os.RemoveAll(snapsDir) // start clean

	// First take
	code, out, errs := twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" --keep 2")
	require.Equal(t, 0, code, "first take: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, out, "SNAPSHOT OK")
	assert.Contains(t, out, "verified=checksum+twin")
	assert.Contains(t, out, "keep=2")

	files, err := os.ReadDir(snapsDir)
	require.NoError(t, err)
	var snapFiles []string
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".rdb" {
			snapFiles = append(snapFiles, f.Name())
		}
	}
	require.Len(t, snapFiles, 1, "expected one snapshot file after first take")
	shaFile := filepath.Join(snapsDir, snapFiles[0]+".sha256")
	_, err = os.Stat(shaFile)
	require.NoError(t, err, "sha256 sidecar should exist")

	// Second take (advance clock by building new app)
	code, out, errs = twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" --keep 2")
	require.Equal(t, 0, code, "second take: exit %d\n%s%s", code, out, errs)

	files, err = os.ReadDir(snapsDir)
	require.NoError(t, err)
	snapFiles = nil
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".rdb" {
			snapFiles = append(snapFiles, f.Name())
		}
	}
	require.Len(t, snapFiles, 2, "expected two snapshot files after second take")

	// Third take - should prune one
	code, out, errs = twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" --keep 2")
	require.Equal(t, 0, code, "third take: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, out, "pruned=1")

	files, err = os.ReadDir(snapsDir)
	require.NoError(t, err)
	snapFiles = nil
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".rdb" {
			snapFiles = append(snapFiles, f.Name())
		}
	}
	require.Len(t, snapFiles, 2, "expected two snapshot files after third take with --keep 2")
}

// TestNovaSprintSnapshotCoverJSON pins snapshot --dir --json: the line decodes as JSON
// with file, sha256, and restore fields.
func TestNovaSprintSnapshotCoverJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	snapsDir := filepath.Join(dir, "snaps")
	_ = os.RemoveAll(snapsDir)

	code, out, errs := twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" --json")
	require.Equal(t, 0, code, "json take: exit %d\n%s%s", code, out, errs)

	var got map[string]any
	err := json.Unmarshal([]byte(out), &got)
	require.NoError(t, err)
	assert.Contains(t, got, "file")
	assert.Contains(t, got, "sha256")
	assert.Contains(t, got, "restore")
}

// TestNovaSprintSnapshotCoverDirIsFile pins snapshot --dir with a regular file:
// exit 1, stderr says FAILED and cannot be made.
func TestNovaSprintSnapshotCoverDirIsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	regularFile := filepath.Join(dir, "notadirectory")
	require.NoError(t, os.WriteFile(regularFile, []byte("hello"), 0644))

	code, out, errs := twinProcess(t, file, "nova-sprint snapshot --dir "+regularFile+" --keep 2")
	assert.Equal(t, 1, code, "should exit 1 when --dir is a regular file")
	assert.Contains(t, errs, "FAILED")
	assert.Contains(t, errs, "cannot be made")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverRefusalNoDir pins snapshot with no --dir: exit 2.
func TestNovaSprintSnapshotCoverRefusalNoDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	code, out, _ := twinProcess(t, file, "nova-sprint snapshot --keep 2")
	assert.Equal(t, 2, code, "should exit 2 when --dir is missing")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverRefusalKeep0 pins snapshot --keep 0: exit 2.
func TestNovaSprintSnapshotCoverRefusalKeep0(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	snapsDir := filepath.Join(dir, "snaps")
	_ = os.RemoveAll(snapsDir)

	code, out, _ := twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" --keep 0")
	assert.Equal(t, 2, code, "should exit 2 when --keep is 0")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverRefusalEveryNegative pins snapshot --every -1s: exit 2.
func TestNovaSprintSnapshotCoverRefusalEveryNegative(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	snapsDir := filepath.Join(dir, "snaps")
	_ = os.RemoveAll(snapsDir)

	code, out, _ := twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" --every -1s")
	assert.Equal(t, 2, code, "should exit 2 when --every is negative")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverRefusalPosWord pins snapshot with a positional word: exit 2.
func TestNovaSprintSnapshotCoverRefusalPosWord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	snapsDir := filepath.Join(dir, "snaps")
	_ = os.RemoveAll(snapsDir)

	code, out, _ := twinProcess(t, file, "nova-sprint snapshot --dir "+snapsDir+" word")
	assert.Equal(t, 2, code, "should exit 2 when given a positional word")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverRefusalDrillWithDir pins --restore-drill with --dir: exit 2.
func TestNovaSprintSnapshotCoverRefusalDrillWithDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	snapsDir := filepath.Join(dir, "snaps")
	_ = os.RemoveAll(snapsDir)

	code, out, _ := twinProcess(t, file, "nova-sprint snapshot --restore-drill "+snapsDir+" --dir "+snapsDir)
	assert.Equal(t, 2, code, "should exit 2 when --restore-drill is combined with --dir")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverRefusalDrillMissingFile pins --restore-drill of a missing file: exit 2.
func TestNovaSprintSnapshotCoverRefusalDrillMissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}

	code, out, _ := twinProcess(t, file, "nova-sprint snapshot --restore-drill /nonexistent")
	assert.Equal(t, 2, code, "should exit 2 when --restore-drill file does not exist")
	assert.Empty(t, out)
}

// TestNovaSprintSnapshotCoverDrillJSON pins --restore-drill --json on a valid backup file:
// JSON has file, sha256, counts, and restore.integrity.
func TestNovaSprintSnapshotCoverDrillJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "backup.bak")
	var outb bytes.Buffer
	require.NoError(t, runBackup(context.Background(), rdbSource{}, store.RDBTwin{}, dest, false, &outb))

	code, out, _ := twinProcess(t, filepath.Join(dir, "sprint.twin"), "nova-sprint snapshot --restore-drill "+dest+" --json")
	require.Equal(t, 0, code, "drill json: exit %d\n%s", code, out)

	var got map[string]any
	err := json.Unmarshal([]byte(out), &got)
	require.NoError(t, err)
	assert.Contains(t, got, "file")
	assert.Contains(t, got, "sha256")
	assert.Contains(t, got, "counts")
	assert.Contains(t, got, "restore")
}
