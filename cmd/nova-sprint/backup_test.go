package main

import (
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

// leakySource hands out one document, so a test can plant a key shape in the
// bytes a backup would write.
type leakySource struct{ doc string }

func (l leakySource) Save(context.Context) ([]byte, store.SnapshotCounts, error) {
	return []byte(l.doc), store.SnapshotCounts{Keys: 1, Cards: 1}, nil
}

type leakyTwin struct{}

func (leakyTwin) Load([]byte) (store.SnapshotCounts, error) {
	return store.SnapshotCounts{Keys: 1, Cards: 1}, nil
}

// The sprint backup is a verb, not a hand procedure: it writes the store to a
// file, restores the file into a twin and compares, and scans the file for key
// shapes (SPEC-SPRINT, sprint-backup-verb). On a twin store, with no socket and
// the clock injected, it lands a verified file and prints the three checks.
func TestSprintBackupWritesRestoresComparesAndScans(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{"nova-sprint init --members m1", "nova-sprint add --stream s1 --count 2 --one"} {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	run := func(args ...string) (int, string, string) {
		a := newApp(func(k string) string { return env[k] })
		defer a.close()
		a.now = func() time.Time { at = at.Add(time.Minute); return at }
		var out, errb strings.Builder
		code := a.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}

	t.Run("a backup is written, restored into a twin, compared and scanned", func(t *testing.T) {
		code, out, errs := run("backup", "--dir", dir, "--keep", "2")
		require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
		assert.Contains(t, out, "BACKUP OK file=")
		assert.Contains(t, out, "restored=twin compared=counts scanned=")
		assert.Contains(t, out, "secrets=0")
		files, err := store.SnapshotFiles(dir)
		require.NoError(t, err)
		assert.Len(t, files, 1)
		for i := 0; i < 2; i++ {
			code, out, errs = run("backup", "--dir", dir, "--keep", "2")
			require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
		}
		files, _ = store.SnapshotFiles(dir)
		assert.Len(t, files, 2, "the directory is pruned to --keep")
	})
	t.Run("a backup with no directory is refused with the remedy", func(t *testing.T) {
		code, out, errs := run("backup")
		assert.Equal(t, 2, code)
		assert.Empty(t, out)
		assert.Contains(t, errs, "run: nova-sprint backup --dir")
	})
}

// A file that holds a key shape is not kept: the backup fails naming the shape
// and never the text, the older backups stay, and nothing new is in the directory.
func TestSprintBackupRefusesAFileThatHoldsAKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	now := func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	clean := &backupSource{src: leakySource{doc: "cards only"}}
	_, err := runBackup(ctx, dir, 3, clean, leakyTwin{}, now)
	require.NoError(t, err)

	planted := "AGE-SECRET-KEY-1" + strings.Repeat("Q", 24)
	leaky := &backupSource{src: leakySource{doc: "a card\n" + planted + "\n"}}
	_, err = runBackup(ctx, dir, 3, leaky, leakyTwin{}, func() time.Time { return now().Add(time.Hour) })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "age-secret-key")
	assert.NotContains(t, err.Error(), planted, "a finding never quotes the key")
	ents, _ := os.ReadDir(dir)
	assert.Len(t, ents, 2, "the clean backup and its checksum stay; the leaky one was never written")
}
