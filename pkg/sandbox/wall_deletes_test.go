//go:build linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wallDeletesEnv makes the test binary the walled child. Run restricts the process
// that calls it, and a Landlock domain cannot be lifted, so the wall is applied in
// a re-exec of this binary and never in the test process itself.
const wallDeletesEnv = "NOVA_TEST_WALL_DELETES_EVERY_WRITE"

// TestTheWallAllowsDeletesInEveryWriteRoot pins the wall under Landlock: a program
// may delete any file under any --write root. The data home is the second --write
// and a cache is the third; neither is the cwd nor the tmp. The command creates a
// file in each and unlinks it (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root").
func TestTheWallAllowsDeletesInEveryWriteRoot(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallDeletesEnv) == "1" {
		os.Exit(runWallDeletesHelper())
	}
	if _, ok := landlockABI(); !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}

	job := realDir(t, t.TempDir())
	data := realDir(t, t.TempDir())
	cache := realDir(t, t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(data, "app"), 0o700))

	p, bad := Build(Input{
		Writes: []string{job, data, cache},
		Home:   data,
		Argv:   []string{"sh", "-c", "true"},
	})
	require.Empty(t, bad, "build refused: %v", bad)
	require.Equal(t, job, p.Cwd)
	require.NotEqual(t, data, p.Cwd)
	require.NotEqual(t, data, p.Tmp)
	require.NotEqual(t, cache, p.Cwd)
	require.NotEqual(t, cache, p.Tmp)
	require.False(t, Inside(data, p.Tmp) || Inside(p.Tmp, data), "the data home must not be the tmp")
	require.False(t, Inside(cache, p.Tmp) || Inside(p.Tmp, cache), "the cache must not be the tmp")
	assert.True(t, p.DeletesIn(data), "the data home is a --write, so deletes belong there")
	assert.True(t, p.DeletesIn(cache), "the cache is a --write, so deletes belong there")

	cmd := exec.Command(os.Args[0], "-test.run=^TestTheWallAllowsDeletesInEveryWriteRoot$", "-test.count=1")
	cmd.Env = append(os.Environ(), wallDeletesEnv+"=1", "WALL_JOB="+job, "WALL_DATA="+data, "WALL_CACHE="+cache)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "the walled child did not run: stdout=%s stderr=%s", out.String(), errb.String())
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())

	assert.Contains(t, got, "data_create=0", "creating a file in the data home was refused")
	assert.Contains(t, got, "data_rm=0", "unlinking a file in the data home was refused")
	assert.Contains(t, got, "data_left=0", "the data-home file was still there after unlink")
	assert.NoFileExists(t, filepath.Join(data, "probe"))
	assert.Contains(t, got, "cache_create=0", "creating a file in the cache --write was refused")
	assert.Contains(t, got, "cache_rm=0", "unlinking a file in the cache --write was refused")
	assert.Contains(t, got, "cache_left=0", "the cache file was still there after unlink")
	assert.NoFileExists(t, filepath.Join(cache, "probe"))
}

func runWallDeletesHelper() int {
	job, data, cache := os.Getenv("WALL_JOB"), os.Getenv("WALL_DATA"), os.Getenv("WALL_CACHE")
	script := strings.Join([]string{
		`echo made > "$2/probe"; echo data_create=$?`,
		`rm "$2/probe"; echo data_rm=$?`,
		`if [ -e "$2/probe" ]; then echo data_left=1; else echo data_left=0; fi`,
		`echo made > "$3/probe"; echo cache_create=$?`,
		`rm "$3/probe"; echo cache_rm=$?`,
		`if [ -e "$3/probe" ]; then echo cache_left=1; else echo cache_left=0; fi`,
	}, "\n")
	p, bad := Build(Input{
		Writes: []string{job, data, cache},
		Home:   data,
		Argv:   []string{"sh", "-c", script, "sh", job, data, cache},
	})
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build refused: %v\n", bad)
		return 2
	}
	code, err := Run(p, os.Environ(), strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run refused: %v\n", err)
		return 2
	}
	return code
}

