package ci

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStopgapWatchShIsRetired is the register's test for the coordinator's hand
// wake script. docs/STOPGAPS.md's `## coordinator-wake` row (Path
// <coordinator-dir>/tmp/buswatch/watch.sh) must be `STATUS: retired <date>`, name
// the nova-sprint `watch` verb as its replacement, and cite on every behaviour
// line a `(test: TestX)` for a test that exists in the tree. The check is the
// register's own parser and rule (parseStopgaps and retiredFaults in
// stopgaps_class_test.go) run on the one row the retirement card touched, so the
// row is red while watch.sh is still live or names a behaviour no test holds.
func TestStopgapWatchShIsRetired(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	rows, faults := parseStopgaps(readFile(t, filepath.Join(root, stopgapsDoc)))
	require.Empty(t, faults, "%s is malformed", stopgapsDoc)

	var watch *stopgap
	for i := range rows {
		if rows[i].Name == "coordinator-wake" {
			watch = &rows[i]
			break
		}
	}
	require.NotNil(t, watch, "%s has no `## coordinator-wake` row: the watch.sh script is listed", stopgapsDoc)
	require.True(t, watch.Retired, "%s's watch.sh row must be `STATUS: retired <YYYY-MM-DD>`, not %q", stopgapsDoc, watch.Status)

	cli := readFile(t, filepath.Join(root, "docs", "CLI.md"))
	tests := treeTestNames(t, root)
	require.Empty(t, retiredFaults([]stopgap{*watch},
		func(tool, verb string) bool { return verbTableHas(cli, tool, verb) },
		func(name string) bool { return tests[name] }),
		"every behaviour of the retired watch.sh row cites a test that exists, and its replacement verb is in its tool's verb table")
}
