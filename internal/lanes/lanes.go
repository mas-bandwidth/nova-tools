// Package lanes is docs/SPEC-JOBS.md section 5: the four priority lanes a cut
// writes and a pull drains.
//
// nova-pulse cut writes queue/lanes/{red,green,small,next}/; nova-swarm pull
// drains red (fixes to a red bench or a red PR), then green (small,
// already-approved PRs), then small (the shortest step budget), then next.
// Ordering inside a lane is source order; a tie the rule cannot break is asked
// of Jev as one typed decision in 400 ms behind the 0.9 floor, and a refusal
// keeps source order.
//
// A lane is a directory, so it is visible and editable: no card is promoted by
// editing prose, and a refusal by the scorer leaves the order deterministic.
package lanes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The four lanes, in the order pull drains them.
const (
	Red   = "red"
	Green = "green"
	Small = "small"
	Next  = "next"
)

// Order is the drain order of section 5.
var Order = []string{Red, Green, Small, Next}

// LanesDir is the directory under the queue that holds the four lanes.
const LanesDir = "lanes"

// SmallBudget is the step budget at or below which a card is small: the
// shortest step budget drains before the bulk lane. It is the read family's
// eight turns (nova-pulse cut's turnBudget).
const SmallBudget = 8

// Card is one card to place in a lane. Red is a fix to a red bench or a red PR;
// Approved is an already-approved PR; Steps is the card's step budget.
type Card struct {
	ID       string
	Kind     string
	Red      bool
	Approved bool
	Steps    int
	Body     string
}

// Lane classifies a card in the section's order: red first, then green, then
// small, then next.
func Lane(c Card) string {
	switch {
	case c.Red:
		return Red
	case c.Approved && c.Steps <= SmallBudget:
		return Green
	case c.Steps <= SmallBudget:
		return Small
	default:
		return Next
	}
}

// Scorer is the one typed decision behind the floor: the seam internal/decide
// satisfies in production and a fake satisfies in a test. It returns the chosen
// option and its confidence, or an error.
type Scorer func(ctx context.Context, state string, options []string) (choice string, confidence float64, err error)

// Entry is one card read back from a lane.
type Entry struct {
	Lane  string
	ID    string
	Steps int
}

// Write writes one card file per card into <queue>/lanes/<lane>/, in source
// order, and returns how many were written to each lane. A lane that receives
// no card is still created, so all four directories are visible.
func Write(queue string, cards []Card) (map[string]int, error) {
	counts := make(map[string]int, len(Order))
	for _, lane := range Order {
		if err := os.MkdirAll(filepath.Join(queue, LanesDir, lane), 0o755); err != nil {
			return counts, fmt.Errorf("lanes: create %s: %w", oneline.Field(lane), err)
		}
		counts[lane] = 0
	}
	seq := make(map[string]int, len(Order))
	for _, c := range cards {
		lane := Lane(c)
		seq[lane]++
		name := cardName(seq[lane], c.Steps, c.ID)
		if err := os.WriteFile(filepath.Join(queue, LanesDir, lane, name), []byte(c.Body), 0o644); err != nil {
			return counts, fmt.Errorf("lanes: write %s: %w", oneline.Field(name), err)
		}
		counts[lane]++
	}
	return counts, nil
}

// cardName is the file name one card carries in a lane. The fixed-width source
// number and step budget sort source order back out of a plain os.ReadDir, so
// no manifest is needed and the card id may hold hyphens.
func cardName(source, steps int, id string) string {
	return fmt.Sprintf("%04d-%03d-%s.card", source, steps, id)
}

// parseCardName is the inverse of cardName.
func parseCardName(name string) (source, steps int, id string, ok bool) {
	base := strings.TrimSuffix(name, ".card")
	if base == name || len(base) < 9 || base[4] != '-' || base[8] != '-' {
		return 0, 0, "", false
	}
	source, err := strconv.Atoi(base[:4])
	if err != nil {
		return 0, 0, "", false
	}
	steps, err = strconv.Atoi(base[5:8])
	if err != nil {
		return 0, 0, "", false
	}
	id = base[9:]
	if id == "" {
		return 0, 0, "", false
	}
	return source, steps, id, true
}

// List reads <queue>/lanes and returns the cards in drain order: lane priority
// first (red, green, small, next), source order inside each lane. A lane that
// does not exist is empty, never an error.
func List(queue string) ([]Entry, error) {
	var out []Entry
	for _, lane := range Order {
		dir := filepath.Join(queue, LanesDir, lane)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("lanes: read %s: %w", oneline.Field(lane), err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			_, steps, id, ok := parseCardName(e.Name())
			if !ok {
				continue
			}
			out = append(out, Entry{Lane: lane, ID: id, Steps: steps})
		}
	}
	return out, nil
}

// Drain returns the cards in drain order. Inside a lane, two cards of the same
// step budget are a tie the rule cannot break: when a scorer is given, it is
// asked once, and an answer at or above the floor moves that card first while a
// refusal -- an error, no answer, or a confidence below the floor -- keeps
// source order.
func Drain(ctx context.Context, queue string, score Scorer, floor float64) ([]Entry, error) {
	entries, err := List(queue)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, lane := range Order {
		group := make([]Entry, 0)
		for _, e := range entries {
			if e.Lane == lane {
				group = append(group, e)
			}
		}
		out = append(out, orderLane(ctx, lane, group, score, floor)...)
	}
	return out, nil
}

// orderLane keeps source order and, only on a tie, asks the one typed decision.
func orderLane(ctx context.Context, lane string, group []Entry, score Scorer, floor float64) []Entry {
	if len(group) < 2 || score == nil {
		return group
	}
	tie := -1
	for i := 1; i < len(group); i++ {
		if group[i].Steps == group[i-1].Steps {
			tie = group[i].Steps
			break
		}
	}
	if tie < 0 {
		return group
	}
	options := make([]string, 0, len(group))
	for _, e := range group {
		options = append(options, e.ID)
	}
	state := fmt.Sprintf("lane %s: %s tie on the step budget %d; which drains first?", lane, strings.Join(options, ", "), tie)
	choice, confidence, err := score(ctx, state, options)
	if err != nil || confidence < floor || !contains(options, choice) {
		return group
	}
	for i, e := range group {
		if e.ID == choice {
			out := make([]Entry, 0, len(group))
			out = append(out, e)
			out = append(out, group[:i]...)
			out = append(out, group[i+1:]...)
			return out
		}
	}
	return group
}

func contains(options []string, choice string) bool {
	for _, o := range options {
		if o == choice {
			return true
		}
	}
	return false
}
