package sprint

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The bench a lane gates on (docs/SPEC-SPRINT.md section 5, "the bench a lane gates
// on"). A lane used to name one machine by habit, so every friend, bud and reader
// queued behind the same bench while another sat idle. The bench is chosen at lane
// start from the fleet's rows: the benches (machines with the bench role, their cores
// from nova-config) with the load their beats carry, least loaded first, and a bench
// over its load cap (BenchCapFactor times its cores) is skipped. One function,
// BenchLine, decides for every generator, so no brief and no read prompt spells a
// bench by name.

// BenchCapFactor is a bench's load cap as a multiple of its cores: a machine whose
// load average is over this many times its cores is skipped, so a crushed machine
// stops taking lanes (the setting's default; the sprint row may lower or raise it).
const BenchCapFactor = 1.5

// BenchRow is one bench as the pick reads it: the machine's name, its whole cores, its
// load (the load average over those cores, from the beat's percent of all its cores)
// and how many go processes it runs (the fleet table's go-process count).
type BenchRow struct {
	Name    string
	Cores   int
	Load    float64
	GoProcs int
}

// BenchLoad is a beat's load as the load average the pick compares: the percent of all
// the machine's cores over that many cores. A machine with no cores reported keeps the
// percent as it stands.
func BenchLoad(pct float64, cores int) float64 {
	if cores <= 0 {
		return pct
	}
	return pct / 100 * float64(cores)
}

// BenchRows is the benches of a fleet as the pick reads them: one row per member in
// the order given that has a beat, its cores, its load average and its go-process
// count (GoProcessCounts). A member with no beat is no bench to gate on; a bench the
// lanes name none of is zero.
func BenchRows(members []string, beats map[string]Beat, goProcs map[string]int) []BenchRow {
	rows := make([]BenchRow, 0, len(members))
	for _, m := range members {
		b, ok := beats[m]
		if !ok || !b.Beaten() {
			continue
		}
		rows = append(rows, BenchRow{Name: m, Cores: b.Cores, Load: BenchLoad(b.Load, b.Cores), GoProcs: goProcs[m]})
	}
	return rows
}

// GoProcessCounts is how many go processes each machine runs: the holders of its go
// lane (LaneGo), the fleet table's go-process count (docs/SPEC-SPRINT.md section 18).
// A machine the lanes name none of is absent from the map, so BenchRows reads it as zero.
func GoProcessCounts(lanes []LaneRow) map[string]int {
	out := map[string]int{}
	for _, l := range lanes {
		if l.Kind == LaneGo {
			out[l.Machine] = len(l.Held)
		}
	}
	return out
}

// benchCap is a bench's load cap: its cores times factor, and one core's cap when the
// row names no cores, so a row with no cores is never skipped for free.
func benchCap(cores int, factor float64) float64 {
	if factor <= 0 {
		factor = BenchCapFactor
	}
	return factor * float64(max(cores, 1))
}

// PickBench is the bench a lane gates on: the least loaded row whose load is at or
// under its cap, ties to fewer go processes and then to the name, so the choice is
// never a name and never depends on the rows' order. ok is false when no row is under
// its cap (the lane has no bench to run on). capFactor is the setting; zero or less
// takes BenchCapFactor.
func PickBench(rows []BenchRow, capFactor float64) (BenchRow, bool) {
	var best BenchRow
	found := false
	for _, r := range rows {
		if r.Load > benchCap(r.Cores, capFactor) {
			continue
		}
		if !found || benchLess(r, best) {
			best, found = r, true
		}
	}
	return best, found
}

// benchLess orders two benches least loaded first, ties to fewer go processes and then
// to the name, so the pick is a total order whatever the rows' order.
func benchLess(a, b BenchRow) bool {
	if a.Load != b.Load {
		return a.Load < b.Load
	}
	if a.GoProcs != b.GoProcs {
		return a.GoProcs < b.GoProcs
	}
	return a.Name < b.Name
}

// BenchLine is the one line every generator writes for the bench a lane gates on:
//
//	BENCH: idle (load 1.0 of 32 cores; crushed 164)
//
// the chosen bench with its load and cores, then every other bench by name and load,
// so the reason the choice was made stands beside it. No bench under its cap says so
// and names the least loaded, so a lane waits instead of naming one by habit. No rows
// is "", for a fleet with no beat to gate on.
func BenchLine(rows []BenchRow, capFactor float64) string {
	if len(rows) == 0 {
		return ""
	}
	chosen, ok := PickBench(rows, capFactor)
	if !ok {
		least := rows[0]
		for _, r := range rows[1:] {
			if benchLess(r, least) {
				least = r
			}
		}
		return fmt.Sprintf("BENCH: none (every bench is over its load cap; the least loaded is %s: load %.1f of %s)",
			least.Name, least.Load, coresText(least.Cores))
	}
	others := make([]BenchRow, 0, len(rows))
	for _, r := range rows {
		if r.Name != chosen.Name {
			others = append(others, r)
		}
	}
	slices.SortFunc(others, func(a, b BenchRow) int { return strings.Compare(a.Name, b.Name) })
	parts := make([]string, 0, len(others))
	for _, r := range others {
		parts = append(parts, r.Name+" "+benchCompact(r.Load))
	}
	line := fmt.Sprintf("BENCH: %s (load %.1f of %s", chosen.Name, chosen.Load, coresText(chosen.Cores))
	if len(parts) > 0 {
		line += "; " + strings.Join(parts, "; ")
	}
	return line + ")"
}

// benchCompact is a load as the reason lists a bench that was not chosen: the least
// digits that say the number, so 164 is "164" and 1.5 is "1.5".
func benchCompact(load float64) string {
	return strconv.FormatFloat(load, 'f', -1, 64)
}

// coresText is a core count as the reason says it: "1 core", "32 cores".
func coresText(cores int) string {
	if cores <= 0 {
		cores = 1
	}
	if cores == 1 {
		return "1 core"
	}
	return strconv.Itoa(cores) + " cores"
}
