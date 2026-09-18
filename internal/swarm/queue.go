package swarm

// Per-bench queues with work stealing (docs/SPEC-JOBS.md section 2).
//
// Each bench holds queue/ as a directory of card files. A worker takes one card by
// rename(queue/<name>.card, taken/<worker>-<name>.card): the rename is atomic within
// the directory, so two workers cannot take one card, and it is the ownership record
// -- there is no central counter and no lock around the whole queue. A worker drains
// its own taken/ before it reaches for another bench, and an idle worker steals from
// the fullest bench on the mirror's five-minute timer, never emptying the victim
// below its own capacity line.
//
// This file is the file operations and the two arithmetic rules only. It starts no
// process, reaches no network, and owns no lease: a lease is section 3.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The per-bench queue's names and cadence, as section 2 prints them.
const (
	// CardExt is the suffix that makes a file in queue/ a card.
	CardExt = ".card"
	// QueueName is the per-bench directory of waiting cards.
	QueueName = "queue"
	// TakenName is the per-bench directory of cards a worker owns, one
	// <worker>-<name>.card each.
	TakenName = "taken"
	// MirrorTimer is the bench mirror's fetch-only cadence: an idle worker steals
	// from the fullest bench once per five minutes, not once per look.
	MirrorTimer = 5 * time.Minute
)

// QueueDir is the bench's queue/ directory.
func QueueDir(benchDir string) string { return filepath.Join(benchDir, QueueName) }

// TakenDir is the bench's taken/ directory, the ownership record of its queue.
func TakenDir(benchDir string) string { return filepath.Join(benchDir, TakenName) }

// QueueCards lists the cards waiting in a bench's queue/, without the .card suffix,
// in sorted order so two workers walk the same list and race on the same first card.
// A bench with no queue/ directory has no cards, not an error.
func QueueCards(benchDir string) ([]string, error) {
	entries, err := os.ReadDir(QueueDir(benchDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, CardExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(name, CardExt))
	}
	sort.Strings(names)
	return names, nil
}

// OwnedCards lists the cards a worker already holds on a bench: the
// taken/<worker>-<name>.card ownership records. A worker drains its own taken/
// before it reaches for another bench.
func OwnedCards(benchDir, worker string) ([]string, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return nil, fmt.Errorf("worker name is empty")
	}
	entries, err := os.ReadDir(TakenDir(benchDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	prefix := worker + "-"
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, CardExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(name, prefix), CardExt))
	}
	sort.Strings(names)
	return names, nil
}

// TakeCard takes one card from a bench's queue by renaming it into the bench's
// taken/ as taken/<worker>-<name>.card, and reports the card's name and whether one
// was taken. The rename is atomic within the directory, so two workers racing for
// one card cannot both take it: the loser sees the card gone and looks at the next
// one. An empty queue is ("", false, nil), not an error.
func TakeCard(benchDir, worker string) (string, bool, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return "", false, fmt.Errorf("worker name is empty")
	}
	taken := TakenDir(benchDir)
	queue := QueueDir(benchDir)
	names, err := QueueCards(benchDir)
	if err != nil {
		return "", false, err
	}
	if len(names) == 0 {
		return "", false, nil
	}
	if err := os.MkdirAll(taken, 0o755); err != nil {
		return "", false, err
	}
	for _, name := range names {
		dst := filepath.Join(taken, worker+"-"+name+CardExt)
		if err := renameSteady(filepath.Join(queue, name+CardExt), dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // another worker took this card first
			}
			return "", false, err
		}
		return name, true, nil
	}
	return "", false, nil
}

// CapacityLine is a bench's capacity line: min(cores*1.5 - load1, (free_gb-25)/2,
// memfree_gb/2), floored to a whole number of workers and never negative
// (docs/SPEC-JOBS.md section 2). A non-positive cores count is a bench with no
// pinning and no bound on the load term, so the line is the smaller memory term.
func CapacityLine(cores int, load1, freeGB, memFreeGB float64) int {
	line := math.Min((freeGB-25)/2, memFreeGB/2)
	if cores > 0 {
		line = math.Min(line, float64(cores)*1.5-load1)
	}
	if line < 0 {
		return 0
	}
	return int(math.Floor(line))
}

// StealCount is how many cards an idle worker may steal from a victim whose queue
// holds queued cards and whose capacity line is capacity: everything above the line,
// so the victim keeps enough to fill its own workers. A victim at or below its line
// is left alone; a negative capacity is an unbounded line and nothing is stolen.
func StealCount(queued, capacity int) int {
	if capacity < 0 || queued <= capacity {
		return 0
	}
	return queued - capacity
}

// FullestBench returns the bench with the most cards in its queue/ among the named
// bench directories, its queued count, and whether any of them holds a card. Ties
// go to the lexicographically first name so the choice is deterministic.
func FullestBench(benches []string) (name string, queued int, ok bool, err error) {
	sorted := append([]string(nil), benches...)
	sort.Strings(sorted)
	for _, b := range sorted {
		cards, cerr := QueueCards(b)
		if cerr != nil {
			return "", 0, false, cerr
		}
		if len(cards) > queued {
			name, queued, ok = b, len(cards), true
		}
	}
	return name, queued, ok, nil
}

// Steal takes from a victim bench's queue/ everything above its capacity line,
// renaming each card into the victim's taken/ as taken/<worker>-<name>.card, and
// returns the names taken. It never empties the victim below its line: each card is
// counted before it is renamed, and a victim at or below the line is left alone. A
// negative capacity is an unbounded line and steals nothing.
func Steal(victimDir, worker string, capacity int) ([]string, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return nil, fmt.Errorf("worker name is empty")
	}
	if capacity < 0 {
		return nil, nil
	}
	queue := QueueDir(victimDir)
	taken := TakenDir(victimDir)
	var stolen []string
	for {
		names, err := QueueCards(victimDir)
		if err != nil {
			return stolen, err
		}
		if len(names) <= capacity {
			return stolen, nil
		}
		if err := os.MkdirAll(taken, 0o755); err != nil {
			return stolen, err
		}
		src := filepath.Join(queue, names[0]+CardExt)
		dst := filepath.Join(taken, worker+"-"+names[0]+CardExt)
		if err := renameSteady(src, dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // another worker took this card first
			}
			return stolen, err
		}
		stolen = append(stolen, names[0])
	}
}

// MirrorDue reports whether an idle worker's steal is due: the bench mirror fetches
// on a five-minute timer, so a worker steals at most once per MirrorTimer. A zero
// lastSteal is a worker that has never stolen and is due.
func MirrorDue(lastSteal, now time.Time) bool {
	if lastSteal.IsZero() {
		return true
	}
	return now.Sub(lastSteal) >= MirrorTimer
}
