package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// planted is a GitHub-token-shaped value for the scan to find; the tests never
// print it, and neither does the verb.
const planted = "ghp_" + "0123456789abcdefghijABCDEFGHIJ"

// twinWithCard is a twin file holding a started card, made by the help's own
// first steps.
func twinWithCard(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range twinSteps[:4] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, out, errs)
	}
	return file
}

// backup writes the store to a file, restores the file into a twin, compares
// it with the store and scans it for secrets: one BACKUP OK line, the file
// private, and the file restores to the very document the store holds (SPEC-SPRINT,
// sprint-backup-verb). No socket and no clock: a twin store.
func TestBackupWritesRestoresAndComparesTheStoreOnATwin(t *testing.T) {
	t.Parallel()
	twin := twinWithCard(t)
	out := filepath.Join(t.TempDir(), "backup.json")

	code, stdout, stderr := twinProcess(t, twin, "nova-sprint backup --file "+out)
	require.Equal(t, 0, code, "%s%s", stdout, stderr)
	assert.Regexp(t, `^BACKUP OK file=\S+ sha256=[0-9a-f]{64} bytes=\d+ keys=\d+ cards=1 restored=twin compared=document secrets=none\n$`, stdout)

	doc, err := os.ReadFile(out)
	require.NoError(t, err)
	after, err := os.ReadFile(twin)
	require.NoError(t, err)
	assert.Equal(t, after, doc, "the backup is the store's document, byte for byte (the twin's own beat at the verb is in both)")
	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a backup is private to its owner")
	back := store.NewMem()
	require.NoError(t, back.Restore(doc))
	again, err := back.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, doc, again, "the file restores to the document it holds")

	left, _ := os.ReadDir(filepath.Dir(out))
	assert.Len(t, left, 1, "the backup leaves its one file and no temporary file")
}

// A secret-shaped value in the store fails the backup: it names the lines, never
// the value, and keeps the file private and marked not clean.
func TestBackupFailsWhenTheFileHoldsASecretAndNeverPrintsIt(t *testing.T) {
	t.Parallel()
	twin := filepath.Join(t.TempDir(), "sprint.twin")
	m := store.NewMem()
	require.NoError(t, m.SetKey(context.Background(), "note", planted))
	doc, err := m.Snapshot()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(twin, doc, 0o600))
	out := filepath.Join(t.TempDir(), "backup.json")

	code, stdout, stderr := twinProcess(t, twin, "nova-sprint backup --file "+out)
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout+stderr, planted, "a finding never prints the value")
	assert.Contains(t, stderr, "backup FAILED: the file holds secret-shaped text at line ")
	assert.Contains(t, stderr, "the file is kept private and is not a clean backup")
	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// A backup never overwrites a file, and names every problem at once.
func TestBackupRefusesAFileThatIsThereAndAMissingFile(t *testing.T) {
	t.Parallel()
	twin := twinWithCard(t)
	there := filepath.Join(t.TempDir(), "there.json")
	require.NoError(t, os.WriteFile(there, []byte("keep me"), 0o600))

	code, _, stderr := twinProcess(t, twin, "nova-sprint backup --file "+there)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "nova-sprint backup REFUSED: ")
	assert.Contains(t, stderr, there+" is there")
	assert.Contains(t, stderr, "; run: nova-sprint backup --file")
	got, _ := os.ReadFile(there)
	assert.Equal(t, "keep me", string(got), "the file that was there is left as it was")

	code, _, stderr = twinProcess(t, twin, "nova-sprint backup")
	assert.Equal(t, 2, code)
	assert.True(t, strings.Contains(stderr, "--file"), stderr)
}

// --dry-run proves and scans the snapshot and writes no file.
func TestBackupDryRunWritesNoFile(t *testing.T) {
	t.Parallel()
	twin := twinWithCard(t)
	out := filepath.Join(t.TempDir(), "backup.json")

	code, stdout, stderr := twinProcess(t, twin, "nova-sprint backup --dry-run --file "+out)
	require.Equal(t, 0, code, "%s%s", stdout, stderr)
	assert.Contains(t, stdout, "BACKUP OK (dry run, no file written) ")
	assert.NoFileExists(t, out)
}
