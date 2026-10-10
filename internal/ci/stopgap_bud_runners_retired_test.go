package ci

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStopgapBudRunnersAreRetired is the register's test for the buds' two hand
// scripts. docs/STOPGAPS.md's `## bud-card-runner` row (Path
// <buds-dir>/<bud>/runner.zsh) and `## bud-reader-runner` row (Path
// <buds-dir>/<bud>/reader.zsh) must be `STATUS: retired <date>`, name the
// nova-friend `run` verb as their replacement, and cite on every behaviour line
// a `(test: TestX)` for a test that exists in the tree. The check is the
// register's own parser and rule (parseStopgaps and retiredFaults in
// stopgaps_class_test.go) run on the two rows the retirement card touched, so
// each row is red while its script is still live or names a behaviour no test
// holds.
func TestStopgapBudRunnersAreRetired(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	rows, faults := parseStopgaps(readFile(t, filepath.Join(root, stopgapsDoc)))
	require.Empty(t, faults, "%s is malformed", stopgapsDoc)

	byName := map[string]*stopgap{}
	for i := range rows {
		byName[rows[i].Name] = &rows[i]
	}

	cli := readFile(t, filepath.Join(root, "docs", "CLI.md"))
	tests := treeTestNames(t, root)
	for _, name := range []string{"bud-card-runner", "bud-reader-runner"} {
		row, ok := byName[name]
		require.True(t, ok, "%s has no `## %s` row: the runner.zsh and reader.zsh scripts are listed", stopgapsDoc, name)
		require.True(t, row.Retired, "%s's %s row must be `STATUS: retired <YYYY-MM-DD>`, not %q", stopgapsDoc, name, row.Status)
		require.Empty(t, retiredFaults([]stopgap{*row},
			func(tool, verb string) bool { return verbTableHas(cli, tool, verb) },
			func(n string) bool { return tests[n] }),
			"%s's %s row: every behaviour cites a test that exists, and its replacement verb is in its tool's verb table", stopgapsDoc, name)
	}
}
