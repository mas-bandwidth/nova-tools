package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// tablesLockBody is the tables lock's body as schema.go has it now, as internal/ci's
// TestSprintTablesAreLocked renders it: one line per column, then the view order,
// default and --all.
func tablesLockBody() string {
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

// The tables lock's body is the merged schema's: the update run of the lock's family
// (tablesLockLedger, at a merge that conflicts in the lock), which with NOVA_CI_UPDATE=1
// writes the lock's comment over the rendered body and says "updated, rerun" when that
// changed it, and passes when nothing is left to write.
func TestTheTablesLockIsRegenerated(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", filepath.FromSlash(tablesLock))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	comment, _ := lockSplit(raw)
	want := tablesLockBody()
	if len(comment) > 0 {
		want = strings.Join(comment, "\n") + "\n" + want
	}
	if !assert.Equal(t, want, string(raw), "the tables lock is not the schema's; regenerate it with "+diffcheck.UpdateEnv+"=1") && allowlist.Updating() {
		require.NoError(t, os.WriteFile(path, []byte(want), 0o644))
		require.Fail(t, tablesLock+" "+diffcheck.UpdatedRerun)
	}
}
