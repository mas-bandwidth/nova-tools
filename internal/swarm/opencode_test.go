package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// THE USAGE SOURCE IS THE DATABASE THE HARNESS ACTUALLY WRITES (rule 12, rule 13).
//
// On 2026-09-11 the enum said `opencode` and the reader read a tab-separated file no
// OpenCode writes: two jobs burned 61,875 and 85,308 tokens against `--tokens 20000` and
// both reported `budget=-/20000`. These tests hold the reader that closes that: the job's
// own data home, `opencode/opencode.db`, read through `sqlite3` READ-ONLY, the five token
// types summed across the message rows, a column no message reported left a dash.

// fakeSQLite3 builds the stand-in sqlite3 and puts it first on PATH. It returns the bin
// directory so a test can take the program away again.
func fakeSQLite3(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "sqlite3")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakesqlite")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fake sqlite3: %v\n%s", err, out)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// writeDB writes the rows a real `sqlite3 -tabs` would print for the reader's query into the
// job's data home, where the harness keeps its database.
func writeDB(t *testing.T, dataHome, body string) string {
	t.Helper()
	path := filepath.Join(dataHome, OpenCodeDB)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A database with usage rows is read: the five token types summed across the messages, the
// model the provider named, and a dash for every field the provider did not report.
func TestTheOpenCodeSourceSumsTheMessageRows(t *testing.T) {
	fakeSQLite3(t)
	dataHome := t.TempDir()
	// providerID, modelID, input, output, cache write, cache read, reasoning -- a NULL
	// column prints as the empty string, which is what the provider not reporting it looks
	// like on the wire.
	writeDB(t, dataHome, "deepseek\tdeepseek-chat\t100\t50\t\t\t\ndeepseek\tdeepseek-chat\t7\t3\t\t20\t\n")

	usage, err := ReadProviderUsage(UsageOpenCode, dataHome)
	if err != nil {
		t.Fatalf("a readable database is not an error: %v", err)
	}
	if !usage.Observed {
		t.Fatal("a database with message rows has been observed")
	}
	for _, c := range []struct{ column, want string }{
		{"tokens_in", "107"},
		{"tokens_out", "53"},
		{"cache_write", Dash},
		{"cache_read", "20"},
		{"reasoning", Dash},
		{"model", "deepseek-chat"},
		{"provider", "deepseek"},
		// The message table holds no repository, and this reader never invents one.
		{"repo", Dash},
	} {
		if got := usage.Values[c.column]; got != c.want {
			t.Errorf("%s is %q, want %q", c.column, got, c.want)
		}
	}
	sum, seen, partial := usage.Sum()
	if sum != 180 || seen != 3 || !partial {
		t.Errorf("the sum of the observed columns is %d over %d columns (partial=%t), want 180 over 3 (partial=true)", sum, seen, partial)
	}
}

// A DATABASE THAT IS NOT THERE YET IS NOT AN ERROR: it is the provider having reported
// nothing, which rule 13 keeps apart from a source that FAILS to read. The first leaves the
// budget unable to fire and the deadline to end the job; the second ends the job.
func TestAnAbsentOpenCodeDatabaseIsNotAnError(t *testing.T) {
	fakeSQLite3(t)
	usage, err := ReadProviderUsage(UsageOpenCode, t.TempDir())
	if err != nil {
		t.Fatalf("a database the harness has not written yet is not an error: %v", err)
	}
	if usage.Observed {
		t.Error("nothing was observed, and the reader says so")
	}
	if len(usage.Values) != 0 {
		t.Errorf("nothing was observed, so there are no values: %v", usage.Values)
	}
}

// A SOURCE THAT FAILS TO READ IS AN ERROR, so rule 13's third sample ends the job RUN
// BUDGET-UNVERIFIABLE rather than reporting a budget nobody can see.
func TestAnUnreadableOpenCodeDatabaseIsAnError(t *testing.T) {
	fakeSQLite3(t)
	dataHome := t.TempDir()
	path := writeDB(t, dataHome, "not a database\n")

	usage, err := ReadProviderUsage(UsageOpenCode, dataHome)
	if err == nil {
		t.Fatalf("a database that cannot be read is an error, got %+v", usage)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal names the database: %v", err)
	}
}

// The one program this source runs is `sqlite3`, and its absence is a source that cannot be
// read -- never a source that quietly reports nothing.
func TestNoSQLiteOnPathIsAnError(t *testing.T) {
	dataHome := t.TempDir()
	writeDB(t, dataHome, "deepseek\tdeepseek-chat\t1\t1\t\t\t\n")
	t.Setenv("PATH", t.TempDir())

	if _, err := ReadProviderUsage(UsageOpenCode, dataHome); err == nil {
		t.Fatal("no sqlite3 on PATH is a usage source that cannot be read")
	} else if !strings.Contains(err.Error(), SQLiteBinary) {
		t.Errorf("the refusal names the program it needs: %v", err)
	}
}

// `usage: none` reports nothing and is never an error: only `--tokens unmetered` tasks run
// under it, and that refusal is made before the first worker.
func TestTheNoneSourceReportsNothing(t *testing.T) {
	usage, err := ReadProviderUsage(UsageNone, t.TempDir())
	if err != nil {
		t.Fatalf("`usage: none` is not an error: %v", err)
	}
	if usage.Observed || len(usage.Values) != 0 {
		t.Errorf("`usage: none` observes nothing: %+v", usage)
	}
}
