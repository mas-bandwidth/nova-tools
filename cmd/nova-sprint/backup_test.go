package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backup (backup.go; docs/SPEC-SPRINT.md, sprint-backup-verb; the sprint backup was a
// hand procedure a child ran, a restore test and a secrets scan): the store is written to
// a file, restored into a twin and compared, and the file is scanned for secrets. These
// run on the in-memory twin with the test app's clock: no socket, no real time.

// backupTwin is a twin store holding one card.
func backupTwin(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: the work (s1)"))
	return ta
}

func TestBackupWritesRestoresAndScansTheStore(t *testing.T) {
	t.Parallel()
	ta := backupTwin(t)
	out := filepath.Join(t.TempDir(), "sprint.backup")
	line := ta.ok("backup --out " + out)
	assert.Contains(t, line, "BACKUP OK file="+out+" sha256=")
	assert.Contains(t, line, "cards=1 restored=twin compared=counts secrets=0")

	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a backup is the sprint's whole record: the owner reads it")
	side, err := os.ReadFile(out + ".sha256")
	require.NoError(t, err)
	assert.Contains(t, string(side), "  sprint.backup\n")

	var got struct {
		File    string
		SHA256  string
		Bytes   int
		Cards   int
		Secrets int
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("backup --out "+out+".2 --json")), &got))
	assert.Equal(t, 1, got.Cards)
	assert.Zero(t, got.Secrets)
	assert.Positive(t, got.Bytes)
}

func TestBackupRefusesAFileHoldingASecretAndLeavesNone(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const token = "ghp_0123456789abcdefghijABCDEFGHIJ012345"
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "s1: the work (s1) token "+token))
	out := filepath.Join(t.TempDir(), "sprint.backup")
	code, so, se := ta.do("backup --out " + out)
	assert.Equal(t, 1, code)
	assert.Contains(t, se, "BACKUP FAILED")
	assert.Regexp(t, `secrets=[1-9]`, se, "each place the value is held counts")
	assert.NotContains(t, so+se, token, "the scan names how many, never the value")
	assert.NoFileExists(t, out, "a backup that holds a secret is not left lying around")
	assert.NoFileExists(t, out+".sha256")
}

func TestBackupRefusals(t *testing.T) {
	t.Parallel()
	ta := backupTwin(t)
	dir := t.TempDir()
	have := filepath.Join(dir, "have")
	require.NoError(t, os.WriteFile(have, []byte("keep"), 0o600))
	for name, c := range map[string]struct{ line, want string }{
		"no out":       {"backup", "--out <file>"},
		"extra words":  {"backup --out " + filepath.Join(dir, "x") + " now", "takes no words"},
		"existing out": {"backup --out " + have, "already exists"},
		"no directory": {"backup --out " + filepath.Join(dir, "no", "such", "x"), "cannot be written"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, se := ta.do(c.line)
			assert.NotZero(t, code)
			assert.Contains(t, se, c.want)
			assert.Contains(t, se, "run: nova-sprint backup", "a refusal carries its remedy")
		})
	}
	b, err := os.ReadFile(have)
	require.NoError(t, err)
	assert.Equal(t, "keep", strings.TrimSpace(string(b)), "an existing file is never overwritten")
}
