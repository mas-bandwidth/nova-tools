package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backupTwin runs one backup over a twin file with a fixed clock: no socket and
// no real time.
func backupTwin(t *testing.T, file string, args ...string) (int, string, string) {
	t.Helper()
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	a.now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	var out, errb strings.Builder
	code := a.run(append([]string{"backup"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// The backup writes the twin store to a file, restores it into a twin and
// compares, and scans it for credentials: a clean store passes with the counts
// restored equal, and a store holding a credential fails naming its kind and
// never the value (SPEC-SPRINT, sprint-backup-verb).
func TestTheBackupRestoresTheStoreAndScansItForSecrets(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range twinSteps[:3] {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	t.Run("a clean store is backed up, restored and compared", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		code, out, errs := backupTwin(t, file, "--dir", dir)
		require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
		assert.Contains(t, out, "BACKUP OK file="+filepath.Join(dir, "snapshot-20261004T120000Z.rdb"))
		assert.Contains(t, out, "restored=keys=")
		assert.Contains(t, out, "secrets=none")
		assert.FileExists(t, filepath.Join(dir, "snapshot-20261004T120000Z.rdb.sha256"))
	})
	t.Run("a credential in the store fails the backup and is never printed", func(t *testing.T) {
		t.Parallel()
		tok := "gh" + "p_" + strings.Repeat("a1B2", 9)
		leaky := filepath.Join(t.TempDir(), "leaky.twin")
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(leaky, []byte(strings.Replace(string(b), `"seq"`, `"kv":{"leak":"`+tok+`"},"seq"`, 1)), 0o644))
		code, out, errs := backupTwin(t, leaky, "--dir", t.TempDir(), "--json")
		assert.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
		assert.Contains(t, errs, "github-token=1")
		assert.NotContains(t, out+errs, tok, "a finding names its kind, never the value")
	})
	t.Run("a refusal names the remedy", func(t *testing.T) {
		t.Parallel()
		code, _, errs := backupTwin(t, file)
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "run: nova-sprint backup --dir")
	})
}

func TestTheSecretScanNamesKindsAndCounts(t *testing.T) {
	t.Parallel()
	aws := "AK" + "IA" + strings.Repeat("A1", 8)
	got := scanSecrets([]byte("x " + aws + " y " + aws + " -----BEGIN OPENSSH PRIVATE KEY-----"))
	assert.Equal(t, []secretFinding{{"aws-access-key", 2}, {"private-key", 1}}, got)
	assert.Empty(t, scanSecrets([]byte("a card brief with no credential")))
}
