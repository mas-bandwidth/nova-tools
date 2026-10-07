package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint/store"
)

// fakeSecrets is a nova-secrets with the exec contract and none of its store:
// names lists one sealed name and one clear one, and exec becomes the command
// after -- with the sealed name's value in its environment, as the real exec
// does. The value is in this script alone, outside every directory the backup
// writes.
func fakeSecrets(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nova-secrets")
	script := `#!/bin/sh
case "$1" in
names)
	echo "SECRETS NAME key=PLANTED_TOKEN clear=false"
	echo "SECRETS NAME key=PORT clear=true"
	echo "SECRETS NAMES OK as=bud keys=2 shown=2 sealed=1 clear=1"
	;;
exec)
	while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
	shift
	echo "SECRETS EXEC OK as=bud keys=1 only=1" >&2
	exec env PLANTED_TOKEN='` + value + `' "$@"
	;;
*)
	echo "SECRETS REFUSED: $1" >&2
	exit 2
	;;
esac
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

// backupTwinFile is a twin store holding cards in several columns of the work
// table, and with planted set, a machine record holding the planted value.
func backupTwinFile(t *testing.T, planted string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1",
		"nova-sprint add --stream s1 --count 3",
		"nova-sprint add --stream s2 --count 2",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
		"nova-sprint take --as m1 --epoch 0",
		"nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --report done",
		"nova-sprint tick",
	} {
		code, out, errs := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s: exit %d\n%s%s", line, code, out, errs)
	}
	if planted != "" {
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		m := store.NewMem()
		require.NoError(t, m.Restore(b))
		require.NoError(t, m.SetKey(t.Context(), "note", "a machine wrote "+planted+" here"))
		doc, err := m.Snapshot()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(file, doc, 0o600))
	}
	return file
}

// The backup of a twin into --out: the RESTORE dump, xz -9 and split into
// parts, the two sums, the restore into a throwaway twin with equal counts,
// the scan through nova-secrets exec with no match, and the README; the
// directory holds those files and nothing else. The twin with a planted value
// fails with exit 1 and a count of 1, the value nowhere in what was printed or
// written, and --out left as it was.
func TestBackupWritesADumpThatRestoresAndHoldsNoSecret(t *testing.T) {
	t.Parallel()
	for _, bin := range []string{"xz", "split"} {
		_, err := exec.LookPath(bin)
		require.NoError(t, err, "the backup runs the system's %s", bin)
	}
	planted := "nsv_PlantedFake_" + strings.Repeat("q7Rk", 6)
	secrets := fakeSecrets(t, planted)
	keys := t.TempDir()
	flags := " --nova-secrets " + secrets + " --secrets-store " + keys + " --secrets-as bud --secrets-key " + filepath.Join(keys, "bud.key") + " --sops /bin/true --part-bytes 600"

	// the clean twin
	file := backupTwinFile(t, "")
	out := filepath.Join(t.TempDir(), "backup")
	code, stdout, stderr := twinProcess(t, file, "nova-sprint backup --out "+out+flags)
	require.Equal(t, 0, code, "backup: exit %d\n%s%s", code, stdout, stderr)
	ok := regexp.MustCompile(`BACKUP OK out=\S+ epoch=0 keys=(\d+) cards=(\d+) \(([^)]*)\) restored=twin keys=(\d+) cards=(\d+) \(([^)]*)\) parts=(\d+) text_sha256=[0-9a-f]{64} xz_sha256=[0-9a-f]{64} secrets=1 matched=0`).FindStringSubmatch(stdout)
	require.NotNil(t, ok, "the counts line:\n%s", stdout)
	assert.Equal(t, ok[1], ok[4], "the restore holds the keys the store held")
	assert.Equal(t, ok[2], ok[5], "the restore holds the cards the store held")
	assert.Equal(t, ok[3], ok[6], "the restore holds the cards of each column the store held")
	assert.GreaterOrEqual(t, len(strings.Fields(ok[3])), 2, "the twin holds cards in several columns: %s", ok[3])
	assert.NotEqual(t, "0", ok[1])
	ents, err := os.ReadDir(out)
	require.NoError(t, err)
	var parts []string
	names := map[string]bool{}
	for _, e := range ents {
		names[e.Name()] = true
		if strings.Contains(e.Name(), ".xz.part-") {
			parts = append(parts, e.Name())
		}
		assert.Contains(t, stdout, "BACKUP FILE "+e.Name()+" ", "one line per file")
	}
	assert.True(t, names["SHA256SUMS"] && names["README.md"], "the sums and the README: %v", names)
	assert.Len(t, ents, len(parts)+2, "the parts, the sums and the README, nothing else: %v", names)
	assert.Equal(t, ok[7], strconv.Itoa(len(parts)), "the parts counted are the parts written")
	require.Greater(t, len(parts), 1, "a dump over --part-bytes is split")
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^[0-9a-f]{64}  sprint-epoch0\.restore\.txt$`, string(sums), "the sum of the text")
	assert.Regexp(t, `(?m)^[0-9a-f]{64}  sprint-epoch0\.restore\.txt\.xz$`, string(sums), "the sum of the xz")
	readme, err := os.ReadFile(filepath.Join(out, "README.md"))
	require.NoError(t, err)
	for _, p := range parts {
		assert.Contains(t, string(readme), p, "the README names every part")
		fi, err := os.Stat(filepath.Join(out, p))
		require.NoError(t, err)
		assert.LessOrEqual(t, fi.Size(), int64(600), "a part is under --part-bytes")
	}
	assert.Contains(t, string(readme), "cat "+strings.Join(parts, " ")+" > sprint-epoch0.restore.txt.xz", "the README's load command, the parts in order")
	assert.Contains(t, string(readme), "redis-cli")
	// the parts put together are the xz whose sum is recorded (the verb
	// itself decompressed them, checked the text's sum and restored them)
	var xzBytes []byte
	for _, p := range parts {
		b, err := os.ReadFile(filepath.Join(out, p))
		require.NoError(t, err)
		xzBytes = append(xzBytes, b...)
	}
	sum := sha256.Sum256(xzBytes)
	assert.Contains(t, string(sums), hex.EncodeToString(sum[:])+"  sprint-epoch0.restore.txt.xz\n")

	// a non-empty --out is refused and left as it is
	code, _, stderr = twinProcess(t, file, "nova-sprint backup --out "+out+flags)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not empty")
	after, _ := os.ReadDir(out)
	assert.Len(t, after, len(ents), "a refused backup leaves the directory as it was")

	// the twin holding a planted value
	file = backupTwinFile(t, planted)
	out = filepath.Join(t.TempDir(), "backup")
	code, stdout, stderr = twinProcess(t, file, "nova-sprint backup --out "+out+flags)
	assert.Equal(t, 1, code, "a backup holding a secret fails: %s%s", stdout, stderr)
	assert.Contains(t, stderr, "matched=1", "the count of the values found")
	assert.NotContains(t, stdout+stderr, planted, "no value is printed")
	assert.NotContains(t, stdout+stderr, "kv:note", "no position of a match is printed")
	_, err = os.Stat(out)
	assert.True(t, os.IsNotExist(err), "a failed backup writes no --out")
	// nothing the backup wrote holds the value: no file under --out's parent,
	// no leftover work directory
	_ = filepath.WalkDir(filepath.Dir(out), func(p string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			require.NoError(t, err)
			assert.NotContains(t, string(b), planted, "%s holds the value", p)
		}
		return nil
	})
	left, _ := os.ReadDir(filepath.Dir(out))
	assert.Empty(t, left, "a failed backup leaves no work directory")
}

