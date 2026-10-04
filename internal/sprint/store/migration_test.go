package store

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// fleetBeforeVerdicts is the fleet table's columns and hidden list before the
// readers' verdicts counted (git show a2aff60e9:internal/sprint/schema.go): the shape
// a live store's fleet table has until the migration runs.
const fleetBeforeVerdicts = "ready,working,width:text:sum,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%,status:text,load:text,withdrawn,ok,failed,ctl:first:none"

// The migration that ships with the readers' verdicts
// (migrations/0001_fleet_finished_redealt.sh; internal/sprint/TABLES.lock, the change
// of 2026-10-04) gives a live store's fleet table, as it stood before, exactly the
// locked shape this build writes: its columns in order and its hidden columns. Each
// line is read as the table layer would run it (nova-table col add <table> <spec>
// --after <col>; nova-table set <table> --hide <col>) and applied to the shape; a line
// of any other form is refused, so the file holds nothing this test does not check.
func TestTheMigrationGivesTheFleetTableItsLockedShape(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("migrations", "0001_fleet_finished_redealt.sh"))
	require.NoError(t, err)
	cols, err := ntable.ParseColumns(fleetBeforeVerdicts)
	require.NoError(t, err)
	hidden := []string{sprint.Withdrawn, sprint.DoneOK, sprint.DoneFailed, sprint.Ctl}
	live := sprint.Names{}.Table(sprint.Fleet)
	ran := 0
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "nova-table" {
			continue
		}
		ran++
		switch {
		case len(f) == 7 && f[1] == "col" && f[2] == "add" && f[5] == "--after":
			require.Equal(t, live, f[3], "%q: the live store's fleet table", line)
			add, err := ntable.ParseColumns(f[4])
			require.NoError(t, err, line)
			at := slices.IndexFunc(cols, func(c ntable.Column) bool { return c.Name == f[6] })
			require.GreaterOrEqual(t, at, 0, "%q: no column %s to add after", line, f[6])
			cols = slices.Insert(cols, at+1, add...)
		case len(f) == 5 && f[1] == "set" && f[3] == "--hide":
			require.Equal(t, live, f[2], "%q: the live store's fleet table", line)
			hidden = append(hidden, f[4])
		default:
			t.Fatalf("%q: a migration line of a form this test does not apply", line)
		}
	}
	require.Equal(t, 3, ran, "the migration's three writes")
	var want ntable.Table
	for _, def := range (sprint.Names{}).Definitions() {
		if def.Name == live {
			want = def
		}
	}
	assert.Equal(t, want.Columns, cols, "the migrated columns are the locked fleet table's, in order")
	assert.ElementsMatch(t, want.Hidden, hidden, "the migrated hidden columns are the locked fleet table's")
	locked, err := ntable.ParseColumns(lockedFleet)
	require.NoError(t, err)
	assert.Equal(t, locked, cols, "the migrated columns are the locked literal (locked_fleet_test.go)")
}
