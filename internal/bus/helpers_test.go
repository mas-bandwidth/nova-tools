package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rosterJSON is the roster every test in this package works against unless it needs a
// broken one. It carries the three shapes the real bus has: a sender with aliases, a
// second sender, and a person who is addressed and never writes.
const rosterJSON = `{
  "participants": [
    {"name": "Ada", "lane": "from-ada", "aliases": ["Ada Vale", "the archivist"],
     "git_name": "Ada", "git_email": "ada@example.com"},
    {"name": "Bo", "lane": "from-bo", "aliases": ["Bo Quill"],
     "git_name": "Bo", "git_email": "bo@example.com"},
    {"name": "Dana"}
  ],
  "groups": [
    {"name": "Everybody on the bus", "members": ["Ada", "Bo", "Dana"]}
  ]
}`

// at is a fixed clock, so an id in an assertion is a constant a reader can check.
func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// writeTable builds a bus under t.TempDir from repo-relative paths to contents. A
// participants.json is written unless files supplies one.
func writeBus(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if _, ok := files[ConfigName]; !ok {
		write(t, root, ConfigName, rosterJSON)
	}
	for path, content := range files {
		write(t, root, path, content)
	}
	return root
}

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadTable is the two calls every verb makes, for a test that only cares about the
// result.
func loadBus(t *testing.T, root string) *Bus {
	t.Helper()
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	tab, err := ReadBus(root, c)
	if err != nil {
		t.Fatalf("ReadTable: %v", err)
	}
	return tab
}

func mustParticipant(t *testing.T, c *Config, name string) Participant {
	t.Helper()
	p, ok := c.Lookup(name)
	if !ok {
		t.Fatalf("roster has no %q", name)
	}
	return p
}

// identityOf is which FILE a path names, taken from a handle on the file itself rather
// than from a second look at the path -- so that comparing two of them across a write says
// whether the file was REPLACED, on every platform this runs on. os.Stat would do on unix;
// on Windows the file id behind os.SameFile is loaded lazily from the path, which after a
// replace is the new file, and the comparison would quietly answer "same" every time.
func identityOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fi
}
