package sprint_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: THE SPRINT TABLES ARE LOCKED. (It was internal/ci's
// sprint_tables_lock_class_test.go; it lives beside TABLES.lock, with the sprint.)
//
// Glenn 2026-10-01: "i never want new things unless i ask for them" / "i dislike this
// drift from the design of nova sprint tables that is *complete and locked*." A column
// added to the fleet table that nobody asked for made the installed build fail every
// tick on the real store.
//
// internal/sprint/TABLES.lock pins, as plain text a reader sees in a diff, every column
// of the work, readers, merge and fleet tables and of the friends table where draws
// (name, projection, fold, hidden flag, and the header label where one is set) in order,
// the order where shows the tables in by default (view) and with --all (view-all).
// This test renders the same text from internal/sprint/schema.go and is red
// on any difference. A change to a table's shape is made by editing the lock file in the
// same PR, which a read then sees.

// renderTablesLock is the lock's body as schema.go has it now: one line per column,
// then the view order, default and --all.
func renderTablesLock() string {
	var b strings.Builder
	for _, t := range append((sprint.Names{}).Definitions(), sprint.FriendsDef()) {
		hidden := map[string]bool{}
		for _, h := range t.Hidden {
			hidden[h] = true
		}
		for _, c := range t.Columns {
			h := "shown"
			if hidden[c.Name] {
				h = "hidden"
			}
			fmt.Fprintf(&b, "%s.%s %s %s %s", t.Name, c.Name, c.Projection, c.Fold, h)
			if c.Label != "" {
				fmt.Fprintf(&b, " label=%s", c.Label)
			}
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "view %s\n", strings.Join(sprint.ShownOrder, " "))
	fmt.Fprintf(&b, "view-all %s\n", strings.Join(sprint.AllOrder, " "))
	return b.String()
}

// lockBody is a lock file's lines without its comments and blanks.
func lockBody(raw string) string {
	var b strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func TestSprintTablesAreLocked(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("TABLES.lock")
	require.NoError(t, err, "the lock file internal/sprint/TABLES.lock is missing")
	want, got := lockBody(string(raw)), renderTablesLock()
	if want == got {
		return
	}
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	var diff []string
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			diff = append(diff, fmt.Sprintf("  line %d: lock has %q, schema.go has %q", i+1, w, g))
		}
	}
	assert.Fail(t, "the sprint tables are locked", "Glenn 2026-10-01: the design is complete and locked. "+
		"schema.go no longer matches internal/sprint/TABLES.lock; a PR that changes a table's shape "+
		"changes the lock file in the same PR, where a read sees it:\n%s", strings.Join(diff, "\n"))
}
