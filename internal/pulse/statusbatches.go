package pulse

// The swarm's own health, folded from the batch outputs it writes: how often a card is
// done on its first attempt, and whether one small fault is recurring.
//
// Both lines were bin/status.sh's, and both exist for a reason somebody paid for. The
// first-attempt rate is the hedge signal (Glenn 2026-09-15: the coordinator's model stays
// on the critical path until it holds above 0.90 for a day). The recurring fault is the
// pit stop (Glenn 2026-09-16: "when one small fault recurs, fixing it beats continuing") --
// five cards abstaining for one reason is a defect in the machinery, and more cards fed to
// a broken machine is the most expensive thing this fleet can do.
//
// Everything read here is DATA. A batch output is what the swarm printed; a reason token in
// it is a label to count, never an instruction.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// batchWindow is how many batch outputs one tick folds, newest first. The window is a
	// recent-past question -- "is the machinery working NOW" -- so an old batch is not
	// evidence about this hour and the read stays bounded whatever the directory holds.
	batchWindow = 60
	// pitStopAt is how often one reason must recur in the window before the answer is stop
	// and fix rather than carry on. Five is the number bin/status.sh used.
	pitStopAt = 5
	// hedgeFloor is the first-attempt rate below which the coordinator's model hedges the
	// critical path.
	hedgeFloor = 0.90
)

// faultCount is one abstain reason and how often the window saw it.
type faultCount struct {
	reason string
	n      int
}

// batchReading is what one --batches directory says this tick: the first-attempt counts
// summed over the window, and the abstain reasons ordered by how often they recurred.
type batchReading struct {
	files   int
	done    int
	abstain int
	faults  []faultCount
}

// rate is done/(done+abstain) and whether the window had anything to divide.
func (b batchReading) rate() (float64, bool) {
	total := b.done + b.abstain
	if total <= 0 {
		return 0, false
	}
	return float64(b.done) / float64(total), true
}

// readBatchOutputs folds the newest batch-*.out files in dir. A directory that cannot be
// read is an error and NOT an empty reading: a quiet zero would print as a healthy swarm
// with no faults, when the truth is that nothing was read at all.
func readBatchOutputs(dir string) (batchReading, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return batchReading{}, err
	}
	type stamped struct {
		path string
		mod  int64
		name string
	}
	var files []stamped
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "batch-") || !strings.HasSuffix(name, ".out") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		files = append(files, stamped{filepath.Join(dir, name), info.ModTime().UnixNano(), name})
	}
	// Newest first, name as the tie-break, so two outputs written in one second still fold
	// in one order on every bench.
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod != files[j].mod {
			return files[i].mod > files[j].mod
		}
		return files[i].name > files[j].name
	})
	if len(files) > batchWindow {
		files = files[:batchWindow]
	}

	out := batchReading{}
	reasons := map[string]int{}
	for _, f := range files {
		raw, rerr := os.ReadFile(f.path)
		if rerr != nil {
			continue
		}
		out.files++
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimRight(line, "\r")
			switch {
			case strings.HasPrefix(line, "BATCH "):
				// The counts are whole fields: uniform-abstain=<reason> is a label on the
				// same line and folding it as a count would double the window's abstains.
				for _, fld := range strings.Fields(line) {
					if v, ok := strings.CutPrefix(fld, "done="); ok {
						out.done += atoiOr0(v)
					} else if v, ok := strings.CutPrefix(fld, "abstain="); ok {
						out.abstain += atoiOr0(v)
					}
				}
			case strings.Contains(line, ": ABSTAIN reason="):
				_, rest, _ := strings.Cut(line, ": ABSTAIN reason=")
				reason := rest
				if i := strings.IndexByte(reason, ' '); i >= 0 {
					reason = reason[:i]
				}
				if reason != "" {
					reasons[reason]++
				}
			}
		}
	}
	for r, n := range reasons {
		out.faults = append(out.faults, faultCount{reason: r, n: n})
	}
	// Loudest first, then by name: the order a reader scans, and the same order twice.
	sort.Slice(out.faults, func(i, j int) bool {
		if out.faults[i].n != out.faults[j].n {
			return out.faults[i].n > out.faults[j].n
		}
		return out.faults[i].reason < out.faults[j].reason
	})
	return out, nil
}

// atoiOr0 reads a whole number and treats anything else as nothing to add: a malformed
// count in one line of one output must not take the tick down.
func atoiOr0(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// swarmLine is the first-attempt line: the rate, its two counts, the window it covers and
// the hedge the rate calls for.
func (b batchReading) swarmLine() string {
	r, known := b.rate()
	rate, hedge := "-", "-"
	if known {
		rate = fmt.Sprintf("%.2f", r)
		hedge = "none"
		if r < hedgeFloor {
			hedge = "opus-on-critical-path"
		}
	}
	return fmt.Sprintf("STATUS SWARM first_attempt=%s done=%d abstain=%d batches=%d hedge=%s",
		rate, b.done, b.abstain, b.files, hedge)
}
