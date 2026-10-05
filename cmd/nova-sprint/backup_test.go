package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint backup is one verb (docs/SPEC-SPRINT.md, sprint-backup-verb): on
// the twin store, with the app's clock and no socket, it writes the store to a
// file, restores it into a twin and compares, and scans the file for secrets.
func TestBackupWritesRestoresComparesAndScansTheTwinStore(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	dir := filepath.Join(t.TempDir(), "backups")

	ta.ok("backup --dir " + dir + " --keep 2")
	code, out, errs := ta.do("backup --dir " + dir + " --keep 2")
	require.Equal(t, 0, code, "backup: stdout %q, stderr %q", out, errs)
	assert.Contains(t, out, "BACKUP OK file="+dir, "backup: %q", out)
	assert.Contains(t, out, "restored=twin compared=counts secrets=none", "backup: %q", out)
	assert.Contains(t, out, "cards=2", "the twin held the store's two cards: %q", out)

	t.Run("the file and its checksum are on disk", func(t *testing.T) {
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, ents, 4, "two backups, each with its sha256")
	})

	t.Run("the directory is pruned to keep after a newer one passes", func(t *testing.T) {
		ta.ok("backup --dir " + dir + " --keep 2")
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, ents, 4, "keep 2 leaves two backups with their sums")
	})

	t.Run("a credential in the store fails the backup, removes the copy and keeps the older ones", func(t *testing.T) {
		before, err := os.ReadDir(dir)
		require.NoError(t, err)
		ta.ok("hold m1 --reason 'token " + "gh" + "p_" + "abcdefghijklmnopqrstuvwxyz0123456789" + " pasted'")
		code, out, errs := ta.do("backup --dir " + dir + " --keep 2")
		assert.Equal(t, 1, code, "backup over a store holding a token: stdout %q, stderr %q", out, errs)
		assert.Regexp(t, `github token x[1-9]`, errs, "the kind and a count are named")
		assert.NotContains(t, errs+out, "abcdefghijklmnopqrstuvwxyz0123456789", "the value is never printed")
		after, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Equal(t, len(before), len(after), "the failed copy is removed and nothing older is pruned")
	})
}

// A backup asks for its directory, and the refusal names the command to run.
func TestBackupRefusesWithoutADirectory(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	code, out, errs := ta.do("backup")
	assert.Equal(t, 2, code, "backup with no --dir: stdout %q, stderr %q", out, errs)
	assert.Contains(t, errs, "nova-sprint backup --dir backups --keep 7", "the refusal names the run: %q", errs)
}

// secretKinds names each credential shape once with its count, and reads a
// clean document as clean.
func TestSecretKindsCountsEachShapeAndPassesACleanDocument(t *testing.T) {
	t.Parallel()
	assert.Empty(t, secretKinds([]byte(`{"cards":{"s1-1":{"brief":"fix the cache key"}}}`)))
	got := secretKinds([]byte("-----BEGIN OPENSSH " + "PRIVATE KEY-----\nAKIA" + "ABCDEFGHIJKLMNOP\nAKIA" + "ABCDEFGHIJKLMNOQ\npostgres://u:" + "hunter2@db/x"))
	assert.Equal(t, map[string]int{"private key block": 1, "aws access key id": 2, "password in a url": 1}, got)
}
