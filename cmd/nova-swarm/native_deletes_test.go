package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fleet's failure of 2026-10-05 17:28 UTC, measured on hetzner with strace: inside the
// wall opencode died in 0.45 s with "disk I/O error", because SQLite could not delete its
// rollback journal,
//
//	unlink(".../slots/<x>/data/opencode/opencode.db-journal") = -1 EACCES
//
// The wall here is the one a native run builds (nativeSandboxArgv, the real nova-sandbox,
// the child's environment from nativeChildEnvFrom): the data home is the second --write and
// HOME, the temp folder the third. Inside it the child creates and unlinks a file named
// like opencode's journal in $HOME/opencode, which is the syscall that failed, unlinks a
// file in its temp folder, and, where the machine has sqlite3, commits a write in
// rollback-journal mode to a database in $HOME/opencode (docs/SPEC-SANDBOX.md,
// "deletes-in-every-write-root"). A wall that withholds the remove rights from the data
// home fails at journal=1 and sqlite=1.
func TestAChildDeletesItsJournalInItsDataHomeOnLinux(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("the landlock wall; the macOS wall is TestAChildDeletesItsJournalInItsDataHomeOnMacOS")
	}
	childDeletesItsJournal(t)
}

// The same on macOS, under the sandbox-exec profile the wall generates (batman's leg of the
// 2026-10-05 failure).
func TestAChildDeletesItsJournalInItsDataHomeOnMacOS(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the sandbox-exec wall; the linux wall is TestAChildDeletesItsJournalInItsDataHomeOnLinux")
	}
	childDeletesItsJournal(t)
}

// journalScript is the walled child: $1 is its temp folder, $2 the sqlite3 to use or "".
const journalScript = `mkdir -p "$HOME/opencode" && : > "$HOME/opencode/opencode.db-journal" && rm "$HOME/opencode/opencode.db-journal"; echo journal=$?
: > "$1/scratch" && rm "$1/scratch"; echo tmp=$?
if [ -n "$2" ]; then "$2" "$HOME/opencode/opencode.db" "PRAGMA journal_mode=DELETE; CREATE TABLE t(x); INSERT INTO t VALUES(1);" >/dev/null; echo sqlite=$?; else echo sqlite=absent; fi
`

func childDeletesItsJournal(t *testing.T) {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	if out, err := exec.Command(builtSandbox, "check").Output(); err != nil || !strings.HasPrefix(string(out), "CHECK OK") || strings.Contains(string(out), "backend=none") {
		t.Skipf("no wall on this machine (%q, %v)", out, err)
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (docs/SPEC-SANDBOX.md rule 2)")
	}
	sqlite := ""
	for _, p := range []string{"/usr/bin/sqlite3", "/bin/sqlite3"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			sqlite = p
			break
		}
	}

	_, slot := aSlot(t)
	if got, err := filepath.EvalSymlinks(slot); err == nil {
		slot = got
	}
	jobDir := filepath.Join(slot, "jobs", "card")
	data := filepath.Join(slot, "data")
	tmp := filepath.Join(slot, "tmp", "card")
	for _, d := range []string{jobDir, data, tmp} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: runtime.GOOS}
	argv := nativeSandboxArgv([]string{"/bin/sh", "-c", journalScript, "sh", tmp, sqlite}, cfg, data, jobDir, tmp)
	require.True(t, hasFlagPair(argv, "--write", data), "the data home is not a --write of the native wall:\n%s", strings.Join(argv, " "))

	cmd := exec.Command(builtSandbox, argv...)
	cmd.Env = nativeChildEnvFrom([]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, data, jobDir, tmp, "", "", "", "", nil)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())
	require.NoError(t, err, "the walled run failed: stdout=%q stderr=%q", got, errb.String())

	assert.Contains(t, got, "journal=0", "the child could not unlink opencode.db-journal in its data home")
	assert.NoFileExists(t, filepath.Join(data, "opencode", "opencode.db-journal"))
	assert.Contains(t, got, "tmp=0", "the child could not unlink a file in its temp folder")
	assert.NoFileExists(t, filepath.Join(tmp, "scratch"))
	if sqlite != "" {
		assert.Contains(t, got, "sqlite=0", "a rollback-journal commit in the data home failed (opencode's \"disk I/O error\")")
		assert.FileExists(t, filepath.Join(data, "opencode", "opencode.db"))
		assert.NoFileExists(t, filepath.Join(data, "opencode", "opencode.db-journal"), "the commit left its journal behind")
	}
}
