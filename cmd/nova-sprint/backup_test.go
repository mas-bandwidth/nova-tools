package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seededTwin is a twin file with a card in it, made by the help's own first
// two steps.
func seededTwin(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range twinSteps[:2] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	return file
}

// The backup writes the store to a file, restores it into a twin, compares and
// scans it: on the twin store, with no socket and the injected clock, it is
// OK, the file is the store's document, and the store is not changed.
func TestTheSprintBackupWritesRestoresComparesAndScansTheStore(t *testing.T) {
	t.Parallel()
	twin := seededTwin(t)
	// a twin file is rewritten by each command, which normalises empty fields:
	// one read settles it, and the backup is held to that
	code, stdout, stderr := twinProcess(t, twin, "nova-sprint where")
	require.Equal(t, 0, code, "where: exit %d\n%s%s", code, stdout, stderr)
	before, err := os.ReadFile(twin)
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "backups", "b.json")
	code, stdout, stderr = twinProcess(t, twin, "nova-sprint backup --file "+out)
	require.Equal(t, 0, code, "backup: exit %d\n%s%s", code, stdout, stderr)
	assert.Contains(t, stdout, "BACKUP OK file="+out, "the line names the file")
	assert.Contains(t, stdout, "verified=checksum+twin+secrets")
	assert.Contains(t, stdout, "cards=1", "the card is counted in the twin")
	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a backup is the owner's alone")
	after, err := os.ReadFile(twin)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the backup reads the store and never changes it")
}

// A store holding what has the shape of a secret is refused: the line names
// the shape and the line, never the text, and the file is removed.
func TestTheSprintBackupRefusesAFileHoldingASecretAndNamesNoValue(t *testing.T) {
	t.Parallel()
	twin := seededTwin(t)
	secret := "ghp_" + strings.Repeat("a1", 15)
	doc, err := os.ReadFile(twin)
	require.NoError(t, err)
	require.Contains(t, string(doc), "s1-1")
	require.NoError(t, os.WriteFile(twin, []byte(strings.ReplaceAll(string(doc), "s1-1", "s1-1-"+secret)), 0o644))
	out := filepath.Join(t.TempDir(), "b.json")
	code, stdout, stderr := twinProcess(t, twin, "nova-sprint backup --file "+out)
	assert.Equal(t, 1, code, "backup over a secret: exit %d\n%s%s", code, stdout, stderr)
	assert.Contains(t, stderr, "forge-token at line", "the shape and line are named")
	assert.NotContains(t, stdout+stderr, secret, "the value is never printed")
	_, err = os.Stat(out)
	assert.True(t, os.IsNotExist(err), "the file with a secret is removed")
}

// An existing file is never overwritten, and a missing --file is a usage refusal.
func TestTheSprintBackupRefusesAnExistingFileAndAMissingFile(t *testing.T) {
	t.Parallel()
	twin := seededTwin(t)
	keep := filepath.Join(t.TempDir(), "keep.json")
	require.NoError(t, os.WriteFile(keep, []byte("mine\n"), 0o644))
	code, _, stderr := twinProcess(t, twin, "nova-sprint backup --file "+keep)
	assert.Equal(t, 2, code, "an existing file: exit %d, stderr %q", code, stderr)
	got, _ := os.ReadFile(keep)
	assert.Equal(t, "mine\n", string(got), "the existing file changed")
	code, _, stderr = twinProcess(t, twin, "nova-sprint backup")
	assert.Equal(t, 2, code, "no --file: exit %d, stderr %q", code, stderr)
	assert.Contains(t, stderr, "--file")
}

// The scan names shapes by line and holds nothing back for clean text.
func TestScanSecretsNamesShapeAndLineOnly(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	b.WriteString("clean\n-----BEGIN " + "OPENSSH PRIVATE KEY-----\nAKIA" + strings.Repeat("A", 16) + "\n")
	hits := scanSecrets(b.Bytes())
	assert.Equal(t, []secretHit{{"pem-private-key", 2}, {"cloud-access-key-id", 3}}, hits)
	assert.Empty(t, scanSecrets([]byte("a card brief with no key in it\n")))
}
