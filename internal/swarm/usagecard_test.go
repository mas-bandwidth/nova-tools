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

// loadCardUsageStore builds the fixture sqlite store (schema + three assistant rows) into a
// temp directory and returns its path.
func loadCardUsageStore(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		t.Skipf("%s is not on PATH", SQLiteBinary)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "card-usage.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "usage.db")
	cmd := exec.Command(SQLiteBinary, db)
	cmd.Stdin = strings.NewReader(string(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	return db
}

// The grouped assistant rows are summed: tokens_in 108, tokens_out 55, cache_write 10,
// cache_read 20, reasoning 5, and usd 1.0000 -- with the provider and model the store named.
func TestUsageRowFromStore(t *testing.T) {
	db := loadCardUsageStore(t, t.TempDir())
	usage, note := ReadCardUsage(db)
	if note != "" {
		t.Fatalf("a readable store carries no note, got %q", note)
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

// A store the reader cannot open -- here because sqlite3 is not on PATH -- still yields a row:
// every token column a dash, and a note naming the program that is missing. The run must not
// fail on a number nobody can read; it writes the dash and says why.
func TestUsageRowWithoutSqlite(t *testing.T) {
	db := loadCardUsageStore(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())

	usage, note := ReadCardUsage(db)
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
