package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The backup verb writes the store to a file, restores it into a twin,
// compares, and scans the file for secrets; on a twin store, with the app's
// clock and no socket (SPEC-SPRINT, sprint-backup-verb).
func TestBackupWritesRestoresComparesAndScansTheStore(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 2")

	t.Run("a clean store is backed up, verified and kept to --keep", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "backups")
		var line string
		for i := 0; i < 3; i++ {
			ta.mu.Lock()
			ta.now = ta.now.Add(1e9)
			ta.mu.Unlock()
			line = ta.ok("backup --dir " + dir + " --keep 2")
		}
		assert.True(t, strings.HasPrefix(line, "BACKUP OK file="), "the success line leads with BACKUP OK: %q", line)
		for _, want := range []string{"sha256=", "restored=twin", "compared=counts", "scanned=secrets found=0", "pruned=1", "keep=2"} {
			assert.Contains(t, line, want)
		}
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, ents, 4, "two backups kept, each with its checksum beside it")
	})

	t.Run("a secret in the store fails the backup, removes the file and never prints the value", func(t *testing.T) {
		t.Parallel()
		sb := newTestApp(t)
		sb.ok("init --readers reader-a --members m1")
		sb.ok("add --stream s1 --count 1 --one")
		secret := "ghp_" + strings.Repeat("aB3dE", 6)
		sb.ok("drop s1-1 --reason " + secret)
		dir := t.TempDir()
		code, out, errs := sb.do("backup --dir " + dir)
		assert.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
		assert.Contains(t, errs, "backup FAILED")
		assert.Contains(t, errs, "secret-shaped value(s) found")
		assert.NotContains(t, out+errs, secret, "a refusal names the count, never the value")
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, ents, "the failed copy is removed")
	})

	t.Run("a missing --dir is refused with its remedy", func(t *testing.T) {
		t.Parallel()
		code, _, errs := ta.do("backup")
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "run: nova-sprint backup --dir")
	})
}
