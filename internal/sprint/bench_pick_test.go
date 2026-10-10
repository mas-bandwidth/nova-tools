package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bench is the least loaded machine, not a name (docs/SPEC-SPRINT.md section 5,
// "the bench a lane gates on"). A lane used to gate on one machine by habit while it
// carried load 164 and another sat at 1.0: the pick takes the fleet's rows, least
// loaded first, and skips a bench over its load cap, so one function decides and no
// generator spells a bench by name.
func TestTheBenchIsTheLeastLoadedMachineNotAName(t *testing.T) {
	t.Parallel()

	fleet := []BenchRow{
		{Name: "crushed", Cores: 64, Load: 164, GoProcs: 31},
		{Name: "idle", Cores: 32, Load: 1.0, GoProcs: 0},
	}
	// the load line: the least loaded bench is chosen and its reason names the rest
	assert.Equal(t, "BENCH: idle (load 1.0 of 32 cores; crushed 164)", BenchLine(fleet, BenchCapFactor))
	pick, ok := PickBench(fleet, BenchCapFactor)
	require.True(t, ok, "the idle bench is under its cap")
	assert.Equal(t, "idle", pick.Name, "the choice is the least loaded machine, not the name crushed")

	// the rows' order is not the choice: the pick is the same with the idle row second
	reversed := []BenchRow{fleet[1], fleet[0]}
	assert.Equal(t, BenchLine(fleet, BenchCapFactor), BenchLine(reversed, BenchCapFactor))
	assert.Equal(t, "idle", mustPick(t, reversed).Name)

	// a bench over its cap is skipped even when it is the least loaded
	capped := []BenchRow{
		{Name: "small", Cores: 2, Load: 5}, // cap 3
		{Name: "big", Cores: 32, Load: 40}, // cap 48
	}
	pick, ok = PickBench(capped, BenchCapFactor)
	require.True(t, ok)
	assert.Equal(t, "big", pick.Name, "small's load 5 is over its cap 3, so it is skipped")
	assert.Equal(t, "BENCH: big (load 40.0 of 32 cores; small 5)", BenchLine(capped, BenchCapFactor))

	// every bench over its cap: no bench is named, and the line says why
	_, ok = PickBench([]BenchRow{{Name: "only", Cores: 1, Load: 9}}, BenchCapFactor)
	assert.False(t, ok, "a bench over its cap is no bench to gate on")
	assert.Equal(t, "BENCH: none (every bench is over its load cap; the least loaded is only: load 9.0 of 1 core)",
		BenchLine([]BenchRow{{Name: "only", Cores: 1, Load: 9}}, BenchCapFactor))
	assert.Empty(t, BenchLine(nil, BenchCapFactor), "a fleet with no beat to gate on writes no bench line")

	// equal loads: the pick ties to fewer go processes, so the busier bench is not chosen
	tied := []BenchRow{
		{Name: "busy", Cores: 32, Load: 4.0, GoProcs: 12},
		{Name: "quiet", Cores: 32, Load: 4.0, GoProcs: 1},
	}
	assert.Equal(t, "quiet", mustPick(t, tied).Name, "equal loads tie to the fewer go processes")

	// a beat's load is a percent of all its cores: the pick reads the load average
	assert.Equal(t, 1.0, BenchLoad(3.125, 32))
	assert.Equal(t, 0.03125, BenchLoad(3.125, 1))
}

// mustPick is PickBench with the test's precondition asserted.
func mustPick(t *testing.T, rows []BenchRow) BenchRow {
	t.Helper()
	pick, ok := PickBench(rows, BenchCapFactor)
	require.True(t, ok)
	return pick
}

// BenchRows joins the fleet's members with their beats: the beat's percent of all the
// machine's cores becomes the load average the pick reads, a member that has never
// beaten is no bench to gate on, and a bench's go-process count is its go lane's holders.
func TestBenchRowsReadsTheBeatsLoadAverage(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 6, 11, 0, 0, 0, time.UTC)
	beats := map[string]Beat{
		"crushed": {At: at, Load: 256, Cores: 64}, // a load average of 2.56 cores each
		"idle":    {At: at, Load: 3.125, Cores: 32},
	}
	goProcs := map[string]int{"crushed": 31}
	rows := BenchRows([]string{"crushed", "idle", "quiet"}, beats, goProcs)
	require.Len(t, rows, 2, "a member that has never beaten is no bench")
	assert.Equal(t, BenchRow{Name: "crushed", Cores: 64, Load: 163.84, GoProcs: 31}, rows[0])
	assert.Equal(t, BenchRow{Name: "idle", Cores: 32, Load: 1.0}, rows[1])
}

// GoProcessCounts reads the go lanes: a machine's go-process count is its go lane's
// holders, and a machine no go lane names is absent.
func TestGoProcessCountsReadsTheGoLanes(t *testing.T) {
	t.Parallel()
	lanes := []LaneRow{
		{Kind: LaneGo, Machine: "crushed", Held: []string{"a", "b"}},
		{Kind: LaneGo, Machine: "idle"},
	}
	assert.Equal(t, map[string]int{"crushed": 2, "idle": 0}, GoProcessCounts(lanes))
}

// The read card brief carries the bench a lane gates on in its STATUS line, and none
// when no bench is named (docs/SPEC-SPRINT.md section 5, "the bench a lane gates on").
func TestTheReadCardBriefCarriesTheBenchLine(t *testing.T) {
	t.Parallel()
	p := Packet{Card: "c.w1", Epoch: 0, Attempt: 1, Primary: "c", Brief: "REPO: o/r\nBASE: main\n"}
	got := ReadCardBrief("friend", "c.w1", p, "", time.Time{}, "BENCH: idle (load 1.0 of 32 cores; crushed 164)")
	assert.Contains(t, got, "BENCH: idle (load 1.0 of 32 cores; crushed 164)")
	assert.NotContains(t, ReadCardBrief("friend", "c.w1", p, "", time.Time{}, ""), "BENCH:")
}