// TestBackupOutPrintsItsResultLine pins the verb law on the backup verb's
// exit-0 path: it prints its one result line through fmt.Fprint* on stdout
// before returning 0 — BACKUP OK, the out directory, the parts, the bytes and
// the restore-check verdict (law #2573, tools/analyzers/cmd/vetlaw).
func TestBackupOutPrintsItsResultLine(t *testing.T) {
	t.Parallel()
	for _, bin := range []string{"xz", "split"} {
		_, err := exec.LookPath(bin)
		require.NoError(t, err, "the backup runs the system's %s", bin)
	}
	planted := "nsv_PlantedFake_" + strings.Repeat("q7Rk", 6)
	secrets := fakeSecrets(t, planted)
	keys := t.TempDir()
	flags := " --nova-secrets " + secrets + " --secrets-store " + keys + " --secrets-as bud --secrets-key " + filepath.Join(keys, "bud.key") + " --sops /bin/true --part-bytes 600"
	file := backupTwinFile(t, "")
	out := filepath.Join(t.TempDir(), "backup")
	code, stdout, stderr := twinProcess(t, file, "nova-sprint backup --out "+out+flags)
	require.Equal(t, 0, code, "backup: exit %d\n%s%s", code, stdout, stderr)
	require.Contains(t, stdout, "BACKUP OK out="+out+" ", "the OK line names the out directory")
	require.Contains(t, stdout, "restored=twin", "the restore-check verdict")
	require.Contains(t, stdout, "parts=", "the parts")
	require.Contains(t, stdout, "bytes=", "the bytes")
	require.Contains(t, stdout, "secrets=1 matched=0", "the scan's verdict")
}