const sqliteJournalEnv = "NOVA_TEST_SQLITE_JOURNAL_IN_THE_DATA_HOME"

// sqliteCommitPy opens a database in the data home and commits one transaction.
// The rollback journal is created while the transaction is open and unlinked by
// the commit. journal_mode=DELETE is the mode that unlinks it.
const sqliteCommitPy = `import os, sqlite3, sys
db = sys.argv[1]
os.makedirs(os.path.dirname(db), exist_ok=True)
con = sqlite3.connect(db, isolation_level=None)
mode = con.execute("PRAGMA journal_mode=DELETE").fetchone()[0]
con.execute("CREATE TABLE t(x INTEGER)")
con.execute("BEGIN")
con.execute("INSERT INTO t VALUES (1)")
journal = db + "-journal"
print("mode=%s" % mode)
print("journal_open=%d" % (1 if os.path.exists(journal) else 0))
con.execute("COMMIT")
print("journal_gone=%d" % (0 if os.path.exists(journal) else 1))
print("row=%s" % con.execute("SELECT x FROM t").fetchone()[0])
`

// A database in the data home commits a transaction by unlinking its rollback
// journal. The data home is a --write that is neither the cwd nor the tmp
// (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root").
func TestASqliteCommitInTheDataHomeUnlinksItsJournal(t *testing.T) {
	t.Parallel()
	if os.Getenv(sqliteJournalEnv) == "1" {
		os.Exit(runSqliteJournalHelper())
	}
	if _, ok := landlockABI(); !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
	python, err := exec.LookPath("python3")
	require.NoError(t, err, "python3 is how this test opens a database; this machine has none")

	job := realDir(t, t.TempDir())
	data := realDir(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(job, "commit.py"), []byte(sqliteCommitPy), 0o644))

	p, bad := Build(Input{
		Writes: []string{job, data},
		Home:   data,
		Reads:  []string{filepath.Dir(python)},
		Argv:   []string{"sh", "-c", "true"},
	})
	require.Empty(t, bad, "build refused: %v", bad)
	require.NotEqual(t, data, p.Cwd)
	require.NotEqual(t, data, p.Tmp)
	require.False(t, Inside(data, p.Tmp) || Inside(p.Tmp, data))

	cmd := exec.Command(os.Args[0], "-test.run=^TestASqliteCommitInTheDataHomeUnlinksItsJournal$", "-test.count=1")
	cmd.Env = append(os.Environ(), sqliteJournalEnv+"=1", "WALL_JOB="+job, "WALL_DATA="+data, "WALL_PYTHON="+python)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "the walled child did not run: stdout=%s stderr=%s", out.String(), errb.String())
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())

	assert.Contains(t, got, "py=0", "the database command failed inside the wall")
	assert.Contains(t, got, "mode=delete")
	assert.Contains(t, got, "journal_open=1", "the rollback journal was never created, so the commit did not unlink one")
	assert.Contains(t, got, "journal_gone=1", "the rollback journal was not unlinked at commit")
	assert.Contains(t, got, "row=1")
	db := filepath.Join(data, "app", "state.db")
	assert.FileExists(t, db)
	assert.NoFileExists(t, db+"-journal")
}

func runSqliteJournalHelper() int {
	job, data, python := os.Getenv("WALL_JOB"), os.Getenv("WALL_DATA"), os.Getenv("WALL_PYTHON")
	script := `"$4" "$1/commit.py" "$2/app/state.db"; echo py=$?`
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+data)
	p, bad := Build(Input{
		Writes: []string{job, data},
		Home:   data,
		Reads:  []string{filepath.Dir(python)},
		Argv:   []string{"sh", "-c", script, "sh", job, data, "", python},
	})
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build refused: %v\n", bad)
		return 2
	}
	code, err := Run(p, env, strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run refused: %v\n", err)
		return 2
	}
	return code
}
