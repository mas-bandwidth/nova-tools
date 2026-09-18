package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
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
// fakeFlushMarker is FlushMarker in testdata/fakesqlite/main.go, repeated here because a
// testdata `package main` cannot be imported. The two are pinned together by
// TestFakeSQLite3FlushMarkerMatchesTheFake.
const fakeFlushMarker = ".flush-on-refusal"

func fakeSQLite3(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "sqlite3")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakesqlite")
	cmd.Env = goenv.Clean(os.Environ())
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

// writeDBAt writes the lines a real `sqlite3 -tabs` would print to an explicit path, so a
// test can plant the store at each location OpenCode may choose.
func writeDBAt(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// THE STORE IS UNDER THE JOB'S DATA HOME, WHICHEVER NAME OPENCODE GAVE IT (rule 12).
//
// The dispatcher exports HOME and XDG_DATA_HOME to the same per-job data home, and OpenCode
// on Linux derives its data directory from HOME -- `$HOME/.local/share/opencode`, beside the
// `auth.json` the bench carries -- while on the Studio it honours XDG_DATA_HOME. The reader
// looked only at the XDG spelling, so on hulk, vision, mini and space a run that exited 0
// with a RESULT wrote an all-dash usage row: the database was there, one directory over,
// and the reader never opened it. Both spellings must answer, primary first.
func TestOpenCodeSourceFindsTheLocalShareStore(t *testing.T) {
	fakeSQLite3(t)
	dataHome := t.TempDir()
	fallback := filepath.Join(dataHome, ".local", "share", "opencode", "opencode.db")
	writeDBAt(t, fallback, "deepseek\tdeepseek-chat\t100\t50\t\t\t\n")

	usage, err := ReadProviderUsage(UsageOpenCode, dataHome)
	if err != nil {
		t.Fatalf("a store under the data home's .local/share is readable: %v", err)
	}
	if !usage.Observed {
		t.Fatal("a store with message rows has been observed")
	}
	if got := usage.Values["tokens_in"]; got != "100" {
		t.Errorf("tokens_in is %q, want 100 read from %s", got, fallback)
	}
	if got := usage.Values["tokens_out"]; got != "50" {
		t.Errorf("tokens_out is %q, want 50 read from %s", got, fallback)
	}
}

// A WRITE-AHEAD LOG THE HARNESS HAS NOT FLUSHED IS WAITED OUT, up to five seconds (rule 13).
//
// OpenCode checks the writer's connection down as it exits, and this read lands in that
// window: the database is there, a -wal sits beside it, and `sqlite3 -readonly` answers
// `database is locked`. The read is retried until the flush lands rather than recording a
// dash for tokens the harness did spend.
func TestOpenCodeSourceWaitsOutAWriteAheadLog(t *testing.T) {
	fakeSQLite3(t)
	dataHome := t.TempDir()
	db := writeDB(t, dataHome, "deepseek\tdeepseek-chat\t100\t50\t\t\t\n")
	wal := db + "-wal"
	if err := os.WriteFile(wal, []byte("unflushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The flush lands on the reader's first refusal, not after a sleep: the fake takes the
	// -wal with it when it answers `database is locked`, so the retry reads a checkpointed
	// database. The test turns on no clock of its own.
	if err := os.WriteFile(wal+fakeFlushMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	usage, err := ReadProviderUsage(UsageOpenCode, dataHome)
	if err != nil {
		t.Fatalf("the read waits out the flush instead of failing: %v", err)
	}
	if got := usage.Values["tokens_in"]; got != "100" {
		t.Errorf("tokens_in is %q, want 100 read after the -wal was flushed", got)
	}
}

// A SOURCE WITH NO READER IS A NAMED REFUSAL, NEVER A SILENT DASH (rule 13).
//
// The usage source is `sqlite3`, and a bench without it used to record dashes with nothing
// said -- the caller saw a completed job and a token row of `-`, and no line named the
// program the read needed. The refusal carries the literal line
// `USAGE REFUSED reason=no_sqlite` so the missing reader is a fact on the record.
func TestOpenCodeSourceWithoutSQLiteIsANamedRefusal(t *testing.T) {
	dataHome := t.TempDir()
	writeDB(t, dataHome, "deepseek\tdeepseek-chat\t100\t50\t\t\t\n")
	t.Setenv("PATH", t.TempDir())

	_, err := ReadProviderUsage(UsageOpenCode, dataHome)
	if err == nil {
		t.Fatal("no sqlite3 on PATH is a usage source that cannot be read")
	}
	if !strings.Contains(err.Error(), "USAGE REFUSED reason=no_sqlite") {
		t.Errorf("the refusal names the missing reader: %v", err)
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

// TestFakeSQLite3FlushMarkerMatchesTheFake keeps the copied constant honest: the fake's own
// source is read and the literal it declares must be the one the tests append.
func TestFakeSQLite3FlushMarkerMatchesTheFake(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "fakesqlite", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := "const FlushMarker = " + strconv.Quote(fakeFlushMarker)
	if !strings.Contains(string(src), want) {
		t.Errorf("testdata/fakesqlite/main.go does not declare %s", want)
	}
}
