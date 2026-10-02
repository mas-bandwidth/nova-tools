package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// init --members <name>:<width> and fleet up <m> --width <n> set a member's
// width; where shows it in the fleet table's width column beside working, and
// --json carries it; a width that is not one is refused before anything is
// written.
func TestTheWidthIsSetByInitAndFleetUpAndShown(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:64,m2:3,m3")
	ta.ok("fleet up m3 --width 8")
	out := ta.ok("where")
	require.Contains(t, out, "fleet | ready | working | width |", "the fleet table has no width beside working")
	var v struct {
		Tables map[string]map[string]map[string]string `json:"tables"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	fleet := v.Tables["fleet"]
	require.Equal(t, "64", fleet["m1"]["width"], "the widths: %v", fleet)
	require.Equal(t, "3", fleet["m2"]["width"], "the widths: %v", fleet)
	require.Equal(t, "8", fleet["m3"]["width"], "the widths: %v", fleet)
	for _, line := range []string{"fleet up m4 --width 0", "fleet up m4 --width 1025", "fleet up m4 --width x"} {
		code, _, errs := ta.do(line)
		assert.NotEqual(t, 0, code, "%s: exit %d, %s", line, code, errs)
		assert.Contains(t, errs, "width", "%s: exit %d, %s", line, code, errs)
	}
	nb := newTestApp(t)
	code, _, errs := nb.do("init --members m1:0")
	assert.NotEqual(t, 0, code, "init --members m1:0: exit %d, %s", code, errs)
	assert.Contains(t, errs, "width", "init --members m1:0: exit %d, %s", code, errs)
}

// The fleet table's footer sums the width column, the fleet's total width:
// eight machines of 64 total 512; a machine whose width changes moves the
// total, and a machine added adds its width.
func TestTheFleetFooterTotalsTheWidths(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2,m3,m4,m5,m6,m7,m8")
	footer := func() string {
		out := ta.ok("where")
		i := strings.Index(out, "fleet |")
		require.GreaterOrEqual(t, i, 0, "where has no fleet table:\n%s", out)
		lines := strings.Split(strings.TrimRight(strings.SplitN(out[i:], "\n\n", 2)[0], "\n"), "\n")
		return lines[len(lines)-1]
	}
	require.Contains(t, footer(), "|   512 |", "eight machines of 64 do not total 512 in the footer")
	ta.ok("fleet up m1 --width 8")
	require.Contains(t, footer(), "|   456 |", "the footer after m1 narrows to 8")
}
