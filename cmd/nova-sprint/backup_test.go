package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint backup is one verb, not a hand procedure (SPEC-SPRINT,
// sprint-backup-verb): the store is written to a file, restored into a twin
// and compared, and the file is scanned for secrets. The run is on the twin
// store with the test clock: no socket, no real time.
func TestTheSprintBackupWritesRestoresComparesAndScansTheStore(t *testing.T) {
	t.Parallel()
	t.Run("a clean store is a verified backup", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		ta.ok("add --stream s1 --count 3")
		dir := t.TempDir()
		out := ta.ok("backup --dir " + dir)
		assert.Contains(t, out, "BACKUP OK file="+dir)
		assert.Contains(t, out, "restored=twin compared=counts secrets=0")
		files, err := filepath.Glob(filepath.Join(dir, "snapshot-*.rdb"))
		require.NoError(t, err)
		assert.Len(t, files, 1, "one backup file, written with its checksum beside it")
		assert.FileExists(t, files[0]+".sha256")
	})
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	t.Run("a card holding a secret fails the backup and leaves no file", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		ta.ok("add --stream s1 --count 1 --one --brief '" + strings.ReplaceAll(passingBrief("deploy with token "+secret), "'", "") + "'")
		dir := t.TempDir()
		code, out, errs := ta.do("backup --dir " + dir)
		assert.Equal(t, 1, code, "%s%s", out, errs)
		assert.Contains(t, errs, "backup FAILED")
		assert.Contains(t, errs, "secret-shaped")
		assert.NotContains(t, out+errs, secret, "the value is never printed")
		left, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, left, "the directory holds only clean verified backups")
	})
	t.Run("a refusal names the remedy", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		code, _, errs := ta.do("backup")
		assert.Equal(t, 2, code)
		assert.True(t, strings.Contains(errs, "run: nova-sprint backup --dir"), errs)
	})
}
