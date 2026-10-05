package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// backup writes the store to a file, restores it into a twin and compares, and
// scans it for secrets (docs/SPEC-SPRINT.md, backup-verb): on the test's twin
// store, with no socket and a clock the test owns.
func TestBackupWritesAVerifiedFileOfTheStoreAndNothingMoreThanTheFile(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2 --one")
	file := filepath.Join(t.TempDir(), "sprint.backup")

	var got backupResult
	require.NoError(t, json.Unmarshal([]byte(ta.ok("backup --file "+file+" --json")), &got))
	doc, err := os.ReadFile(file)
	require.NoError(t, err)
	sum := sha256.Sum256(doc)
	live, err := ta.m.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(live), string(doc), "the file is the store's document")
	assert.Equal(t, hex.EncodeToString(sum[:]), got.SHA256)
	assert.Equal(t, len(doc), got.Bytes)
	assert.Equal(t, 2, got.Counts.Cards)
	assert.Equal(t, "checksum+twin+compare", got.Verify)
	assert.Equal(t, "none", got.Secrets)

	assert.Contains(t, ta.ok("backup --file "+file), "BACKUP OK file="+file)
	entries, _ := os.ReadDir(filepath.Dir(file))
	assert.Len(t, entries, 1, "the backup leaves its one file and no temporary file")
}

// A store holding a credential is not backed up: the file is removed, and the
// refusal names the line and the pattern, never the value.
func TestBackupRefusesAFileThatHoldsASecretAndRemovesIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	secret := "AKIA" + strings.Repeat("Q7", 8)
	ta.ok("hold m1 --reason 'rotated " + secret + "'")
	file := filepath.Join(t.TempDir(), "sprint.backup")

	code, out, errs := ta.do("backup --file " + file)
	assert.Equal(t, 1, code, out+errs)
	assert.Contains(t, errs, "AWS access key id at line ")
	assert.Contains(t, errs, "the file was removed")
	assert.NotContains(t, errs+out, secret, "a refusal never prints the value")
	_, err := os.Stat(file)
	assert.ErrorIs(t, err, os.ErrNotExist, "a file at --file is a verified one")
}

func TestBackupRefusesWithoutAFileAndWithWords(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	code, _, errs := ta.do("backup")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--file <path>")
	code, _, _ = ta.do("backup --file x extra")
	assert.Equal(t, 2, code)
}

// The restore compare catches a twin that restores other counts than the store
// held, and a file that is no snapshot; a document restores to a fixed point.
func TestBackupTakeCatchesAFileTheTwinDoesNotRestoreWhole(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	doc, err := ta.m.Snapshot()
	require.NoError(t, err)
	require.NoError(t, memRoundTrip(doc), "a store's document restores to a fixed point")

	file := filepath.Join(t.TempDir(), "b")
	_, err = backupTake(context.Background(), file, fixedSource{doc: doc, counts: store.SnapshotCounts{Keys: -1, Cards: 5}}, store.MemTwin{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "other counts")
	_, statErr := os.Stat(file)
	assert.ErrorIs(t, statErr, os.ErrNotExist)

	_, err = backupTake(context.Background(), file, fixedSource{doc: []byte("not a snapshot")}, store.MemTwin{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not restore")
	_, statErr = os.Stat(file)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestScanSecretsNamesPatternAndLineNeverTheValue(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"private key block":   "a\n-----BEGIN OPENSSH PRIVATE KEY-----\n",
		"GitHub token":        "x\ny\nghp_" + strings.Repeat("a1", 20),
		"API key (sk-)":       "key sk-" + strings.Repeat("Z9", 12),
		"password assignment": `{"password": "hunter2hunter2"}`,
	} {
		f := scanSecrets([]byte(doc))
		require.NotEmpty(t, f, name)
		assert.Equal(t, name, f[0].Pattern)
	}
	assert.Equal(t, 3, scanSecrets([]byte("x\ny\nghp_" + strings.Repeat("a1", 20)))[0].Line)
	assert.Empty(t, scanSecrets([]byte(`{"reason":"the cache trim","head":"9f3c2e1a"}`)))
}

// fixedSource is a snapshot source that gives a fixed document and counts.
type fixedSource struct {
	doc    []byte
	counts store.SnapshotCounts
}

func (f fixedSource) Save(context.Context) ([]byte, store.SnapshotCounts, error) {
	c := f.counts
	if c == (store.SnapshotCounts{}) {
		c = store.SnapshotCounts{Keys: -1, Cards: -1}
	}
	return f.doc, c, nil
}
