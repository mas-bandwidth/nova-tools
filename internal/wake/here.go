package wake

import (
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// READY IS A MEASUREMENT, AND THE TOOL TAKES IT (rule 18).
//
// 2026-09-09: READY was sent to a friend for a profiling window, and two heavy
// children were started three minutes later. Mercury's correction, verbatim:
// "READY only when the system is quiet enough for the task." So a readiness
// receipt is a measurement read AT THE INSTANT OF SENDING -- load average and
// process count -- and a promise about the next ten minutes.
//
// THE PROCESS COUNT IS A NUMBER THE KERNEL ALREADY HAS, AND NO LISTING OF ANY
// KIND. Draft 1 took the count from the process filesystem's own entries, which
// is a listing -- walking that directory is reading the process table entry by
// entry -- and test 4's tripwire forbids that path's name in this package
// outright, so the code rule 18 demands could not be written without turning
// test 4 red. The repair is the mechanism, not an exemption: a tripwire with a
// hole in it is not a tripwire. No entry is fetched, so no pid, no name and no
// command line is ever in this process's memory.
//
// THE COUNT IS A LOAD NUMBER AND NAMES NOBODY: procs= says how busy this bench
// is and cannot say WHOSE work it is, so a window that finds the bench loud
// learns that it is loud and must name the noise itself, from its own record of
// what it started. Naming stays the window's.
//
// THE WORD READY APPEARS NOWHERE IN THIS TOOL'S OUTPUT: the receipt is the
// window's, and it is a promise about the next ten minutes, made on the numbers.

// Bench is one reading of this machine.
type Bench struct {
	Load     [3]float64
	HasLoad  bool
	CPUs     int
	Procs    int
	HasProcs bool
}

// ReadBench takes the three numbers at this instant.
func ReadBench() Bench {
	b := Bench{CPUs: runtime.NumCPU()}
	if load, ok := loadAverage(); ok {
		b.Load, b.HasLoad = load, true
	}
	if n, ok := processCount(); ok {
		b.Procs, b.HasProcs = n, true
	}
	return b
}

// HereLine is the one line --here prints. A platform with neither reading
// prints load=- or procs=-, which is A READING RATHER THAN A REFUSAL.
func HereLine(now time.Time, b Bench, quietLoad string) string {
	load := "-"
	if b.HasLoad {
		load = fmt.Sprintf("%.2f,%.2f,%.2f", b.Load[0], b.Load[1], b.Load[2])
	}
	procs := "-"
	if b.HasProcs {
		procs = strconv.Itoa(b.Procs)
	}
	line := fmt.Sprintf("WAKE HERE at=%s load=%s cpus=%d procs=%s",
		oneline.Field(Stamp(now)), oneline.Field(load), b.CPUs, oneline.Field(procs))
	if quietLoad != "" {
		// A comparison the caller asked for, not a verdict the tool reached.
		want, err := strconv.ParseFloat(quietLoad, 64)
		quiet := "-"
		if err == nil && b.HasLoad {
			quiet = boolWord(b.Load[0] <= want)
		}
		line += " quiet=" + quiet
	}
	return line
}
