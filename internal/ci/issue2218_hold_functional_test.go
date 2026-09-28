//go:build functional

package ci

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIssue2218ChangeBaseReadsTheBasesList is the git half of
// TestIssue2218AddedUnexecutedRowFails (rowan hold 3 at d3f2ddf5, item 1): in a
// real history, ChangeBase finds the change's base both ways, ListAtCommit reads
// the list as the base had it, and the row the change appended is the one
// AddedListRows reports. It runs git a dozen times: exec of a whole program is
// the functional tier's (Glenn 2026-09-26, nova-tools#4328).
func TestIssue2218ChangeBaseReadsTheBasesList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "dev")
	writeGo(t, root, "docs/CLI.md", "```\n$ nova-foo run\n```\n")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "pr")
	writeGo(t, root, "docs/CLI.md", "```\n$ nova-foo run\n$ nova-foo newverb --x 1\n```\n")
	writeGo(t, root, UnexecutedListPath, "# list\n$ nova-foo run\n$ nova-foo newverb --x 1\n")
	git("commit", "-q", "-am", "adds an unexecuted example")

	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(`{"pull_request":{"base":{"sha":"`+base+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]map[string]string{
		"pull_request event": {"GITHUB_EVENT_PATH": event},
		"local merge base":   {"GITHUB_BASE_REF": "dev"},
	} {
		got, err := ChangeBase(root, func(k string) string { return env[k] })
		if err != nil || got != base {
			t.Errorf("%s: ChangeBase = %q, %v; want %s", name, got, err, base)
			continue
		}
		baseList, present, err := ListAtCommit(root, got, UnexecutedListPath)
		if err != nil || !present {
			t.Fatalf("%s: ListAtCommit = present %v, %v; want the base's list", name, present, err)
		}
		head := loadAllowlist(t, filepath.Join(root, UnexecutedListPath), unexecutedListOptions)
		if added := AddedListRows(baseList, head.Text()); !slices.Equal(added, []string{"$ nova-foo newverb --x 1"}) {
			t.Errorf("%s: added rows = %q; want the appended row, which fails the class test", name, added)
		}
	}
	if _, present, err := ListAtCommit(root, base, "internal/ci/testdata/absent.txt"); err != nil || present {
		t.Errorf("ListAtCommit of a file the base lacks = present %v, %v; want introduced (false, nil)", present, err)
	}
}
