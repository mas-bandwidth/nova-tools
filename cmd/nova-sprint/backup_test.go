package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backup (backup.go; SPEC-SPRINT, sprint-backup-verb): the store is written to
// a file, restored into a twin and compared, and the file scanned for key
// shapes. The rig is the test app's in-memory twin: no socket, and the clock
// is the app's.

// A clean store backs up: the file is written, the twin restores it equal and
// the scan finds nothing.
func TestBackupWritesRestoresAndScansACleanStore(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	file := filepath.Join(t.TempDir(), "sprint.backup")
	code, out, errb := ta.do("backup --file " + file)
	require.Equal(t, 0, code, errb)
	assert.Contains(t, out, "BACKUP OK file="+file)
	assert.Contains(t, out, "restored=equal")
	assert.Contains(t, out, "secrets=none")
	assert.Contains(t, out, "cards=")
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.NotEmpty(t, b)
	snap, err := ta.m.Snapshot()
	require.NoError(t, err)
	assert.True(t, backupSame(snap, b), "the file is the store as it is")
	assert.False(t, backupSame(snap, append(append([]byte(nil), b...), 'x')), "a damaged file is not the store")
}

// A store holding a key's shape is not backed up: the file is removed, the
// finding names the shape and the line, and the text is never printed.
func TestBackupRefusesAFileHoldingAKeyShape(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	key := "gh" + "p_" + strings.Repeat("a1B2", 9)
	goal := filepath.Join(t.TempDir(), "goal.txt")
	require.NoError(t, os.WriteFile(goal, []byte("first line\nuse "+key+" to push\n"), 0o644))
	ta.ok("goal set friend-a --file " + goal)
	file := filepath.Join(t.TempDir(), "sprint.backup")
	code, out, errb := ta.do("backup --file " + file)
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "nova-sprint backup FAILED")
	assert.Contains(t, errb, "shape=forge-token line=")
	assert.NotContains(t, out+errb, key, "the matched text is never printed")
	_, err := os.Stat(file)
	assert.True(t, os.IsNotExist(err), "a backup holding a secret is removed")
}

// The refusals each name their remedy, and a file already there is never
// overwritten.
func TestBackupRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	have := filepath.Join(dir, "have.backup")
	require.NoError(t, os.WriteFile(have, []byte("mine"), 0o644))
	for name, tc := range map[string]struct {
		line, want string
		code       int
	}{
		"no file":      {"backup", "wants --file <path>", 2},
		"a word":       {"backup stray --file x", "takes no words", 2},
		"file present": {"backup --file " + have, "already exists", 1},
		"no directory": {"backup --file " + filepath.Join(dir, "no", "such", "f"), "cannot be written", 2},
	} {
		code, _, errb := ta.do(tc.line)
		assert.Equal(t, tc.code, code, name)
		assert.Contains(t, errb, "REFUSED", name)
		assert.Contains(t, errb, tc.want, name)
		assert.Contains(t, errb, "; run: ", name)
	}
	kept, err := os.ReadFile(have)
	require.NoError(t, err)
	assert.Equal(t, "mine", string(kept))
}

// The scan's shapes are the hygiene gate's, row for row: a shape added there
// and missing here fails this test.
func TestBackupShapesAreTheHygieneShapes(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "hygiene", "keyshapes.txt"))
	require.NoError(t, err)
	want := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		name, re, ok := strings.Cut(line, "\t")
		require.True(t, ok, line)
		want[name] = re
	}
	got := map[string]string{}
	for _, s := range backupShapes {
		got[s.name] = s.re.String()
	}
	assert.Equal(t, want, got)
}
