package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rosterJSON is the roster every test in this package works against unless it needs a
// broken one. It carries the three shapes the real table has: a sender with aliases, a
// second sender, and a person who is addressed and never writes.
const rosterJSON = `{
  "participants": [
    {"name": "Rowan", "lane": "from-rowan", "aliases": ["Rowan Claude", "the keeper"],
     "git_name": "Rowan", "git_email": "rowan@mas-bandwidth.com"},
    {"name": "Stella", "lane": "from-stella", "aliases": ["Stella Codex"],
     "git_name": "Stella", "git_email": "stella@mas-bandwidth.com"},
    {"name": "Glenn"}
  ],
  "groups": [
    {"name": "Everybody at the table", "members": ["Rowan", "Stella", "Glenn"]}
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

// writeTable builds a table under t.TempDir from repo-relative paths to contents. A
// participants.json is written unless files supplies one.
func writeTable(t *testing.T, files map[string]string) string {
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
func loadTable(t *testing.T, root string) *Table {
	t.Helper()
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	tab, err := ReadTable(root, c)
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
