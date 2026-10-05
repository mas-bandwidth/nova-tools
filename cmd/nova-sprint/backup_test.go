package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// twinWithKey is a twin file holding one key set to value, as a store a worker
// wrote a credential into would.
func twinWithKey(t *testing.T, path, value string) {
	t.Helper()
	m := store.NewMem()
	require.NoError(t, m.SetKey(context.Background(), "note", value))
	doc, err := m.Snapshot()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, doc, 0o600))
}

// backup writes the store to a file, restores the file into a twin, compares
// and scans it, all on a twin store with no socket and no clock: the file is
// the store's document, owner-only, and the line says what was proved.
func TestBackupWritesRestoresComparesAndScansATwinStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	dest := filepath.Join(dir, "backup.bak")
	code, out, errs := twinProcess(t, file, "nova-sprint backup --file "+dest)
	require.Equal(t, 0, code, "backup: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, out, "BACKUP OK file="+dest)
	assert.Contains(t, out, "restored=twin compared=document+counts secrets=none")
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	want, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got), "the backup is the store's document")
	fi, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "a backup is owner-only")
	ents, _ := os.ReadDir(dir)
	assert.Len(t, ents, 2, "the twin and the backup, no temporary file")
}

// A store holding a credential is refused: the file is removed and the
// refusal names the line, never the value.
func TestBackupRefusesAndRemovesAFileHoldingASecret(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	secret := "ghp_" + strings.Repeat("a1B2c3", 5)
	twinWithKey(t, file, secret)
	dest := filepath.Join(dir, "backup.bak")
	code, out, errs := twinProcess(t, file, "nova-sprint backup --file "+dest)
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "backup FAILED")
	assert.Contains(t, errs, "secret")
	assert.NotContains(t, out+errs, secret, "the value is never printed")
	_, err := os.Stat(dest)
	assert.True(t, os.IsNotExist(err), "a backup holding a secret is removed")
}

// Words the verb refuses, each with a remedy: no --file, a file already there
// (never overwritten), a word it does not take.
func TestBackupRefusesWhatItCannotDoSafely(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	code, _, errs := twinProcess(t, file, "nova-sprint init --readers reader-a,reader-b --members m1")
	require.Equal(t, 0, code, errs)
	taken := filepath.Join(dir, "taken.bak")
	require.NoError(t, os.WriteFile(taken, []byte("keep me"), 0o600))
	for name, c := range map[string]struct {
		line, word string
		code       int
	}{
		"no file": {"nova-sprint backup", "REFUSED", 2},
		"exists":  {"nova-sprint backup --file " + taken, "FAILED", 1},
		"extra":   {"nova-sprint backup --file " + filepath.Join(dir, "x.bak") + " extra", "REFUSED", 2},
	} {
		code, out, errs := twinProcess(t, file, c.line)
		assert.Equal(t, c.code, code, "%s: %s%s", name, out, errs)
		assert.Contains(t, errs, c.word, name)
		assert.Contains(t, errs, "; run: nova-sprint backup", name)
	}
	b, _ := os.ReadFile(taken)
	assert.Equal(t, "keep me", string(b), "an existing file is left as it is")
}

// failTwin loads the document but counts it other than the store did.
type failTwin struct{ c store.SnapshotCounts }

func (f failTwin) Load([]byte) (store.SnapshotCounts, error) { return f.c, nil }

// A restore that counts other than the store held, or that cannot load the
// file, fails the backup and removes the file.
func TestBackupFailsAndRemovesTheFileWhenTheRestoreDiffers(t *testing.T) {
	t.Parallel()
	m := store.NewMem()
	require.NoError(t, m.SetKey(context.Background(), "k", "v"))
	for name, tw := range map[string]store.SnapshotTwin{
		"counts": failTwin{store.SnapshotCounts{Keys: 99, Cards: 0}},
		"load":   store.RDBTwin{},
	} {
		dest := filepath.Join(t.TempDir(), "b.bak")
		var out bytes.Buffer
		err := runBackup(context.Background(), store.MemSource{M: m}, tw, dest, false, &out)
		require.Error(t, err, name)
		_, serr := os.Stat(dest)
		assert.True(t, os.IsNotExist(serr), "%s: the failed backup is removed", name)
	}
}

func TestBackupDryRunVerifiesAndWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	dest := filepath.Join(dir, "backup.bak")
	code, out, errs := twinProcess(t, file, "nova-sprint backup --file "+dest+" --dry-run")
	require.Equal(t, 0, code, "backup: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, out, "BACKUP DRY-RUN file="+dest)
	assert.Contains(t, out, "nothing was written")
	_, err := os.Stat(dest)
	assert.True(t, os.IsNotExist(err), "dry run writes no file")
}
