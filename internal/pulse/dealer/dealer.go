package dealer

// A Dealer distributes labels to benches with crash-safe atomic operations.
// Each label is a file in a pool directory. Dealing a label moves it atomically
// (os.Rename) from the pool into a bench directory, so a SIGKILL mid-operation
// either committed the move or did not -- a label is never half-dealt. A
// restarted dealer picks up the labels still in the pool and resumes
// round-robin distribution, so a label is never dealt twice and is never lost.
//
// The dealer replaces three shell scripts that ran per bench:
//
//	bin/card-dealer (42 lines) -- the label dispenser
//	bin/fill-loops-up (40 lines) -- the heartbeat that fill loops are running
//	bin/fill-loop.sh  (10 lines) -- the per-bench fill loop
//
// As one Go process the distributor and the per-bench loops are the same
// supervised process: when the dealer dies, every bench loop stops with it.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Deal is one committed assignment: a label dealt to a bench.
type Deal struct {
	Label string
	Bench string
}

// A Dealer distributes labels in atomically: each label is a file, and
// dealing is a rename from pool/ to bench/ -- atomic on the same file-system,
// so a crash leaves a label either in the pool or on a bench, never in
// neither and never in both.
type Dealer struct {
	pool    string
	benches []string
}

// New returns a Dealer over the pool directory and the bench directories.
// Every bench directory is created if it does not exist.
func New(pool string, benches []string) (*Dealer, error) {
	if pool == "" {
		return nil, fmt.Errorf("dealer: missing pool directory")
	}
	if len(benches) == 0 {
		return nil, fmt.Errorf("dealer: no benches named")
	}
	for _, b := range benches {
		if err := os.MkdirAll(b, 0o755); err != nil {
			return nil, fmt.Errorf("dealer: cannot open bench directory %s: %w", b, err)
		}
	}
	if err := os.MkdirAll(pool, 0o755); err != nil {
		return nil, fmt.Errorf("dealer: cannot open pool directory %s: %w", pool, err)
	}
	return &Dealer{pool: pool, benches: benches}, nil
}

// Ready returns the labels still in the pool, in filename order. A label is a
// file that is still in the pool directory; a file that was already renamed
// into a bench directory is no longer ready.
func (d *Dealer) Ready() ([]string, error) {
	entries, err := os.ReadDir(d.pool)
	if err != nil {
		return nil, err
	}
	var labels []string
	for _, e := range entries {
		if !e.IsDir() {
			labels = append(labels, e.Name())
		}
	}
	sort.Strings(labels)
	return labels, nil
}

// Dealt returns the labels already dealt to the named bench, in filename order.
func (d *Dealer) Dealt(bench string) ([]string, error) {
	entries, err := os.ReadDir(bench)
	if err != nil {
		return nil, err
	}
	var labels []string
	for _, e := range entries {
		if !e.IsDir() {
			labels = append(labels, e.Name())
		}
	}
	sort.Strings(labels)
	return labels, nil
}

// AllDealt collects every label dealt to any bench, in filename order.
func (d *Dealer) AllDealt() ([]string, error) {
	var all []string
	for _, bench := range d.benches {
		dealt, err := d.Dealt(bench)
		if err != nil {
			return nil, fmt.Errorf("dealer: cannot read bench %s: %w", bench, err)
		}
		all = append(all, dealt...)
	}
	sort.Strings(all)
	return all, nil
}

// BenchCount returns the number of benches this dealer serves.
func (d *Dealer) BenchCount() int { return len(d.benches) }

// Bench returns the i-th bench name.
func (d *Dealer) Bench(i int) string { return d.benches[i] }

// DealOne moves one label from the pool to the named bench atomically.
// The move is os.Rename, atomic on the same file-system: a SIGKILL between
// the rename and the return leaves the label either in the pool or on the
// bench, never in neither and never in both.
func (d *Dealer) DealOne(bench, label string) (Deal, error) {
	src := filepath.Join(d.pool, label)
	dst := filepath.Join(bench, label)
	if err := os.Rename(src, dst); err != nil {
		return Deal{}, err
	}
	return Deal{Label: label, Bench: bench}, nil
}

// DealRoundRobin distributes labels across benches in round-robin order:
// one label per bench per pass, repeating until the pool is empty. It returns
// every committed deal and the first error, or nil when the pool was already
// empty. Every deal in the result was atomically committed, so a process that
// is killed after the first few deals may safely resume from where it left off
// by calling DealRoundRobin again -- the labels already on a bench stay there.
func (d *Dealer) DealRoundRobin() ([]Deal, error) {
	ready, err := d.Ready()
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		return nil, nil
	}
	var deals []Deal
	idx := 0
	benchIdx := 0
	var firstErr error
	for {
		progressed := false
		for range d.benches {
			if idx >= len(ready) {
				break
			}
			bench := d.benches[benchIdx]
			benchIdx = (benchIdx + 1) % len(d.benches)
			label := ready[idx]
			idx++
			deal, err := d.DealOne(bench, label)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			progressed = true
			deals = append(deals, deal)
		}
		if !progressed {
			break
		}
		// Refresh the ready list after a pass so that a file-scan picks
		// up any label that arrived while we were dealing.
		if idx >= len(ready) {
			ready, err = d.Ready()
			if err != nil {
				return deals, err
			}
			if len(ready) == 0 {
				break
			}
			idx = 0
		}
	}
	return deals, firstErr
}
