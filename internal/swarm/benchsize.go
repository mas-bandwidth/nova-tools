package swarm

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// Bench width: a measured power of two (docs/SPEC-SWARM.md "Benches").
//
// `bench size` runs the known-answer card W times concurrently for
// W = 1, 2, 4, ... and keeps doubling while three rules hold at the end of
// each round: (a) the one-minute load is at most 1.25 x cores; (b) throughput
// scales, cards per minute at W at least 1.5 x cards per minute at W/2;
// (c) no card abstained. The width is the last W that held. A batch fills a
// bench up to its width and, per tick, launches at most cores x 1.5 - load
// cards, never more than cores in one tick: the bench's headroom. A loaded
// machine therefore drops down without changing its width.

// SizeLoadFactor is rule (a): the one-minute load holds at most 1.25 x cores.
const SizeLoadFactor = 1.25

// SizeScaleFactor is rule (b): cards per minute at W holds at least 1.5 x
// cards per minute at W/2.
const SizeScaleFactor = 1.5

// SizeHeadroomFactor is the launch headroom per tick: cores x 1.5 - load.
const SizeHeadroomFactor = 1.5

// SizeRound is one doubling round's measurements.
type SizeRound struct {
	W               int
	Cores           int
	Load            float64
	CardsPerMin     float64
	PrevCardsPerMin float64
	Abstains        int
	Held            bool
}

// IsPowerOfTwo reports whether n is 1, 2, 4, 8, ...: the only widths a bench
// is ever measured at.
func IsPowerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}

// SizeRoundHolds answers the three rules for one round: load at most
// 1.25 x cores, throughput at least 1.5 x the previous round (vacuous for the
// first round, which has no W/2), and no abstain.
func SizeRoundHolds(r SizeRound) bool {
	if r.Abstains != 0 {
		return false
	}
	if r.Cores > 0 && r.Load > SizeLoadFactor*float64(r.Cores) {
		return false
	}
	if r.W > 1 && r.PrevCardsPerMin > 0 && r.CardsPerMin < SizeScaleFactor*r.PrevCardsPerMin {
		return false
	}
	return true
}

// MeasureWidthFromRounds is the doubling loop over already-run rounds: the
// width is the last W that held, and the doubling ends at the first round
// that breaks a rule. No round held is width 0.
func MeasureWidthFromRounds(rounds []SizeRound) int {
	width := 0
	for _, r := range rounds {
		if !SizeRoundHolds(r) {
			break
		}
		width = r.W
	}
	return width
}

// Headroom is how many cards one tick may launch: cores x 1.5 minus the
// one-minute load, never more than cores in one tick, floored at 0. A
// non-positive cores count is no pinning and no cap: -1, unbounded.
func Headroom(cores int, load float64) int {
	if cores <= 0 {
		return -1
	}
	h := int(math.Floor(SizeHeadroomFactor*float64(cores) - load))
	if h < 0 {
		return 0
	}
	if h > cores {
		return cores
	}
	return h
}

// PlanBenchLaunch fills a bench up to its width and drops down under load:
// at most width cards, at most headroom cards, at most queued cards. A width
// of 0 is an unmeasured bench and fills by cores as today; a non-positive
// cores count caps by width only.
func PlanBenchLaunch(cores int, load float64, width, queued int) int {
	if queued <= 0 {
		return 0
	}
	n := queued
	if width > 0 && n > width {
		n = width
	}
	if cores > 0 {
		if h := Headroom(cores, load); n > h {
			n = h
		}
	} else if width <= 0 {
		return queued
	}
	if n < 0 {
		return 0
	}
	return n
}

// EffectiveWidth is what a batch fills a bench to: its measured width, or,
// for a row without one, its core count as today, or -1 for unbounded.
func EffectiveWidth(b Bench) int {
	if b.Width > 0 {
		return b.Width
	}
	list, err := CoresList(b.Cores)
	if err != nil || b.Cores == "-" || b.Cores == "" {
		if c := coreCount(b.Cores); c >= 0 {
			return c
		}
		return -1
	}
	return len(list)
}

// WidthNeedsRemeasure is the adopt step's question: re-measure whenever the
// tool version changed under the row, or the row was never measured. A machine
// change arrives the same way, through probe: a bench whose cores no longer
// match is proved again before it is sized again.
func WidthNeedsRemeasure(row Bench, currentVersion string) bool {
	if row.Width <= 0 {
		return true
	}
	return row.Version != currentVersion
}

// Version8 is the row's version: the tool's identity in 8 characters.
func Version8(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}

// RecordBenchWidth writes width, measured and version onto the bench's row in
// place and writes the measured table beside it at <path>.measured: one
// header line and one row naming the bench, its width, stamp and version. It
// returns the bench's core count and the table's row count for the BENCH
// WIDTH line.
func RecordBenchWidth(path, name string, width int, measured, version string) (cores, rows int, err error) {
	if !IsPowerOfTwo(width) {
		return 0, 0, fmt.Errorf("width %d is not a power of two", width)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 7 && len(cols) != 10 {
			return 0, 0, fmt.Errorf("--benches line %d wants 7 columns or 7 with width<TAB>measured<TAB>version, got %d fields", i+1, len(cols))
		}
		if cols[0] == "name" {
			continue
		}
		rows++
		if strings.TrimSpace(cols[0]) != name {
			continue
		}
		found = true
		base := cols[:7]
		lines[i] = strings.Join(base, "\t") + "\t" + fmt.Sprint(width) + "\t" + measured + "\t" + version
		if list, cerr := CoresList(base[3]); cerr == nil {
			cores = len(list)
			if strings.TrimSpace(base[3]) == "-" {
				cores = -1
			}
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("no bench %s in %s", name, path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return 0, 0, err
	}
	measuredPath := path + ".measured"
	summary := "bench\twidth\tmeasured\tversion\n" + name + "\t" + fmt.Sprint(width) + "\t" + measured + "\t" + version + "\n"
	if err := os.WriteFile(measuredPath, []byte(summary), 0o600); err != nil {
		return 0, 0, err
	}
	return cores, rows, nil
}

// WriteMeasuredTable writes one doubling round beside the benches table: one
// header line and one row per W, so the width on the row can be re-derived
// without re-running the bench.
func WriteMeasuredTable(path, name string, rounds []SizeRound, now time.Time) error {
	var b strings.Builder
	b.WriteString("bench\tw\tload\tcores\tcards_per_min\tabstains\theld\tat\n")
	stamp := now.UTC().Format(time.RFC3339)
	for _, r := range rounds {
		r.Held = SizeRoundHolds(r)
		fmt.Fprintf(&b, "%s\t%d\t%.2f\t%d\t%.2f\t%d\t%t\t%s\n",
			name, r.W, r.Load, r.Cores, r.CardsPerMin, r.Abstains, r.Held, stamp)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
