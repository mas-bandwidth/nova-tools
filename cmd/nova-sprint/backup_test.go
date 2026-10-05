package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backupProcess is one command over a twin file with an injected clock: no
// socket, and no real time.
func backupProcess(t *testing.T, file string, at time.Time, line string) (int, string, string) {
	t.Helper()
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	a := newApp(func(k string) string { return env[k] })
	defer a.close()
	a.now = func() time.Time { return at }
	var out, errb bytes.Buffer
	code := a.run(split(strings.TrimPrefix(line, prog+" ")), &out, &errb)
	return code, out.String(), errb.String()
}

// seedBackupTwin runs the twin's flow as far as the finish, which carries report.
func seedBackupTwin(t *testing.T, file, report string) {
	t.Helper()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, line := range twinSteps[:6] {
		code, out, errs := backupProcess(t, file, at, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	code, out, errs := backupProcess(t, file, at, "nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --report "+report)
	require.Equal(t, 0, code, "finish: exit %d\n%s%s", code, out, errs)
}

// The sprint backup is one verb: the store is written to a checksummed file,
// restored into a twin and compared, and the file scanned for secrets; it runs
// on the twin store with no socket and no real time, keeps the newest --keep,
// and a store that holds a secret gets no backup file and keeps the older ones.
func TestBackupWritesRestoresComparesAndScansTheTwinStore(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	t.Run("a clean store is backed up, verified and pruned to keep", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "sprint.twin")
		seedBackupTwin(t, file, "done")
		dir := filepath.Join(t.TempDir(), "backups")
		for i := 0; i < 3; i++ {
			code, out, errs := backupProcess(t, file, at.Add(time.Duration(i)*time.Minute), "nova-sprint backup --dir "+dir+" --keep 2")
			require.Equal(t, 0, code, "exit %d\n%s%s", code, out, errs)
			assert.Contains(t, out, "BACKUP OK file="+dir)
			assert.Contains(t, out, "verified=checksum+twin secrets=none")
			assert.Contains(t, out, "cards=1")
		}
		ents, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, ents, 4, "two backups kept, each with its checksum beside it")
		code, out, _ := backupProcess(t, file, at, "nova-sprint backup --dir "+dir+" --json")
		require.Equal(t, 0, code)
		assert.Contains(t, out, `"sha256"`)
	})
	t.Run("a store holding a secret is refused, named by shape and never printed", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "sprint.twin")
		secret := "ghp_" + strings.Repeat("a1B2", 8)
		seedBackupTwin(t, file, secret)
		dir := filepath.Join(t.TempDir(), "backups")
		code, out, errs := backupProcess(t, file, at, "nova-sprint backup --dir "+dir)
		require.Equal(t, 1, code, "%s%s", out, errs)
		assert.Contains(t, errs, "backup FAILED")
		assert.Contains(t, errs, "forge-token")
		assert.NotContains(t, out+errs, secret, "the value is never printed")
		ents, _ := os.ReadDir(dir)
		assert.Empty(t, ents, "the failed copy and its checksum are removed")
	})
	t.Run("a missing --dir is refused with the command to run", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "sprint.twin")
		seedBackupTwin(t, file, "done")
		code, _, errs := backupProcess(t, file, at, "nova-sprint backup")
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "run: nova-sprint backup --dir")
	})
}

func TestBackupSecretKindsNameShapesOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"pem-private-key"}, backupSecretKinds([]byte("-----BEGIN OPENSSH PRIVATE KEY-----")))
	assert.Equal(t, []string{"password-in-address"}, backupSecretKinds([]byte("redis://user:hunter2@host:6379")))
	assert.Empty(t, backupSecretKinds([]byte("a card brief with no key in it; sk-short")))
}
