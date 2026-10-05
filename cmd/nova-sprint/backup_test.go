package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seededBackupApp is a twin store with a sprint in it, the app's clock fixed
// and no socket: the whole backup runs in this process (SPEC-SPRINT, backup).
func seededBackupApp(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 2")
	return ta
}

// The sprint backup is one verb: the store is written to a file, the file is
// restored into a twin and compared, and the file is scanned for secrets. A
// clean store reports every step done and leaves the file and its checksum.
func TestBackupWritesRestoresComparesAndScansACleanStore(t *testing.T) {
	t.Parallel()
	ta := seededBackupApp(t)
	dir := filepath.Join(t.TempDir(), "backups")
	code, out, errs := ta.do("backup --dir " + dir)
	require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
	for _, want := range []string{"BACKUP OK ", "restored=twin", "compared=counts", "secrets=0", "file=" + dir} {
		assert.Contains(t, out, want, "the success line names every step it did")
	}
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2, "one backup file and its .sha256 beside it")
}

// A secret in the store is found in the file: the verb fails, names the kind
// and the count, never prints the secret, and leaves the file (it is a faithful
// copy of the store; the caller decides) with the next step in the line.
func TestBackupFailsAndNamesASecretInTheStoreWithoutPrintingIt(t *testing.T) {
	t.Parallel()
	ta := seededBackupApp(t)
	token := "ghp_" + strings.Repeat("a1B2", 9) // built at run time: this file holds no token
	ta.ok("hold m1 --reason " + token)
	dir := filepath.Join(t.TempDir(), "backups")
	code, out, errs := ta.do("backup --dir " + dir)
	assert.Equal(t, 1, code, "a backup holding a secret is a failure: stdout %q stderr %q", out, errs)
	all := out + errs
	assert.Contains(t, all, "backup FAILED", "the failure says it failed")
	assert.Regexp(t, `github-token=[1-9]`, all, "the kind and its count are named (the store keeps the reason in more than one row)")
	assert.NotContains(t, all, token, "the secret itself is never printed")
	assert.Contains(t, all, "run: ", "the failure names the remedy")
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2, "the file stays for the caller to deal with")
}

// --dry-run prints the plan and writes nothing: no directory is made.
func TestBackupDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ta := seededBackupApp(t)
	dir := filepath.Join(t.TempDir(), "backups")
	code, out, errs := ta.do("backup --dir " + dir + " --dry-run")
	require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
	assert.Contains(t, out, "BACKUP OK dry-run", "the line says it is a plan")
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "a dry run makes no directory (stat: %v)", err)
}

// A call with no --dir is refused with the remedy, and --json prints the one
// result value of a clean run.
func TestBackupRefusesNoDirAndPrintsJSON(t *testing.T) {
	t.Parallel()
	ta := seededBackupApp(t)
	code, _, errs := ta.do("backup")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "backup REFUSED: wants --dir", "the refusal names the missing flag")
	assert.Contains(t, errs, "; run: nova-sprint backup --dir", "the refusal carries a command that runs")

	var got struct {
		File    string         `json:"file"`
		SHA256  string         `json:"sha256"`
		Secrets map[string]int `json:"secrets"`
	}
	ta.json("backup --dir "+filepath.Join(t.TempDir(), "b"), &got)
	assert.NotEmpty(t, got.File)
	assert.Len(t, got.SHA256, 64)
	assert.Empty(t, got.Secrets)
}
