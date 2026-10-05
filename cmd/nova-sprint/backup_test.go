package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// backupTwin makes a twin file holding a small sprint and returns its path.
func backupTwin(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1",
		"nova-sprint add --stream s1 --count 1 --one",
	} {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	return file
}

// backupRun runs backup over the twin on a fixed clock, as a shell would.
func backupRun(t *testing.T, file string, args ...string) (int, string, string) {
	t.Helper()
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	a.now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	var out, errb bytes.Buffer
	code := a.run(append([]string{"backup"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// The backup verb writes the store to a file, restores the file into a twin
// and finds the twin equal, and scans the file for secrets (SPEC-SPRINT,
// sprint-backup-verb): on a twin store, with no socket and a fixed clock.
func TestTheSprintBackupWritesRestoresAndScansTheStore(t *testing.T) {
	t.Parallel()
	file := backupTwin(t)
	dir := filepath.Join(t.TempDir(), "backups")
	code, out, errs := backupRun(t, file, "--dir", dir)
	require.Equal(t, 0, code, "backup: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, out, "BACKUP OK", "the success line leads with the status word: %q", out)
	assert.Contains(t, out, "restored=equal secrets=none", "the line says the restore matched and the scan was clean: %q", out)
	files, err := store.SnapshotFiles(dir)
	require.NoError(t, err)
	require.Len(t, files, 1, "one backup file in %s", dir)
	assert.Contains(t, files[0], "20261004T120000Z", "the name carries the injected clock: %s", files[0])
	doc, err := os.ReadFile(filepath.Join(dir, files[0]))
	require.NoError(t, err)
	twin := store.NewMem()
	require.NoError(t, twin.Restore(doc), "the file restores into a twin")
	again, err := twin.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(doc), string(again), "the restored twin snapshots to the file's bytes")
	// the twin file the store lives in is untouched by a backup
	before, _ := os.ReadFile(file)
	_, _, _ = backupRun(t, file, "--dir", dir)
	after, _ := os.ReadFile(file)
	assert.Equal(t, string(before), string(after), "a backup changes no card and no row of the store")
}

// A store holding a secret is not backed up: nothing is written, the older
// backups stay, the line names the kind and never the value.
func TestTheSprintBackupRefusesAStoreHoldingASecret(t *testing.T) {
	t.Parallel()
	file := backupTwin(t)
	dir := filepath.Join(t.TempDir(), "backups")
	code, out, errs := backupRun(t, file, "--dir", dir)
	require.Equal(t, 0, code, "the clean backup first: exit %d\n%s%s", code, out, errs)
	// built in two parts so this file holds no token-shaped text itself
	secret := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	doc, err := os.ReadFile(file)
	require.NoError(t, err)
	m := store.NewMem()
	require.NoError(t, m.Restore(doc))
	require.NoError(t, m.SetKey(context.Background(), "note", "token "+secret))
	doc, err = m.Snapshot()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, doc, 0o600))

	a := newApp(func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}[k]
	})
	defer a.close()
	a.now = func() time.Time { return time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC) }
	var o, e bytes.Buffer
	code = a.run([]string{"backup", "--dir", dir}, &o, &e)
	assert.Equal(t, 1, code, "a secret in the store: exit %d, stdout %q, stderr %q", code, o.String(), e.String())
	assert.Contains(t, e.String(), "backup FAILED", "the failure leads with its status word: %q", e.String())
	assert.Contains(t, e.String(), "GitHub token", "the line names the kind of secret: %q", e.String())
	assert.NotContains(t, e.String()+o.String(), secret, "the secret's value is never printed")
	files, err := store.SnapshotFiles(dir)
	require.NoError(t, err)
	assert.Len(t, files, 1, "the refused backup wrote no file and the older backup stays: %v", files)
}

// A call with no directory, or a stray word, is refused with the remedy.
func TestTheSprintBackupRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	file := backupTwin(t)
	for name, args := range map[string][]string{"no dir": nil, "stray word": {"--dir", t.TempDir(), "extra"}, "keep zero": {"--dir", t.TempDir(), "--keep", "0"}} {
		code, out, errs := backupRun(t, file, args...)
		assert.Equal(t, 2, code, "%s: exit %d, stdout %q", name, code, out)
		assert.Contains(t, errs, "backup REFUSED", "%s: %q", name, errs)
		assert.Contains(t, errs, "run: nova-sprint backup", "%s: the remedy is a command: %q", name, errs)
	}
}
