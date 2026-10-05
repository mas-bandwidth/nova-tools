package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backup is the sprint's backup in one verb (docs/SPEC-SPRINT.md, "backup"):
// the store is written to a file, restored into a twin and compared, and the
// file is scanned for secrets, on the twin store with no socket and the test's
// own clock. A file that holds a secret is removed and the older backup stays.
func TestBackupWritesRestoresAndScansTheStoreWithoutASocket(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	ta.ok("add --stream s1 --count 2 --one")
	dir := filepath.Join(t.TempDir(), "backups")

	out := ta.ok("backup --dir " + dir)
	assert.Regexp(t, `^BACKUP OK file=\S+snapshot-\S+\.rdb sha256=[0-9a-f]{64} bytes=\d+ keys=\d+ cards=\d+ verified=checksum\+twin\+secrets pruned=0 keep=7\n$`, out)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2, "the file and its checksum")

	t.Run("a second backup is pruned to --keep", func(t *testing.T) {
		ta.ok("backup --dir " + dir + " --keep 1")
		files, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, files, 2, "one backup and its checksum stay")
	})
	t.Run("a secret in the store refuses the backup and removes its file", func(t *testing.T) {
		before, err := os.ReadDir(dir)
		require.NoError(t, err)
		secret := "AGE-SECRET-" + "KEY-1" + strings.Repeat("Q", 58)
		require.NoError(t, ta.m.SetKey(context.Background(), "leak", secret))
		code, out, errs := ta.do("backup --dir " + dir)
		assert.Equal(t, 1, code)
		assert.Empty(t, out)
		assert.Contains(t, errs, "age secret key")
		assert.NotContains(t, errs, secret, "a refusal names the kind, never the value")
		after, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Equal(t, len(before), len(after), "the failed file and its checksum are gone and the older backup stays")
	})
	t.Run("no directory is a refusal with its remedy", func(t *testing.T) {
		code, _, errs := ta.do("backup")
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "run: nova-sprint backup --dir")
	})
}

// secretKinds names each shape of credential once and never matches plain text.
func TestSecretKindsNamesTheShapesAndOnlyThose(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"private key block":      "-----BEGIN OPENSSH " + "PRIVATE KEY-----",
		"github token":           "ghp_" + strings.Repeat("a", 36),
		"cloud access key":       "AKIA" + strings.Repeat("A", 16),
		"slack token":            "xoxb-" + "1234567890-abcdef",
		"password in an address": "redis://:hunter2@host:6379",
		"api key":                "sk-ant-" + strings.Repeat("x", 40),
	}
	for kind, text := range cases {
		assert.Equal(t, []string{kind}, secretKinds([]byte("brief: "+text+" end")), kind)
	}
	assert.Empty(t, secretKinds([]byte("redis://host:6379 and a card s1-1 with the word token")))
}
