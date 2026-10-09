package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A reader on the verdicts' ledger is its machine: one machine reading on its reader row
// (reader-m2) and on its fleet row (m2) is one reader, counted once, held by both rows; and a
// reader breaking nearly everything is set beside another reader's own last verdicts, ok
// against broken, whatever their tiers (docs/SPEC-SPRINT.md section 8).
func TestAMachineOnTwoRowsIsOneReader(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Readers: NewTable(Readers), Fleet: NewTable(Fleet)}
	var readers, fleet []string
	line := func(i int, row, verdict, tier string) string {
		return ReadVerdict{At: t0.Add(-time.Duration(30-i) * time.Second), Reader: row, Verdict: verdict, Tier: tier,
			Finding: "RULE: the branch this read names is not on origin"}.line()
	}
	for i := range 10 {
		readers = append(readers, line(i, ReaderPrefix+"m2", "broken", "pro"))
		fleet = append(fleet, line(10+i, "m2", "broken", "pro"))
	}
	for i := range 6 {
		fleet = append(fleet, line(20+i, FriendRow("amy"), "ok", "flash"))
	}
	s.Readers.SetProp(PropReadsWindow, strings.Join(readers, "\n"))
	s.Fleet.SetProp(PropReadsWindow, strings.Join(fleet, "\n"))

	what, ok := BrokenReadsOutrun(s, nil)
	require.True(t, ok)
	require.Contains(t, what, "by reader: amy 6 ok, 0 broken; m2 0 ok, 20 broken;", "m2 once, both rows' verdicts")
	require.NotContains(t, what, ReaderPrefix+"m2")

	b := ReadersBreaking(s)
	require.Len(t, b, 1, "m2's last 20 are its two rows' 20")
	require.Equal(t, "m2", b[0].Reader)
	require.Equal(t, []string{"m2", ReaderPrefix + "m2"}, b[0].Holds, "held by each row it reads on")
	require.Equal(t, "amy", b[0].Other, "set beside amy's own verdicts, though she read another tier")
	require.Equal(t, 6, b[0].OtherTally.ok)
	conds := readsWindowConds(s, TickReq{})
	require.Len(t, conds, 2)
	require.Equal(t, []string{"hold m2", "hold " + ReaderPrefix + "m2", "look at the reader", "act"}, conds[1].decisions)
}
