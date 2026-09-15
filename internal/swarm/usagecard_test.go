package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SLICE 10: THE USAGE ROW IS READ FROM THE HARNESS'S OWN STORE.
//
// The native run's numbers come from the messages table of the harness's sqlite store: the
// assistant rows grouped by provider and model, summed into the six numeric columns. A column
// no message reported stays a dash. The fixture is a small schema with three rows, built in
// the test with the real sqlite3 into a temp directory, and read back by ReadCardUsage.

// loadCardUsageStoreAt builds the fixture sqlite store (schema + three assistant rows) at the
// named path and returns it.
func loadCardUsageStoreAt(t *testing.T, path string) string {
	t.Helper()
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		t.Skipf("%s is not on PATH", SQLiteBinary)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "card-usage.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(SQLiteBinary, path)
	cmd.Stdin = strings.NewReader(string(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	return path
}

// The store the run's own data directory holds at <dataHome>/opencode/opencode.db is read
// first and summed: tokens_in 108, tokens_out 55, cache_write 10, cache_read 20, reasoning 5,
// and usd 1.0000 -- with the provider and model the store named. Reading from the primary
// location carries no note and the path that answered.
func TestUsageReadsRunDataDirStore(t *testing.T) {
	dataHome := t.TempDir()
	db := loadCardUsageStoreAt(t, filepath.Join(dataHome, "opencode", "opencode.db"))
	usage, note, path := ReadCardUsage(dataHome)
	if note != "" {
		t.Fatalf("a readable primary store carries no note, got %q", note)
	}
	if path != db {
		t.Errorf("the reader answers with the store it read: %q, want %q", path, db)
	}
	if !usage.Observed {
		t.Fatal("a store with assistant rows has been observed")
	}
	for _, c := range []struct{ column, want string }{
		{"tokens_in", "108"},
		{"tokens_out", "55"},
		{"cache_write", "10"},
		{"cache_read", "20"},
		{"reasoning", "5"},
		{"usd", "1.0000"},
		{"provider", "deepseek"},
		{"model", "deepseek-chat"},
	} {
		if got := usage.Values[c.column]; got != c.want {
			t.Errorf("%s is %q, want %q", c.column, got, c.want)
		}
	}
}

// A data home with no store at either standard location yields the dash row, an empty path,
// and a note that names the store the reader looked for -- never a silent dash.
func TestUsageRowNamesMissingStore(t *testing.T) {
	dataHome := t.TempDir()
	usage, note, path := ReadCardUsage(dataHome)
	if note == "" {
		t.Fatal("a missing store is a note naming it, not silence")
	}
	if !strings.Contains(note, "opencode") {
		t.Errorf("the note names the harness store it looked for: %q", note)
	}
	if path != "" {
		t.Errorf("a missing store answers with no path, got %q", path)
	}
	for _, c := range TokenColumns {
		if got := usage.Values[c]; got != Dash {
			t.Errorf("%s is %q, want %q when the store is missing", c, got, Dash)
		}
	}
	if got := usage.Values["usd"]; got != Dash {
		t.Errorf("usd is %q, want %q when the store is missing", got, Dash)
	}
}

// OpenCode honours HOME/.local/share as well as XDG_DATA_HOME, and the native run points both
// at the same data directory. A store at <dataHome>/.local/share/opencode/opencode.db is read
// when the primary location is empty, and the note records which path answered.
func TestUsageFallsBackToLocalShareStore(t *testing.T) {
	dataHome := t.TempDir()
	db := loadCardUsageStoreAt(t, filepath.Join(dataHome, ".local", "share", "opencode", "opencode.db"))
	usage, note, path := ReadCardUsage(dataHome)
	if path != db {
		t.Errorf("the reader answers with the fallback store it read: %q, want %q", path, db)
	}
	if !strings.Contains(note, db) {
		t.Errorf("the note records the store it read (%s), got %q", db, note)
	}
	if got := usage.Values["tokens_in"]; got != "108" {
		t.Errorf("tokens_in is %q, want %q from the fallback store", got, "108")
	}
}

// A store the reader cannot open -- here because sqlite3 is not on PATH -- still yields a row:
// every token column a dash, and a note naming the program that is missing. The run must not
// fail on a number nobody can read; it writes the dash and says why.
func TestUsageRowWithoutSqlite(t *testing.T) {
	dataHome := t.TempDir()
	loadCardUsageStoreAt(t, filepath.Join(dataHome, "opencode", "opencode.db"))
	t.Setenv("PATH", t.TempDir())

	usage, note, _ := ReadCardUsage(dataHome)
	if note == "" {
		t.Fatal("no sqlite3 on PATH is a note, not silence")
	}
	if !strings.Contains(note, SQLiteBinary) {
		t.Errorf("the note names the program it needs: %q", note)
	}
	for _, c := range TokenColumns {
		if got := usage.Values[c]; got != Dash {
			t.Errorf("%s is %q, want %q when the store cannot be read", c, got, Dash)
		}
	}
	if got := usage.Values["usd"]; got != Dash {
		t.Errorf("usd is %q, want %q when the store cannot be read", got, Dash)
	}
}
