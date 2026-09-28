//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// The store the run's own data directory holds at <dataHome>/opencode/opencode.db is read
// first and summed: tokens_in 108, tokens_out 55, cache_write 10, cache_read 20, reasoning 5,
// and usd 1.0000 -- with the provider and model the store named. Reading from the primary
// location carries no note, no absence reason, and the path that answered.
func TestUsageReadsRunDataDirStore(t *testing.T) {
	t.Parallel()

	dataHome := t.TempDir()
	db := loadCardUsageStoreAt(t, filepath.Join(dataHome, "opencode", "opencode.db"))
	started, ended := cardUsageWindow()
	usage, note, path, reason := ReadCardUsage(dataHome, started, ended)
	if note != "" {
		t.Fatalf("a readable primary store carries no note, got %q", note)
	}
	if reason != "" {
		t.Fatalf("a store with assistant rows has no absence reason, got %q", reason)
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

// OpenCode honours HOME/.local/share as well as XDG_DATA_HOME, and the native run points both
// at the same data directory. A store at <dataHome>/.local/share/opencode/opencode.db is read
// when the primary location is empty, and the note records which path answered.
func TestUsageFallsBackToLocalShareStore(t *testing.T) {
	t.Parallel()

	dataHome := t.TempDir()
	db := loadCardUsageStoreAt(t, filepath.Join(dataHome, ".local", "share", "opencode", "opencode.db"))
	started, ended := cardUsageWindow()
	usage, note, path, reason := ReadCardUsage(dataHome, started, ended)
	if reason != "" {
		t.Fatalf("a fallback store with rows carries no absence reason, got %q", reason)
	}
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
// every token column a dash, a note naming the program that is missing, and the no-sqlite3
// absence reason. The run must not fail on a number nobody can read; it writes the dash and
// says why.
func TestUsageRowWithoutSqlite(t *testing.T) {
	dataHome := t.TempDir()
	loadCardUsageStoreAt(t, filepath.Join(dataHome, "opencode", "opencode.db"))
	t.Setenv("PATH", t.TempDir())
	started, ended := cardUsageWindow()

	usage, note, _, reason := ReadCardUsage(dataHome, started, ended)
	if note == "" {
		t.Fatal("no sqlite3 on PATH is a note, not silence")
	}
	if !strings.Contains(note, SQLiteBinary) {
		t.Errorf("the note names the program it needs: %q", note)
	}
	if reason != "no-sqlite3" {
		t.Errorf("a missing sqlite3 names its absence reason, got %q", reason)
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

// TestUsageRowFromStoreMillis reads the harness's own schema -- the `message` table, JSON in
// `data`, and `time_created` in MILLISECONDS since the epoch -- and proves three facts at once:
// role is filtered to assistant, the window (widened five seconds each side) keeps only the
// rows whose millisecond stamp lands inside it, and the kept rows are grouped by providerID
// and modelID and summed. Rows outside the window, and non-assistant rows, never reach the sum.
func TestUsageRowFromStoreMillis(t *testing.T) {
	t.Parallel()

	dataHome := t.TempDir()
	started := time.UnixMilli(2000000000000)
	ended := started.Add(10 * time.Second)
	// one inside assistant row at started (kept); one at started-6s (outside the 5s widen);
	// one at ended+6s (outside); one user row inside the window (role filtered); a second
	// provider/model inside the window (grouped apart).
	body := cardUsageSchema + `
INSERT INTO message (data, time_created) VALUES
  ('{"role":"assistant","providerID":"acme","modelID":"m1","tokens":{"input":100,"output":50,"cache":{"write":10,"read":20},"reasoning":5},"cost":0.25}', 2000000000000),
  ('{"role":"assistant","providerID":"acme","modelID":"m1","tokens":{"input":999,"output":999},"cost":9.99}', 1999999994000),
  ('{"role":"assistant","providerID":"acme","modelID":"m1","tokens":{"input":777,"output":777},"cost":7.77}', 2000000016000),
  ('{"role":"user","providerID":"acme","modelID":"m1","tokens":{"input":111,"output":111},"cost":1.11}', 2000000003000);`
	db := loadCardUsageSQL(t, filepath.Join(dataHome, "opencode", "opencode.db"), body)
	usage, _, path, reason := ReadCardUsage(dataHome, started, ended)
	if reason != "" {
		t.Fatalf("the store answered with rows, no absence reason, got %q", reason)
	}
	if path != db {
		t.Errorf("the reader answers with the store it read: %q, want %q", path, db)
	}
	if got := usage.Values["tokens_in"]; got != "100" {
		t.Errorf("tokens_in is %q, want the in-window assistant rows only (100)", got)
	}
	if got := usage.Values["tokens_out"]; got != "50" {
		t.Errorf("tokens_out is %q, want %q", got, "50")
	}
	if got := usage.Values["cache_write"]; got != "10" {
		t.Errorf("cache_write is %q, want %q", got, "10")
	}
	if got := usage.Values["reasoning"]; got != "5" {
		t.Errorf("reasoning is %q, want %q", got, "5")
	}
	if got := usage.Values["cache_read"]; got != "20" {
		t.Errorf("cache_read is %q, want %q", got, "20")
	}
	if got := usage.Values["usd"]; got != "0.2500" {
		t.Errorf("usd is %q, want %q", got, "0.2500")
	}
	if got := usage.Values["provider"]; got != "acme" {
		t.Errorf("provider is %q, want %q", got, "acme")
	}
	if got := usage.Values["model"]; got != "m1" {
		t.Errorf("model is %q, want %q", got, "m1")
	}
}

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

// loadCardUsageSQL runs one raw SQL body (schema and rows) into a fresh store at path.
func loadCardUsageSQL(t *testing.T, path, body string) string {
	t.Helper()
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		t.Skipf("%s is not on PATH", SQLiteBinary)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(SQLiteBinary, path)
	cmd.Stdin = strings.NewReader(body)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	return path
}

// cardUsageSchema is the schema the harness's own store has: a `message` table whose `data`
// column is JSON and whose `time_created` column is milliseconds since the epoch.
const cardUsageSchema = `CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);`
