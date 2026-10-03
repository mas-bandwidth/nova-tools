package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The fleet table's footer sums only the members that are up (the owner,
// 2026-10-02: "width 132?!"): a held and a down member show their width on
// their own rows and add nothing to the total, which is the width that can take
// a card; ready, working, done and ok% are folded over the same up members.
func TestTheFleetFooterSumsOnlyTheMembersUp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.live = []string{"m1", "m2"}
	ta.ok("init --readers reader-a,reader-b")
	ta.ok("fleet up m1 --width 4")
	ta.ok("fleet up m2 --width 32")
	ta.ok("fleet up m3 --width 8")
	ta.ok("fleet down m2") // held
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	rows := ta.fleetRows()
	require.Equal(t, sprint.Up, rows["m1"]["status"], "%v", rows)
	require.Equal(t, sprint.Held, rows["m2"]["status"], "%v", rows)
	require.Equal(t, sprint.Down, rows["m3"]["status"], "%v", rows)
	fleet := strings.TrimRight(tableOf(ta.ok("where"), "fleet"), "\n")
	lines := strings.Split(fleet, "\n")
	require.GreaterOrEqual(t, len(lines), 7, fleet)
	footer := lines[len(lines)-1]
	cells := strings.Split(footer, " | ")
	require.GreaterOrEqual(t, len(cells), 6, "the footer's cells:\n%s", fleet)
	assert.Equal(t, "4", strings.TrimSpace(cells[3]), "the width total is m1's alone, not 4+32+8:\n%s", fleet)
	assert.Equal(t, "2", strings.TrimSpace(cells[1]), "both cards are on m1, the one member up:\n%s", fleet)
	assert.True(t, strings.HasPrefix(footer, "      |     2 |       0 |     4 |    0 | 0.0% |"), "the footer lines up with the rows:\n%s", fleet)
	assert.Contains(t, fleet, "m2    |     0 |       0 |    32 |", "a held member's width shows on its row")
}
